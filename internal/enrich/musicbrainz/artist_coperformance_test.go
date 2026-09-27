package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/resolution"
)

const coperformanceAnchor = "6514cffa-fbe0-4965-ad88-e998ead8a82a"
const coperformanceArtist = "ea524cc6-191a-4c05-ab88-3bb5c0880ca5"

// CC0 positive-link derivative of the MusicBrainz artist endpoint with
// inc=recording-rels+artist-credits, retrieved 2026-09-26. The full response was
// 70,311 bytes/88 relations; this fixture keeps only two performance links.
// https://musicbrainz.org/ws/2/artist/6514cffa-fbe0-4965-ad88-e998ead8a82a?inc=recording-rels%2Bartist-credits&fmt=json
func coperformanceFixture(t *testing.T) artistCoperformanceDocument {
	t.Helper()
	raw, err := os.ReadFile("testdata/artist-coperformance.json")
	if err != nil {
		t.Fatal(err)
	}
	var document artistCoperformanceDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func coperformanceIntent() core.MusicIntent {
	grounding := &core.IdentityGrounding{Provider: "MusicBrainz+paipack", MatchedSpelling: "Tony Allen", MatchType: "canonical", SnapshotVersion: "fixture"}
	for i := range 12 {
		grounding.Candidates = append(grounding.Candidates, core.IdentityCandidate{Kind: core.ReferenceArtist, ID: fmt.Sprintf("00000000-1111-1111-1111-%012d", i+1), Name: "Tony Allen"})
	}
	grounding.Candidates[10].ID = coperformanceArtist
	anchor := core.IntentReference{Kind: core.ReferenceArtist, Query: "Fela Kuti", Influence: core.InfluencePositive,
		Evidence:   []core.SourceEvidence{{Text: "Fela Kuti", Explicit: true}},
		Grounding:  &core.IdentityGrounding{Provider: "MusicBrainz+paipack", MatchedSpelling: "Fela Kuti", MatchType: "canonical", SnapshotVersion: "fixture", Candidates: []core.IdentityCandidate{{Kind: core.ReferenceArtist, ID: coperformanceAnchor, Name: "Fela Kuti"}}},
		Resolution: &core.ReferenceResolution{Status: core.ResolutionResolved, Selected: &core.ResolutionCandidate{Kind: core.ReferenceArtist, Artist: "Fela Kuti", Representatives: []core.WeightedTrack{{TrackID: "anchor"}}}},
	}
	target := core.IntentReference{Kind: core.ReferenceArtist, Query: "Tony Allen", Influence: core.InfluencePositive, Evidence: []core.SourceEvidence{{Text: "Tony Allen", Explicit: true}}, Grounding: grounding}
	return core.MusicIntent{Version: core.CurrentIntentVersion, OriginalDescription: "Make a 10-song playlist inspired by Fela Kuti and Tony Allen, with a variety of artists.", References: []core.IntentReference{anchor, target}, Translation: &core.IntentTranslation{Atoms: []core.IntentAtom{{Kind: "artist", Value: "Tony Allen", Grounding: grounding}}}}
}

func TestArtistCoperformanceKeepsCandidatesAndSourcesWithoutModelGuessing(t *testing.T) {
	for _, defect := range []string{"none", "one recording", "rival once", "engineer", "producer", "backward", "missing direction", "missing credits", "wrong anchor", "missing relations", "too many relations", "truncated", "confirmed", "explicit selection", "spelling choice", "negative", "inferred", "required", "endpoint", "unresolved anchor", "three anchors"} {
		t.Run(defect, func(t *testing.T) {
			intent, document := coperformanceIntent(), coperformanceFixture(t)
			noRequest := false
			switch defect {
			case "one recording":
				document.Relations[1].Recording.ID = document.Relations[0].Recording.ID
			case "rival once":
				rival := document.Relations[0]
				rival.Recording.ArtistCredit = append([]mbArtistCredit(nil), rival.Recording.ArtistCredit...)
				rival.Recording.ArtistCredit[0].Artist.ID = intent.References[1].Grounding.Candidates[0].ID
				document.Relations = append(document.Relations, rival)
			case "engineer", "producer":
				for i := range document.Relations {
					document.Relations[i].Type = defect
				}
			case "backward":
				for i := range document.Relations {
					document.Relations[i].Direction = "backward"
				}
			case "missing direction":
				document.Relations[0].Direction = ""
			case "missing credits":
				document.Relations[0].Recording.ArtistCredit = nil
			case "wrong anchor":
				document.ID = contextAlbumID
			case "missing relations":
				document.Relations = nil
			case "too many relations":
				for len(document.Relations) <= 1024 {
					document.Relations = append(document.Relations, document.Relations[0])
				}
			case "truncated":
				intent.References[1].Grounding.Truncated, noRequest = true, true
			case "confirmed":
				intent.References[1].Grounding.Confirmed, noRequest = true, true
			case "explicit selection":
				intent.References[1].TrackID, noRequest = "chosen", true
			case "spelling choice":
				intent.References[1].SpellingDecision, noRequest = "original", true
			case "negative":
				intent.References[1].Influence, noRequest = core.InfluenceNegative, true
			case "inferred":
				intent.References[1].Evidence[0].Explicit, noRequest = false, true
			case "required":
				intent.HardConstraints, noRequest = []core.HardConstraint{{Kind: "require_artist", Value: "Tony Allen"}}, true
			case "endpoint":
				intent.Destination, noRequest = &intent.References[1], true
			case "unresolved anchor":
				intent.References[0].Resolution, noRequest = nil, true
			case "three anchors":
				for i := range 2 {
					anchor := intent.References[0]
					g := *anchor.Grounding
					g.Candidates = []core.IdentityCandidate{{Kind: core.ReferenceArtist, Name: "Other Anchor", ID: fmt.Sprintf("22222222-2222-2222-2222-%012d", i)}}
					anchor.Grounding = &g
					intent.References = append(intent.References, anchor)
				}
				noRequest = true
			}
			before, _ := json.Marshal(intent)
			client, calls := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/ws/2/artist/"+coperformanceAnchor || r.URL.Query().Get("inc") != "recording-rels+artist-credits" || strings.Contains(r.URL.RawQuery, "playlist") {
					t.Errorf("unexpected identity-only lookup: %s", r.URL)
				}
				_ = json.NewEncoder(w).Encode(document)
			})
			got := client.corroborateArtistReferences(context.Background(), intent, &core.KnowledgeSnapshot{})
			selected, ok := got.References[1].Grounding.CorroboratedArtist()
			if ok != (defect == "none") {
				t.Fatalf("defect=%s proof=%+v", defect, got.References[1].Grounding.Corroboration)
			}
			if noRequest && calls.Load() != 0 {
				t.Fatal("ineligible scope dispatched an online lookup")
			}
			after, _ := json.Marshal(intent)
			if string(before) != string(after) {
				t.Fatal("corroboration mutated input or original translation")
			}
			if ok {
				g := got.References[1].Grounding
				if selected.ID != coperformanceArtist || len(g.Candidates) != 12 || g.Confirmed || g.Corroboration.Supports[0].Source.License != "CC0-1.0" || len(g.Corroboration.Supports[0].RecordingIDs) != 2 {
					t.Fatalf("identity proof lost provenance or original alternatives: %+v", g)
				}
				_ = client.corroborateArtistReferences(context.Background(), intent, &core.KnowledgeSnapshot{})
				if calls.Load() != 1 {
					t.Fatal("identity response did not use existing cache")
				}
			}
		})
	}
}

func TestArtistCoperformanceChecksEveryAnchorWithinSharedRequestBudget(t *testing.T) {
	for _, defect := range []string{"none", "second rival", "second unavailable", "one request left"} {
		t.Run(defect, func(t *testing.T) {
			intent := coperformanceIntent()
			anchor := intent.References[0]
			g := *anchor.Grounding
			g.Candidates = []core.IdentityCandidate{{Kind: core.ReferenceArtist, ID: contextAlbumID, Name: "Another Anchor"}}
			anchor.Grounding = &g
			intent.References = append(intent.References, anchor)
			client, calls := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				document := coperformanceFixture(t)
				document.ID = strings.TrimPrefix(r.URL.Path, "/ws/2/artist/")
				if document.ID == coperformanceAnchor && defect == "second unavailable" {
					http.Error(w, "unavailable", http.StatusBadRequest)
					return
				}
				if document.ID == coperformanceAnchor && defect == "second rival" {
					document.Relations = document.Relations[:1]
					document.Relations[0].Recording.ArtistCredit[0].Artist.ID = intent.References[1].Grounding.Candidates[0].ID
				}
				_ = json.NewEncoder(w).Encode(document)
			})
			budget := &knowledgeBudget{}
			if defect == "one request left" {
				budget.requests = KnowledgeRequests - 1
			}
			ctx := context.WithValue(context.Background(), knowledgeBudgetKey{}, budget)
			got := client.corroborateArtistReferences(ctx, intent, &core.KnowledgeSnapshot{})
			if _, ok := got.References[1].Grounding.CorroboratedArtist(); ok != (defect == "none") {
				t.Fatalf("partial/conflicting anchor context selected identity: %+v", got.References[1].Grounding)
			}
			want := int32(2)
			if defect == "one request left" {
				want = 1
			}
			if calls.Load() != want {
				t.Fatalf("requests=%d want=%d", calls.Load(), want)
			}
			if defect == "none" && len(got.References[1].Grounding.Corroboration.Supports) != 2 {
				t.Fatal("second positive anchor provenance missing")
			}
		})
	}
}

type identifiedSeedCatalog struct {
	*fakes.Catalog
	recordingID string
	artistID    string
}

func (c identifiedSeedCatalog) Meta(id string) (core.TrackMeta, bool) {
	meta, ok := c.Catalog.Meta(id)
	if id == "target" {
		meta.MusicBrainzRecording = c.recordingID
		if c.artistID != "" {
			meta.Annotations = []core.MetadataAnnotation{{Kind: "artist_mbid", Value: c.artistID, SourceKey: "MUSICBRAINZ_ARTISTID"}}
		}
	}
	return meta, ok
}

func TestArtistCoperformancePreparationRequiresCatalogSeedAndReplays(t *testing.T) {
	for _, identity := range []string{"recording", "artist", "missing title", "unknown", "conflicting recording", "conflicting artist", "conflicting both"} {
		t.Run(identity, func(t *testing.T) {
			client, calls := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Query().Get("inc") == "recording-rels+artist-credits":
					_ = json.NewEncoder(w).Encode(coperformanceFixture(t))
				case r.URL.Path == "/ws/2/recording":
					if r.URL.Query().Get("artist") != coperformanceArtist {
						t.Errorf("unpinned artist recording lookup: %s", r.URL)
					}
					document := coperformanceFixture(t)
					_ = json.NewEncoder(w).Encode(map[string]any{"recording-count": 1, "recordings": []mbRecording{document.Relations[0].Recording}})
				case r.URL.Path == "/ws/2/artist":
					_, _ = fmt.Fprintf(w, `{"count":1,"artists":[{"id":%q,"name":"Fela Kuti"}]}`, coperformanceAnchor)
				case strings.HasPrefix(r.URL.Path, "/ws/2/artist/"):
					_, _ = fmt.Fprintf(w, `{"id":%q,"relations":[]}`, strings.TrimPrefix(r.URL.Path, "/ws/2/artist/"))
				default:
					t.Errorf("unexpected recovery (including unverified namesake fallback): %s", r.URL)
					http.NotFound(w, r)
				}
			})
			title := "Afro Disco Beat"
			if identity == "missing title" {
				title = "Unrelated Song"
			}
			cat := identifiedSeedCatalog{Catalog: fakes.NewCatalog(2, fakes.CatalogTrack{ID: "anchor", Display: "Fela Kuti - Fixture Song", Audio: []float32{1, 0}, Track: []float32{1, 0}}, fakes.CatalogTrack{ID: "target", Display: "Tony Allen - " + title, Audio: []float32{0, 1}, Track: []float32{0, 1}})}
			switch identity {
			case "recording", "missing title":
				cat.recordingID = coperformanceFixture(t).Relations[0].Recording.ID
			case "artist":
				cat.artistID = coperformanceArtist
			case "conflicting recording":
				cat.recordingID, cat.artistID = contextAlbumID, coperformanceArtist
			case "conflicting artist":
				cat.recordingID, cat.artistID = coperformanceFixture(t).Relations[0].Recording.ID, contextAlbumID
			case "conflicting both":
				cat.recordingID, cat.artistID = contextAlbumID, contextAlbumID
			}
			intent := coperformanceIntent()
			intent.Controls.RecommendationMode = core.EnhancedHybrid
			intent, _ = resolution.Apply(cat, intent)
			got, err := client.PrepareMusic(context.Background(), intent, cat, cat, nil)
			if err != nil {
				t.Fatal(err)
			}
			got, issues := resolution.Apply(cat, got)
			ref := got.References[1]
			if _, ok := ref.Grounding.CorroboratedArtist(); !ok {
				t.Fatal("preparation did not preserve corroboration")
			}
			if identity == "recording" || identity == "artist" {
				if len(issues) != 0 || ref.TrackID != "target" || ref.Resolution.Selected == nil || ref.Resolution.Selected.EntityID != coperformanceArtist {
					t.Fatalf("verified identity did not reach catalog seed: ref=%+v issues=%+v", ref, issues)
				}
			} else if ref.TrackID != "" || len(issues) == 0 {
				t.Fatal("same-name catalog result bypassed missing recording seed")
			}
			before := calls.Load()
			replayed, err := client.PrepareMusic(context.Background(), got, cat, cat, nil)
			if err != nil || calls.Load() != before || !reflect.DeepEqual(replayed.References[1].Grounding, ref.Grounding) {
				t.Fatal("saved corroboration re-fetched or changed")
			}
		})
	}
}

func TestPinnedSeedPrefersExactRecordingAmongNameMatches(t *testing.T) {
	recording := coperformanceFixture(t).Relations[0].Recording
	cat := identifiedSeedCatalog{Catalog: fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "unknown", Display: "Tony Allen - Afro Disco Beat", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "target", Display: "Tony Allen - Afro Disco Beat", Audio: []float32{0, 1}, Track: []float32{0, 1}},
	), recordingID: recording.ID}
	identity := seedRecordingIdentity{artistID: coperformanceArtist, recording: recording, requirePositive: true}
	track, ok := matchSeedRecording(context.Background(), cat, cat, recording.Title, []string{"Tony Allen"}, identity)
	if !ok || track.ID != "target" {
		t.Fatalf("exact recording did not disambiguate catalog namesakes: %+v %t", track, ok)
	}
	cat.recordingID = ""
	if _, ok := matchSeedRecording(context.Background(), cat, cat, recording.Title, []string{"Tony Allen"}, identity); ok {
		t.Fatal("absent identifiers resolved ambiguous catalog names")
	}
}

func TestArtistCoperformanceParentCancellationStopsPreparation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, _ := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) { cancel(); <-r.Context().Done() })
	cat := fakes.NewCatalog(2)
	done := make(chan error, 1)
	go func() { _, err := client.PrepareMusic(ctx, coperformanceIntent(), cat, cat, nil); done <- err }()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("cancellation=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled corroboration did not stop preparation")
	}
}

func TestArtistCoperformanceRetriesCountTowardDispatchAndTimeLimits(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusServiceUnavailable} {
		synctest.Test(t, func(t *testing.T) {
			client := &Client{base: "https://musicbrainz.org", ua: "fixture", interval: time.Second, limiter: &requestLimiter{gate: make(chan struct{}, 1)}}
			dispatches := 0
			client.hc = &http.Client{Transport: &limitedTransport{client: client, base: transportFunc(func(*http.Request) (*http.Response, error) {
				dispatches++
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"unavailable"}`))}, nil
			})}}
			budget := &knowledgeBudget{}
			ctx := context.WithValue(context.Background(), knowledgeBudgetKey{}, budget)
			started := time.Now()
			got := client.corroborateArtistReferences(ctx, coperformanceIntent(), &core.KnowledgeSnapshot{})
			if dispatches != 2 || budget.requests != 2 || time.Since(started) > 8*time.Second {
				t.Fatalf("retry escaped shared budget or child timeout: requests=%d charged=%d elapsed=%v", dispatches, budget.requests, time.Since(started))
			}
			if _, ok := got.References[1].Grounding.CorroboratedArtist(); ok || ctx.Err() != nil {
				t.Fatal("optional failed corroboration selected an identity or canceled parent")
			}
		})
	}
}

func TestSubmittedArtistCorroborationLeavesGenerationKnowledgeUnset(t *testing.T) {
	client, calls := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(coperformanceFixture(t))
	})
	got, err := client.CorroborateArtistReferences(context.Background(), coperformanceIntent())
	if err != nil || got.Knowledge != nil {
		t.Fatalf("identity-only submission preempted generation knowledge: %+v %v", got.Knowledge, err)
	}
	if _, ok := got.References[1].Grounding.CorroboratedArtist(); !ok || calls.Load() != 1 {
		t.Fatal("submitted identity proof missing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.CorroborateArtistReferences(ctx, coperformanceIntent()); err != context.Canceled || calls.Load() != 1 {
		t.Fatal("canceled submission dispatched identity lookup")
	}
}

func TestSubmittedArtistCorroborationReturnsParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, _ := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) { cancel(); <-r.Context().Done() })
	done := make(chan error, 1)
	go func() { _, err := client.CorroborateArtistReferences(ctx, coperformanceIntent()); done <- err }()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("cancellation=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("submitted corroboration did not stop after cancellation")
	}
}
