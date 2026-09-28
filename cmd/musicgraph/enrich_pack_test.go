package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
)

func testClassifier() core.MusicClassifierEvidence {
	id := core.MusicClassifierIdentity{Model: "discogs-effnet", Revision: "1", WeightsSHA256: strings.Repeat("a", 64), MetadataSHA256: strings.Repeat("b", 64)}
	return core.MusicClassifierEvidence{Version: 1, Encoder: id, Preprocessing: "sample16k/v1", Runtime: "test", AudioSHA256: strings.Repeat("c", 64), Source: "fixture", SourceID: "recording", License: "CC0-1.0", Coverage: core.LibraryCLAPCoverage{CoveredSeconds: 10, Incomplete: true, PartialReason: "sample", Segments: []core.LibraryAudioInterval{{EndSeconds: 10}}}, Heads: []core.MusicClassifierHead{{Kind: "instrumentation", Model: id, Classes: []string{"piano"}, Scores: []float32{.8}}}}
}

func TestEnrichPackValidatesExactCatalogAndExportsIndexedEvidence(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	input := filepath.Join(dir, "input.paipack")
	output := filepath.Join(dir, "enriched.paipack")
	evidencePath := filepath.Join(dir, "evidence.jsonl")
	_, err := librarypack.Write(ctx, input, librarypack.Pack{CorpusGeneration: "fixture", MetadataGeneration: "fixture", Tracks: []librarypack.Track{{ID: "track", Artist: "Fixture", Title: "Track", DurationMilliseconds: 20000}}}, librarypack.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(content)
	catalogSHA := hex.EncodeToString(hash[:])
	row := classifierRow{CatalogSHA256: catalogSHA, TrackID: "track", ClassifierEvidence: []core.MusicClassifierEvidence{testClassifier()}, PreparationKey: strings.Repeat("d", 64)}
	writeRow := func() {
		t.Helper()
		b, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(evidencePath, append(b, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeRow()
	args := []string{"enrich-pack", "-input", input, "-output", output, "-classifiers", evidencePath, "-sha256", catalogSHA}
	if err = run(ctx, args, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	m, err := librarypack.OpenManager(ctx, filepath.Join(dir, "verify"), librarypack.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s, err := m.Stage(ctx, output)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Discard(s) }()
	if s.Manifest().Version != librarypack.IndexedFormatVersion {
		t.Fatal("output cannot be installed")
	}
	evidence, err := s.Generation().ClassifierEvidence(ctx, "track")
	if err != nil || len(evidence) != 1 || evidence[0].Heads[0].Scores[0] != .8 {
		t.Fatal(evidence, err)
	}
	inventory, err := inventoryFromPack(ctx, output, "fixture", "CC0-1.0", 0)
	if err != nil || !inventory.Tracks[0].Classifier {
		t.Fatal("coverage lost classifier", err)
	}
	if err = run(ctx, args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("existing output overwritten")
	}
	args[4] = filepath.Join(dir, "missing.paipack")
	row.TrackID = "unknown"
	writeRow()
	if err = run(ctx, args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("unknown track joined")
	}
	if _, err = os.Stat(args[4]); !os.IsNotExist(err) {
		t.Fatal("failed join published a pack")
	}
	row.CatalogSHA256 = strings.Repeat("0", 64)
	writeRow()
	if err = run(ctx, args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("different source catalog accepted")
	}
}
