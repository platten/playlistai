package mbindex

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTypedPipelinePreservesRecordsAndSkipsUnusedPayload(t *testing.T) {
	// Large valid objects still work, but discarded metadata is not retained in
	// the queue or copied through json.RawMessage before decoding a second time.
	raw := `{"id":"first","name":"A","aliases":[{"name":"Alias"}],"ignored":"` + strings.Repeat("x", (16<<20)+1) + `"}` + "\n" + `{"id":"second","name":"B"}`
	var rows []artistDump
	err := streamJSON(context.Background(), strings.NewReader(raw), "mbdump/artist", "artists", int64(len(raw)), nil, func(row artistDump) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil || len(rows) != 2 || rows[0].ID != "first" || len(rows[0].Aliases) != 1 || rows[1].ID != "second" || len(rows[1].Aliases) != 0 {
		t.Fatalf("projection/order/field reset: rows=%+v err=%v", rows, err)
	}
}

func TestTypedPipelineFailures(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"consumer", "cancellation", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			input := strings.Repeat(`{"id":"a"}`+"\n", 100)
			if kind == "malformed" {
				input = `{"id":"a"}` + "\n" + `{"id":`
			}
			failure := errors.New("writer failure")
			var calls int
			err := streamJSON(ctx, strings.NewReader(input), "mbdump/artist", "artists", int64(len(input)), nil, func(row artistDump) error {
				calls++
				if kind == "consumer" {
					return failure
				}
				if kind == "cancellation" {
					cancel()
				}
				return nil
			})
			if calls != 1 || err == nil {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			if kind == "consumer" && !errors.Is(err, failure) || kind == "cancellation" && !errors.Is(err, context.Canceled) || kind == "malformed" && !strings.Contains(err.Error(), "mbdump/artist row 2") {
				t.Fatal(err)
			}
		})
	}
}

func TestBuildCacheConfiguration(t *testing.T) {
	t.Parallel()
	for _, value := range []int{-1, 4097} {
		if _, err := Build(context.Background(), BuildOptions{SQLiteCacheMiB: value}); err == nil {
			t.Fatalf("invalid cache %d accepted", value)
		}
	}
	path := filepath.Join(t.TempDir(), "cache.sqlite")
	for i := 0; i < 2; i++ {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if err := configureBuildDB(context.Background(), db, 64); err != nil {
			t.Fatal(err)
		}
		var cache int
		if err := db.QueryRow("PRAGMA cache_size").Scan(&cache); err != nil || cache != -65536 {
			t.Fatalf("connection %d cache=%d err=%v", i, cache, err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestParallelBuildFailureKeepsExistingOutput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	output := filepath.Join(dir, "index.sqlite")
	if err := os.WriteFile(output, []byte("previous index"), 0600); err != nil {
		t.Fatal(err)
	}
	artist := filepath.Join(dir, "artist.tar.xz")
	writeDump(t, artist, "artist", []any{map[string]any{"id": "a", "name": "A"}})
	_, err := Build(context.Background(), BuildOptions{Output: output, ArtistArchive: artist, RecordingArchive: filepath.Join(dir, "missing.tar.xz"), Snapshot: "20260912-001001", Replace: true})
	if err == nil {
		t.Fatal("missing recording archive accepted")
	}
	raw, err := os.ReadFile(output)
	if err != nil || string(raw) != "previous index" {
		t.Fatalf("previous output changed: %q %v", raw, err)
	}
	files, err := filepath.Glob(filepath.Join(dir, ".musicbrainz-*.sqlite"))
	if err != nil || len(files) != 0 {
		t.Fatalf("stages retained after failure: %v %v", files, err)
	}
}

func TestCanceledImportsReleaseStagesAndKeepExistingOutput(t *testing.T) {
	t.Parallel()
	for _, entity := range []string{"artists", "recordings"} {
		t.Run(entity, func(t *testing.T) {
			dir := t.TempDir()
			output := filepath.Join(dir, "index.sqlite")
			if err := os.WriteFile(output, []byte("previous index"), 0600); err != nil {
				t.Fatal(err)
			}
			artist := filepath.Join(dir, "artist.tar.xz")
			recording := filepath.Join(dir, "recording.tar.xz")
			writeDump(t, artist, "artist", []any{map[string]any{"id": "a", "name": "A"}})
			writeDump(t, recording, "recording", []any{map[string]any{"id": "r", "title": "R"}})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, err := Build(ctx, BuildOptions{
				Output: output, ArtistArchive: artist, RecordingArchive: recording,
				Snapshot: "20260912-001001", Replace: true,
				Progress: func(p BuildProgress) {
					// This callback runs after the stage has opened its transaction.
					if p.Entity == entity {
						cancel()
					}
				},
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation was not preserved: %v", err)
			}
			raw, err := os.ReadFile(output)
			if err != nil || string(raw) != "previous index" {
				t.Fatalf("previous output changed: %q %v", raw, err)
			}
			files, err := filepath.Glob(filepath.Join(dir, ".musicbrainz-*.sqlite"))
			if err != nil || len(files) != 0 {
				t.Fatalf("stages retained after cancellation: %v %v", files, err)
			}
		})
	}
}

func TestImportTransactionCancellationPreventsBeginAndCommit(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "stage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := beginImportTransaction(ctx, db); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context began a transaction: %v", err)
	}
	tx, err := beginImportTransaction(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("CREATE TABLE unpublished(id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if err := commitImportTransaction(ctx, tx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled import committed: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("import no longer owns rollback: %v", err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='unpublished'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("canceled transaction persisted changes: count=%d err=%v", count, err)
	}
}

func TestRepeatedArtistsKeepFirstFallbackAndAuthoritativeNames(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	artist, recording := filepath.Join(dir, "artist.tar.xz"), filepath.Join(dir, "recording.tar.xz")
	writeDump(t, artist, "artist", []any{map[string]any{"id": "authoritative", "name": "Official Name"}})
	credit := func(id, name string) map[string]any {
		return map[string]any{"name": name, "artist": map[string]any{"id": id, "name": name}}
	}
	writeDump(t, recording, "recording", []any{
		map[string]any{"id": "one", "title": "One", "artist-credit": []any{credit("fallback", "First Name"), credit("authoritative", "Credit Name")}},
		map[string]any{"id": "two", "title": "Two", "artist-credit": []any{credit("fallback", "Second Name"), credit("authoritative", "Other Credit")}},
	})
	output := filepath.Join(dir, "index.sqlite")
	if _, err := Build(context.Background(), BuildOptions{Output: output, ArtistArchive: artist, RecordingArchive: recording, Snapshot: "20260912-001001"}); err != nil {
		t.Fatal(err)
	}
	store, err := Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for id, want := range map[string]string{"fallback": "First Name", "authoritative": "Official Name"} {
		var got string
		if err := store.db.QueryRow("SELECT name FROM artists WHERE mbid=?", id).Scan(&got); err != nil || got != want {
			t.Fatalf("artist %s=%q want %q: %v", id, got, want, err)
		}
	}
	rows, err := store.ArtistRecordings(context.Background(), "fallback", 10, 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("recordings: %+v %v", rows, err)
	}
	if got := []string{rows[0].Artists[0].Name, rows[1].Artists[0].Name}; !reflect.DeepEqual(got, []string{"First Name", "Second Name"}) {
		t.Fatalf("credit-specific names lost: %v", got)
	}
}
