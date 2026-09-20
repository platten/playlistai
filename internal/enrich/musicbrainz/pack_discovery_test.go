package musicbrainz

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

type packVersionResolver struct {
	ports.ReferenceResolver
	version string
}

func (r packVersionResolver) CatalogVersion() string { return r.version }

func TestPackDiscoveryRejectsChangedCatalogWithoutNetwork(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); http.Error(w, "unexpected network", 500) }))
	defer server.Close()
	client, err := New(Config{UserAgent: "PlaylistAI fixture", MirrorURL: server.URL, WikidataURL: server.URL, WikipediaURL: server.URL, Interval: time.Nanosecond, CachePath: filepath.Join(t.TempDir(), "cache.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	base := fakes.NewCatalog(2)
	for _, scenario := range []struct{ requestKeyKnown, noProfiles bool }{{false, false}, {true, false}, {true, true}} {
		intent := core.MusicIntent{Seed: "42", Controls: core.IntentControls{RecommendationMode: core.EnhancedHybrid}, Knowledge: &core.KnowledgeSnapshot{
			PackProfiles: []core.DiscoveryProfile{{Artist: "Old artist", PackIDs: []string{"old-pack"}}}, DiscoveryRecorded: true,
			Discovery: []core.TrackRef{{ID: "pack:old:track", Artist: "Old artist", Title: "Song"}},
		}}
		if scenario.noProfiles {
			intent.Knowledge.PackProfiles = nil
			intent.Knowledge.DiscoveryCatalog = "catalog+old-pack"
			intent.Preferences.Genres = []core.IntentPreference{{Value: "ambient", Influence: core.InfluencePositive}}
		}
		intent.Knowledge.DiscoveryKey = discoveryKey(intent, "catalog+old-pack")
		if scenario.requestKeyKnown {
			intent.Knowledge.DiscoveryRequestKey = discoveryKey(intent, "")
		}
		stream := client.OpenCandidates(intent, base, packVersionResolver{base, "catalog+new-pack"})
		if stream == nil {
			t.Fatal("missing replay compatibility check")
		}
		if _, err := stream.Next(context.Background()); !errors.Is(err, ports.ErrDiscoveryGenerationMismatch) {
			t.Fatalf("scenario=%+v got %v", scenario, err)
		}
		if len(intent.Knowledge.Discovery) != 1 {
			t.Fatal("mutated original history")
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("replay performed %d provider requests", calls.Load())
	}
}

type packIdentityCatalog struct {
	ports.Catalog
	tracks map[string]core.TrackMeta
}

func (c packIdentityCatalog) Meta(id string) (core.TrackMeta, bool) {
	meta, ok := c.tracks[id]
	return meta, ok
}

func TestPackRecordingIndexLinksIdentityAndRejectsConflicts(t *testing.T) {
	const mbid = "aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb"
	ref := core.TrackRef{ID: "pack:source:track", Artist: "Artist", Title: "Song", RecordingIdentity: "musicbrainz:" + mbid}
	for _, tc := range []struct {
		name, packMBID, packISRC, providerMBID string
		providerISRCs                          []string
		want                                   string
	}{
		{"matching recording MBID", mbid, "", mbid, nil, ref.ID},
		{"matching ISRC", "", "USAAA1200001", mbid, []string{"USAAA1200001"}, ref.ID},
		{"unidentified exact name", "", "", mbid, nil, ref.ID},
		{"conflicting recording MBID", mbid, "", "bbbbbbbb-1111-2222-3333-bbbbbbbbbbbb", nil, ""},
		{"conflicting ISRC with same name", "", "USAAA1200001", mbid, []string{"USAAA1200002"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cat := packIdentityCatalog{Catalog: fakes.NewCatalog(2), tracks: map[string]core.TrackMeta{ref.ID: {Ref: ref, MusicBrainzRecording: tc.packMBID, ISRC: tc.packISRC}}}
			index := indexKnownArtistRecordings(cat, []core.TrackRef{ref})
			r := mbRecording{ID: tc.providerMBID, Title: ref.Title, ISRCs: tc.providerISRCs, ArtistCredit: []mbArtistCredit{{Name: ref.Artist}}}
			if got := matchKnownRecording(cat, index, r); got != tc.want {
				t.Fatalf("matched %q want %q", got, tc.want)
			}
		})
	}
	other := ref
	other.ID = "pack:source:other"
	other.RecordingIdentity = ""
	cat := packIdentityCatalog{Catalog: fakes.NewCatalog(2), tracks: map[string]core.TrackMeta{ref.ID: {Ref: ref}, other.ID: {Ref: other}}}
	index := indexKnownArtistRecordings(cat, []core.TrackRef{ref, other})
	if got := matchKnownRecording(cat, index, mbRecording{Title: ref.Title, ArtistCredit: []mbArtistCredit{{Name: ref.Artist}}}); got != "" {
		t.Fatalf("ambiguous name matched %q", got)
	}
}

func TestPackDiscoveryReplaysWithoutNetworkAndKeysProfiles(t *testing.T) {
	base := fakes.NewCatalog(2)
	intent := core.MusicIntent{Seed: "42", Controls: core.IntentControls{RecommendationMode: core.EnhancedHybrid}, Knowledge: &core.KnowledgeSnapshot{
		PackProfiles: []core.DiscoveryProfile{{Artist: "Artist", Moods: []string{"melancholic"}, PackIDs: []string{"pack1"}}},
		Discovery:    []core.TrackRef{{ID: "recording", Artist: "Artist", Title: "Song"}}, DiscoveryRecorded: true,
	}}
	intent.Knowledge.DiscoveryKey = discoveryKey(intent, base.CatalogVersion())
	client := &Client{}
	stream := client.OpenCandidates(intent, base, base)
	if stream == nil {
		t.Fatal("mood-only pack discovery did not open")
	}
	got, err := stream.Next(context.Background())
	if err != nil || got.ID != "recording" {
		t.Fatalf("replay: %+v %v", got, err)
	}
	prior := intent.Knowledge.DiscoveryKey
	intent.Knowledge.PackProfiles[0].PackIDs = []string{"pack2"}
	if discoveryKey(intent, base.CatalogVersion()) == prior {
		t.Fatal("pack provenance not in discovery key")
	}
	if stream.Snapshot().PackProfiles[0].PackIDs[0] != "pack1" {
		t.Fatal("snapshot aliases caller's profile")
	}
}

func TestMetadataRecordingWithoutPreviewRetainsIdentityAndRecordingTags(t *testing.T) {
	base := fakes.NewCatalog(2)
	path := filepath.Join(t.TempDir(), "dynamic.sqlite")
	dynamic, err := catalog.OpenDynamic(base, base, path)
	if err != nil {
		t.Fatal(err)
	}
	defer dynamic.Close()
	client := &Client{}
	r := mbRecording{ID: "aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb", Title: "Quiet Song", Genres: []mbTag{{Name: "ambient", Count: 2}}, ArtistCredit: []mbArtistCredit{{Name: "Quiet Artist"}}}
	r.ArtistCredit[0].Artist.ID = contextArtistID
	var snapshot core.KnowledgeSnapshot
	if err := client.addDynamicKnowledgeRecording(context.Background(), r, dynamic, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Candidates) != 1 || snapshot.Candidates[0].ID != "musicbrainz:"+r.ID {
		t.Fatalf("metadata-only candidate: %+v", snapshot)
	}
	if len(snapshot.Tracks[0].GenreTags) != 1 {
		t.Fatal("recording tags lost")
	}
	reopened, err := catalog.OpenDynamic(base, base, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	meta, ok := reopened.Meta(snapshot.Candidates[0].ID)
	if !ok || meta.MusicBrainzRecording != r.ID || meta.PreviewURL != "" || meta.Ref.RecordingIdentity != "musicbrainz:"+r.ID {
		t.Fatalf("metadata identity did not persist: %+v", meta)
	}
	if _, ok := reopened.Vectors(meta.Ref.ID); ok {
		t.Fatal("invented dense vectors")
	}
}

func TestWikidataArtistNeighborsRequireReciprocalIdentity(t *testing.T) {
	const targetID = "33333333-3333-3333-3333-333333333333"
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprint(conflict), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/ws/2/artist/" + contextArtistID:
					fmt.Fprintf(w, `{"id":%q,"name":"Origin","relations":[{"type":"wikidata","url":{"resource":"https://www.wikidata.org/wiki/Q123"}}]}`, contextArtistID)
				case "/wiki/Special:EntityData/Q123.json":
					fmt.Fprintf(w, `{"entities":{"Q123":{"id":"Q123","lastrevid":7,"claims":{"P434":[{"mainsnak":{"snaktype":"value","datavalue":{"value":%q}}}],"P737":[{"mainsnak":{"snaktype":"value","datavalue":{"value":{"id":"Q456"}}}}]}}}}`, contextArtistID)
				case "/wiki/Special:EntityData/Q456.json":
					fmt.Fprintf(w, `{"entities":{"Q456":{"id":"Q456","lastrevid":8,"claims":{"P434":[{"mainsnak":{"snaktype":"value","datavalue":{"value":%q}}}]}}}}`, targetID)
				case "/ws/2/artist/" + targetID:
					qid := "Q456"
					if conflict {
						qid = "Q999"
					}
					fmt.Fprintf(w, `{"id":%q,"name":"Related","relations":[{"type":"wikidata","url":{"resource":"https://www.wikidata.org/wiki/%s"}}]}`, targetID, qid)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.Error(w, "unexpected", 404)
				}
			}))
			defer server.Close()
			client, err := New(Config{UserAgent: "PlaylistAI fixture", MirrorURL: server.URL, WikidataURL: server.URL, WikipediaURL: server.URL, Interval: time.Nanosecond, CachePath: filepath.Join(t.TempDir(), "cache.sqlite")})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			var snapshot core.KnowledgeSnapshot
			got := client.wikidataArtistNeighbors(context.Background(), contextArtistID, "Origin", &snapshot)
			if conflict && len(got) != 0 || !conflict && (len(got) != 1 || got[0].ID != targetID) {
				t.Fatalf("neighbors=%+v", got)
			}
			if !conflict && !strings.Contains(strings.Join(snapshot.Sources, " "), "revision=8") {
				t.Fatal("source revision missing")
			}
			if calls.Load() != 4 {
				t.Fatalf("unbounded requests: %d", calls.Load())
			}
		})
	}
}
