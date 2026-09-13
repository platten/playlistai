package mbindex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// BenchmarkBundleCompression uses a deterministic synthetic SQLite index, not a
// production MusicBrainz snapshot. Fixture creation is excluded from timings.
func BenchmarkBundleCompression(b *testing.B) {
	index := compressionFixture(b, 40_000)
	raw, err := os.ReadFile(index)
	if err != nil {
		b.Fatal(err)
	}
	for _, level := range []zstd.EncoderLevel{zstd.SpeedDefault, zstd.SpeedBetterCompression, zstd.SpeedBestCompression} {
		b.Run(level.String(), func(b *testing.B) {
			benchmarkCompression(b, raw, level, bundleWindow)
		})
	}
}

func BenchmarkBundleCompressionWindow(b *testing.B) {
	index := compressionFixture(b, 40_000)
	raw, err := os.ReadFile(index)
	if err != nil {
		b.Fatal(err)
	}
	for _, window := range []int{8 << 20, 32 << 20, bundleWindow} {
		b.Run(fmt.Sprintf("%dMiB", window>>20), func(b *testing.B) {
			benchmarkCompression(b, raw, zstd.SpeedBetterCompression, window)
		})
	}
}

func benchmarkCompression(b *testing.B, raw []byte, level zstd.EncoderLevel, window int) {
	b.Helper()
	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	var compressed int64
	for b.Loop() {
		out := &compressionCounter{}
		zw, err := zstd.NewWriter(out, zstd.WithEncoderLevel(level), zstd.WithEncoderConcurrency(2), zstd.WithWindowSize(window))
		if err != nil {
			b.Fatal(err)
		}
		if _, err = io.Copy(zw, bytes.NewReader(raw)); err != nil {
			b.Fatal(err)
		}
		if err = zw.Close(); err != nil {
			b.Fatal(err)
		}
		compressed = out.size
	}
	b.ReportMetric(float64(compressed), "compressed-bytes/op")
	b.ReportMetric(float64(compressed)/float64(len(raw)), "compressed/input")
}

type compressionCounter struct{ size int64 }

func (w *compressionCounter) Write(p []byte) (int, error) {
	w.size += int64(len(p))
	return len(p), nil
}

func TestPackageStreamingHashAndProgress(t *testing.T) {
	index := compressionFixture(t, 120)
	raw, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256(raw)
	dir := filepath.Join(t.TempDir(), "bundle")
	progress := map[string]BundleProgress{}
	m, err := PackageWithProgress(context.Background(), index, dir, 1024, func(update BundleProgress) {
		if previous := progress[update.Stage]; update.Done < previous.Done {
			t.Errorf("progress regressed: %+v -> %+v", previous, update)
		}
		progress[update.Stage] = update
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.Index.Size != int64(len(raw)) || m.Index.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("streaming checksum differs from input: %+v", m.Index)
	}
	for _, stage := range []string{"hash-index", "compress-index"} {
		if got := progress[stage]; got.Total != m.Index.Size || got.Done != got.Total {
			t.Fatalf("incomplete %s progress: %+v", stage, got)
		}
	}
	if _, err := VerifyBundle(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
}

func TestPackageCancellationDoesNotPublishManifest(t *testing.T) {
	index := compressionFixture(t, 120)
	dir := filepath.Join(t.TempDir(), "bundle")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	completed := false
	_, err := PackageWithProgress(ctx, index, dir, 1024, func(update BundleProgress) {
		if update.Stage == "hash-index" && update.Total > 0 && update.Done == update.Total {
			cancel()
		}
		if update.Stage == "compress-index" && update.Total > 0 && update.Done == update.Total {
			completed = true
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if completed {
		t.Fatal("canceled compression reported complete")
	}
	if _, err := os.Stat(filepath.Join(dir, "musicbrainz-manifest.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled package published a manifest: %v", err)
	}
}

func compressionFixture(tb testing.TB, count int) string {
	tb.Helper()
	index := filepath.Join(tb.TempDir(), "musicbrainz.sqlite")
	db, err := sql.Open("sqlite", index)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=OFF; PRAGMA synchronous=OFF;" + buildSchema); err != nil {
		tb.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		tb.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := []string{
		"INSERT INTO artists VALUES(?,?,?,?,?)",
		"INSERT INTO artist_aliases VALUES(?,?,?,?,?)",
		"INSERT INTO artist_tags VALUES(?,?,?,?)",
		"INSERT INTO recordings VALUES(?,?,?,?,?,?,?)",
		"INSERT INTO recording_artists VALUES(?,?,?,?,?,?)",
		"INSERT INTO recording_isrcs VALUES(?,?)",
		"INSERT INTO recording_tags VALUES(?,?,?,?)",
	}
	statements := make([]*sql.Stmt, len(queries))
	for i, query := range queries {
		statements[i], err = tx.Prepare(query)
		if err != nil {
			tb.Fatal(err)
		}
		defer statements[i].Close()
	}
	exec := func(which int, args ...any) {
		tb.Helper()
		if _, err := statements[which].Exec(args...); err != nil {
			tb.Fatal(err)
		}
	}
	rng := rand.New(rand.NewPCG(417, 923))
	mbid := func() string {
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", rng.Uint32(), rng.Uint32()&65535, rng.Uint32()&65535, rng.Uint32()&65535, rng.Uint64()&0xffffffffffff)
	}
	words := []string{"Night", "River", "Dream", "Golden", "Silent", "Blue", "Last", "City", "Sun", "Midnight", "Winter", "Sea", "Summer", "星空", "Lumière", "Never", "Our", "Love", "Time", "Dance"}
	phrase := func() string {
		return fmt.Sprintf("%s %s %s", words[rng.IntN(len(words))], words[rng.IntN(len(words))], words[rng.IntN(len(words))])
	}
	tags := []string{"dream pop", "ambient", "indie rock", "jazz", "electronic", "classical", "pop", "experimental"}
	artistCount := max(1, count/10)
	artistIDs := make([]string, artistCount)
	artistNames := make([]string, artistCount)
	for i := range artistCount {
		artistIDs[i], artistNames[i] = mbid(), phrase()
		tag := tags[rng.IntN(len(tags))]
		exec(0, artistIDs[i], artistNames[i], artistNames[i], artistNames[i], "")
		exec(1, artistIDs[i], "The "+artistNames[i], "The "+artistNames[i], artistNames[i], "en")
		exec(2, tag, artistIDs[i], tag, 1+rng.IntN(40))
	}
	for i := range count {
		id, title := mbid(), phrase()
		artist := rng.IntN(artistCount)
		tag := tags[rng.IntN(len(tags))]
		exec(3, id, title, title, artistNames[artist], 120000+rng.IntN(360000), fmt.Sprintf("%04d-09-13", 1950+rng.IntN(76)), "")
		exec(4, id, 0, artistIDs[artist], artistNames[artist], artistNames[artist], "")
		exec(5, id, fmt.Sprintf("USTST%07d", i))
		exec(6, id, tag, tag, 1+rng.IntN(25))
	}
	info := Info{Version: IndexVersion, Snapshot: "20260912-001001", CoreLicense: "CC0-1.0", TagsLicense: "CC-BY-NC-SA-3.0", Artists: int64(artistCount), Recordings: int64(count)}
	raw, _ := json.Marshal(info)
	if _, err := tx.Exec("INSERT INTO info VALUES('manifest',?)", string(raw)); err != nil {
		tb.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		tb.Fatal(err)
	}
	if _, err := db.Exec("CREATE INDEX artist_name_key ON artists(name_key); CREATE INDEX recording_title_key ON recordings(title_key); CREATE INDEX recording_artist ON recording_artists(artist_mbid,recording_mbid); CREATE INDEX recording_artist_name ON recording_artists(name_key,recording_mbid); CREATE INDEX recording_tags_key ON recording_tags(tag_key,recording_mbid); VACUUM;"); err != nil {
		tb.Fatal(err)
	}
	if err := db.Close(); err != nil {
		tb.Fatal(err)
	}
	return index
}
