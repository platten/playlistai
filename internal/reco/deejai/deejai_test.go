package deejai_test

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/reco/deejai"
)

const dim = 8

func unit(rng *rand.Rand) []float32 {
	v := make([]float32, dim)
	var s float64
	for i := range v {
		v[i] = float32(rng.NormFloat64())
		s += float64(v[i]) * float64(v[i])
	}
	n := float32(math.Sqrt(s))
	for i := range v {
		v[i] /= n
	}
	return v
}

// fakeCatalog builds n tracks across `artists` distinct artists with random
// vectors, plus a matching fake similarity engine.
func fakeEngine(t *testing.T, n, artists int, seed int64) (*deejai.Engine, *fakes.Catalog) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	rows := make([]fakes.CatalogTrack, n)
	for i := range rows {
		a := 'A' + rune(i%artists)
		rows[i] = fakes.CatalogTrack{
			ID:      "trk" + itoa(i),
			Display: "Artist " + string(a) + " - Song " + itoa(i),
			Audio:   unit(rng),
			Track:   unit(rng),
		}
	}
	cat := fakes.NewCatalog(dim, rows...)
	return deejai.New(cat, fakes.NewSimilarityEngine(cat)), cat
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func baseIntent() core.MusicIntent {
	return core.MusicIntent{
		Count:       12,
		Lookback:    3,
		Creativity:  0.5,
		Seed:        1,
		Constraints: core.IntentConstraints{NoRepeatArtistBackToBack: true},
	}
}

func TestSimilarBasics(t *testing.T) {
	t.Parallel()
	eng, _ := fakeEngine(t, 60, 5, 42)
	intent := baseIntent()
	intent.Seeds.TrackIDs = []string{"trk3"}

	pl, err := eng.Build(context.Background(), intent)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(pl.Tracks) != 12 {
		t.Fatalf("count = %d, want 12", len(pl.Tracks))
	}
	if pl.Tracks[0].ID != "trk3" || pl.Rationale[0].Kind != "required" {
		t.Fatalf("first pick should be the seed, got %+v / %s", pl.Tracks[0], pl.Rationale[0].Kind)
	}

	seen := map[string]bool{}
	var prevArtist string
	for i, ref := range pl.Tracks {
		if seen[ref.ID] {
			t.Fatalf("duplicate id %q at %d", ref.ID, i)
		}
		seen[ref.ID] = true
		art := ref.Artist
		if i > 0 && art == prevArtist {
			t.Fatalf("back-to-back artist %q at %d", art, i)
		}
		prevArtist = art
	}
	if len(pl.Rationale) != len(pl.Tracks) {
		t.Fatal("rationale/tracks length mismatch")
	}
}

func TestSeedResolutionAndErrNoSeeds(t *testing.T) {
	t.Parallel()
	eng, _ := fakeEngine(t, 30, 4, 7)

	// by query
	intent := baseIntent()
	intent.Seeds = core.IntentSeeds{}
	intent.References = []core.IntentReference{{Kind: core.ReferenceTrack, Query: "Song 5", Influence: core.InfluencePositive}}
	pl, err := eng.Build(context.Background(), intent)
	if err != nil {
		t.Fatalf("Build by query: %v", err)
	}
	if len(pl.Intent.References) != 1 || pl.Intent.References[0].TrackID != "trk5" {
		t.Fatalf("query seed resolution = %+v, want trk5", pl.Intent.References)
	}

	// nothing resolvable
	empty := baseIntent()
	empty.Version = core.CurrentIntentVersion
	empty.Seeds.Queries = []string{"zzz nonexistent"}
	if _, err := eng.Build(context.Background(), empty); !errors.Is(err, core.ErrNoSeeds) {
		t.Fatalf("want ErrNoSeeds, got %v", err)
	}
}

func TestDirectBuildRejectsAmbiguousTrackReference(t *testing.T) {
	t.Parallel()
	cat := fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "one", Display: "Artist One - Home", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "two", Display: "Artist Two - Home", Audio: []float32{0, 1}, Track: []float32{0, 1}},
	)
	eng := deejai.New(cat, fakes.NewSimilarityEngine(cat))
	_, err := eng.Build(context.Background(), core.MusicIntent{
		Version:    core.CurrentIntentVersion,
		References: []core.IntentReference{{Kind: core.ReferenceTrack, Query: "Home", Influence: core.InfluencePositive}},
		Controls:   core.IntentControls{TotalTrackCount: 3, AudioWeight: .5, CooccurrenceWeight: .5},
	})
	if !errors.Is(err, core.ErrAmbiguousReference) {
		t.Fatalf("Build error = %v, want ErrAmbiguousReference", err)
	}
}

func TestDeterminismAndNoise(t *testing.T) {
	t.Parallel()
	eng, _ := fakeEngine(t, 80, 6, 11)

	intent := baseIntent()
	intent.Seeds.TrackIDs = []string{"trk1"}

	a, _ := eng.Build(context.Background(), intent)
	b, _ := eng.Build(context.Background(), intent)
	if !sameIDs(a, b) {
		t.Fatal("noise=0 build is not deterministic")
	}

	// same Seed + noise → still deterministic
	intent.Noise = 0.4
	c1, _ := eng.Build(context.Background(), intent)
	c2, _ := eng.Build(context.Background(), intent)
	if !sameIDs(c1, c2) {
		t.Fatal("noise>0 with a fixed Seed is not deterministic")
	}
	// different Seed → different walk (overwhelmingly likely)
	intent.Seed = 999
	c3, _ := eng.Build(context.Background(), intent)
	if sameIDs(c1, c3) {
		t.Fatal("noise>0 ignored the Seed")
	}
	if c1.Seed == c3.Seed {
		t.Fatal("Playlist.Seed not echoed")
	}
}

func TestJourneyWaypointsAndLength(t *testing.T) {
	t.Parallel()
	eng, _ := fakeEngine(t, 100, 8, 5)

	intent := baseIntent()
	intent.Mode = core.ModeJourney
	intent.Count = 6 // total, including required waypoints
	intent.Seeds.TrackIDs = []string{"trk2", "trk40", "trk90"}

	pl, err := eng.Build(context.Background(), intent)
	if err != nil {
		t.Fatalf("Build journey: %v", err)
	}
	if len(pl.Tracks) != 6 {
		t.Fatalf("journey length = %d, want 6", len(pl.Tracks))
	}
	if pl.Tracks[0].ID != "trk2" || pl.Tracks[3].ID != "trk40" || pl.Tracks[5].ID != "trk90" {
		t.Fatalf("waypoints out of place: %s %s %s",
			pl.Tracks[0].ID, pl.Tracks[3].ID, pl.Tracks[5].ID)
	}
	seen := map[string]int{}
	for _, r := range pl.Tracks {
		seen[r.ID]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Fatalf("id %q appears %d times", id, n)
		}
	}
}

func TestArtistsExclude(t *testing.T) {
	t.Parallel()
	eng, _ := fakeEngine(t, 60, 5, 3)
	intent := baseIntent()
	intent.Count = 20
	intent.Seeds.TrackIDs = []string{"trk0"} // Artist A
	intent.Constraints.ArtistsExclude = []string{"artist c"}

	pl, _ := eng.Build(context.Background(), intent)
	for _, r := range pl.Tracks[1:] {
		if strings.EqualFold(r.Artist, "Artist C") {
			t.Fatalf("excluded artist present: %+v", r)
		}
	}
}

func TestExcludeSeedArtists(t *testing.T) {
	t.Parallel()
	eng, _ := fakeEngine(t, 60, 5, 4)
	intent := baseIntent()
	intent.Version = core.CurrentIntentVersion
	intent.Seeds.TrackIDs = []string{"trk0"} // reference Artist A is not required
	intent.Constraints.ExcludeSeedArtists = true

	pl, err := eng.Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range pl.Tracks {
		if strings.EqualFold(ref.Artist, "Artist A") {
			t.Fatalf("seed artist present: %+v", ref)
		}
	}
}

func TestFilteredSearchExpandsPastInitialWindow(t *testing.T) {
	t.Parallel()
	rows := make([]fakes.CatalogTrack, 0, searchWindowForTest+2)
	rows = append(rows, fakes.CatalogTrack{
		ID: "seed", Display: "Guide - Start", Audio: []float32{1, 0}, Track: []float32{1, 0},
	})
	for i := 0; i < searchWindowForTest; i++ {
		rows = append(rows, fakes.CatalogTrack{
			ID: "blocked-" + itoa(i), Display: "Blocked - Song " + itoa(i),
			Audio: []float32{1, 0}, Track: []float32{1, 0},
		})
	}
	rows = append(rows, fakes.CatalogTrack{
		ID: "eligible", Display: "Allowed - Last", Audio: []float32{0, 1}, Track: []float32{0, 1},
	})
	cat := fakes.NewCatalog(2, rows...)
	eng := deejai.New(cat, fakes.NewSimilarityEngine(cat))
	pl, err := eng.Build(context.Background(), core.MusicIntent{
		Version: core.CurrentIntentVersion,
		Seeds:   core.IntentSeeds{TrackIDs: []string{"seed"}},
		Count:   1, Lookback: 1, Creativity: 0.5,
		Constraints: core.IntentConstraints{ArtistsExclude: []string{"Blocked"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pl.Tracks) != 1 || pl.Tracks[0].ID != "eligible" {
		t.Fatalf("search did not expand to eligible track: %+v", pl.Tracks)
	}
}

const searchWindowForTest = 4096

func TestExhaustedFiltersReturnPartialWithoutFallback(t *testing.T) {
	t.Parallel()
	eng, _ := fakeEngine(t, 12, 2, 9)
	intent := baseIntent()
	intent.Version = core.CurrentIntentVersion
	intent.Count = 8
	intent.Seeds.TrackIDs = []string{"trk0"}
	intent.Constraints.ArtistsExclude = []string{"Artist A", "Artist B"}

	pl, err := eng.Build(context.Background(), intent)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(pl.Tracks) != 0 {
		t.Fatalf("hard exclusions were bypassed: %+v", pl.Tracks)
	}
	if len(pl.Notices) != 1 || pl.Notices[0].Code != "eligible_tracks_exhausted" {
		t.Fatalf("missing structured exhaustion notice: %+v", pl.Notices)
	}
}

func TestDuplicateRecordingAcrossDifferentIDs(t *testing.T) {
	t.Parallel()
	rows := []fakes.CatalogTrack{
		{ID: "seed", Display: "Guide - Start", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		{ID: "take-a", Display: "The Artist - Same Song", Audio: []float32{0.99, 0.01}, Track: []float32{0.99, 0.01}},
		{ID: "take-b", Display: "  the artist  - SAME SONG ", Audio: []float32{0.98, 0.02}, Track: []float32{0.98, 0.02}},
		{ID: "other", Display: "Other - Different", Audio: []float32{0.8, 0.2}, Track: []float32{0.8, 0.2}},
		{ID: "end", Display: "Guide Two - End", Audio: []float32{0, 1}, Track: []float32{0, 1}},
	}
	cat := fakes.NewCatalog(2, rows...)
	eng := deejai.New(cat, fakes.NewSimilarityEngine(cat))
	intents := []core.MusicIntent{
		{Version: core.CurrentIntentVersion, Seeds: core.IntentSeeds{TrackIDs: []string{"seed"}}, Count: 3, Lookback: 1, Creativity: 0.5, Seed: 1},
		{Version: core.CurrentIntentVersion, Seeds: core.IntentSeeds{TrackIDs: []string{"seed", "end"}}, Mode: core.ModeJourney, Count: 3, Lookback: 1, Creativity: 0.5, Seed: 1},
	}
	for _, intent := range intents {
		pl, err := eng.Build(context.Background(), intent)
		if err != nil {
			t.Fatal(err)
		}
		seenTake := false
		for _, ref := range pl.Tracks {
			if ref.ID != "take-a" && ref.ID != "take-b" {
				continue
			}
			if seenTake {
				t.Fatalf("%s emitted duplicate recording under another ID: %+v", intent.Mode, pl.Tracks)
			}
			seenTake = true
		}
	}
}

func TestRequiredTrackConflictsAndShortCount(t *testing.T) {
	t.Parallel()
	eng, _ := fakeEngine(t, 20, 4, 12)

	conflict := core.MusicIntent{
		Version:     core.CurrentIntentVersion,
		Seeds:       core.IntentSeeds{TrackIDs: []string{"trk0"}},
		Required:    core.IntentSeeds{TrackIDs: []string{"trk0"}},
		Count:       5,
		Constraints: core.IntentConstraints{ExcludeSeedArtists: true},
	}
	if _, err := eng.Build(context.Background(), conflict); !errors.Is(err, core.ErrRequiredTrackConflict) {
		t.Fatalf("seed inclusion conflict = %v, want ErrRequiredTrackConflict", err)
	}
	artistConflict := core.MusicIntent{
		Version:  core.CurrentIntentVersion,
		Required: core.IntentSeeds{TrackIDs: []string{"trk1"}},
		Count:    5,
		Constraints: core.IntentConstraints{
			ArtistsExclude: []string{"artist b"},
		},
	}
	if _, err := eng.Build(context.Background(), artistConflict); !errors.Is(err, core.ErrRequiredTrackConflict) {
		t.Fatalf("artist inclusion conflict = %v, want ErrRequiredTrackConflict", err)
	}

	short := core.MusicIntent{
		Version:  core.CurrentIntentVersion,
		Required: core.IntentSeeds{TrackIDs: []string{"trk0", "trk1", "trk2"}},
		Mode:     core.ModeJourney,
		Count:    2,
	}
	if _, err := eng.Build(context.Background(), short); !errors.Is(err, core.ErrCountBelowRequired) {
		t.Fatalf("short count error = %v, want ErrCountBelowRequired", err)
	}
}

func sameIDs(a, b core.Playlist) bool {
	if len(a.Tracks) != len(b.Tracks) {
		return false
	}
	for i := range a.Tracks {
		if a.Tracks[i].ID != b.Tracks[i].ID {
			return false
		}
	}
	return true
}
