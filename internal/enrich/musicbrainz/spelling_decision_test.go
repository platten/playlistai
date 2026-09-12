package musicbrainz

import (
	"context"
	"net/http"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func TestRejectedArtistSpellingNeverTriggersOnlineRecoveryOrContext(t *testing.T) {
	c, calls := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("rejected name reached provider: %s", r.URL)
		http.Error(w, "unexpected", 500)
	})
	cat := contextCatalog()
	ref := core.IntentReference{Kind: core.ReferenceArtist, Query: "christrian loeffler", Influence: core.InfluencePositive, SpellingDecision: "original"}
	m := core.MusicIntent{References: []core.IntentReference{ref}, Destination: &ref}
	got := c.resolveMissingArtists(context.Background(), m, cat, cat, &core.KnowledgeSnapshot{}, ports.NopProgress{})
	if got.References[0].Query != ref.Query || got.References[0].TrackID != "" || got.Destination.SpellingDecision != "original" {
		t.Fatal("rejection lost")
	}
	if _, ok := c.referenceContext(context.Background(), got, ref, "playlist", cat, cat); ok {
		t.Fatal("rejected name got artist context")
	}
	if calls.Load() != 0 {
		t.Fatal("provider was called")
	}
}
