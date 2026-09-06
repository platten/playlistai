package catalog

import (
	"database/sql"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestTypedResolutionExactOutsideOldWindowAndCollisions(t *testing.T) {
	c := metadataResolverCatalog(t)
	for i := 0; i < 70; i++ {
		insertResolverTrack(t, c.db, i, "decoy"+string(rune('A'+i%26)), "Other", "Late Artist Story")
	}
	insertResolverTrack(t, c.db, 70, "late", "Late Artist", "Only Song")
	insertResolverTrack(t, c.db, 71, "artist-halo", "Halo", "Unrelated")
	insertResolverTrack(t, c.db, 72, "track-halo", "Beyoncé", "Halo")

	artist := c.ResolveReference(core.IntentReference{Kind: core.ReferenceArtist, Query: "Late Artist"})
	if artist.Status != core.ResolutionResolved || artist.Selected == nil || artist.Selected.Artist != "Late Artist" {
		t.Fatalf("late exact artist did not win: %+v", artist)
	}
	artistCollision := c.ResolveReference(core.IntentReference{Kind: core.ReferenceArtist, Query: "Halo"})
	trackCollision := c.ResolveReference(core.IntentReference{Kind: core.ReferenceTrack, Query: "Halo"})
	if artistCollision.Selected == nil || artistCollision.Selected.Artist != "Halo" {
		t.Fatalf("artist namespace collision: %+v", artistCollision)
	}
	if trackCollision.Selected == nil || trackCollision.Selected.EntityID != "track-halo" {
		t.Fatalf("track namespace collision: %+v", trackCollision)
	}
}

func TestResolutionAliasesUnicodeAmbiguityAndUnmatched(t *testing.T) {
	c := metadataResolverCatalog(t)
	insertResolverTrack(t, c.db, 0, "diddy", "Diddy", "Bad Boy for Life")
	insertResolverTrack(t, c.db, 1, "rsk", "坂本龍一", "Energy Flow")
	insertResolverTrack(t, c.db, 2, "alpha", "Alpha", "One")
	insertResolverTrack(t, c.db, 3, "beta", "Beta", "Two")
	mustExec(t, c.db, `INSERT INTO artist_aliases VALUES ('Diddy','Puff Daddy','puff daddy','puff daddy')`)
	mustExec(t, c.db, `INSERT INTO artist_aliases VALUES ('Alpha','Shared Name','shared name','shared name')`)
	mustExec(t, c.db, `INSERT INTO artist_aliases VALUES ('Beta','Shared Name','shared name','shared name')`)
	c.hasAliases = true

	alias := c.ResolveReference(core.IntentReference{Kind: core.ReferenceArtist, Query: "Puff Daddy"})
	if alias.Selected == nil || alias.Selected.Artist != "Diddy" || alias.Selected.Evidence[0].Match != "alias" {
		t.Fatalf("alias resolution = %+v", alias)
	}
	nonLatin := c.ResolveReference(core.IntentReference{Kind: core.ReferenceArtist, Query: "坂本龍一"})
	if nonLatin.Selected == nil || nonLatin.Selected.Artist != "坂本龍一" {
		t.Fatalf("Unicode resolution = %+v", nonLatin)
	}
	ambiguous := c.ResolveReference(core.IntentReference{Kind: core.ReferenceArtist, Query: "Shared Name"})
	if ambiguous.Status != core.ResolutionAmbiguous || len(ambiguous.Alternatives) != 2 {
		t.Fatalf("alias ambiguity = %+v", ambiguous)
	}
	missing := c.ResolveReference(core.IntentReference{Kind: core.ReferenceTrack, Query: "words that do not exist"})
	if missing.Status != core.ResolutionUnresolved || missing.Selected != nil {
		t.Fatalf("unmatched resolution = %+v", missing)
	}
	trailing := c.ResolveReference(core.IntentReference{Kind: core.ReferenceArtist, Query: "Diddy unrelated trailing words"})
	if trailing.Status != core.ResolutionUnresolved {
		t.Fatalf("resolver silently discarded trailing words: %+v", trailing)
	}
	filler := c.ResolveReference(core.IntentReference{Kind: core.ReferenceArtist, Query: "songs like Diddy"})
	if filler.Selected == nil || filler.Selected.Artist != "Diddy" || filler.Selected.Evidence[len(filler.Selected.Evidence)-1].Match != "filler" {
		t.Fatalf("documented filler fallback = %+v", filler)
	}
}

func TestArtistRepresentativesDeterministicAndDiverse(t *testing.T) {
	c := openTestdata(t)
	first := c.ResolveReference(core.IntentReference{Kind: core.ReferenceArtist, Query: "Kavinsky"})
	second := c.ResolveReference(core.IntentReference{Kind: core.ReferenceArtist, Query: "Kavinsky"})
	if first.Selected == nil || len(first.Selected.Representatives) != maxRepresentatives {
		t.Fatalf("representatives = %+v", first)
	}
	if !reflect.DeepEqual(first.Selected.Representatives, second.Selected.Representatives) {
		t.Fatalf("cached representatives are nondeterministic:\n%+v\n%+v", first, second)
	}
	seen := map[string]struct{}{}
	var total float64
	for _, representative := range first.Selected.Representatives {
		seen[representative.TrackID] = struct{}{}
		total += representative.Weight
	}
	if len(seen) != maxRepresentatives || total < .999 || total > 1.001 {
		t.Fatalf("invalid weighted representatives: %+v", first.Selected.Representatives)
	}
	rows := c.artistSearchRows("kavinsky", "kavinsky")
	var firstIDs []string
	for _, row := range rows {
		if normalizeSearch(row.ref.Artist) == "kavinsky" {
			firstIDs = append(firstIDs, row.ref.ID)
			if len(firstIDs) == maxRepresentatives {
				break
			}
		}
	}
	var representativeIDs []string
	for _, representative := range first.Selected.Representatives {
		representativeIDs = append(representativeIDs, representative.TrackID)
	}
	if reflect.DeepEqual(representativeIDs, firstIDs) {
		t.Fatalf("representatives followed catalog ID order instead of embedding diversity: %v", representativeIDs)
	}
}

func metadataResolverCatalog(t *testing.T) *Catalog {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/resolver.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mustExec(t, db, `CREATE TABLE tracks (row INTEGER PRIMARY KEY, id TEXT, artist TEXT, title TEXT, search TEXT, unicode_search TEXT)`)
	mustExec(t, db, `CREATE TABLE artist_aliases (artist TEXT, alias TEXT, alias_search TEXT, alias_unicode TEXT)`)
	return &Catalog{db: db, version: "test:v2", hasAliases: true, hasUnicodeSearch: true, resolutionCache: make(map[string]core.ReferenceResolution), representativeCache: make(map[string][]core.WeightedTrack)}
}

func insertResolverTrack(t *testing.T, db *sql.DB, row int, id, artist, title string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO tracks VALUES (?, ?, ?, ?, ?, ?)`, row, id, artist, title, normalizeSearch(artist+" "+title), normalizeUnicodeSearch(artist+" "+title))
	if err != nil {
		t.Fatal(err)
	}
}

func mustExec(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.Exec(statement); err != nil {
		t.Fatal(err)
	}
}
