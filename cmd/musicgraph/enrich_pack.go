package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/discoveryasset"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/musicgraph"
)

type classifierRow struct {
	CatalogSHA256      string                         `json:"catalogSha256"`
	TrackID            string                         `json:"trackId"`
	ClassifierEvidence []core.MusicClassifierEvidence `json:"classifierEvidence"`
	PreparationKey     string                         `json:"preparationKey"`
}

type classifierTrackSource struct {
	source  librarypack.TrackSource
	pending map[string][]core.MusicClassifierEvidence
}

func (s *classifierTrackSource) Next(ctx context.Context) (librarypack.Track, bool, error) {
	track, ok, err := s.source.Next(ctx)
	if err != nil {
		return track, ok, err
	}
	if !ok && len(s.pending) > 0 {
		return track, false, errors.New("classifier evidence references track IDs absent from the exact catalog")
	}
	if evidence, exists := s.pending[track.ID]; exists {
		track.ClassifierEvidence = append(track.ClassifierEvidence, evidence...)
		delete(s.pending, track.ID)
	}
	return track, ok, nil
}

func readClassifierRows(ctx context.Context, path, catalogSHA string) (map[string][]core.MusicClassifierEvidence, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, "", err
	}
	if !info.Mode().IsRegular() || info.Size() > musicgraph.MaxInventoryBytes {
		return nil, "", errors.New("classifier JSONL must be a bounded regular file")
	}
	h := sha256.New()
	limited := &io.LimitedReader{R: f, N: musicgraph.MaxInventoryBytes + 1}
	scanner := bufio.NewScanner(io.TeeReader(limited, h))
	scanner.Buffer(make([]byte, 4096), librarypack.DefaultLimits().MaxJSONBytes)
	out := map[string][]core.MusicClassifierEvidence{}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		var row classifierRow
		d := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		d.DisallowUnknownFields()
		if err := d.Decode(&row); err != nil {
			return nil, "", err
		}
		if d.Decode(new(any)) != io.EOF {
			return nil, "", errors.New("trailing classifier row data")
		}
		if row.CatalogSHA256 != catalogSHA || row.TrackID == "" || len(row.TrackID) > 1024 || len(row.ClassifierEvidence) == 0 || len(row.ClassifierEvidence) > 8 || out[row.TrackID] != nil {
			return nil, "", errors.New("classifier rows require the exact catalog SHA256 and unique catalog track IDs")
		}
		for _, evidence := range row.ClassifierEvidence {
			if err := evidence.Validate(); err != nil {
				return nil, "", err
			}
		}
		out[row.TrackID] = row.ClassifierEvidence
	}
	if err := scanner.Err(); err != nil {
		return nil, "", err
	}
	if limited.N == 0 {
		return nil, "", errors.New("classifier JSONL exceeds input size limit")
	}
	if len(out) == 0 {
		return nil, "", errors.New("classifier JSONL contains no evidence")
	}
	return out, hex.EncodeToString(h.Sum(nil)), ctx.Err()
}

func runEnrichPack(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("musicgraph enrich-pack", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "", "source .paipack")
	output := flags.String("output", "", "new indexed .paipack output")
	classifiers := flags.String("classifiers", "", "classifier preparation JSONL")
	expected := flags.String("sha256", "", "expected source catalog archive SHA256")
	installed := flags.Int64("installed-bytes", 0, "already installed additional prepared assets")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *input == "" || *output == "" || *classifiers == "" || *expected == "" {
		return errors.New("enrich-pack requires input, classifiers, source SHA256 and new output path")
	}
	if _, err := os.Lstat(*output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output must not exist")
	}
	if err := musicgraph.CheckAdditionalDataBudget(*installed); err != nil {
		return err
	}
	if *installed == musicgraph.MaxAdditionalInstalledBytes {
		return errors.New("no additional prepared-data capacity remains")
	}
	rows, digest, err := readClassifierRows(ctx, *classifiers, *expected)
	if err != nil {
		return err
	}
	count := len(rows)
	work, err := os.MkdirTemp(filepath.Dir(*output), ".classifier-pack-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	limits := librarypack.DefaultLimits()
	manager, err := librarypack.OpenManager(ctx, filepath.Join(work, "input"), limits)
	if err != nil {
		return err
	}
	defer manager.Close()
	staged, err := manager.Stage(ctx, *input)
	if err != nil {
		return err
	}
	defer func() { _ = manager.Discard(staged) }()
	g := staged.Generation()
	if g.PackSHA256() != *expected {
		return errors.New("source catalog archive SHA256 mismatch")
	}
	m := g.Manifest()
	learning, err := g.Learning(ctx)
	if err != nil {
		return err
	}
	statistics, _, err := g.Statistics(ctx)
	if err != nil {
		return err
	}
	created := time.Time{}
	if m.CreatedAt != "" {
		created, err = time.Parse(time.RFC3339Nano, m.CreatedAt)
		if err != nil {
			return err
		}
	}
	metadataHash := sha256.Sum256([]byte(m.MetadataGeneration + "/" + digest))
	pack := librarypack.Pack{CreatedAt: created, CorpusGeneration: m.CorpusGeneration, MetadataGeneration: "classifier-" + hex.EncodeToString(metadataHash[:]), MERTGeneration: m.MERTGeneration, CLAPGeneration: m.CLAPGeneration, ClusterGeneration: m.ClusterGeneration, StatisticsGeneration: m.StatisticsGeneration, MERT: m.MERT, CLAP: m.CLAP, CLAPModel: m.CLAPModel, Learning: learning, Statistics: statistics}
	source, err := g.OpenTrackSource(ctx)
	if err != nil {
		return err
	}
	defer source.Close()
	limits.MaxExpandedBytes = musicgraph.MaxAdditionalInstalledBytes - *installed
	limits.MaxArchiveBytes = limits.MaxExpandedBytes
	limits.MaxMemberBytes = limits.MaxExpandedBytes
	rawPath := filepath.Join(work, "derived.paipack")
	if _, err = librarypack.WriteSource(ctx, rawPath, pack, &classifierTrackSource{source: source, pending: rows}, limits); err != nil {
		return err
	}
	indexedPath := filepath.Join(work, "indexed.paipack")
	manifest, err := discoveryasset.BuildIndexedFromPack(ctx, rawPath, indexedPath, limits)
	if err != nil {
		return err
	}
	var sizes []int64
	for _, file := range manifest.Files {
		if file.Name != librarypack.IndexBundleName {
			sizes = append(sizes, file.Size)
		}
	}
	for _, file := range manifest.IndexFiles {
		sizes = append(sizes, file.Size)
	}
	manifestRaw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	sizes = append(sizes, int64(len(manifestRaw)+1))
	if err = musicgraph.CheckAdditionalDataBudget(*installed, sizes...); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Link(indexedPath, *output); err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(map[string]any{"packId": manifest.PackID, "classifierTracks": count, "sourceCatalogSha256": *expected, "evidenceSha256": digest})
}
