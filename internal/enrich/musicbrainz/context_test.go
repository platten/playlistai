package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

const contextArtistID = "11111111-1111-1111-1111-111111111111"
const contextAlbumID = "22222222-2222-2222-2222-222222222222"
const contextAlbumTwo = "33333333-3333-3333-3333-333333333333"

func contextTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); handler(w, r) }))
	t.Cleanup(server.Close)
	c, err := New(Config{UserAgent: "PlaylistAI context fixture", MirrorURL: server.URL, DeezerURL: server.URL, WikidataURL: server.URL, WikipediaURL: server.URL, Interval: time.Nanosecond, CachePath: filepath.Join(t.TempDir(), "cache.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, &calls
}

func contextIntent(cat *fakes.Catalog) core.MusicIntent {
	ref := core.IntentReference{Kind: core.ReferenceArtist, Query: "Fixture Artist", Influence: core.InfluencePositive}
	r := cat.ResolveReference(ref)
	ref.Resolution = &r
	return core.MusicIntent{Version: core.CurrentIntentVersion, OriginalDescription: "music like Fixture Artist", Controls: core.IntentControls{RecommendationMode: core.EnhancedHybrid}, References: []core.IntentReference{ref}}.Normalized()
}

func contextCatalog() *fakes.Catalog {
	return fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "a", Display: "Fixture Artist - Early One", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "b", Display: "Fixture Artist - Early Two", Audio: []float32{0, 1}, Track: []float32{0, 1}},
		fakes.CatalogTrack{ID: "later", Display: "Fixture Artist - Later Hit", Audio: []float32{1, 0}, Track: []float32{1, 0}},
	)
}

func contextResponse(t *testing.T, w http.ResponseWriter, r *http.Request, defect string) {
	t.Helper()
	switch r.URL.Path {
	case "/ws/2/artist":
		if strings.HasPrefix(r.URL.Query().Get("query"), "tag:") {
			_, _ = fmt.Fprint(w, `{"count":0,"artists":[]}`)
			return
		}
		_, _ = fmt.Fprintf(w, `{"count":1,"artists":[{"id":%q,"name":"Fixture Artist"}]}`, contextArtistID)
	case "/ws/2/artist/" + contextArtistID:
		name, ended := "Fixture Artist", false
		if defect == "wrong_artist" {
			name = "Someone Else"
		}
		if defect == "ended" {
			ended = true
		}
		_, _ = fmt.Fprintf(w, `{"id":%q,"name":%q,"genres":[{"name":"electronica","count":3}],"relations":[{"type":"wikidata","ended":%t,"url":{"resource":"https://www.wikidata.org/wiki/Q123"}}]}`, contextArtistID, name, ended)
	case "/wiki/Special:EntityData/Q123.json":
		mbid := contextArtistID
		if defect == "wrong_wikidata" {
			mbid = contextAlbumID
		}
		_, _ = fmt.Fprintf(w, `{"entities":{"Q123":{"id":"Q123","lastrevid":42,"claims":{"P434":[{"rank":"normal","mainsnak":{"snaktype":"value","datavalue":{"value":%q}}}]},"sitelinks":{"enwiki":{"title":"Fixture Artist"}}}}}`, mbid)
	case "/w/api.php":
		if r.URL.Query().Get("exchars") != "1200" || r.URL.Query().Get("titles") != "Fixture Artist" || r.URL.Query().Get("maxlag") != "5" {
			t.Errorf("unbounded or private request: %s", r.URL)
		}
		qid := "Q123"
		if defect == "wrong_wikipedia" {
			qid = "Q999"
		}
		_, _ = fmt.Fprintf(w, `{"query":{"pages":[{"pageid":7,"ns":0,"title":"Fixture Artist","lastrevid":77,"pageprops":{"wikibase_item":%q},"extract":"His music has warm and melancholic textures. His music is not aggressive. He was born in a dark room."}]}}`, qid)
	default:
		t.Errorf("unexpected context request: %s", r.URL)
		http.NotFound(w, r)
	}
}

func TestContextLinkedArtistStartCacheOfflineAndReplay(t *testing.T) {
	c, calls := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) { contextResponse(t, w, r, "") })
	cat := contextCatalog()
	intent := contextIntent(cat)
	start := intent.References[0]
	intent.Start, intent.References = &start, nil
	original, _ := json.Marshal(intent.Start)
	got, err := c.PrepareMusic(context.Background(), intent, cat, cat, nil)
	if err != nil || len(got.Knowledge.ContextPlans) != 1 {
		t.Fatalf("missing context: %+v %v", got.Knowledge, err)
	}
	plan := got.Knowledge.ContextPlans[0]
	if plan.Scope != "journey_start" || plan.Profile.EntityKey != "musicbrainz:artist:"+contextArtistID || len(plan.Profile.Sources) != 3 || len(plan.Seeds) == 0 {
		t.Fatalf("bad linked plan: %+v", plan)
	}
	if !reflect.DeepEqual(plan.Profile.Characteristics, []string{"warm", "melancholic"}) && !reflect.DeepEqual(plan.Profile.Characteristics, []string{"melancholic", "warm"}) {
		t.Fatalf("unscoped/negative prose interpreted: %v", plan.Profile.Characteristics)
	}
	if plan.Profile.Sources[0].License != "CC-BY-NC-SA-3.0" || plan.Profile.Sources[1].Revision != "42" || plan.Profile.Sources[2].Revision != "77" {
		t.Fatal("source/license/revision lost")
	}
	after, _ := json.Marshal(got.Start)
	if string(original) != string(after) || len(got.Preferences.Genres) != 0 || len(got.EssentialCriteria) != 0 || len(got.Knowledge.Tracks) != 0 {
		t.Fatal("context rewrote intent or recording evidence")
	}
	before := calls.Load()
	warm, err := c.PrepareMusic(context.Background(), intent, cat, cat, nil)
	if err != nil || calls.Load() != before || warm.Knowledge.ID != got.Knowledge.ID {
		t.Fatal("cache changed immutable profile")
	}
	encoded, _ := json.Marshal(got)
	var replay core.MusicIntent
	if err := json.Unmarshal(encoded, &replay); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PrepareMusic(context.Background(), replay, cat, cat, nil); err != nil || calls.Load() != before {
		t.Fatal("history used network")
	}
	if _, err := c.db.Exec("UPDATE mb_cache SET fetched_at=?", time.Now().Add(-40*24*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	offline := context.WithValue(context.Background(), cacheOnlyKey{}, true)
	var snapshot core.KnowledgeSnapshot
	c.prepareContext(offline, intent, cat, cat, &snapshot, ports.NopProgress{})
	if len(snapshot.ContextPlans) != 1 || calls.Load() != before || snapshot.ContextPlans[0].Profile.ID != plan.Profile.ID {
		t.Fatal("offline stale context lost")
	}
}

func TestContextRejectsMismatchedSourceIdentities(t *testing.T) {
	for _, defect := range []string{"wrong_artist", "wrong_wikidata", "wrong_wikipedia", "ended"} {
		t.Run(defect, func(t *testing.T) {
			c, calls := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) { contextResponse(t, w, r, defect) })
			cat := contextCatalog()
			var snapshot core.KnowledgeSnapshot
			c.prepareContext(context.Background(), contextIntent(cat), cat, cat, &snapshot, ports.NopProgress{})
			if defect == "wrong_artist" {
				if len(snapshot.ContextPlans) != 0 {
					t.Fatal("wrong artist became context")
				}
				return
			}
			if len(snapshot.ContextPlans) != 1 || snapshot.ContextPlans[0].Profile.Description != "" {
				t.Fatalf("wrong linked prose accepted: %+v", snapshot.ContextPlans)
			}
			if defect == "wrong_wikidata" && calls.Load() != 3 || defect == "ended" && calls.Load() != 2 {
				t.Fatal("invalid link reached Wikipedia")
			}
		})
	}
}

func TestContextEarlyAlbumSeedsKeepReferenceAndRecordingEvidence(t *testing.T) {
	c, _ := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ws/2/artist":
			_, _ = fmt.Fprintf(w, `{"count":1,"artists":[{"id":%q,"name":"Fixture Artist"}]}`, contextArtistID)
		case "/ws/2/artist/" + contextArtistID:
			_, _ = fmt.Fprintf(w, `{"id":%q,"name":"Fixture Artist","genres":[{"name":"rock","count":2}]}`, contextArtistID)
		case "/ws/2/release-group":
			if !strings.Contains(r.URL.Query().Get("query"), "AND NOT secondarytype:* AND status:official") {
				t.Error("studio exclusions were not applied before pagination")
				_, _ = fmt.Fprint(w, `{"count":300,"release-groups":[]}`)
				return
			}
			_, _ = fmt.Fprintf(w, `{"count":2,"release-groups":[{"id":%q,"title":"Second","primary-type":"Album","first-release-date":"1974","artist-credit":[{"name":"Fixture Artist","artist":{"id":%q}}]},{"id":%q,"title":"First","primary-type":"Album","first-release-date":"1973","artist-credit":[{"name":"Fixture Artist","artist":{"id":%q}}]}]}`, contextAlbumTwo, contextArtistID, contextAlbumID, contextArtistID)
		case "/ws/2/recording":
			title := "Early One"
			if r.URL.Query().Get("query") == "rgid:"+contextAlbumTwo {
				title = "Early Two"
			}
			_, _ = fmt.Fprintf(w, `{"recordings":[{"id":%q,"title":%q,"artist-credit":[{"name":"Fixture Artist","artist":{"id":%q}}]}]}`, title, title, contextArtistID)
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
		}
	})
	cat := contextCatalog()
	intent := contextIntent(cat)
	intent.OriginalDescription = "Like early Fixture Artist, no polished pop"
	intent.References[0].Resolution.Selected.Representatives = []core.WeightedTrack{{TrackID: "later", Weight: 1}}
	var snapshot core.KnowledgeSnapshot
	c.prepareContext(context.Background(), intent, cat, cat, &snapshot, ports.NopProgress{})
	if len(snapshot.ContextPlans) != 1 {
		t.Fatal("no early context")
	}
	plan := snapshot.ContextPlans[0]
	if len(plan.Seeds) != 2 || plan.Seeds[0].TrackID != "a" || plan.Seeds[1].TrackID != "b" || plan.Profile.FirstYear != 1973 || plan.Profile.LastYear != 1974 {
		t.Fatalf("bad era seed selection: %+v", plan)
	}
	if intent.References[0].Resolution.Selected.Representatives[0].TrackID != "later" || len(intent.Temporal) != 0 || len(snapshot.Tracks) != 0 || len(snapshot.Candidates) != 0 {
		t.Fatal("context mutated reference or evidence")
	}
}

func TestContextDiscoveryRespectsRequestedGenresNegativesAndEndpoints(t *testing.T) {
	cat := contextCatalog()
	intent := contextIntent(cat)
	plan := core.ContextSeedPlan{ReferenceKind: core.ReferenceArtist, Query: "Fixture Artist", Scope: "playlist", Profile: core.ContextProfile{Genres: []string{"electronica", "pop"}, ExtractorVersion: core.ContextProfileVersion}}
	intent.Preferences.Genres = []core.IntentPreference{{Value: "pop", Influence: core.InfluenceNegative}}
	if got := contextualDiscoveryGenres(intent, []core.ContextSeedPlan{plan}); !reflect.DeepEqual(got, []string{"electronica"}) {
		t.Fatal(got)
	}
	intent.Preferences.Genres[0].Scope = "journey_end"
	if got := contextualDiscoveryGenres(intent, []core.ContextSeedPlan{plan}); !reflect.DeepEqual(got, []string{"electronica", "pop"}) {
		t.Fatal("end-only exclusion suppressed starting discovery", got)
	}
	plan.Scope = "journey_end"
	if got := contextualDiscoveryGenres(intent, []core.ContextSeedPlan{plan}); len(got) != 0 {
		t.Fatal("endpoint seeded starting category")
	}
	plan.Scope = "playlist"
	intent.Preferences.Genres = []core.IntentPreference{{Value: "classical", Influence: core.InfluencePositive}}
	if got := contextualDiscoveryGenres(intent, []core.ContextSeedPlan{plan}); !reflect.DeepEqual(got, []string{"classical"}) {
		t.Fatal("context broadened explicit category", got)
	}
	intent.Preferences.Genres = nil
	plan.Profile.ExtractorVersion = "obsolete"
	if got := contextualDiscoveryGenres(intent, []core.ContextSeedPlan{plan}); len(got) != 0 {
		t.Fatal("obsolete profile reused")
	}
	plan.Profile.ExtractorVersion = core.ContextProfileVersion
	plan.EntityKey = "another artist identity"
	if got := contextualDiscoveryGenres(intent, []core.ContextSeedPlan{plan}); len(got) != 0 {
		t.Fatal("same query hid a different resolved identity")
	}
	plan.EntityKey = ""
	intent.References[0].Influence = core.InfluenceNegative
	if got := contextualDiscoveryGenres(intent, []core.ContextSeedPlan{plan}); len(got) != 0 {
		t.Fatal("stale positive context reused")
	}
}

func TestContextCancellationAndDeejAIOnlyDoNotFetch(t *testing.T) {
	c, calls := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected network") })
	cat := contextCatalog()
	intent := contextIntent(cat)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.PrepareMusic(ctx, intent, cat, cat, nil); err == nil || calls.Load() != 0 {
		t.Fatal("cancellation ignored")
	}
	intent.Controls.RecommendationMode = core.DeejAIOnly
	if _, err := c.PrepareMusic(context.Background(), intent, cat, cat, nil); err != nil || calls.Load() != 0 {
		t.Fatal("Deej-AI used optional context")
	}
}

func TestContextDiverseSeedsUseCatalogAudioSpace(t *testing.T) {
	cat := fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "a", Display: "Artist - One", Audio: []float32{1, 0}},
		fakes.CatalogTrack{ID: "b", Display: "Artist - Two", Audio: []float32{1, 0}},
		fakes.CatalogTrack{ID: "c", Display: "Artist - Three", Audio: []float32{1, 0}},
		fakes.CatalogTrack{ID: "d", Display: "Artist - Four", Audio: []float32{1, 0}},
		fakes.CatalogTrack{ID: "z", Display: "Artist - Different", Audio: []float32{0, 1}},
	)
	seeds := []core.WeightedTrack{{TrackID: "a"}, {TrackID: "b"}, {TrackID: "c"}, {TrackID: "d"}, {TrackID: "z"}}
	got := contextDiverseSeeds(context.Background(), seeds, cat)
	if len(got) != 4 || got[0].TrackID != "a" || got[1].TrackID != "z" {
		t.Fatalf("diverse recording lost to sorted IDs: %v", got)
	}
}

func TestContextRequestCeilingAndInFlightCancellation(t *testing.T) {
	t.Run("request ceiling", func(t *testing.T) {
		c, calls := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) { contextResponse(t, w, r, "") })
		cat := contextCatalog()
		ctx := context.WithValue(context.Background(), knowledgeBudgetKey{}, &knowledgeBudget{requests: KnowledgeRequests - 2})
		var snapshot core.KnowledgeSnapshot
		c.prepareContext(ctx, contextIntent(cat), cat, cat, &snapshot, ports.NopProgress{})
		if calls.Load() != 2 || len(snapshot.ContextPlans) != 1 || snapshot.ContextPlans[0].Profile.Description != "" {
			t.Fatal("optional context bypassed shared budget")
		}
	})
	t.Run("in-flight cancellation", func(t *testing.T) {
		started := make(chan struct{})
		c, _ := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/ws/2/artist/"+contextArtistID {
				close(started)
				<-r.Context().Done()
				return
			}
			contextResponse(t, w, r, "")
		})
		cat := contextCatalog()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { _, err := c.PrepareMusic(ctx, contextIntent(cat), cat, cat, nil); done <- err }()
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("context lookup never began")
		}
		cancel()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("caller cancellation swallowed")
			}
		case <-time.After(time.Second):
			t.Fatal("provider ignored cancellation")
		}
	})
}

func TestContextDiscoveryKeyIncludesSourcePolicyAndStart(t *testing.T) {
	cat := contextCatalog()
	intent := contextIntent(cat)
	intent.Knowledge = &core.KnowledgeSnapshot{ContextPlans: []core.ContextSeedPlan{{Profile: core.ContextProfile{ID: "old", ExtractorVersion: core.ContextProfileVersion}}}}
	before := discoveryKey(intent, cat.CatalogVersion())
	intent.Knowledge.ContextPlans[0].Profile.ID = "new"
	if discoveryKey(intent, cat.CatalogVersion()) == before {
		t.Fatal("changed context reused discovery cache")
	}
	before = discoveryKey(intent, cat.CatalogVersion())
	start := intent.References[0]
	intent.Start = &start
	if discoveryKey(intent, cat.CatalogVersion()) == before {
		t.Fatal("new start reused discovery cache")
	}
}

func TestContextScopedPeriodAndSafeLinks(t *testing.T) {
	cat := contextCatalog()
	intent := contextIntent(cat)
	ref := intent.References[0]
	for _, prompt := range []string{"not early Fixture Artist", "avoid early Fixture Artist", "unlike early Fixture Artist", "like Fixture Artist, but not early Fixture Artist", `like Fixture Artist, but not "early Fixture Artist"`, "Fixture Artist today rather than early Fixture Artist", "Fixture Artist today instead of early Fixture Artist"} {
		intent.OriginalDescription = prompt
		if _, early := contextualPeriod(intent, ref, "Fixture Artist", "playlist"); early {
			t.Fatal("negative era became positive seed scope")
		}
	}
	intent.Temporal = []core.TemporalRequirement{{Basis: "original_release", StartYear: 1970, EndYear: 1979, Scope: "journey_end"}}
	if period, _ := contextualPeriod(intent, ref, "Fixture Artist", "journey_start"); period != nil {
		t.Fatal("end period leaked to start")
	}
	if period, _ := contextualPeriod(intent, ref, "Fixture Artist", "journey_end"); period == nil {
		t.Fatal("matching period lost")
	}
	for _, base := range []string{"https://evil.example", "http://127.0.0.1@evil.example", "http://127.0.0.1/path"} {
		if _, err := contextBase(base, "https://www.wikidata.org"); err == nil {
			t.Fatal("unsafe provider override accepted")
		}
	}
	if validContextMetadata("/wiki/Special:EntityData/Q1.json", "wikidata-context-v1:", []byte(`{"entities":{"Q2":{"id":"Q2","lastrevid":1}}}`)) {
		t.Fatal("wrong entity cached as valid response")
	}
}

func TestContextMalformedSuccessIsNotCached(t *testing.T) {
	for _, tc := range []struct{ name, path, namespace, invalid, valid string }{
		{"entity", "/ws/2/artist/" + contextArtistID, "entity-context-v1:", `{"id":"wrong","name":"Fixture Artist"}`, `{"id":"` + contextArtistID + `","name":"Fixture Artist"}`},
		{"wikidata", "/wiki/Special:EntityData/Q123.json", "wikidata-context-v1:", `{"entities":{"Q9":{"id":"Q9","lastrevid":1}}}`, `{"entities":{"Q123":{"id":"Q123","lastrevid":42}}}`},
		{"wikipedia", "/w/api.php?action=query", "wikipedia-context-v1:", `{"query":{"unrelated":[]}}`, `{"query":{"pages":[{"pageid":7}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var responses atomic.Int32
			c, calls := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				body := tc.invalid
				if responses.Add(1) > 1 {
					body = tc.valid
				}
				_, _ = fmt.Fprint(w, body)
			})
			if _, err := c.metadataGet(context.Background(), c.base, tc.path, tc.namespace, c.contextClient, false); err == nil {
				t.Fatal("malformed success accepted")
			}
			if _, err := c.metadataGet(context.Background(), c.base, tc.path, tc.namespace, c.contextClient, false); err != nil || calls.Load() != 2 {
				t.Fatal("malformed response prevented corrected source retry", err)
			}
			if _, err := c.metadataGet(context.Background(), c.base, tc.path, tc.namespace, c.contextClient, false); err != nil || calls.Load() != 2 {
				t.Fatal("valid response was not cached", err)
			}
		})
	}
}

func TestContextGenresRankVotesBeforeLimiting(t *testing.T) {
	tags := []mbTag{{Name: "aor", Count: 1}, {Name: "arena rock", Count: 1}, {Name: "blues rock", Count: 2}, {Name: "classic rock", Count: 1}, {Name: "glam metal", Count: 1}, {Name: "hard rock", Count: 20}, {Name: "rock", Count: 50}, {Name: "hip-hop", Count: 3}, {Name: "hip hop", Count: 2}, {Name: "pop", Count: -1}}
	got := contextGenres(tags)
	if len(got) != 6 || got[0] != "rock" || got[1] != "hard rock" {
		t.Fatalf("API response order crowded out representative genres: %v", got)
	}
	aliasCount := 0
	for _, genre := range got {
		if genre == "hip hop" || genre == "hip-hop" {
			aliasCount++
		}
	}
	if aliasCount != 1 {
		t.Fatal("exact aliases consumed multiple genre slots", got)
	}
	for i, j := 0, len(tags)-1; i < j; i, j = i+1, j-1 {
		tags[i], tags[j] = tags[j], tags[i]
	}
	if other := contextGenres(tags); !reflect.DeepEqual(got, other) {
		t.Fatal("genre order depends on API response order", got, other)
	}
}

func TestContextCharacteristicsPreserveGenreCompounds(t *testing.T) {
	if got := contextCharacteristics("Their musical style incorporates heavy metal and dark ambient."); len(got) != 0 {
		t.Fatal("genre phrase became isolated texture", got)
	}
	got := contextCharacteristics("Their music incorporates heavy metal with an independently heavy texture.")
	if !reflect.DeepEqual(got, []string{"heavy"}) {
		t.Fatal("independent texture claim lost", got)
	}
	if got := contextCharacteristics("Their music has warm jazz influences.", "warm jazz"); len(got) != 0 {
		t.Fatal("source genre compound was split", got)
	}
}

func TestContextSameNameArtistsRequireCatalogRecordingCorroboration(t *testing.T) {
	for _, scenario := range []string{"unique", "both", "single_track", "wrong_credit", "missing_credit_name", "unavailable_rival", "incomplete_rival", "incomplete_winner"} {
		t.Run(scenario, func(t *testing.T) {
			c, _ := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/ws/2/artist":
					_, _ = fmt.Fprintf(w, `{"count":2,"artists":[{"id":%q,"name":"Fixture Artist"},{"id":%q,"name":"Fixture Artist"}]}`, contextArtistID, contextAlbumID)
				case "/ws/2/recording":
					query := r.URL.Query().Get("query")
					if !strings.Contains(query, `recording:"Early One"`) || !strings.Contains(query, `recording:"Early Two"`) {
						t.Error("identity lookup not grounded in known catalog titles")
					}
					rival := strings.HasPrefix(query, "arid:"+contextAlbumID)
					if rival && scenario == "unavailable_rival" {
						http.Error(w, "unavailable", http.StatusForbidden)
						return
					}
					if rival && scenario == "incomplete_rival" {
						_, _ = fmt.Fprint(w, `{"count":101,"recordings":[]}`)
						return
					}
					if rival && scenario != "both" {
						_, _ = fmt.Fprint(w, `{"count":0,"recordings":[]}`)
						return
					}
					id := contextArtistID
					if rival || scenario == "wrong_credit" {
						id = contextAlbumID
					}
					count := 2
					if scenario == "single_track" {
						count = 1
					}
					recordings := []map[string]any{}
					for i, title := range []string{"Early One", "Early Two"}[:count] {
						name := "Fixture Artist"
						if scenario == "missing_credit_name" {
							name = ""
						}
						recordings = append(recordings, map[string]any{"id": fmt.Sprint(i), "title": title, "artist-credit": []map[string]any{{"name": name, "artist": map[string]string{"id": id, "name": "Fixture Artist"}}}})
					}
					if scenario == "incomplete_winner" {
						count = 101
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"count": count, "recordings": recordings})
				default:
					t.Errorf("unexpected identity lookup: %s", r.URL)
					http.NotFound(w, r)
				}
			})
			cat := contextCatalog()
			selected := *contextIntent(cat).References[0].Resolution.Selected
			artist, sources, ok := c.contextArtistIdentity(context.Background(), selected, cat)
			if ok != (scenario == "unique" || scenario == "missing_credit_name" || scenario == "incomplete_winner") {
				t.Fatalf("unsafe identity decision: %s %v %v", scenario, artist, ok)
			}
			if ok && (artist.ID != contextArtistID || len(sources) != 3) {
				t.Fatal("catalog corroboration provenance lost")
			}
		})
	}
}
