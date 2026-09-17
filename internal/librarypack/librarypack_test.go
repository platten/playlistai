package librarypack

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func fixturePack(generation string) Pack {
	return Pack{
		CreatedAt:          time.Unix(1_700_000_000, 0),
		CorpusGeneration:   "corpus-" + generation,
		MetadataGeneration: "metadata-" + generation,
		MERTGeneration:     "mert-" + generation,
		MERT: VectorSpace{
			Name: "library_mert", Dimension: 2, DType: "float32", ByteOrder: "little", Normalized: true,
			Model: "MERT-v1-95M", ModelRevision: "revision", GraphSHA256: strings.Repeat("a", 64),
			Decoder: "decoder/v1", Preprocessing: "mert-local/v1", Sampling: "balanced/v1",
			Pooling: "unique-duration/v1", Scope: "sampled_excerpts", Missingness: "absent rows have no vector",
		},
		Tracks: []Track{
			{ID: "local:main:z", Artist: "東京", Title: "夜明け", RawTags: json.RawMessage(`{"genre":["電子音楽"]}`), Missingness: json.RawMessage(`{"dsp":"unsupported"}`), RootAlias: "music-main", RelativePath: "東京/夜明け.flac"},
			{ID: "local:main:a", Artist: "Artist", Title: "Alpha", Album: "Album", RawTags: json.RawMessage(`{"genre":["R&B"]}`), DSP: json.RawMessage(`{"sample_peak_dbfs":-1.2}`), Missingness: json.RawMessage(`{}`), MERT: []float32{1, 0}},
			{ID: "local:main:b", Artist: "AC/DC", Title: "Beta", Failure: "", Unsupported: "", MERT: []float32{0, 1}},
		},
	}
}

func writeFixture(t *testing.T, name, generation string) (string, Manifest) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	manifest, err := Write(context.Background(), path, fixturePack(generation), Limits{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	return path, manifest
}

func TestWriteStageActivateRoundTripAndCanonicalOrdering(t *testing.T) {
	archive, written := writeFixture(t, "library.paipack", "one")
	manager, err := OpenManager(context.Background(), t.TempDir(), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	staged, err := manager.Stage(context.Background(), archive)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if staged.Manifest().PackID != written.PackID || !validHash(staged.PackSHA256()) {
		t.Fatal("staged identity missing")
	}
	if err := manager.Activate(context.Background(), staged); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	lease, err := manager.Pin()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	g := lease.Generation()
	rows, err := g.List(context.Background(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	gotIDs := []string{rows[0].ID, rows[1].ID, rows[2].ID}
	if !reflect.DeepEqual(gotIDs, []string{"local:main:a", "local:main:b", "local:main:z"}) {
		t.Fatalf("noncanonical order: %v", gotIDs)
	}
	if rows[2].RelativePath != "東京/夜明け.flac" || strings.Contains(rows[2].RelativePath, t.TempDir()) {
		t.Fatalf("unsafe path exported: %+v", rows[2])
	}
	vector, ok, err := g.Vector(context.Background(), "local:main:b")
	if err != nil || !ok || !reflect.DeepEqual(vector, []float32{0, 1}) {
		t.Fatalf("Vector = %v %v %v", vector, ok, err)
	}
	if _, ok, err := g.Vector(context.Background(), "local:main:z"); err != nil || ok {
		t.Fatalf("missing vector = %v %v", ok, err)
	}

	// The active record is sufficient to reopen the same verified generation.
	lease.Release()
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenManager(context.Background(), manager.root, Limits{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	reopenedLease, err := reopened.Pin()
	if err != nil {
		t.Fatal(err)
	}
	defer reopenedLease.Release()
	if reopenedLease.Generation().Manifest().PackID != written.PackID {
		t.Fatal("reopened another generation")
	}
}

func TestSemanticIdentityAndFilesAreStableAcrossInputOrder(t *testing.T) {
	pack := fixturePack("stable")
	first, firstManifest := writePackAt(t, "first.paipack", pack)
	for left, right := 0, len(pack.Tracks)-1; left < right; left, right = left+1, right-1 {
		pack.Tracks[left], pack.Tracks[right] = pack.Tracks[right], pack.Tracks[left]
	}
	second, secondManifest := writePackAt(t, "second.paipack", pack)
	if firstManifest.PackID != secondManifest.PackID || !reflect.DeepEqual(fileEntriesByName(firstManifest.Files), fileEntriesByName(secondManifest.Files)) {
		t.Fatal("semantic output depends on input completion order")
	}
	firstBytes, _ := os.ReadFile(first)
	secondBytes, _ := os.ReadFile(second)
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("canonical writer produced different archive bytes")
	}
}

func writePackAt(t *testing.T, name string, pack Pack) (string, Manifest) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	manifest, err := Write(context.Background(), path, pack, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	return path, manifest
}

func TestWriterRejectsUnsafePathsOversizedRecordsAndFakeVectors(t *testing.T) {
	for name, mutate := range map[string]func(*Pack){
		"absolute path":    func(p *Pack) { p.Tracks[0].RelativePath = "/mnt/music/song.flac" },
		"traversal":        func(p *Pack) { p.Tracks[0].RelativePath = "../song.flac" },
		"zero vector":      func(p *Pack) { p.Tracks[1].MERT = []float32{0, 0} },
		"nonfinite vector": func(p *Pack) { p.Tracks[1].MERT = []float32{float32(math.NaN()), 0} },
	} {
		t.Run(name, func(t *testing.T) {
			pack := fixturePack("bad")
			mutate(&pack)
			if _, err := Write(context.Background(), filepath.Join(t.TempDir(), "bad.paipack"), pack, Limits{}); err == nil {
				t.Fatal("invalid pack accepted")
			}
		})
	}
	pack := fixturePack("large")
	pack.Tracks[0].Artist = strings.Repeat("x", 128)
	if _, err := Write(context.Background(), filepath.Join(t.TempDir(), "large.paipack"), pack, Limits{MaxRecordBytes: 64}); err == nil {
		t.Fatal("oversized record accepted")
	}
}

func TestStageRejectsChecksumCorruptionAndValidlyChecksummedFakeVector(t *testing.T) {
	archive, _ := writeFixture(t, "valid.paipack", "corrupt")
	entries := readArchive(t, archive)
	entries[MERTVectorsName][len(entries[MERTVectorsName])-1] ^= 0xff
	corrupt := filepath.Join(t.TempDir(), "corrupt.paipack")
	writeRawArchive(t, corrupt, entries, nil)
	manager, _ := OpenManager(context.Background(), t.TempDir(), Limits{})
	if staged, err := manager.Stage(context.Background(), corrupt); err == nil {
		_ = manager.Discard(staged)
		t.Fatal("checksum corruption accepted")
	}

	entries = readArchive(t, archive)
	for offset := 32; offset < 40; offset++ {
		entries[MERTVectorsName][offset] = 0
	}
	var manifest Manifest
	if err := json.Unmarshal(entries[ManifestName], &manifest); err != nil {
		t.Fatal(err)
	}
	for i := range manifest.Files {
		if manifest.Files[i].Name == MERTVectorsName {
			sum := sha256.Sum256(entries[MERTVectorsName])
			manifest.Files[i].SHA256 = hex.EncodeToString(sum[:])
		}
	}
	manifest.PackID = semanticID(manifest)
	entries[ManifestName], _ = json.MarshalIndent(manifest, "", "  ")
	fake := filepath.Join(t.TempDir(), "fake.paipack")
	writeRawArchive(t, fake, entries, nil)
	if staged, err := manager.Stage(context.Background(), fake); err == nil {
		_ = manager.Discard(staged)
		t.Fatal("checksummed zero vector accepted")
	}
}

func TestStageRejectsTraversalAndArchiveLimits(t *testing.T) {
	archive, _ := writeFixture(t, "valid.paipack", "limits")
	entries := readArchive(t, archive)
	traversal := filepath.Join(t.TempDir(), "traversal.paipack")
	writeRawArchive(t, traversal, entries, []rawMember{{name: "../escaped", data: []byte("bad")}})
	root := t.TempDir()
	manager, _ := OpenManager(context.Background(), root, Limits{MaxMembers: 4})
	if staged, err := manager.Stage(context.Background(), traversal); err == nil {
		_ = manager.Discard(staged)
		t.Fatal("traversal accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "escaped")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("traversal created an outside file")
	}

	manager, _ = OpenManager(context.Background(), t.TempDir(), Limits{MaxMemberBytes: 64})
	if staged, err := manager.Stage(context.Background(), archive); err == nil {
		_ = manager.Discard(staged)
		t.Fatal("oversized member accepted")
	}
	manager, _ = OpenManager(context.Background(), t.TempDir(), Limits{MaxArchiveBytes: 8})
	if staged, err := manager.Stage(context.Background(), archive); err == nil {
		_ = manager.Discard(staged)
		t.Fatal("oversized archive accepted")
	}
}

func TestCancellationAndDiskFullLeaveDestinationUnchanged(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "library.paipack")
	old := []byte("previous complete pack")
	if err := os.WriteFile(destination, old, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Write(ctx, destination, fixturePack("cancel"), Limits{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Write error = %v", err)
	}
	got, _ := os.ReadFile(destination)
	if !bytes.Equal(got, old) {
		t.Fatal("canceled write replaced destination")
	}

	dir := t.TempDir()
	metadata, vectors := filepath.Join(dir, MetadataName), filepath.Join(dir, MERTVectorsName)
	if err := os.WriteFile(metadata, bytes.Repeat([]byte("m"), 1024), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vectors, bytes.Repeat([]byte("v"), 1024), 0o600); err != nil {
		t.Fatal(err)
	}
	err := writeArchive(context.Background(), &failingWriter{remaining: 100}, []byte(`{"manifest":true}`), metadata, vectors)
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("disk-full error = %v", err)
	}
}

type generatedTrackSource struct {
	rows   []Track
	index  int
	vector []float32
	err    error
	stopAt int
}

func (s *generatedTrackSource) Next(ctx context.Context) (Track, bool, error) {
	if err := ctx.Err(); err != nil {
		return Track{}, false, err
	}
	if s.stopAt > 0 && s.index == s.stopAt {
		return Track{}, false, s.err
	}
	if s.index == len(s.rows) {
		return Track{}, false, nil
	}
	row := s.rows[s.index]
	if len(row.MERT) > 0 {
		if s.vector == nil {
			s.vector = make([]float32, len(row.MERT))
		}
		copy(s.vector, row.MERT)
		row.MERT = s.vector // deliberately reused on the next call
	}
	s.index++
	return row, true, nil
}

func TestWriteSourceRoundTripPreservesClustersLearningAndReusedBuffers(t *testing.T) {
	cluster, alternative := 4, 2
	learning := json.RawMessage(`{"version":1,"metadata":{"version":"tfidf/v1"},"spherical":{"version":"spherical/v1","dimension":2,"clusters":5,"centroids":[1,0,0,1,1,0,0,1,1,0],"counts":[1,1,1,1,1]}}`)
	statistics := json.RawMessage(`{"version":"library-dsp-priority-quantiles/v1","generation":"dspstats-fixture","groups":[{"id":"dsp-a"}]}`)
	pack := fixturePack("stream")
	pack.Tracks = nil
	pack.ClusterGeneration = "clusters-stream"
	pack.StatisticsGeneration = "dspstats-fixture"
	rows := []Track{
		{ID: "local:main:a", Artist: "Artist", Title: "Alpha", MERT: []float32{1, 0}, Cluster: &cluster, ClusterScore: .91, Alternative: &alternative, AltScore: .73},
		{ID: "local:main:b", Artist: "Artist", Title: "Beta", MERT: []float32{0, 1}},
		{ID: "local:main:c", Artist: "Artist", Title: "Gamma", MERT: []float32{1, 0}},
	}
	pack.Learning = learning
	pack.Statistics = statistics
	archive := filepath.Join(t.TempDir(), "stream.paipack")
	manifest, err := WriteSource(context.Background(), archive, pack, &generatedTrackSource{rows: rows}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Coverage.Tracks != 3 || manifest.Coverage.MERT != 3 || manifest.ClusterGeneration != pack.ClusterGeneration {
		t.Fatalf("manifest=%+v", manifest)
	}
	manager, err := OpenManager(context.Background(), t.TempDir(), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	staged, err := manager.Stage(context.Background(), archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Activate(context.Background(), staged); err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Pin()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	track, ok, err := lease.Generation().Lookup(context.Background(), "local:main:a")
	if err != nil || !ok || track.Cluster == nil || *track.Cluster != cluster || track.Alternative == nil || *track.Alternative != alternative {
		t.Fatalf("track=%+v ok=%v err=%v", track, ok, err)
	}
	vector, ok, err := lease.Generation().Vector(context.Background(), "local:main:a")
	if err != nil || !ok || !reflect.DeepEqual(vector, []float32{1, 0}) {
		t.Fatalf("first reused vector=%v ok=%v err=%v", vector, ok, err)
	}
	gotLearning, err := lease.Generation().Learning(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var normalized struct {
		Version  int `json:"version"`
		Metadata struct {
			Version string
		} `json:"metadata"`
		Spherical *struct {
			Clusters int
		} `json:"spherical"`
	}
	if err := json.Unmarshal(gotLearning, &normalized); err != nil || normalized.Version != 1 || normalized.Metadata.Version != "tfidf/v1" || normalized.Spherical == nil || normalized.Spherical.Clusters != 5 {
		t.Fatalf("normalized learning=%s decoded=%+v err=%v", gotLearning, normalized, err)
	}
	var legacyRows int
	if err := lease.Generation().db.QueryRow("SELECT COUNT(*) FROM pack_info WHERE key='learning_json'").Scan(&legacyRows); err != nil || legacyRows != 0 {
		t.Fatalf("monolithic learning payload remains: rows=%d err=%v", legacyRows, err)
	}
	gotStatistics, ok, err := lease.Generation().Statistics(context.Background())
	var normalizedStatistics struct {
		Version    string `json:"version"`
		Generation string `json:"generation"`
		Groups     []struct {
			ID string `json:"id"`
		} `json:"groups"`
	}
	decodeErr := json.Unmarshal(gotStatistics, &normalizedStatistics)
	if err != nil || decodeErr != nil || !ok || normalizedStatistics.Version != "library-dsp-priority-quantiles/v1" || normalizedStatistics.Generation != "dspstats-fixture" || len(normalizedStatistics.Groups) != 1 || normalizedStatistics.Groups[0].ID != "dsp-a" {
		t.Fatalf("statistics=%s decoded=%+v ok=%v err=%v decode=%v", gotStatistics, normalizedStatistics, ok, err, decodeErr)
	}
}

func TestWriteSourceRejectsUnorderedRowsAndPreservesPriorPackOnFailures(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "library.paipack")
	old := []byte("prior complete pack")
	if err := os.WriteFile(destination, old, 0o600); err != nil {
		t.Fatal(err)
	}
	pack := fixturePack("stream-failure")
	pack.Tracks = nil
	unordered := &generatedTrackSource{rows: []Track{{ID: "local:z", Artist: "A", Title: "Z"}, {ID: "local:a", Artist: "A", Title: "A"}}}
	if _, err := WriteSource(context.Background(), destination, pack, unordered, Limits{}); err == nil {
		t.Fatal("unordered stream accepted")
	}
	if got, _ := os.ReadFile(destination); !bytes.Equal(got, old) {
		t.Fatal("unordered stream replaced prior pack")
	}
	for name, failure := range map[string]error{"canceled": context.Canceled, "disk full": syscall.ENOSPC} {
		t.Run(name, func(t *testing.T) {
			source := &generatedTrackSource{rows: []Track{{ID: "local:a", Artist: "A", Title: "A"}, {ID: "local:b", Artist: "B", Title: "B"}}, stopAt: 1, err: failure}
			if _, err := WriteSource(context.Background(), destination, pack, source, Limits{}); !errors.Is(err, failure) {
				t.Fatalf("error=%v want=%v", err, failure)
			}
			if got, _ := os.ReadFile(destination); !bytes.Equal(got, old) {
				t.Fatal("failed stream replaced prior pack")
			}
		})
	}
}

type failingWriter struct{ remaining int }

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.remaining <= 0 {
		return 0, syscall.ENOSPC
	}
	if len(p) > w.remaining {
		n := w.remaining
		w.remaining = 0
		return n, syscall.ENOSPC
	}
	w.remaining -= len(p)
	return len(p), nil
}

func TestPinnedReaderSurvivesReplacementAndRemoval(t *testing.T) {
	firstArchive, _ := writeFixture(t, "first.paipack", "first")
	secondPack := fixturePack("second")
	secondPack.Tracks[1].Title = "Replacement"
	secondArchive, _ := writePackAt(t, "second.paipack", secondPack)
	root := t.TempDir()
	manager, err := OpenManager(context.Background(), root, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	activate := func(archive string) {
		staged, err := manager.Stage(context.Background(), archive)
		if err != nil {
			t.Fatal(err)
		}
		if err := manager.Activate(context.Background(), staged); err != nil {
			t.Fatal(err)
		}
	}
	activate(firstArchive)
	oldLease, _ := manager.Pin()
	oldDir := oldLease.Generation().dir
	activate(secondArchive)
	newLease, _ := manager.Pin()
	newDir := newLease.Generation().dir
	oldTrack, ok, err := oldLease.Generation().Lookup(context.Background(), "local:main:a")
	if err != nil || !ok || oldTrack.Title != "Alpha" {
		t.Fatalf("old pin changed: %+v %v %v", oldTrack, ok, err)
	}
	newTrack, _, _ := newLease.Generation().Lookup(context.Background(), "local:main:a")
	if newTrack.Title != "Replacement" {
		t.Fatalf("new pin = %+v", newTrack)
	}
	if _, err := os.Stat(oldDir); err != nil {
		t.Fatal("old generation removed beneath reader")
	}
	oldLease.Release()
	if _, err := os.Stat(oldDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retired generation not cleaned: %v", err)
	}
	if err := manager.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Pin(); !errors.Is(err, ErrNoActiveGeneration) {
		t.Fatalf("Pin after remove = %v", err)
	}
	if _, ok, err := newLease.Generation().Lookup(context.Background(), "local:main:a"); err != nil || !ok {
		t.Fatalf("removed pinned generation unusable: %v %v", ok, err)
	}
	if _, err := os.Stat(newDir); err != nil {
		t.Fatal("removed generation deleted beneath reader")
	}
	newLease.Release()
	if _, err := os.Stat(newDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed generation not cleaned: %v", err)
	}
}

func TestManagerRejectsOverlappingMutationsAndCanceledActivation(t *testing.T) {
	archive, _ := writeFixture(t, "library.paipack", "mutation")
	manager, _ := OpenManager(context.Background(), t.TempDir(), Limits{})
	defer manager.Close()
	staged, err := manager.Stage(context.Background(), archive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Stage(context.Background(), archive); !errors.Is(err, ErrMutationInProgress) {
		t.Fatalf("overlapping Stage = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stagedDir := staged.dir
	if err := manager.Activate(ctx, staged); !errors.Is(err, context.Canceled) {
		t.Fatalf("Activate = %v", err)
	}
	if _, err := manager.Pin(); !errors.Is(err, ErrNoActiveGeneration) {
		t.Fatalf("canceled activation published: %v", err)
	}
	if _, err := os.Stat(stagedDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled staging not cleaned: %v", err)
	}
}

func TestIdenticalStagePinsActiveGenerationForDerivativeRebuild(t *testing.T) {
	archive, manifest := writeFixture(t, "library.paipack", "same-active")
	manager, err := OpenManager(context.Background(), t.TempDir(), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	staged, err := manager.Stage(context.Background(), archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Activate(context.Background(), staged); err != nil {
		t.Fatal(err)
	}
	identical, err := manager.Stage(context.Background(), archive)
	if err != nil {
		t.Fatal(err)
	}
	if identical.Generation() == nil || identical.Generation().Manifest().PackID != manifest.PackID {
		t.Fatal("identical stage did not pin its active generation")
	}
	if err := manager.Discard(identical); err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Pin()
	if err != nil {
		t.Fatalf("discarding identical stage closed active generation: %v", err)
	}
	defer lease.Release()
	if _, ok, err := lease.Generation().Lookup(context.Background(), "local:main:a"); err != nil || !ok {
		t.Fatalf("active generation after identical discard: ok=%v err=%v", ok, err)
	}
}

type rawMember struct {
	name string
	data []byte
}

func readArchive(t *testing.T, name string) map[string][]byte {
	t.Helper()
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := zstd.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	out := map[string][]byte{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name], err = io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func writeRawArchive(t *testing.T, name string, entries map[string][]byte, extra []rawMember) {
	t.Helper()
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	zw, err := zstd.NewWriter(f, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(zw)
	order := []string{ManifestName, MetadataName, MERTVectorsName}
	for _, key := range order {
		data := entries[key]
		if err := tw.WriteHeader(&tar.Header{Name: key, Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	for _, member := range extra {
		if err := tw.WriteHeader(&tar.Header{Name: member.name, Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(member.data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(member.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestVectorHeaderUsesLittleEndianFloat32(t *testing.T) {
	archive, _ := writeFixture(t, "vectors.paipack", "encoding")
	vectors := readArchive(t, archive)[MERTVectorsName]
	if string(vectors[:8]) != string(vectorMagic[:]) || binary.LittleEndian.Uint32(vectors[12:16]) != 2 {
		t.Fatal("invalid vector header")
	}
	if math.Float32frombits(binary.LittleEndian.Uint32(vectors[32:36])) != 1 {
		t.Fatal("vector is not little-endian float32")
	}
}

func TestMetadataOnlyPackPreservesExplicitMissingness(t *testing.T) {
	pack := fixturePack("metadata-only")
	pack.MERT = VectorSpace{}
	pack.MERTGeneration = ""
	for i := range pack.Tracks {
		pack.Tracks[i].MERT = nil
	}
	archive, manifest := writePackAt(t, "metadata-only.paipack", pack)
	if manifest.Coverage.MERT != 0 || manifest.MERT.Dimension != 0 {
		t.Fatalf("MERT coverage = %+v", manifest)
	}
	manager, _ := OpenManager(context.Background(), t.TempDir(), Limits{})
	defer manager.Close()
	staged, err := manager.Stage(context.Background(), archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Activate(context.Background(), staged); err != nil {
		t.Fatal(err)
	}
	lease, _ := manager.Pin()
	defer lease.Release()
	track, ok, err := lease.Generation().Lookup(context.Background(), "local:main:z")
	if err != nil || !ok || string(track.Missingness) != `{"dsp":"unsupported"}` || !reflect.DeepEqual(track.Capabilities, []string{"metadata", "local_path"}) {
		t.Fatalf("metadata-only track = %+v %v %v", track, ok, err)
	}
}

func TestConcurrentPinsReadOneGeneration(t *testing.T) {
	archive, manifest := writeFixture(t, "library.paipack", "concurrent")
	manager, _ := OpenManager(context.Background(), t.TempDir(), Limits{})
	defer manager.Close()
	staged, _ := manager.Stage(context.Background(), archive)
	if err := manager.Activate(context.Background(), staged); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errorsCh := make(chan error, 32)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, err := manager.Pin()
			if err != nil {
				errorsCh <- err
				return
			}
			defer lease.Release()
			if lease.Generation().Manifest().PackID != manifest.PackID {
				errorsCh <- errors.New("mixed generation")
			}
			if _, ok, err := lease.Generation().Vector(context.Background(), "local:main:a"); err != nil || !ok {
				errorsCh <- errors.New("concurrent vector read failed")
			}
		}()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Error(err)
	}
}

func TestManifestFileOrderingHelper(t *testing.T) {
	files := []File{{Name: "z"}, {Name: "a"}}
	got := fileEntriesByName(files)
	if !sort.SliceIsSorted(got, func(i, j int) bool { return got[i].Name < got[j].Name }) {
		t.Fatal("files not sorted")
	}
}
