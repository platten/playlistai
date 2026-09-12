package catalog

import (
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestArtistSpellingCorrectionKeepsIdentityAndAmbiguity(t *testing.T) {
	c := metadataResolverCatalog(t)
	insertResolverTrack(t, c.db, 0, "loeffler", "Christian Löffler", "Haul")
	insertResolverTrack(t, c.db, 1, "decoy", "Another Artist", "christrian loeffler")
	r := c.ResolveReference(core.IntentReference{Kind: core.ReferenceArtist, Query: "christrian loeffler"})
	if r.Selected == nil || r.Selected.Artist != "Christian Löffler" || r.Selected.Evidence[0].Match != "spelling" {
		t.Fatalf("correction: %+v", r)
	}
	for _, query := range []string{"Christian Löffler unrelated", "loeffler", "christian xoeffler"} {
		if got := c.ResolveReference(core.IntentReference{Kind: core.ReferenceArtist, Query: query}); got.Status != core.ResolutionUnresolved {
			t.Fatalf("guessed %q: %+v", query, got)
		}
	}
	insertResolverTrack(t, c.db, 2, "chris", "Christina Palmer", "First")
	insertResolverTrack(t, c.db, 3, "christine", "Christine Palmer", "Second")
	r = c.ResolveReference(core.IntentReference{Kind: core.ReferenceArtist, Query: "Christin Palmer"}) //nolint:misspell // Deliberately ambiguous misspelling of both catalog names.
	if r.Status != core.ResolutionAmbiguous || len(r.Alternatives) != 2 {
		t.Fatalf("ambiguous spelling guessed: %+v", r)
	}
}

func TestTypoWithEquivalentCatalogSpellingsIsDeterministic(t *testing.T) {
	c := metadataResolverCatalog(t)
	insertResolverTrack(t, c.db, 0, "plain", "Christian Loffler", "First")
	insertResolverTrack(t, c.db, 1, "accent", "Christian Löffler", "Second")
	c.artistRows = map[string][]int{"Christian Loffler": {0}, "Christian Löffler": {1}}
	for range 50 {
		r := c.resolveArtistTypo("Christan Loffler")
		if r.Selected == nil || r.Selected.Artist != "Christian Loffler" || r.Selected.EntityID != "artist:christian loffler" {
			t.Fatalf("unstable correction: %+v", r)
		}
	}
}
