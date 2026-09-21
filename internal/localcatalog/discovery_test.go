package localcatalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/sqliteuri"
)

func TestSharedDiscoveryMoodProfilesAndNamespaces(t *testing.T) {
	ctx := context.Background()
	personal, manager := openTestCatalog(t, []librarypack.Track{
		{ID: "early", Artist: "Artist", Title: "Early", Album: "Early album", RawTags: []byte(`{"genre":"ambient","mood":"calm","originaldate":"1994-01-01","date":"2020"}`)},
		{ID: "late", Artist: "Artist", Title: "Late", Album: "Late album", RawTags: []byte(`{"genre":"rock","mood":"energetic","originaldate":"2005"}`)},
		{ID: "false", Artist: "Other", Title: "calm", Album: "Words", RawTags: []byte(`{"comment":"calm"}`)},
		{ID: "similar", Artist: "Similar", Title: "Music", RawTags: []byte(`{"genre":"ambient","mood":"calm"}`)},
	}, nil)
	defer personal.Close()
	lease, err := manager.Pin()
	if err != nil {
		t.Fatal(err)
	}
	shared, err := Open(lease, Options{SourceID: "shared", Shared: true})
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Close()
	if shared.NamespacedID("early") != "pack:shared:early" || shared.Provenance().Source != "shared_pack" {
		t.Fatal(shared.Provenance())
	}
	path, err := shared.ResolvePath(ctx, shared.NamespacedID("early"))
	if err != nil || path.State != PathNoEvidence {
		t.Fatalf("shared path: %+v %v", path, err)
	}
	intent := core.MusicIntent{Preferences: core.SemanticPreferences{Moods: []core.IntentPreference{{Value: "calm", Influence: core.InfluencePositive}}}}
	queries := recommendationQueries(ports.RetrievalRequest{Intent: intent})
	if len(queries) != 2 {
		t.Fatalf("queries %+v", queries)
	}
	var typed *MetadataQuery
	for _, query := range queries {
		if query.Metadata != nil && query.Metadata.Criterion != nil {
			typed = query.Metadata
		}
	}
	if typed == nil {
		t.Fatal("typed mood query missing")
	}
	hits, err := shared.Search(ctx, *typed)
	if err != nil || len(hits) != 2 {
		t.Fatalf("typed mood hits=%+v err=%v", hits, err)
	}
	profiles, err := shared.DiscoveryProfiles(ctx, core.MusicIntent{References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Artist"}}}, 20)
	if err != nil {
		t.Fatal(err)
	}
	foundEarly, foundLate, foundSimilar := false, false, false
	for _, p := range profiles {
		switch {
		case p.Album == "Early album":
			foundEarly = p.FirstYear == 1994 && p.LastYear == 1994
		case p.Album == "Late album":
			foundLate = p.FirstYear == 2005
		case p.Artist == "Similar":
			foundSimilar = true
		}
	}
	if !foundEarly || !foundLate || !foundSimilar {
		t.Fatalf("profiles %+v", profiles)
	}
	overlay, err := NewDiscoveryOverlay(ctx, []*Catalog{personal, shared}, testBase{}, testBase{}, baseRetriever{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer overlay.Close()
	for _, id := range []string{"local:main:early", "pack:shared:early", "bundled"} {
		if _, ok := overlay.Catalog.Meta(id); !ok {
			t.Fatalf("missing composite identity %s", id)
		}
	}
}

func TestCompanionProfilesAreBoundToPackAndReadAtRuntime(t *testing.T) {
	ctx := context.Background()
	personal, manager := openTestCatalog(t, []librarypack.Track{{ID: "recording", Artist: "Artist", Title: "Song", RawTags: []byte(`{"mood":"calm"}`)}}, nil)
	defer personal.Close()
	path := filepath.Join(t.TempDir(), "discovery.sqlite")
	dsn, err := sqliteuri.Writable(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE packs(pack_id TEXT,sha256 TEXT);CREATE TABLE profiles(pack_id TEXT,artist TEXT,album TEXT,period TEXT,kind TEXT,value TEXT,recordings INTEGER,albums INTEGER)"); err != nil {
		t.Fatal(err)
	}
	p := personal.Provenance()
	if _, err := db.Exec("INSERT INTO packs VALUES(?,?)", p.PackID, p.PackSHA256); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO profiles VALUES(?,'Artist','','','genre','ambient',3,2)", p.PackID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Pin()
	if err != nil {
		t.Fatal(err)
	}
	shared, err := Open(lease, Options{SourceID: "shared", Shared: true, ProfilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Close()
	profile, err := shared.artistProfile(ctx, "Artist")
	if err != nil || len(profile.Genres) != 1 || profile.Genres[0] != "ambient" {
		t.Fatalf("companion unused: %+v %v", profile, err)
	}
	if len(profile.Moods) != 0 {
		t.Fatalf("unexpected fallback on companion missingness: %+v", profile)
	}
	profile, err = shared.artistProfile(ctx, "artist")
	if err != nil || len(profile.Genres) != 1 || profile.Genres[0] != "ambient" {
		t.Fatalf("case-insensitive companion lookup: %+v %v", profile, err)
	}
	if err := shared.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE packs SET sha256='wrong'"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	lease, err = manager.Pin()
	if err != nil {
		t.Fatal(err)
	}
	if catalog, err := Open(lease, Options{SourceID: "shared", Shared: true, ProfilePath: path}); err == nil {
		_ = catalog.Close()
		t.Fatal("accepted mismatched companion")
	}
}

type identityMetadataBase struct {
	testBase
	meta core.TrackMeta
}

func (b identityMetadataBase) Meta(id string) (core.TrackMeta, bool) {
	if id == b.meta.Ref.ID {
		return b.meta, true
	}
	return b.testBase.Meta(id)
}

func TestSharedEvidenceTransfersOnlyAuthoritativeRecordingIdentity(t *testing.T) {
	ctx := context.Background()
	local, _ := openTestCatalog(t, []librarypack.Track{{ID: "same", Artist: "Artist", Title: "Song", ISRC: "USABC1200001", RawTags: []byte(`{"mood":"calm"}`), MERT: []float32{1, 0, 0}}}, nil)
	defer local.Close()
	base := identityMetadataBase{meta: core.TrackMeta{Ref: core.TrackRef{ID: "external", Artist: "Artist", Title: "Song", RecordingIdentity: "isrc:USABC1200001"}}}
	c := &CompositeCatalog{base: base, local: local, mode: ModeCombined}
	if meta, ok := c.Meta("external"); !ok || len(meta.Annotations) != 1 {
		t.Fatalf("metadata %+v %v", meta, ok)
	}
	if _, ok, err := c.LibraryVector(ctx, "external"); !ok || err != nil {
		t.Fatalf("vector %v %v", ok, err)
	}
	base.meta.Ref.RecordingIdentity = "isrc:USABC1200002"
	c.base = base
	if meta, _ := c.Meta("external"); len(meta.Annotations) != 0 {
		t.Fatalf("mismatched evidence %+v", meta)
	}
}

func TestResolvedKnowledgeBindsOnlyMatchingRecording(t *testing.T) {
	ctx := context.Background()
	const mbid = "11111111-2222-3333-4444-555555555555"
	local, _ := openTestCatalog(t, []librarypack.Track{{ID: "same", Artist: "Artist", Title: "Song", MusicBrainzRecording: mbid, MERT: []float32{1, 0, 0}}}, nil)
	defer local.Close()
	ref := core.TrackRef{ID: "external", Artist: "Artist", Title: "Song"}
	base := identityMetadataBase{meta: core.TrackMeta{Ref: ref}}
	c := &CompositeCatalog{base: base, local: local, mode: ModeCombined}
	for _, tc := range []struct {
		name   string
		status core.ResolutionStatus
		title  string
		want   bool
	}{
		{"resolved", core.ResolutionResolved, "Song", true},
		{"stale-title", core.ResolutionResolved, "Other", false},
		{"unresolved", core.ResolutionUnresolved, "Song", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			knowledgeRef := ref
			knowledgeRef.Title = tc.title
			c.BindRecordingKnowledge([]core.EnrichedTrack{{Ref: knowledgeRef, RecordingID: mbid, IdentityStatus: tc.status}})
			if _, ok, err := c.LibraryVector(ctx, ref.ID); err != nil || ok != tc.want {
				t.Fatalf("vector=%v err=%v", ok, err)
			}
			if base.meta.MusicBrainzRecording != "" {
				t.Fatal("mutated base catalog")
			}
		})
	}
}

func TestAnnotationsPreserveExplicitMultipleValues(t *testing.T) {
	values := annotations([]byte(`{"genre":["ambient","electronic","ambient"],"mood":"calm","comment":{"genre":"false"},"style":"rock/pop"}`))
	if len(values) != 4 {
		t.Fatalf("annotations: %+v", values)
	}
	if values[0].Value != "ambient" || values[1].Value != "electronic" || values[3].Value != "rock/pop" {
		t.Fatalf("multiplicity: %+v", values)
	}
}

func TestRecordingKnowledgeRejectsExistingCanonicalIdentityConflict(t *testing.T) {
	const mbid = "11111111-2222-3333-4444-555555555555"
	for _, identity := range []string{"musicbrainz:22222222-2222-3333-4444-555555555555", "isrc:USABC1200001"} {
		ref := core.TrackRef{ID: "external", Artist: "Artist", Title: "Song", RecordingIdentity: identity}
		base := identityMetadataBase{meta: core.TrackMeta{Ref: ref}}
		c := &CompositeCatalog{base: base, mode: ModeCombined}
		c.BindRecordingKnowledge([]core.EnrichedTrack{{Ref: ref, RecordingID: mbid, ISRC: "USABC1200002", IdentityStatus: core.ResolutionResolved}})
		meta, _ := c.baseMetadata(ref.ID)
		if meta.MusicBrainzRecording != "" {
			t.Fatalf("conflicting identity accepted: %+v", meta)
		}
	}
}
