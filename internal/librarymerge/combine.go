// Package librarymerge validates and combines portable Playlist AI library
// packs without requiring audio codecs, models, native libraries, or network
// access.
package librarymerge

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/platten/playlistai/internal/librarylearn"
	"github.com/platten/playlistai/internal/librarypack"
)

const (
	mergeAlgorithmVersion = "paipack-combine/v1"
	defaultMaxRAM         = int64(2 << 30)
	defaultTrainingSample = 50_000
	mergeBatchRows        = 4096
	maximumAliasLength    = 128
)

// Options controls bounded temporary storage and deterministic resource fits.
// Workers <= 0 selects the current Go parallelism.
type Options struct {
	WorkDir        string `json:"workDir,omitempty"`
	MaxRAM         int64  `json:"maxRam"`
	Workers        int    `json:"workers"`
	Seed           uint64 `json:"seed"`
	TrainingSample int    `json:"trainingSample"`
	Clusters       int    `json:"clusters"`
}

// EvidenceCounts records how many incoming rows matched prior rows through
// each independently safe evidence type. One incoming row is counted at most
// once per evidence type.
type EvidenceCounts struct {
	ISRC        int `json:"isrc"`
	MusicBrainz int `json:"musicBrainzRecording"`
	AcoustID    int `json:"acoustId"`
	Fingerprint int `json:"fingerprint"`
}

// Report describes the complete deterministic merge.
type Report struct {
	InputPacks              int                  `json:"inputPacks"`
	UniqueInputPacks        int                  `json:"uniqueInputPacks"`
	InputTracks             int                  `json:"inputTracks"`
	OutputTracks            int                  `json:"outputTracks"`
	RemovedDuplicates       int                  `json:"removedDuplicates"`
	EvidenceMatches         EvidenceCounts       `json:"evidenceMatches"`
	ConflictingFields       map[string]int       `json:"conflictingFields"`
	AliasRewrites           int                  `json:"aliasRewrites"`
	TrackIDRewrites         int                  `json:"trackIdRewrites"`
	SkippedIdenticalPacks   int                  `json:"skippedIdenticalPacks"`
	EffectiveTrainingSample int                  `json:"effectiveTrainingSample"`
	Manifest                librarypack.Manifest `json:"manifest"`
}

type stagedInput struct {
	order    int
	path     string
	manager  *librarypack.Manager
	staged   *librarypack.Staged
	manifest librarypack.Manifest
}

func (s *stagedInput) close() {
	if s == nil {
		return
	}
	if s.staged != nil && s.manager != nil {
		_ = s.manager.Discard(s.staged)
		s.staged = nil
	}
	if s.manager != nil {
		_ = s.manager.Close()
		s.manager = nil
	}
}

// Combine fully validates each input, merges its recording groups, rebuilds
// learned resources, and atomically replaces output only after all prior work
// succeeds.
func Combine(ctx context.Context, inputs []string, output string, options Options) (Report, error) {
	report := Report{InputPacks: len(inputs), ConflictingFields: map[string]int{}}
	if len(inputs) < 2 {
		return report, errors.New("librarymerge: at least two input packs are required")
	}
	if strings.TrimSpace(output) == "" {
		return report, errors.New("librarymerge: output path is required")
	}
	options, err := normalizeOptions(options)
	if err != nil {
		return report, err
	}
	if err := rejectOutputInputAlias(inputs, output); err != nil {
		return report, err
	}
	workParent := options.WorkDir
	if workParent != "" {
		if err := os.MkdirAll(workParent, 0o700); err != nil {
			return report, fmt.Errorf("librarymerge: create work directory: %w", err)
		}
	}
	work, err := os.MkdirTemp(workParent, "paipack-combine-*")
	if err != nil {
		return report, fmt.Errorf("librarymerge: create temporary work area: %w", err)
	}
	defer os.RemoveAll(work)

	staged, err := stageInputs(ctx, work, inputs, &report)
	if err != nil {
		return report, err
	}
	defer func() {
		for _, input := range staged {
			input.close()
		}
	}()
	if len(staged) == 0 {
		return report, errors.New("librarymerge: no unique input packs remain")
	}
	vectorSpace, err := compatibleVectorSpace(staged)
	if err != nil {
		return report, err
	}
	aliasMap, rewrites, err := buildAliasMap(staged)
	if err != nil {
		return report, err
	}
	report.AliasRewrites = rewrites

	store, err := openMergeStore(ctx, filepath.Join(work, "merge.sqlite"))
	if err != nil {
		return report, err
	}
	defer store.close()
	for _, input := range staged {
		if err := store.ingest(ctx, input, aliasMap, &report); err != nil {
			return report, fmt.Errorf("librarymerge: ingest %q: %w", input.path, err)
		}
	}
	if err := store.resolveGroups(ctx); err != nil {
		return report, err
	}
	membershipDigest, err := store.mergeGroups(ctx, &report)
	if err != nil {
		return report, err
	}
	if report.OutputTracks > librarypack.DefaultLimits().MaxTracks {
		return report, fmt.Errorf("librarymerge: merged output has %d tracks, exceeding the %d-track limit", report.OutputTracks, librarypack.DefaultLimits().MaxTracks)
	}
	if err := store.assignTrackIDs(ctx, &report); err != nil {
		return report, err
	}
	resources, err := store.rebuildResources(ctx, vectorSpace, options, membershipDigest, staged)
	if err != nil {
		return report, err
	}
	report.EffectiveTrainingSample = len(resources.trainingSample)

	source, err := store.outputSource(ctx)
	if err != nil {
		return report, err
	}
	defer source.Close()
	manifest, err := librarypack.WriteSource(ctx, output, librarypack.Pack{
		CorpusGeneration:     resources.corpusGeneration,
		MetadataGeneration:   resources.metadataGeneration,
		MERTGeneration:       resources.mertGeneration,
		ClusterGeneration:    resources.clusterGeneration,
		StatisticsGeneration: resources.statisticsGeneration,
		MERT:                 vectorSpace,
		Learning:             resources.learning,
		Statistics:           resources.statistics,
	}, source, librarypack.DefaultLimits())
	if err != nil {
		return report, fmt.Errorf("librarymerge: write output: %w", err)
	}
	report.Manifest = manifest
	return report, nil
}

func normalizeOptions(options Options) (Options, error) {
	if options.MaxRAM == 0 {
		options.MaxRAM = defaultMaxRAM
	}
	if options.MaxRAM < 1<<20 {
		return options, errors.New("librarymerge: max RAM must be at least 1MiB")
	}
	if options.Workers <= 0 {
		options.Workers = runtime.GOMAXPROCS(0)
	}
	if options.Workers <= 0 || options.Workers > 65_536 {
		return options, errors.New("librarymerge: workers must be auto or a positive bounded integer")
	}
	if options.TrainingSample == 0 {
		options.TrainingSample = defaultTrainingSample
	}
	if options.TrainingSample < 0 {
		return options, errors.New("librarymerge: training sample cannot be negative")
	}
	if options.Clusters < 0 || options.Clusters > 4096 {
		return options, errors.New("librarymerge: clusters must be between 0 and 4096")
	}
	return options, nil
}

func rejectOutputInputAlias(inputs []string, output string) error {
	resolvedOutput, outputInfo, err := resolvedPath(output)
	if err != nil {
		return err
	}
	for _, input := range inputs {
		resolvedInput, inputInfo, err := resolvedPath(input)
		if err != nil {
			return fmt.Errorf("librarymerge: resolve input %q: %w", input, err)
		}
		if resolvedInput == resolvedOutput || inputInfo != nil && outputInfo != nil && os.SameFile(inputInfo, outputInfo) {
			return fmt.Errorf("librarymerge: output %q resolves to input %q", output, input)
		}
	}
	return nil
}

func resolvedPath(name string) (string, os.FileInfo, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", nil, err
	}
	info, statErr := os.Stat(abs)
	if statErr == nil {
		resolved, err := filepath.EvalSymlinks(abs)
		return resolved, info, err
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		return "", nil, statErr
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if errors.Is(err, os.ErrNotExist) {
		parent = filepath.Dir(abs)
		err = nil
	}
	if err != nil {
		return "", nil, err
	}
	return filepath.Join(parent, filepath.Base(abs)), nil, nil
}

func stageInputs(ctx context.Context, work string, paths []string, report *Report) ([]*stagedInput, error) {
	seen := map[string]struct{}{}
	var inputs []*stagedInput
	cleanup := func() {
		for _, input := range inputs {
			input.close()
		}
	}
	for order, name := range paths {
		root := filepath.Join(work, fmt.Sprintf("input-%06d", order))
		manager, err := librarypack.OpenManager(ctx, root, librarypack.DefaultLimits())
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("librarymerge: prepare input %q: %w", name, err)
		}
		staged, err := manager.Stage(ctx, name)
		if err != nil {
			_ = manager.Close()
			cleanup()
			if strings.Contains(err.Error(), "version 4") || strings.Contains(err.Error(), "version 3") || strings.Contains(err.Error(), "version 2") || strings.Contains(err.Error(), "version 1") {
				return nil, fmt.Errorf("librarymerge: input %q uses an obsolete paipack version; rebuild it with the current playlist-indexer: %w", name, err)
			}
			return nil, fmt.Errorf("librarymerge: validate input %q: %w", name, err)
		}
		manifest := staged.Manifest()
		if _, duplicate := seen[manifest.PackID]; duplicate {
			report.SkippedIdenticalPacks++
			_ = manager.Discard(staged)
			_ = manager.Close()
			continue
		}
		seen[manifest.PackID] = struct{}{}
		inputs = append(inputs, &stagedInput{order: order, path: name, manager: manager, staged: staged, manifest: manifest})
		report.UniqueInputPacks++
		report.InputTracks += manifest.Coverage.Tracks
	}
	return inputs, nil
}

func compatibleVectorSpace(inputs []*stagedInput) (librarypack.VectorSpace, error) {
	var space librarypack.VectorSpace
	found := false
	for _, input := range inputs {
		if input.manifest.Coverage.MERT == 0 {
			continue
		}
		if !found {
			space, found = input.manifest.MERT, true
			continue
		}
		if !reflect.DeepEqual(space, input.manifest.MERT) {
			return librarypack.VectorSpace{}, fmt.Errorf("librarymerge: incompatible MERT representation contracts in pack %s", input.manifest.PackID)
		}
	}
	return space, nil
}

type aliasKey struct{ packID, alias string }

func buildAliasMap(inputs []*stagedInput) (map[aliasKey]string, int, error) {
	owners := map[string][]string{}
	for _, input := range inputs {
		for _, alias := range input.manifest.RootAliases {
			owners[alias] = append(owners[alias], input.manifest.PackID)
		}
	}
	used := map[string]struct{}{}
	for alias, packs := range owners {
		if len(packs) == 1 {
			used[alias] = struct{}{}
		}
	}
	result := map[aliasKey]string{}
	rewrites := 0
	for _, input := range inputs {
		aliases := append([]string(nil), input.manifest.RootAliases...)
		sort.Strings(aliases)
		for _, alias := range aliases {
			key := aliasKey{input.manifest.PackID, alias}
			if len(owners[alias]) == 1 {
				result[key] = alias
				continue
			}
			candidate, err := suffixedIdentifier(alias, input.manifest.PackID, maximumAliasLength, used)
			if err != nil {
				return nil, 0, err
			}
			result[key] = candidate
			used[candidate] = struct{}{}
			rewrites++
		}
	}
	return result, rewrites, nil
}

func suffixedIdentifier(base, identity string, maximum int, used map[string]struct{}) (string, error) {
	for length := 12; length <= len(identity); length += 4 {
		suffix := identity[:min(length, len(identity))]
		prefix := base
		if len(prefix)+1+len(suffix) > maximum {
			prefix = prefix[:maximum-1-len(suffix)]
		}
		candidate := prefix + "-" + suffix
		if _, exists := used[candidate]; !exists {
			return candidate, nil
		}
	}
	digest := sha256.Sum256([]byte(identity + "\x00" + base))
	for length := 12; length <= len(digest)*2; length += 4 {
		suffix := hex.EncodeToString(digest[:])[:length]
		prefix := base
		if len(prefix)+1+len(suffix) > maximum {
			prefix = prefix[:maximum-1-len(suffix)]
		}
		candidate := prefix + "-" + suffix
		if _, exists := used[candidate]; !exists {
			return candidate, nil
		}
	}
	return "", errors.New("librarymerge: could not derive a unique bounded identifier")
}

func encodeVector(vector []float32) []byte {
	if len(vector) == 0 {
		return []byte{}
	}
	raw := make([]byte, len(vector)*4)
	for index, value := range vector {
		binary.LittleEndian.PutUint32(raw[index*4:], math.Float32bits(value))
	}
	return raw
}

func decodeVector(raw []byte) ([]float32, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw)%4 != 0 {
		return nil, errors.New("librarymerge: corrupt temporary vector")
	}
	vector := make([]float32, len(raw)/4)
	for index := range vector {
		vector[index] = math.Float32frombits(binary.LittleEndian.Uint32(raw[index*4:]))
	}
	return vector, nil
}

func generationID(prefix string, sourcePackIDs []string, options Options, membership string) string {
	raw, _ := json.Marshal(struct {
		Version          string   `json:"version"`
		Sources          []string `json:"sources"`
		Seed             uint64   `json:"seed"`
		TrainingSample   int      `json:"trainingSample"`
		Clusters         int      `json:"clusters"`
		MaxRAM           int64    `json:"maxRam"`
		MembershipDigest string   `json:"membershipDigest"`
	}{mergeAlgorithmVersion, sourcePackIDs, options.Seed, options.TrainingSample, options.Clusters, options.MaxRAM, membership})
	sum := sha256.Sum256(raw)
	return prefix + "-" + hex.EncodeToString(sum[:12])
}

func sourcePackIDs(inputs []*stagedInput) []string {
	ids := make([]string, len(inputs))
	for index, input := range inputs {
		ids[index] = input.manifest.PackID
	}
	return ids
}

func keyedPriority(seed uint64, domain, id string) [32]byte {
	h := sha256.New()
	var raw [8]byte
	binary.LittleEndian.PutUint64(raw[:], seed)
	_, _ = h.Write([]byte(librarylearn.SamplingVersion))
	_, _ = h.Write(raw[:])
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(domain))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(id))
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

func openSQLite(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode=DELETE; PRAGMA synchronous=OFF; PRAGMA temp_store=FILE; PRAGMA cache_size=-8192;`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
