package recognition

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/genrevocab"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/mbindex"
	"github.com/platten/playlistai/internal/ports"
)

func TestArtistFirstRecognitionUsesLongestNamesAndContext(t *testing.T) {
	store := recognitionStore(t)
	defer store.Close()
	prompt := "music by Low with low bass, like The Ambient And The Loud Machine"
	got := Apply(context.Background(), prompt, lexicon.Extract(prompt), store, nil)
	grounded := groundedAtoms(got)
	if len(grounded) != 2 || grounded[0].Value != "Low" || grounded[1].Value != "The Ambient And The Loud Machine" {
		t.Fatalf("unexpected grounded artists: %+v", grounded)
	}
	for _, atom := range got.Atoms {
		if atom.Kind == "genre" && atom.Evidence[0].Start >= grounded[1].Evidence[0].Start && atom.Evidence[0].End <= grounded[1].Evidence[0].End {
			t.Fatalf("artist name leaked genre constraint: %+v", atom)
		}
	}
}

func TestReferenceListRetainsIndependentArtistsInSourceOrder(t *testing.T) {
	store := recognitionStore(t)
	defer store.Close()
	prompt := "like Low and Portishead, then jazz and soul"
	got := Apply(context.Background(), prompt, lexicon.Extract(prompt), store, nil)
	grounded := groundedAtoms(got)
	if len(grounded) != 2 || grounded[0].Value != "Low" || grounded[1].Value != "Portishead" {
		t.Fatalf("independent references changed: %+v", grounded)
	}
}

func TestNegativeSingleWordArtistUsesContextWithoutConsumingDescription(t *testing.T) {
	store := recognitionStore(t)
	defer store.Close()
	for _, prompt := range []string{"No Air", "music by Low with low bass and air guitar"} {
		got := Apply(context.Background(), prompt, lexicon.Extract(prompt), store, nil)
		grounded := groundedAtoms(got)
		if prompt == "No Air" {
			if len(grounded) != 1 || grounded[0].Value != "Air" || grounded[0].Polarity != "negative" {
				t.Fatalf("negative artist was not grounded: %+v", grounded)
			}
		} else if len(grounded) != 1 || grounded[0].Value != "Low" {
			t.Fatalf("descriptive air/low wording became an artist: %+v", grounded)
		}
	}
}

func TestArtistScopedTitleDisambiguatesHomonymAndPreservesRole(t *testing.T) {
	store := recognitionStore(t)
	defer store.Close()
	prompt := "include Shared Name — Unique Signal, then some jazz"
	got := Apply(context.Background(), prompt, lexicon.Extract(prompt), store, nil)
	grounded := groundedAtoms(got)
	if len(grounded) != 1 || grounded[0].Kind != "required_track" || grounded[0].Grounding == nil || len(grounded[0].Grounding.Candidates) != 1 || grounded[0].Grounding.Candidates[0].ArtistID != "shared-b" {
		t.Fatalf("recording did not uniquely ground the homonym: %+v", grounded)
	}
	if grounded[0].Evidence[0].Text != "Shared Name — Unique Signal" {
		t.Fatalf("wrong source occurrence: %+v", grounded[0].Evidence)
	}
}

func TestArtistScopedTitleRetainsAmbiguousRecordingVersions(t *testing.T) {
	store := recognitionStore(t)
	defer store.Close()
	prompt := "like Shared Name — Signal"
	got := Apply(context.Background(), prompt, lexicon.Extract(prompt), store, nil)
	grounded := groundedAtoms(got)
	if len(grounded) != 1 || grounded[0].Grounding == nil || len(grounded[0].Grounding.Candidates) != 2 {
		t.Fatalf("recording ambiguity was collapsed: %+v", grounded)
	}
	artists := map[string]bool{}
	for _, candidate := range grounded[0].Grounding.Candidates {
		artists[candidate.ArtistID] = true
	}
	if !artists["shared-a"] || !artists["shared-b"] {
		t.Fatalf("artist-scoped candidates were crossed or lost: %+v", grounded[0].Grounding.Candidates)
	}
}

func TestMultipleVersionsFromOneHomonymKeepItsCanonicalArtist(t *testing.T) {
	store := recognitionStore(t)
	defer store.Close()
	prompt := "like Shared Name — Versions"
	got := Apply(context.Background(), prompt, lexicon.Extract(prompt), store, nil)
	grounded := groundedAtoms(got)
	if len(grounded) != 1 || grounded[0].Value != "Shared Name — Versions" || len(grounded[0].Grounding.Candidates) != 2 {
		t.Fatalf("one artist's recording versions used another homonym: %+v", grounded)
	}
	for _, candidate := range grounded[0].Grounding.Candidates {
		if candidate.ArtistID != "shared-b" {
			t.Fatalf("unexpected artist candidate: %+v", grounded[0].Grounding.Candidates)
		}
	}
}

func TestSeveralArtistTitlesPairWithinTheirClauses(t *testing.T) {
	store := recognitionStore(t)
	defer store.Close()
	prompt := "like Low — Lullaby and Portishead — Roads"
	got := Apply(context.Background(), prompt, lexicon.Extract(prompt), store, nil)
	grounded := groundedAtoms(got)
	if len(grounded) != 2 || grounded[0].Kind != "track" || grounded[1].Kind != "track" {
		t.Fatalf("artist/title pairs were not retained: %+v", grounded)
	}
	if got, want := grounded[0].Grounding.Candidates[0].ID, "lullaby-low"; got != want {
		t.Fatalf("first title crossed clauses: got %q want %q", got, want)
	}
	if got, want := grounded[1].Grounding.Candidates[0].ID, "roads-portishead"; got != want {
		t.Fatalf("second title attached to the wrong artist: got %q want %q", got, want)
	}
	if grounded[0].Evidence[0].Start >= grounded[1].Evidence[0].Start {
		t.Fatalf("source order changed: %+v", grounded)
	}
}

func TestArtistTitlesRespectDescriptorsTitleByArtistAndQuotedAdjacency(t *testing.T) {
	store := recognitionStore(t)
	defer store.Close()
	for _, tc := range []struct {
		prompt string
		ids    []string
	}{
		{"like Low — Lullaby with soft jazz", []string{"lullaby-low"}},
		{"Song A by Artist A and Song B by Artist B", []string{"song-a", "song-b"}},
		{"Song A by Artist A Live", []string{"song-a"}},
		{`like Low "Lullaby"`, []string{"lullaby-low"}},
		{"like Portishead Roads", []string{"roads-portishead"}},
	} {
		got := Apply(context.Background(), tc.prompt, lexicon.Extract(tc.prompt), store, nil)
		grounded := groundedAtoms(got)
		if len(grounded) != len(tc.ids) {
			t.Fatalf("%q: grounded=%+v", tc.prompt, grounded)
		}
		for i, id := range tc.ids {
			if grounded[i].Kind != "track" || grounded[i].Grounding.Candidates[0].ID != id {
				t.Fatalf("%q: pair %d=%+v", tc.prompt, i, grounded[i])
			}
		}
	}
}

func TestAliasAndUTF8OffsetsSurvive(t *testing.T) {
	store := recognitionStore(t)
	defer store.Close()
	prompt := "类似 music by Bjork and music by 宇多田ヒカル"
	got := Apply(context.Background(), prompt, lexicon.Extract(prompt), store, nil)
	grounded := groundedAtoms(got)
	if len(grounded) != 2 {
		t.Fatalf("expected two grounded aliases: %+v", grounded)
	}
	for _, atom := range grounded {
		e := atom.Evidence[0]
		if prompt[e.Start:e.End] != e.Text {
			t.Fatalf("UTF-8 byte offsets changed: %+v", e)
		}
	}
	if grounded[0].Grounding.MatchType != "alias" || grounded[0].Grounding.Candidates[0].Name != "Björk" {
		t.Fatalf("alias provenance lost: %+v", grounded[0].Grounding)
	}
}

func TestRulesParserPreservesGroundedRecordingWithoutModel(t *testing.T) {
	store := recognitionStore(t)
	defer store.Close()
	prompt := "include Shared Name — Signal"
	source := Apply(context.Background(), prompt, lexicon.Extract(prompt), store, nil)
	intent, err := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt, SourceFacts: &source})
	if err != nil {
		t.Fatal(err)
	}
	if len(intent.RequiredTracks) != 1 || intent.RequiredTracks[0].Grounding == nil || len(intent.RequiredTracks[0].Grounding.Candidates) != 2 {
		t.Fatalf("grounding vanished through rules reconciliation: %+v", intent.RequiredTracks)
	}
}

func TestRulesParserPreservesGroundedTrackExclusion(t *testing.T) {
	store := recognitionStore(t)
	defer store.Close()
	prompt := "avoid Low — Lullaby"
	source := Apply(context.Background(), prompt, lexicon.Extract(prompt), store, nil)
	intent, err := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt, SourceFacts: &source})
	if err != nil {
		t.Fatal(err)
	}
	if len(intent.References) != 1 || intent.References[0].Kind != core.ReferenceTrack || intent.References[0].Influence != core.InfluenceNegative || intent.References[0].Grounding == nil {
		t.Fatalf("grounded track exclusion changed polarity: %+v", intent.References)
	}
}

func TestProviderGenresPreserveOrAndRelationships(t *testing.T) {
	vocabulary := &genrevocab.Vocabulary{ContentSHA256: "fixture", Genres: []genrevocab.Genre{{MBID: "x", Name: "Alpha genre"}, {MBID: "y", Name: "Omega style"}}}
	for _, tc := range []struct {
		join    string
		grouped bool
	}{{"or", true}, {"and", false}} {
		prompt := "Alpha genre " + tc.join + " Omega style"
		got := Apply(context.Background(), prompt, lexicon.Extract(prompt), nil, vocabulary)
		var genres []core.IntentAtom
		for _, atom := range got.Atoms {
			if atom.Kind == "genre" {
				genres = append(genres, atom)
			}
		}
		if len(genres) != 2 || (genres[0].Group != "") != tc.grouped || genres[0].Group != genres[1].Group {
			t.Fatalf("provider genre %s relationship changed: %+v", tc.join, genres)
		}
	}
}

type crowdedLookup struct{}

func (crowdedLookup) SnapshotIdentity() mbindex.SnapshotIdentity {
	return mbindex.SnapshotIdentity{IndexVersion: mbindex.IndexVersion, Snapshot: "20260920-120000"}
}
func (crowdedLookup) LookupArtistNames(_ context.Context, names []string) ([]mbindex.ArtistNameLookup, error) {
	out := make([]mbindex.ArtistNameLookup, len(names))
	for i, name := range names {
		out[i] = mbindex.ArtistNameLookup{Name: name, NameKey: core.NormalizeIdentityPart(name)}
		if out[i].NameKey == "shared name" {
			out[i].Candidates = []mbindex.ArtistIdentity{{MBID: "a", Name: "Shared Name"}, {MBID: "b", Name: "Shared Name"}}
		}
	}
	return out, nil
}
func (crowdedLookup) LookupArtistRecordings(_ context.Context, queries []mbindex.ArtistRecordingQuery) ([]mbindex.ArtistRecordingLookup, error) {
	out := make([]mbindex.ArtistRecordingLookup, len(queries))
	for i, query := range queries {
		out[i] = mbindex.ArtistRecordingLookup{ArtistMBID: query.ArtistMBID, Title: query.Title}
		for n := 0; n < 40; n++ {
			out[i].Candidates = append(out[i].Candidates, mbindex.RecordingIdentity{MBID: fmt.Sprintf("%s-%02d", query.ArtistMBID, n), Title: query.Title, ArtistCredit: "Shared Name"})
		}
	}
	return out, nil
}

func TestRecordingGroundingHasOneGlobalCandidateBound(t *testing.T) {
	prompt := "like Shared Name — Crowded"
	got := Apply(context.Background(), prompt, lexicon.Extract(prompt), crowdedLookup{}, nil)
	grounded := groundedAtoms(got)
	if len(grounded) != 1 || grounded[0].Grounding == nil || len(grounded[0].Grounding.Candidates) != mbindex.MaxLookupCandidates || !grounded[0].Grounding.Truncated {
		t.Fatalf("combined recording candidates were not bounded: %+v", grounded)
	}
}

func TestLookupLimitsFallBackWithoutInventingIdentity(t *testing.T) {
	store := recognitionStore(t)
	defer store.Close()
	prompt := strings.TrimSpace(strings.Repeat("ordinaryword ", MaxLexicalWords+1))
	got := Apply(context.Background(), prompt, lexicon.Extract(prompt), store, nil)
	if !got.Recognition.Incomplete || got.Recognition.ReferenceLookup != "incomplete" || len(groundedAtoms(got)) != 0 || got.OriginalText != prompt {
		t.Fatalf("lookup limit did not retain an honest fallback: %+v", got.Recognition)
	}
}

func groundedAtoms(source core.IntentTranslation) []core.IntentAtom {
	var out []core.IntentAtom
	for _, atom := range source.Atoms {
		if atom.Grounding != nil {
			out = append(out, atom)
		}
	}
	return out
}

func recognitionStore(t *testing.T) *mbindex.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "musicbrainz.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	schema := `
CREATE TABLE info(key TEXT PRIMARY KEY,value TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE artists(mbid TEXT PRIMARY KEY,name TEXT NOT NULL,name_key TEXT NOT NULL,sort_name TEXT NOT NULL,comment TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE artist_aliases(artist_mbid TEXT NOT NULL,name TEXT NOT NULL,name_key TEXT NOT NULL,sort_name TEXT NOT NULL,locale TEXT NOT NULL,PRIMARY KEY(artist_mbid,name,locale)) WITHOUT ROWID;
CREATE TABLE recordings(mbid TEXT PRIMARY KEY,title TEXT NOT NULL,title_key TEXT NOT NULL,artist_credit TEXT NOT NULL,duration_ms INTEGER NOT NULL,first_release_date TEXT NOT NULL,comment TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE recording_artists(recording_mbid TEXT NOT NULL,position INTEGER NOT NULL,artist_mbid TEXT NOT NULL,name TEXT NOT NULL,name_key TEXT NOT NULL,join_phrase TEXT NOT NULL,PRIMARY KEY(recording_mbid,position)) WITHOUT ROWID;
CREATE TABLE recording_isrcs(recording_mbid TEXT NOT NULL,isrc TEXT NOT NULL,PRIMARY KEY(recording_mbid,isrc)) WITHOUT ROWID;
CREATE TABLE recording_tags(recording_mbid TEXT NOT NULL,tag_key TEXT NOT NULL,tag TEXT NOT NULL,votes INTEGER NOT NULL,PRIMARY KEY(recording_mbid,tag_key,tag)) WITHOUT ROWID;
CREATE TABLE artist_tags(tag_key TEXT NOT NULL,artist_mbid TEXT NOT NULL,tag TEXT NOT NULL,votes INTEGER NOT NULL,PRIMARY KEY(tag_key,artist_mbid,tag)) WITHOUT ROWID;
CREATE INDEX artist_name_key ON artists(name_key);
CREATE INDEX artist_alias_key ON artist_aliases(name_key);
CREATE INDEX recording_title_key ON recordings(title_key);
CREATE INDEX recording_artist ON recording_artists(artist_mbid,recording_mbid);
CREATE INDEX recording_artist_name ON recording_artists(name_key,recording_mbid);`
	if _, err = db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	artists := [][4]string{
		{"low", "Low", "low", ""},
		{"air", "Air", "air", ""},
		{"portishead", "Portishead", "portishead", ""},
		{"long", "The Ambient And The Loud Machine", "the ambient and the loud machine", "six-word fixture"},
		{"bjork", "Björk", "björk", ""},
		{"utada", "宇多田ヒカル", "宇多田ヒカル", ""},
		{"shared-a", "Shared Name", "shared name", "first"},
		{"shared-b", "Shared Name", "shared name", "second"},
		{"artist-a", "Artist A", "artist a", ""},
		{"artist-b", "Artist B", "artist b", ""},
	}
	for _, artist := range artists {
		if _, err = db.Exec(`INSERT INTO artists VALUES(?,?,?,?,?)`, artist[0], artist[1], artist[2], artist[1], artist[3]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO artist_aliases VALUES('bjork','Bjork','bjork','Bjork','en')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`
INSERT INTO recordings VALUES('unique-signal-b','Unique Signal','unique signal','Shared Name',180000,'2000','');
INSERT INTO recording_artists VALUES('unique-signal-b',0,'shared-b','Shared Name','shared name','');
INSERT INTO recordings VALUES('signal-a','Signal','signal','Shared Name',180000,'2000','first version');
INSERT INTO recording_artists VALUES('signal-a',0,'shared-a','Shared Name','shared name','');
INSERT INTO recordings VALUES('signal-b','Signal','signal','Shared Name',180000,'2001','second version');
INSERT INTO recording_artists VALUES('signal-b',0,'shared-b','Shared Name','shared name','');
INSERT INTO recordings VALUES('versions-b-1','Versions','versions','Shared Name',180000,'2001','first mix');
INSERT INTO recording_artists VALUES('versions-b-1',0,'shared-b','Shared Name','shared name','');
INSERT INTO recordings VALUES('versions-b-2','Versions','versions','Shared Name',180000,'2002','second mix');
INSERT INTO recording_artists VALUES('versions-b-2',0,'shared-b','Shared Name','shared name','');
INSERT INTO recordings VALUES('lullaby-low','Lullaby','lullaby','Low',180000,'2001','');
INSERT INTO recording_artists VALUES('lullaby-low',0,'low','Low','low','');
INSERT INTO recordings VALUES('roads-portishead','Roads','roads','Portishead',180000,'2002','');
INSERT INTO recording_artists VALUES('roads-portishead',0,'portishead','Portishead','portishead','');
INSERT INTO recordings VALUES('song-a','Song A','song a','Artist A',180000,'2003','');
INSERT INTO recording_artists VALUES('song-a',0,'artist-a','Artist A','artist a','');
INSERT INTO recordings VALUES('song-b','Song B','song b','Artist B',180000,'2004','');
INSERT INTO recording_artists VALUES('song-b',0,'artist-b','Artist B','artist b','');`); err != nil {
		t.Fatal(err)
	}
	info := mbindex.Info{Version: mbindex.IndexVersion, Snapshot: "20260920-120000", BuiltAt: "2026-09-20T12:00:00Z", CoreLicense: "CC0-1.0", TagsLicense: "CC-BY-NC-SA-3.0", Artists: int64(len(artists)), Recordings: 9}
	raw, _ := json.Marshal(info)
	if _, err = db.Exec(`INSERT INTO info VALUES('manifest',?)`, string(raw)); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := mbindex.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return store
}
