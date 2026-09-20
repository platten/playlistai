package musicbrainz

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/mbindex"
	"github.com/platten/playlistai/internal/sqliteuri"
)

type dynamicCatalogFixture struct {
	*fakes.Catalog
	registered []core.TrackMeta
}

func (c *dynamicCatalogFixture) RegisterDynamicTrack(meta core.TrackMeta) error {
	c.registered = append(c.registered, meta)
	return nil
}

type candidatePreviewFixture struct{ result core.ResolvedAudioPreview }

func (p candidatePreviewFixture) ResolveAudioPreview(context.Context, core.TrackRef, core.EnrichedTrack) (core.ResolvedAudioPreview, error) {
	return p.result, nil
}

func TestDynamicMusicBrainzCandidateRegistersMetadataWithoutPreview(t *testing.T) {
	length := int64(241000)
	recording := mbRecording{ID: acousticTestID, Title: "Outside Song", Length: &length, ISRCs: []string{"USAAA0000001"}, FirstReleaseDate: "2024-02-03"}
	recording.ArtistCredit = []mbArtistCredit{{Name: "Outside Artist"}}
	recording.ArtistCredit[0].Artist.ID = contextArtistID
	recording.ArtistCredit[0].Artist.Name = "Outside Artist"
	recording.Genres = []mbTag{{Name: "dream pop", Count: 8}}
	cat := &dynamicCatalogFixture{Catalog: fakes.NewCatalog(2)}
	client := &Client{candidatePreview: candidatePreviewFixture{result: core.ResolvedAudioPreview{
		Identity: core.PreviewIdentity{Status: core.ResolutionResolved, Provider: "deezer", ProviderID: "77", ISRC: "USAAA0000001"},
		URL:      "https://cdn.example/77.mp3",
	}}}
	var snapshot core.KnowledgeSnapshot
	if err := client.addDynamicKnowledgeRecording(context.Background(), recording, cat, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(cat.registered) != 1 || cat.registered[0].Ref.ID != "musicbrainz:"+acousticTestID || cat.registered[0].FullRecordingDuration == nil || cat.registered[0].PreviewURL != "" {
		t.Fatalf("registration = %+v", cat.registered)
	}
	if len(snapshot.Candidates) != 1 || snapshot.Candidates[0].ID != "musicbrainz:"+acousticTestID || len(snapshot.Tracks) != 1 || snapshot.Tracks[0].RecordingID != acousticTestID {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if snapshot.Tracks[0].GenreTags[0].Name != "dream pop" {
		t.Fatalf("MusicBrainz evidence lost: %+v", snapshot.Tracks[0])
	}
}

func TestDynamicMusicBrainzCandidateRejectsUnidentifiedRecording(t *testing.T) {
	recording := mbRecording{ID: "mb-recording", Title: "Outside Song", ArtistCredit: []mbArtistCredit{{Name: "Outside Artist"}}}
	cat := &dynamicCatalogFixture{Catalog: fakes.NewCatalog(2)}
	client := &Client{candidatePreview: candidatePreviewFixture{result: core.ResolvedAudioPreview{Identity: core.PreviewIdentity{Status: core.ResolutionUnresolved, Provider: "deezer"}}}}
	var snapshot core.KnowledgeSnapshot
	if err := client.addDynamicKnowledgeRecording(context.Background(), recording, cat, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(cat.registered) != 0 || len(snapshot.Candidates) != 0 {
		t.Fatalf("unresolved candidate admitted: %+v %+v", cat.registered, snapshot)
	}
}

func TestEnhancedCandidateStreamUsesOfflineMusicBrainzOutsideDeejAI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "musicbrainz.sqlite")
	dsn, err := sqliteuri.Writable(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := `
CREATE TABLE info(key TEXT PRIMARY KEY,value TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE artists(mbid TEXT PRIMARY KEY,name TEXT NOT NULL,name_key TEXT NOT NULL,sort_name TEXT NOT NULL,comment TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE artist_aliases(artist_mbid TEXT NOT NULL,name TEXT NOT NULL,name_key TEXT NOT NULL,sort_name TEXT NOT NULL,locale TEXT NOT NULL,PRIMARY KEY(artist_mbid,name,locale)) WITHOUT ROWID;
CREATE TABLE artist_tags(tag_key TEXT NOT NULL,artist_mbid TEXT NOT NULL,tag TEXT NOT NULL,votes INTEGER NOT NULL,PRIMARY KEY(tag_key,artist_mbid,tag)) WITHOUT ROWID;
CREATE TABLE recordings(mbid TEXT PRIMARY KEY,title TEXT NOT NULL,title_key TEXT NOT NULL,artist_credit TEXT NOT NULL,duration_ms INTEGER NOT NULL,first_release_date TEXT NOT NULL,comment TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE recording_artists(recording_mbid TEXT NOT NULL,position INTEGER NOT NULL,artist_mbid TEXT NOT NULL,name TEXT NOT NULL,name_key TEXT NOT NULL,join_phrase TEXT NOT NULL,PRIMARY KEY(recording_mbid,position)) WITHOUT ROWID;
CREATE TABLE recording_isrcs(recording_mbid TEXT NOT NULL,isrc TEXT NOT NULL,PRIMARY KEY(recording_mbid,isrc)) WITHOUT ROWID;
CREATE TABLE recording_tags(recording_mbid TEXT NOT NULL,tag_key TEXT NOT NULL,tag TEXT NOT NULL,votes INTEGER NOT NULL,PRIMARY KEY(recording_mbid,tag_key,tag)) WITHOUT ROWID;`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	info, _ := json.Marshal(mbindex.Info{Version: mbindex.IndexVersion, Snapshot: "20260912-001001", BuiltAt: "2026-09-12T00:10:01Z", CoreLicense: "CC0-1.0", TagsLicense: "CC-BY-NC-SA-3.0", Artists: 1, Recordings: 1, ArtistTags: 1})
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO info VALUES('manifest',?)`, []any{string(info)}},
		{`INSERT INTO artists VALUES('artist-1','Outside Artist','outside artist','Outside Artist','')`, nil},
		{`INSERT INTO artist_tags VALUES('dream pop','artist-1','dream pop',20)`, nil},
		{`INSERT INTO recordings VALUES('recording-1','Outside Song','outside song','Outside Artist',241000,'2024-02-03','')`, nil},
		{`INSERT INTO recording_artists VALUES('recording-1',0,'artist-1','Outside Artist','outside artist','')`, nil},
		{`INSERT INTO recording_isrcs VALUES('recording-1','USAAA0000001')`, nil},
		{`INSERT INTO recording_tags VALUES('recording-1','dream pop','dream pop',8)`, nil},
	}
	for _, statement := range statements {
		query := strings.NewReplacer("artist-1", contextArtistID, "recording-1", acousticTestID).Replace(statement.query)
		if _, err := db.Exec(query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	offline, err := mbindex.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer offline.Close()
	base := fakes.NewCatalog(2)
	overlay, err := catalog.OpenDynamic(base, base, filepath.Join(t.TempDir(), "candidates.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer overlay.Close()
	client := &Client{offline: offline, candidatePreview: forbiddenCandidatePreview{t: t}}
	intent := core.MusicIntent{Seed: "42", Count: 1, Controls: core.IntentControls{RecommendationMode: core.EnhancedHybrid}}
	intent.Preferences.Genres = []core.IntentPreference{{Value: "dream pop", Influence: core.InfluencePositive}}
	stream := client.OpenCandidates(intent, overlay, overlay)
	track, err := stream.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if track.ID != "musicbrainz:"+acousticTestID || track.Artist != "Outside Artist" || track.Title != "Outside Song" {
		t.Fatalf("candidate = %+v", track)
	}
	meta, ok := overlay.Meta(track.ID)
	if !ok || meta.PreviewURL != "" || meta.FullRecordingDuration == nil {
		t.Fatalf("dynamic metadata = %+v ok=%v", meta, ok)
	}
	snapshot := stream.Snapshot()
	if len(snapshot.Tracks) != 1 || snapshot.Tracks[0].RecordingID != acousticTestID || len(snapshot.Tracks[0].GenreTags) != 1 {
		t.Fatalf("knowledge snapshot = %+v", snapshot)
	}
}
