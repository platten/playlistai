package librarymerge

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/platten/playlistai/internal/librarypack"
)

func testVectorSpace(revision string) librarypack.VectorSpace {
	return librarypack.VectorSpace{
		Name: "library_mert", Dimension: 2, DType: "float32", ByteOrder: "little", Normalized: true,
		Model: "MERT-v1-95M", ModelRevision: revision, GraphSHA256: strings.Repeat("a", 64), Decoder: "decoder/v1",
		Preprocessing: "mert-local/v1", Sampling: "balanced/v1", Pooling: "unique-duration/v1",
		Scope: "sampled_excerpts", Missingness: "absent rows have no vector",
	}
}

func writeTestPack(t *testing.T, dir, name, generation string, space librarypack.VectorSpace, tracks []librarypack.Track) string {
	t.Helper()
	path := filepath.Join(dir, name)
	mertGeneration := ""
	for _, track := range tracks {
		if len(track.MERT) > 0 {
			mertGeneration = "mert-" + generation
			break
		}
	}
	if _, err := librarypack.Write(context.Background(), path, librarypack.Pack{
		CorpusGeneration: "corpus-" + generation, MetadataGeneration: "metadata-" + generation,
		MERTGeneration: mertGeneration, MERT: space, Tracks: tracks,
	}, librarypack.DefaultLimits()); err != nil {
		t.Fatalf("write pack: %v", err)
	}
	return path
}

func TestCombineDeduplicatesTransitivelyEnrichesAndRebuilds(t *testing.T) {
	dir := t.TempDir()
	space := testVectorSpace("same")
	first := writeTestPack(t, dir, "first.paipack", "first", space, []librarypack.Track{
		{ID: "primary", Artist: "The Artist", Title: "A Long Song Title", ISRC: "US-AAA-26-00001", AcoustID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", RootAlias: "music", RelativePath: "one/song.flac", RawTags: json.RawMessage(`{"genre":"Rock","mood":"Calm"}`)},
		{ID: "same-id", Artist: "First", Title: "Unique One", RootAlias: "music", RelativePath: "same.flac", RawTags: json.RawMessage(`{"genre":"Jazz"}`), MERT: []float32{1, 0}},
		{ID: "same-path-one", Artist: "Path", Title: "One", RootAlias: "music", RelativePath: "duplicate.flac", RawTags: json.RawMessage(`{"genre":"Pop"}`), MERT: []float32{0, 1}},
	})
	second := writeTestPack(t, dir, "second.paipack", "second", space, []librarypack.Track{
		{ID: "bridge", Artist: "The Artist", Title: "A Long Song Title", Album: "Filled Album", AcoustID: "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE", MusicBrainzRecording: "11111111-2222-3333-4444-555555555555", RootAlias: "music", RelativePath: "two/song.flac", RawTags: json.RawMessage(`{"style":"Indie"}`), MERT: []float32{0.70710677, 0.70710677}},
		{ID: "same-id", Artist: "Second", Title: "Unique Two", RootAlias: "music", RelativePath: "same.flac", RawTags: json.RawMessage(`{"genre":"Folk"}`), MERT: []float32{-1, 0}},
		{ID: "same-path-two", Artist: "Path", Title: "Two", RootAlias: "music", RelativePath: "duplicate.flac", RawTags: json.RawMessage(`{"genre":"Electronic"}`), MERT: []float32{0, -1}},
		{ID: "tail", Artist: "Other", Title: "Metadata Link", MusicBrainzRecording: "11111111-2222-3333-4444-555555555555", RawTags: json.RawMessage(`{"genre":"Rock"}`)},
	})
	output := filepath.Join(dir, "merged.paipack")
	report, err := Combine(context.Background(), []string{first, second}, output, Options{Seed: 42, MaxRAM: 64 << 20, Workers: 2, TrainingSample: 10})
	if err != nil {
		t.Fatal(err)
	}
	if report.InputTracks != 7 || report.OutputTracks != 5 || report.RemovedDuplicates != 2 {
		t.Fatalf("counts = %+v", report)
	}
	if report.EvidenceMatches.AcoustID != 1 || report.EvidenceMatches.MusicBrainz != 1 {
		t.Fatalf("evidence = %+v", report.EvidenceMatches)
	}
	if report.AliasRewrites != 2 || report.TrackIDRewrites != 2 || report.Manifest.Coverage.MERT != 5 {
		t.Fatalf("rewrites/coverage = %+v", report)
	}
	if report.Manifest.ClusterGeneration == "" {
		t.Fatal("four retained vectors did not rebuild clusters")
	}

	manager, err := librarypack.OpenManager(context.Background(), filepath.Join(dir, "staged"), librarypack.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	staged, err := manager.Stage(context.Background(), output)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Discard(staged) }()
	source, err := staged.Generation().OpenTrackSource(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	tracks := map[string]librarypack.Track{}
	for {
		track, ok, err := source.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		tracks[track.ID] = track
	}
	primary := tracks["primary"]
	if primary.Album != "Filled Album" || primary.MusicBrainzRecording == "" || len(primary.MERT) != 2 || primary.RecordingIdentity != "musicbrainz:11111111-2222-3333-4444-555555555555" {
		t.Fatalf("enriched primary = %+v", primary)
	}
	var rawTags map[string]string
	if err := json.Unmarshal(primary.RawTags, &rawTags); err != nil || rawTags["genre"] != "Rock" || rawTags["mood"] != "Calm" || rawTags["style"] != "Indie" {
		t.Fatalf("merged raw tags = %s (%v)", primary.RawTags, err)
	}
	if _, exists := tracks["same-id"]; exists {
		t.Fatal("colliding track ID was not rewritten for every recording group")
	}
	if _, one := tracks["same-path-one"]; !one {
		t.Fatal("first same-path recording was incorrectly deduplicated")
	}
	if _, two := tracks["same-path-two"]; !two {
		t.Fatal("second same-path recording was incorrectly deduplicated")
	}
	if len(report.Manifest.RootAliases) != 2 || report.Manifest.RootAliases[0] == "music" || report.Manifest.RootAliases[1] == "music" {
		t.Fatalf("root aliases = %v", report.Manifest.RootAliases)
	}
	for _, track := range tracks {
		if len(track.MERT) > 0 && track.Cluster == nil {
			t.Fatalf("vector track %q lacks rebuilt cluster", track.ID)
		}
	}
	learning, err := staged.Generation().Learning(context.Background())
	if err != nil || !bytes.Contains(learning, []byte(`"rock"`)) || bytes.Contains(learning, []byte(`"calm"`)) || bytes.Contains(learning, []byte(`"indie"`)) {
		t.Fatalf("metadata model did not use genres only: %s (%v)", learning, err)
	}
}

func TestCombineSkipsIdenticalPackAndIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	pack := writeTestPack(t, dir, "source.paipack", "source", librarypack.VectorSpace{}, []librarypack.Track{{ID: "one", Artist: "A", Title: "B", RawTags: json.RawMessage(`{"genre":"Rock"}`)}})
	firstOutput, secondOutput := filepath.Join(dir, "first.paipack"), filepath.Join(dir, "second.paipack")
	first, err := Combine(context.Background(), []string{pack, pack}, firstOutput, Options{Seed: 7, MaxRAM: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Combine(context.Background(), []string{pack, pack}, secondOutput, Options{Seed: 7, MaxRAM: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if first.SkippedIdenticalPacks != 1 || first.OutputTracks != 1 || first.Manifest.PackID != second.Manifest.PackID || !reflect.DeepEqual(first.Manifest.Files, second.Manifest.Files) {
		t.Fatalf("non-deterministic repeated-pack merge:\nfirst=%+v\nsecond=%+v", first, second)
	}
}

func TestCombineRejectsIncompatibleVectorsWithoutReplacingOutput(t *testing.T) {
	dir := t.TempDir()
	first := writeTestPack(t, dir, "first.paipack", "first", testVectorSpace("one"), []librarypack.Track{{ID: "one", Artist: "A", Title: "One", MERT: []float32{1, 0}}})
	second := writeTestPack(t, dir, "second.paipack", "second", testVectorSpace("two"), []librarypack.Track{{ID: "two", Artist: "B", Title: "Two", MERT: []float32{0, 1}}})
	output := filepath.Join(dir, "existing.paipack")
	if err := os.WriteFile(output, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Combine(context.Background(), []string{first, second}, output, Options{MaxRAM: 64 << 20}); err == nil || !strings.Contains(err.Error(), "incompatible MERT") {
		t.Fatalf("error = %v", err)
	}
	raw, err := os.ReadFile(output)
	if err != nil || string(raw) != "keep me" {
		t.Fatalf("existing output changed: %q (%v)", raw, err)
	}
}

func TestCombineRejectsOutputThatIsAnInput(t *testing.T) {
	dir := t.TempDir()
	first := writeTestPack(t, dir, "first.paipack", "first", librarypack.VectorSpace{}, []librarypack.Track{{ID: "one", Artist: "A", Title: "One"}})
	second := writeTestPack(t, dir, "second.paipack", "second", librarypack.VectorSpace{}, []librarypack.Track{{ID: "two", Artist: "B", Title: "Two"}})
	if _, err := Combine(context.Background(), []string{first, second}, first, Options{MaxRAM: 64 << 20}); err == nil || !strings.Contains(err.Error(), "resolves to input") {
		t.Fatalf("error = %v", err)
	}
}

func TestCombineUsesCorroboratedTaggedFingerprintAndValidIdentifiersOnly(t *testing.T) {
	dir := t.TempDir()
	fingerprintValue := "AQADtNQYhYkYnGhw7Xembedded"
	digest := sha256.Sum256([]byte(fingerprintValue))
	fingerprint := &librarypack.AudioFingerprint{
		Contract: "acoustid-chromaprint-tag/v1;algorithm=1", Format: "acoustid-chromaprint-base64", Algorithm: 1,
		Fingerprint: fingerprintValue, FingerprintSHA256: hex.EncodeToString(digest[:]), Scope: "embedded_tag", DecoderRuntimeID: "embedded_tag",
	}
	first := writeTestPack(t, dir, "first.paipack", "first", librarypack.VectorSpace{}, []librarypack.Track{
		{ID: "source", Artist: "The Artist", Title: "A Long Song Title", DurationMilliseconds: 180_000, DurationReliable: true, DurationProvenance: "tag", AudioFingerprint: fingerprint},
		{ID: "invalid-one", Artist: "Invalid", Title: "One", AcoustID: "vendor-value"},
	})
	second := writeTestPack(t, dir, "second.paipack", "second", librarypack.VectorSpace{}, []librarypack.Track{
		{ID: "match", Artist: "The Artist", Title: "A Long Song Title!", DurationMilliseconds: 181_000, DurationReliable: true, DurationProvenance: "tag", AudioFingerprint: fingerprint},
		{ID: "far-duration", Artist: "The Artist", Title: "A Long Song Title", DurationMilliseconds: 240_000, DurationReliable: true, DurationProvenance: "tag", AudioFingerprint: fingerprint},
		{ID: "uncorroborated", Artist: "Different", Title: "Unrelated", AudioFingerprint: fingerprint},
		{ID: "invalid-two", Artist: "Invalid", Title: "Two", AcoustID: "vendor-value"},
	})
	report, err := Combine(context.Background(), []string{first, second}, filepath.Join(dir, "merged.paipack"), Options{MaxRAM: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if report.OutputTracks != 5 || report.RemovedDuplicates != 1 || report.EvidenceMatches.Fingerprint != 1 || report.EvidenceMatches.AcoustID != 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestCanceledCombinePreservesExistingOutput(t *testing.T) {
	dir := t.TempDir()
	first := writeTestPack(t, dir, "first.paipack", "first", librarypack.VectorSpace{}, []librarypack.Track{{ID: "one", Artist: "A", Title: "One"}})
	second := writeTestPack(t, dir, "second.paipack", "second", librarypack.VectorSpace{}, []librarypack.Track{{ID: "two", Artist: "B", Title: "Two"}})
	output := filepath.Join(dir, "existing.paipack")
	if err := os.WriteFile(output, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Combine(ctx, []string{first, second}, output, Options{MaxRAM: 64 << 20}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	raw, err := os.ReadFile(output)
	if err != nil || string(raw) != "keep me" {
		t.Fatalf("existing output changed: %q (%v)", raw, err)
	}
}

func TestCombineRejectsVersionFourWithRebuildGuidance(t *testing.T) {
	dir := t.TempDir()
	current := writeTestPack(t, dir, "current.paipack", "current", librarypack.VectorSpace{}, []librarypack.Track{{ID: "one", Artist: "A", Title: "One"}})
	versionFour := filepath.Join(dir, "version-four.paipack")
	rewritePackVersion(t, current, versionFour, 4)
	_, err := Combine(context.Background(), []string{versionFour, current}, filepath.Join(dir, "merged.paipack"), Options{MaxRAM: 64 << 20})
	if err == nil || !strings.Contains(err.Error(), "rebuild it with the current playlist-indexer") {
		t.Fatalf("error = %v", err)
	}
}

func rewritePackVersion(t *testing.T, source, target string, version int) {
	t.Helper()
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	zr, err := zstd.NewReader(input)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	output, err := os.Create(target)
	if err != nil {
		t.Fatal(err)
	}
	zw, err := zstd.NewWriter(output)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(zw)
	tr := tar.NewReader(zr)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == librarypack.ManifestName {
			var manifest librarypack.Manifest
			if err := json.Unmarshal(raw, &manifest); err != nil {
				t.Fatal(err)
			}
			manifest.Version = version
			raw, err = json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
		}
		header.Size = int64(len(raw))
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(raw); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(tw.Close(), zw.Close(), output.Close()); err != nil {
		t.Fatal(err)
	}
}
