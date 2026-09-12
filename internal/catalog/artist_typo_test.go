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
	if r.Status != core.ResolutionAmbiguous || r.Selected != nil || len(r.Alternatives) != 1 || r.Alternatives[0].Artist != "Christian Löffler" || r.Alternatives[0].Evidence[0].Match != "spelling" {
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
		if r.Selected != nil || len(r.Alternatives) != 1 || r.Alternatives[0].Artist != "Christian Loffler" || r.Alternatives[0].EntityID != "artist:christian loffler" {
			t.Fatalf("unstable correction: %+v", r)
		}
	}
}

func TestArtistSpellingChoiceNeverReusesRejectedCorrection(t *testing.T) {
	c := metadataResolverCatalog(t)
	stmt, err := c.db.Prepare("SELECT artist, title, '' FROM tracks WHERE id = ?")
	if err != nil {
		t.Fatal(err)
	}
	c.metaStmt = stmt
	t.Cleanup(func() { _ = stmt.Close() })
	insertResolverTrack(t, c.db, 0, "loeffler", "Christian Löffler", "Haul")
	ref := core.IntentReference{Kind: core.ReferenceArtist, Query: "christrian loeffler"}
	proposed := c.ResolveReference(ref)
	if proposed.Status != core.ResolutionAmbiguous {
		t.Fatal(proposed)
	}
	ref.SpellingDecision, ref.TrackID = "accepted", "loeffler"
	accepted := c.ResolveReference(ref)
	if accepted.Status != core.ResolutionResolved || accepted.Selected.Artist != "Christian Löffler" {
		t.Fatal(accepted)
	}
	ref.SpellingDecision, ref.Resolution = "original", &accepted
	for range 2 {
		rejected := c.ResolveReference(ref)
		if rejected.Status != core.ResolutionUnresolved || rejected.Selected != nil || len(rejected.Alternatives) != 0 {
			t.Fatalf("rejection silently corrected or re-prompted: %+v", rejected)
		}
	}
	for _, query := range []string{"Christian Löffler", "Christian Loeffler"} {
		exact := c.ResolveReference(core.IntentReference{Kind: core.ReferenceArtist, Query: query})
		if exact.Status != core.ResolutionResolved || exact.Selected.Artist != "Christian Löffler" {
			t.Fatalf("exact spelling prompted for correction: %q %+v", query, exact)
		}
	}
}
