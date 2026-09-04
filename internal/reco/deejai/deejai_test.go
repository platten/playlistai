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
	if pl.Tracks[0].ID != "trk3" || pl.Rationale[0].Kind != "seed" {
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
	intent.Seeds.Queries = []string{"song 5"}
	pl, err := eng.Build(context.Background(), intent)
	if err != nil {
		t.Fatalf("Build by query: %v", err)
	}
	if pl.Tracks[0].ID != "trk5" {
		t.Fatalf("query seed resolved to %q, want trk5", pl.Tracks[0].ID)
	}

	// nothing resolvable
	empty := baseIntent()
	empty.Seeds.Queries = []string{"zzz nonexistent"}
	if _, err := eng.Build(context.Background(), empty); !errors.Is(err, core.ErrNoSeeds) {
		t.Fatalf("want ErrNoSeeds, got %v", err)
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
	intent.Count = 6 // intermediates per segment
	intent.Seeds.TrackIDs = []string{"trk2", "trk40", "trk90"}

	pl, err := eng.Build(context.Background(), intent)
	if err != nil {
		t.Fatalf("Build journey: %v", err)
	}
	// 3 waypoints + 2 segments * 6 = 15
	if len(pl.Tracks) != 15 {
		t.Fatalf("journey length = %d, want 15", len(pl.Tracks))
	}
	if pl.Tracks[0].ID != "trk2" || pl.Tracks[7].ID != "trk40" || pl.Tracks[14].ID != "trk90" {
		t.Fatalf("waypoints out of place: %s %s %s",
			pl.Tracks[0].ID, pl.Tracks[7].ID, pl.Tracks[14].ID)
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
