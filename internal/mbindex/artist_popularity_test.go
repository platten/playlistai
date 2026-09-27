package mbindex

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

type fixturePopularity struct {
	calls int
	wrong bool
}

func (*fixturePopularity) SnapshotIdentity() string { return "pop-v1" }
func (p *fixturePopularity) LookupArtistPopularity(ctx context.Context, ids []string) (map[string]core.ArtistPopularity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.calls++
	if len(ids) > 1000 {
		return nil, errors.New("oversized batch")
	}
	out := map[string]core.ArtistPopularity{}
	for _, id := range ids {
		u := int64(1)
		if id == "artist-crowded-065" {
			u = 10000
		}
		out[id] = core.ArtistPopularity{Snapshot: "pop-v1", UniqueListeners: &u}
	}
	if p.wrong {
		out["not-requested"] = core.ArtistPopularity{Snapshot: "pop-v1"}
	}
	return out, nil
}

func TestArtistPopularityRanksBeforeIdentityCapAndClones(t *testing.T) {
	base := buildIdentityLookupStore(t)
	reader := &fixturePopularity{}
	store := base.WithArtistPopularity(reader)
	got, err := store.LookupArtistNames(context.Background(), []string{"Crowded Name", "Crowded Name"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got[0].Candidates) != 64 || !got[0].Truncated || got[0].Candidates[0].MBID != "artist-crowded-065" {
		t.Fatalf("winner outside old cap lost: %+v", got[0])
	}
	*got[0].Candidates[0].Popularity.UniqueListeners = 0
	if *got[1].Candidates[0].Popularity.UniqueListeners != 10000 {
		t.Fatal("duplicate inputs share mutable popularity")
	}
	if base.AutomaticArtistResolution() || !store.AutomaticArtistResolution() || !strings.Contains(store.SnapshotIdentity().Snapshot, "pop-v1") {
		t.Fatal("request-local identity not pinned")
	}
	legacy, err := base.LookupArtistNames(context.Background(), []string{"Crowded Name"})
	if err != nil {
		t.Fatal(err)
	}
	if legacy[0].Candidates[0].MBID != "artist-crowded-000" || legacy[0].Candidates[0].Popularity != nil {
		t.Fatal("legacy lookup changed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := reader.calls
	if _, err = store.LookupArtistNames(ctx, []string{"Shared Name"}); !errors.Is(err, context.Canceled) || reader.calls != before {
		t.Fatal("canceled lookup reached reader")
	}
	reader.wrong = true
	if _, err = store.LookupArtistNames(context.Background(), []string{"Shared Name"}); err == nil {
		t.Fatal("unrequested popularity accepted")
	}
}
