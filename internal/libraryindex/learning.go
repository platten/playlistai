package libraryindex

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarylearn"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/librarysearch"
	"github.com/platten/playlistai/internal/localaudio"
)

type LearningGeneration struct {
	Version        int                          `json:"version"`
	ID             string                       `json:"id"`
	CreatedAt      string                       `json:"createdAt"`
	Snapshot       CorpusSnapshot               `json:"snapshot"`
	Seed           uint64                       `json:"seed"`
	TrainingSample []string                     `json:"trainingSample"`
	Metadata       librarylearn.MetadataModel   `json:"metadata"`
	Spherical      *librarylearn.SphericalModel `json:"spherical,omitempty"`
	// AssignmentStore is a generation-relative immutable SQLite file. New
	// generations keep corpus-sized assignments out of learning.json; the slice
	// remains readable for version-1 generations written by older builds.
	AssignmentStore string                           `json:"assignmentStore,omitempty"`
	Assignments     []librarylearn.ClusterAssignment `json:"assignments,omitempty"`
	Index           *librarysearch.Manifest          `json:"index,omitempty"`
	Coverage        librarypack.Coverage             `json:"coverage"`
	VectorSpace     librarypack.VectorSpace          `json:"vectorSpace"`
	DSPStatistics   librarylearn.DSPStatisticsModel  `json:"dspStatistics"`
}

type FitOptions struct {
	Seed           uint64
	TrainingSample int
	Clusters       int
	Refit          bool
	Plan           ResourcePlan
}

type FitResult struct {
	Generation LearningGeneration `json:"generation"`
	Path       string             `json:"path"`
	Skipped    bool               `json:"skipped"`
}

func (s *State) Fit(ctx context.Context, options FitOptions) (FitResult, error) {
	for _, kind := range []string{"metadata", "audio", "clap"} {
		pending, leased, err := s.JobCounts(ctx, kind)
		if err != nil {
			return FitResult{}, err
		}
		if pending != 0 || leased != 0 {
			return FitResult{}, fmt.Errorf("library indexer: cannot fit before %s jobs drain (pending=%d leased=%d)", kind, pending, leased)
		}
	}
	snapshot, err := s.CreateSnapshot(ctx)
	if err != nil {
		return FitResult{}, err
	}
	store, err := openFrozenStore(snapshot.Path)
	if err != nil {
		return FitResult{}, err
	}
	defer store.Close()
	coverage, vectorSpace, vectorDimension, err := store.summary(ctx)
	if err != nil {
		return FitResult{}, err
	}
	if coverage.Tracks == 0 {
		return FitResult{}, errors.New("library indexer: no imported tracks are available to fit")
	}
	sampleLimit := effectiveTrainingSample(options.TrainingSample, coverage.MERT, vectorDimension, options.Plan.MaxRAM)
	options.TrainingSample = sampleLimit
	id := learningID(snapshot, options)
	dir := filepath.Join(s.dir, "generations", "learning", id)
	manifestPath := filepath.Join(dir, "learning.json")
	if !options.Refit {
		if generation, err := readLearningGeneration(manifestPath); err == nil {
			return FitResult{Generation: generation, Path: dir, Skipped: true}, nil
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return FitResult{}, err
	}
	metadataSource, err := store.metadataSource(ctx)
	if err != nil {
		return FitResult{}, err
	}
	metadataModel, metadataErr := librarylearn.BuildMetadataSource(ctx, metadataSource, librarylearn.MetadataOptions{Workers: options.Plan.FitWorkers, SVDDim: 64, SVDIterations: 4, MaxScratchBytes: fitScratchBudget(options.Plan.MaxRAM)})
	closeErr := metadataSource.Close()
	if err := errors.Join(metadataErr, closeErr); err != nil {
		return FitResult{}, err
	}
	dspSource, err := store.dspSource(ctx)
	if err != nil {
		return FitResult{}, err
	}
	dspStatistics, dspErr := librarylearn.BuildDSPStatistics(ctx, dspSource, librarylearn.DSPStatisticsOptions{Seed: options.Seed, Workers: options.Plan.FitWorkers, MaxSamplesPerFeature: 1024, MaxScratchBytes: dspStatsScratchBudget(options.Plan.MaxRAM), CorpusTracks: coverage.Tracks})
	closeErr = dspSource.Close()
	if err := errors.Join(dspErr, closeErr); err != nil {
		return FitResult{}, err
	}
	sampleIDs, err := store.diverseSample(ctx, dir, sampleLimit, options.Seed)
	if err != nil {
		return FitResult{}, err
	}
	training, err := store.loadSelectedVectors(ctx, sampleIDs)
	if err != nil {
		return FitResult{}, err
	}
	defer clearDenseVectors(training)
	generation := LearningGeneration{Version: 1, ID: id, CreatedAt: time.Now().UTC().Format(time.RFC3339), Snapshot: snapshot, Seed: options.Seed, TrainingSample: sampleIDs, Metadata: metadataModel, Coverage: coverage, VectorSpace: vectorSpace, DSPStatistics: dspStatistics}
	if len(training) >= 4 {
		clusters := options.Clusters
		if clusters <= 0 {
			clusters = int(math.Sqrt(float64(len(training)) / 2))
			clusters = max(2, min(256, clusters))
		}
		clusters = min(clusters, len(training))
		checkpointPath := filepath.Join(dir, "kmeans-checkpoint.json")
		checkpoint, err := readSphericalCheckpoint(checkpointPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return FitResult{}, err
		}
		model, err := librarylearn.FitSpherical(ctx, training, librarylearn.SphericalOptions{Clusters: clusters, BatchSize: min(1024, len(training)), LogicalBlock: 64, MaxEpochs: 20, Workers: options.Plan.FitWorkers, Seed: options.Seed, Tolerance: 1e-5, MaxScratchBytes: fitScratchBudget(options.Plan.MaxRAM), InputGeneration: snapshot.Generation}, checkpoint, func(value librarylearn.SphericalCheckpoint) error {
			return writeSphericalCheckpoint(checkpointPath, value)
		})
		if err != nil {
			return FitResult{}, err
		}
		vectorSource, err := store.vectorSource(ctx)
		if err != nil {
			return FitResult{}, err
		}
		assignmentName, assignErr := writeAssignmentStore(ctx, dir, vectorSource, model, options.Plan.FitWorkers)
		closeErr := vectorSource.Close()
		if err := errors.Join(assignErr, closeErr); err != nil {
			return FitResult{}, err
		}
		generation.Spherical, generation.AssignmentStore = &model, assignmentName
	}
	if coverage.MERT > 0 {
		rows, err := store.searchSource(ctx)
		if err != nil {
			return FitResult{}, err
		}
		indexRoot := filepath.Join(s.dir, "generations", "indexes")
		indexBudget := indexScratchBudget(options.Plan.MaxRAM)
		indexDir, indexManifest, buildErr := librarysearch.Build(ctx, rows, librarysearch.BuildOptions{Root: indexRoot, SourceGeneration: snapshot.Generation, Contract: vectorSpaceContract(generation.VectorSpace), Dimension: vectorDimension, ShardRows: indexShardRows(vectorDimension, indexBudget), Workers: options.Plan.IndexWorkers, MaxScratchBytes: indexBudget})
		closeErr := rows.Close()
		if err := errors.Join(buildErr, closeErr); err != nil {
			return FitResult{}, err
		}
		manager, err := librarysearch.OpenManager(ctx, indexRoot)
		if err != nil {
			return FitResult{}, err
		}
		if err := manager.Activate(ctx, indexDir); err != nil {
			_ = manager.Close()
			return FitResult{}, err
		}
		_ = manager.Close()
		generation.Index = &indexManifest
	}
	if err := writeLearningGeneration(dir, generation); err != nil {
		return FitResult{}, err
	}
	if err := publishLearningPointer(filepath.Join(s.dir, "generations", "learning"), id); err != nil {
		return FitResult{}, err
	}
	return FitResult{Generation: generation, Path: dir}, nil
}

func learningID(snapshot CorpusSnapshot, options FitOptions) string {
	raw, _ := json.Marshal(struct {
		Version, Snapshot  string
		Seed               uint64
		Training, Clusters int
	}{"library-learning/v2-dspstats", snapshot.Generation, options.Seed, options.TrainingSample, options.Clusters})
	sum := sha256.Sum256(raw)
	return "learn-" + hex.EncodeToString(sum[:12])
}

func decodeFloat32Vector(raw []byte) ([]float32, error) {
	if len(raw)%4 != 0 || len(raw) == 0 {
		return nil, errors.New("invalid packed vector bytes")
	}
	vector := make([]float32, len(raw)/4)
	var norm float64
	for i := range vector {
		vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
		if math.IsNaN(float64(vector[i])) || math.IsInf(float64(vector[i]), 0) {
			return nil, errors.New("nonfinite vector")
		}
		norm += float64(vector[i]) * float64(vector[i])
	}
	if norm < .999 || norm > 1.001 {
		return nil, errors.New("non-normalized vector")
	}
	return vector, nil
}

func normalizeEntity(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}
func entityID(kind, value string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + normalizeEntity(value)))
	return kind + ":" + hex.EncodeToString(sum[:12])
}
func normalizedEntityIDs(kind string, values []string) []string {
	ids := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		if id := entityID(kind, value); !seen[id] {
			seen[id], ids = true, append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}
func tagValues(values []localaudio.TagValue) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value.Value) != "" {
			out = append(out, value.Value)
		}
	}
	return out
}

func vectorSpaceContract(space librarypack.VectorSpace) string {
	raw, _ := json.Marshal(space)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func writeLearningGeneration(dir string, generation LearningGeneration) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".learning-*.tmp")
	if err != nil {
		return err
	}
	path := temp.Name()
	defer os.Remove(path)
	encoder := json.NewEncoder(temp)
	if err = encoder.Encode(generation); err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(path, filepath.Join(dir, "learning.json")); err != nil {
		return err
	}
	return syncFileAndDirectory(filepath.Join(dir, "learning.json"), dir)
}

func readLearningGeneration(path string) (LearningGeneration, error) {
	var generation LearningGeneration
	file, err := os.Open(path)
	if err != nil {
		return generation, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&generation); err != nil || generation.Version != 1 || generation.ID == "" {
		return LearningGeneration{}, errors.New("library indexer: invalid learning generation")
	}
	return generation, nil
}

func publishLearningPointer(root, id string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(root, ".active-")
	if err != nil {
		return err
	}
	path := temp.Name()
	defer os.Remove(path)
	if _, err = temp.WriteString(id + "\n"); err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(path, filepath.Join(root, "active")); err != nil {
		return err
	}
	return syncFileAndDirectory(filepath.Join(root, "active"), root)
}

func (s *State) ActiveLearning() (LearningGeneration, error) {
	root := filepath.Join(s.dir, "generations", "learning")
	raw, err := os.ReadFile(filepath.Join(root, "active"))
	if err != nil {
		return LearningGeneration{}, err
	}
	id := strings.TrimSpace(string(raw))
	if id == "" || filepath.Base(id) != id {
		return LearningGeneration{}, errors.New("library indexer: invalid active learning pointer")
	}
	return readLearningGeneration(filepath.Join(root, id, "learning.json"))
}

func (s *State) TrackVector(ctx context.Context, trackID string) ([]float32, error) {
	var raw []byte
	if err := s.reader.QueryRowContext(ctx, `SELECT v.vector FROM mert_results v JOIN files f ON f.id=v.file_id AND f.source_revision=v.source_revision WHERE v.file_id=? AND f.status='present' ORDER BY v.rowid DESC LIMIT 1`, trackID).Scan(&raw); err != nil {
		return nil, err
	}
	return decodeFloat32Vector(raw)
}

func (s *State) SearchNeighbors(ctx context.Context, trackID string, limit, workers int) ([]librarysearch.Hit, error) {
	vector, err := s.TrackVector(ctx, trackID)
	if err != nil {
		return nil, err
	}
	defer clear(vector)
	manager, err := librarysearch.OpenManager(ctx, filepath.Join(s.dir, "generations", "indexes"))
	if err != nil {
		return nil, err
	}
	defer manager.Close()
	handle, err := manager.Pin()
	if err != nil {
		return nil, err
	}
	defer handle.Release()
	return handle.Search(ctx, librarysearch.Query{Vector: vector, Limit: limit, Workers: workers, Exclude: map[string]struct{}{trackID: {}}})
}

func (s *State) ExportPack(ctx context.Context, output string) (librarypack.Manifest, error) {
	generation, err := s.ActiveLearning()
	if err != nil {
		return librarypack.Manifest{}, err
	}
	assignmentCursor, err := openGenerationAssignments(ctx, filepath.Join(s.dir, "generations", "learning", generation.ID), generation.AssignmentStore)
	if err != nil {
		return librarypack.Manifest{}, err
	}
	if assignmentCursor != nil {
		defer assignmentCursor.Close()
	}
	trackSource, err := openFrozenPackSource(ctx, generation.Snapshot.Path, assignmentCursor, generation.Assignments)
	if err != nil {
		return librarypack.Manifest{}, err
	}
	defer trackSource.Close()
	portableLearning, err := json.Marshal(struct {
		Version        int                          `json:"version"`
		Seed           uint64                       `json:"seed"`
		TrainingSample []string                     `json:"trainingSample"`
		Metadata       librarylearn.MetadataModel   `json:"metadata"`
		Spherical      *librarylearn.SphericalModel `json:"spherical,omitempty"`
	}{generation.Version, generation.Seed, generation.TrainingSample, generation.Metadata, generation.Spherical})
	if err != nil {
		return librarypack.Manifest{}, err
	}
	portableStatistics, err := json.Marshal(generation.DSPStatistics)
	if err != nil {
		return librarypack.Manifest{}, err
	}
	clusterGeneration := ""
	if generation.Spherical != nil {
		clusterGeneration = generation.ID + "-clusters"
	}
	statisticsGeneration := ""
	if generation.DSPStatistics.Generation != "" {
		statisticsGeneration = generation.DSPStatistics.Generation
	}
	clapSpace, clapModel, clapGeneration, err := clapVectorSpace(ctx, generation.Snapshot.Path, generation.ID)
	if err != nil {
		return librarypack.Manifest{}, err
	}
	return librarypack.WriteSource(ctx, output, librarypack.Pack{CreatedAt: time.Now().UTC(), CorpusGeneration: generation.Snapshot.Generation, MetadataGeneration: generation.ID + "-metadata", MERTGeneration: generation.ID + "-mert", CLAPGeneration: clapGeneration, ClusterGeneration: clusterGeneration, StatisticsGeneration: statisticsGeneration, MERT: generation.VectorSpace, CLAP: clapSpace, CLAPModel: clapModel, Learning: portableLearning, Statistics: portableStatistics}, trackSource, librarypack.DefaultLimits())
}

func clapVectorSpace(ctx context.Context, snapshotPath, generationID string) (librarypack.VectorSpace, *core.AudioModelIdentity, string, error) {
	store, err := openFrozenStore(snapshotPath)
	if err != nil {
		return librarypack.VectorSpace{}, nil, "", err
	}
	defer store.Close()
	rows, err := store.db.QueryContext(ctx, `SELECT c.data FROM files f JOIN jobs j ON j.file_id=f.id AND j.source_revision=f.source_revision AND j.kind='clap' AND j.state='completed' JOIN clap_results c ON c.file_id=f.id AND c.source_revision=f.source_revision AND c.contract=j.semantic_key WHERE f.status='present' AND length(c.vector)>0 ORDER BY f.id`)
	if err != nil {
		return librarypack.VectorSpace{}, nil, "", err
	}
	defer rows.Close()
	var first *CLAPRecord
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return librarypack.VectorSpace{}, nil, "", err
		}
		var record CLAPRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return librarypack.VectorSpace{}, nil, "", err
		}
		if first == nil {
			first = &record
		} else if first.Model != record.Model || first.Sampling != record.Sampling {
			return librarypack.VectorSpace{}, nil, "", errors.New("library indexer: incompatible CLAP models or sampling contracts in snapshot")
		}
	}
	if err := rows.Err(); err != nil {
		return librarypack.VectorSpace{}, nil, "", err
	}
	if first == nil {
		return librarypack.VectorSpace{}, nil, "", nil
	}
	record := *first
	space := librarypack.VectorSpace{Name: "library_clap", Dimension: record.Model.Dimension, DType: "float32", ByteOrder: "little", Normalized: true, Model: record.Model.Model, ModelRevision: record.Model.Revision, GraphSHA256: record.Model.Weights, Decoder: "pinned-ffmpeg", Preprocessing: record.Model.Preprocessing, Sampling: record.Sampling, Pooling: "duration-weighted-mean-l2/v1", Scope: "two-distributed-excerpts", Missingness: "absent-row"}
	return space, &record.Model, generationID + "-clap", nil
}
