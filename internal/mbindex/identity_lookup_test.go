package mbindex

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestLookupArtistNamesPreservesIdentityAndProvenance(t *testing.T) {
	store := buildIdentityLookupStore(t)

	got, err := store.LookupArtistNames(context.Background(), []string{
		"Shared Name", "The Signal Bloom", "S. Bloom", "BJÖRK & THE NORTH", "missing", "Shared Name",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 {
		t.Fatalf("got %d lookup results", len(got))
	}
	if len(got[0].Candidates) != 2 || got[0].Candidates[0].MBID != "artist-shared-a" || got[0].Candidates[1].MBID != "artist-shared-b" {
		t.Fatalf("homonyms were not retained deterministically: %+v", got[0])
	}
	if candidate := got[0].Candidates[0]; candidate.Disambiguation != "first shared artist" || candidate.MatchType != ArtistMatchCanonical || candidate.MatchedName != "Shared Name" {
		t.Fatalf("canonical provenance missing: %+v", candidate)
	}
	if candidates := got[1].Candidates; len(candidates) != 1 || candidates[0].MBID != "artist-signal" || candidates[0].MatchType != ArtistMatchAlias || candidates[0].MatchedName != "The Signal Bloom" {
		t.Fatalf("alias lookup: %+v", got[1])
	}
	if candidates := got[2].Candidates; len(candidates) != 1 || candidates[0].MBID != "artist-signal" || candidates[0].MatchType != ArtistMatchCredit || candidates[0].MatchedName != "S. Bloom" {
		t.Fatalf("credit lookup: %+v", got[2])
	}
	if candidates := got[3].Candidates; len(candidates) != 1 || candidates[0].MBID != "artist-north" || candidates[0].Name != "Björk & The North" {
		t.Fatalf("accented artist lookup: %+v", got[3])
	}
	if len(got[4].Candidates) != 0 || got[4].Truncated {
		t.Fatalf("missing lookup produced candidates: %+v", got[4])
	}
	if len(got[5].Candidates) != 2 {
		t.Fatalf("duplicate input lost results: %+v", got[5])
	}
	got[0].Candidates[0].Name = "mutated"
	if got[5].Candidates[0].Name == "mutated" {
		t.Fatal("duplicate input results share mutable candidate storage")
	}

	identity := store.SnapshotIdentity()
	if identity.IndexVersion != IndexVersion || identity.Snapshot != "20260912-001001" {
		t.Fatalf("snapshot identity: %+v", identity)
	}
}

func TestLookupArtistRecordingsScopesExactTitlesToArtist(t *testing.T) {
	store := buildIdentityLookupStore(t)

	got, err := store.LookupArtistRecordings(context.Background(), []ArtistRecordingQuery{
		{ArtistMBID: "artist-shared-a", Title: "Shared Song"},
		{ArtistMBID: "artist-shared-b", Title: "Shared Song"},
		{ArtistMBID: "artist-shared-b", Title: "Only A"},
		{ArtistMBID: "artist-shared-a", Title: "SHARED SONG"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if candidates := got[0].Candidates; len(candidates) != 1 || candidates[0].MBID != "recording-shared-a" || candidates[0].Disambiguation != "A version" {
		t.Fatalf("artist A title lookup: %+v", got[0])
	}
	if candidates := got[1].Candidates; len(candidates) != 1 || candidates[0].MBID != "recording-shared-b" {
		t.Fatalf("artist B title lookup: %+v", got[1])
	}
	if len(got[2].Candidates) != 0 {
		t.Fatalf("title crossed artist identity: %+v", got[2])
	}
	if candidates := got[3].Candidates; len(candidates) != 1 || candidates[0].MBID != "recording-shared-a" {
		t.Fatalf("normalized title lookup: %+v", got[3])
	}
}

func TestIdentityLookupBoundsAndCancellation(t *testing.T) {
	store := buildIdentityLookupStore(t)

	artistMatches, err := store.LookupArtistNames(context.Background(), []string{"Crowded Name"})
	if err != nil {
		t.Fatal(err)
	}
	if len(artistMatches[0].Candidates) != MaxLookupCandidates || !artistMatches[0].Truncated {
		t.Fatalf("artist candidate bound: count=%d truncated=%v", len(artistMatches[0].Candidates), artistMatches[0].Truncated)
	}
	recordingMatches, err := store.LookupArtistRecordings(context.Background(), []ArtistRecordingQuery{{ArtistMBID: "artist-signal", Title: "Crowded Title"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(recordingMatches[0].Candidates) != MaxLookupCandidates || !recordingMatches[0].Truncated {
		t.Fatalf("recording candidate bound: count=%d truncated=%v", len(recordingMatches[0].Candidates), recordingMatches[0].Truncated)
	}

	tooManyNames := make([]string, MaxLookupKeys+1)
	if _, err := store.LookupArtistNames(context.Background(), tooManyNames); !errors.Is(err, ErrLookupBatchTooLarge) {
		t.Fatalf("artist batch error = %v", err)
	}
	tooManyRecordings := make([]ArtistRecordingQuery, MaxLookupKeys+1)
	if _, err := store.LookupArtistRecordings(context.Background(), tooManyRecordings); !errors.Is(err, ErrLookupBatchTooLarge) {
		t.Fatalf("recording batch error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.LookupArtistNames(ctx, []string{"Shared Name"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("artist cancellation error = %v", err)
	}
	if _, err := store.LookupArtistRecordings(ctx, []ArtistRecordingQuery{{ArtistMBID: "artist-shared-a", Title: "Shared Song"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("recording cancellation error = %v", err)
	}
}

func TestIdentityLookupQueryPlansUseInstalledIndexes(t *testing.T) {
	store := buildIdentityLookupStore(t)
	cases := []struct {
		name, query, index string
		args               []any
	}{
		{"canonical artist", `SELECT mbid FROM artists WHERE name_key=?`, "artist_name_key", []any{"shared name"}},
		{"artist alias", `SELECT artist_mbid FROM artist_aliases WHERE name_key=?`, "artist_alias_key", []any{"the signal bloom"}},
		{"recording title", `SELECT mbid FROM recordings WHERE title_key=?`, "recording_title_key", []any{"shared song"}},
		{"artist recordings", `SELECT recording_mbid FROM recording_artists WHERE artist_mbid=?`, "recording_artist", []any{"artist-shared-a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := store.db.Query(`EXPLAIN QUERY PLAN `+tc.query, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var details []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				details = append(details, detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if joined := strings.Join(details, "\n"); !strings.Contains(joined, tc.index) {
				t.Fatalf("query did not use %s: %s", tc.index, joined)
			}
		})
	}
}

func buildIdentityLookupStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	artistArchive := filepath.Join(dir, "artist.tar.xz")
	recordingArchive := filepath.Join(dir, "recording.tar.xz")
	artists := []any{
		map[string]any{"id": "artist-signal", "name": "Signal Bloom", "aliases": []any{map[string]any{"name": "The Signal Bloom", "locale": "en"}}},
		map[string]any{"id": "artist-shared-a", "name": "Shared Name", "disambiguation": "first shared artist"},
		map[string]any{"id": "artist-shared-b", "name": "Shared Name", "disambiguation": "second shared artist"},
		map[string]any{"id": "artist-north", "name": "Björk & The North"},
	}
	for i := 0; i < MaxLookupCandidates+2; i++ {
		artists = append(artists, map[string]any{"id": fmt.Sprintf("artist-crowded-%03d", i), "name": "Crowded Name"})
	}
	credit := func(id, name string) map[string]any {
		return map[string]any{"name": name, "artist": map[string]any{"id": id, "name": name}}
	}
	recordings := []any{
		map[string]any{"id": "recording-credit", "title": "Credit Source", "artist-credit": []any{credit("artist-signal", "S. Bloom")}},
		map[string]any{"id": "recording-shared-a", "title": "Shared Song", "disambiguation": "A version", "artist-credit": []any{credit("artist-shared-a", "Shared Name")}},
		map[string]any{"id": "recording-shared-b", "title": "Shared Song", "artist-credit": []any{credit("artist-shared-b", "Shared Name")}},
		map[string]any{"id": "recording-only-a", "title": "Only A", "artist-credit": []any{credit("artist-shared-a", "Shared Name")}},
	}
	for i := 0; i < MaxLookupCandidates+2; i++ {
		recordings = append(recordings, map[string]any{
			"id":            fmt.Sprintf("recording-crowded-%03d", i),
			"title":         "Crowded Title",
			"artist-credit": []any{credit("artist-signal", "Signal Bloom")},
		})
	}
	writeDump(t, artistArchive, "artist", artists)
	writeDump(t, recordingArchive, "recording", recordings)
	index := filepath.Join(dir, "musicbrainz.sqlite")
	if _, err := Build(context.Background(), BuildOptions{
		Output: index, Snapshot: "20260912-001001", ArtistArchive: artistArchive, RecordingArchive: recordingArchive,
	}); err != nil {
		t.Fatal(err)
	}
	store, err := Open(index)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close identity lookup store: %v", err)
		}
	})
	return store
}
