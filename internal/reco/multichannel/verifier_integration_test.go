package multichannel

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/enrich/musicbrainz"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

// Embedded genre tags are retrieval hints; this catalog supplies no reviewed
// claims. Decisive evidence must pass through the real external-source adapter.
type verifierIntegrationCatalog struct {
	*fakes.Catalog
	recordings map[string]string
}

func (c verifierIntegrationCatalog) Meta(id string) (core.TrackMeta, bool) {
	meta, ok := c.Catalog.Meta(id)
	if ok {
		meta.MusicBrainzRecording = c.recordings[id]
		meta.Annotations = []core.MetadataAnnotation{{Kind: "genre", Value: "electronic", Origin: "embedded_tag", SourceKey: "GENRE"}}
	}
	return meta, ok
}

func TestMusicBrainzVerifierReachesFrozenSupportedRecommendation(t *testing.T) {
	for _, referenced := range []bool{false, true} {
		t.Run(fmt.Sprintf("referenced=%t", referenced), func(t *testing.T) {
			var rows []fakes.CatalogTrack
			var ids []string
			recordings, titles := map[string]string{}, map[string]string{}
			for i := range enhancedMinimumComparisons {
				id := fmt.Sprintf("candidate-%03d", i)
				mbid := fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)
				ids = append(ids, id)
				recordings[id], titles[mbid] = mbid, id
				rows = append(rows, fakes.CatalogTrack{ID: id, Display: "Artist - " + id, Audio: []float32{1, 0}, Track: []float32{1, 0}})
			}
			base := fakes.NewCatalog(2, rows...)
			catalog := verifierIntegrationCatalog{base, recordings}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch {
				case strings.HasPrefix(r.URL.Path, "/ws/2/recording/"):
					id := strings.TrimPrefix(r.URL.Path, "/ws/2/recording/")
					title, ok := titles[id]
					if !ok {
						t.Errorf("unexpected recording %q", id)
						http.NotFound(w, r)
						return
					}
					relations := "[]"
					if id == recordings[ids[0]] {
						relations = `[{"type":"wikidata","url":{"resource":"https://www.wikidata.org/wiki/Q123"}}]`
					}
					_, _ = fmt.Fprintf(w, `{"id":%q,"title":%q,"artist-credit":[{"name":"Artist"}],"genres":[{"name":"electronic","count":4}],"relations":%s}`, id, title, relations)
				case r.URL.Path == "/wiki/Special:EntityData/Q123.json":
					references := ""
					if referenced {
						references = `,"references":[{"snaks":{"P854":[{"snaktype":"value","datavalue":{"value":"https://label.example/recording"}}]}}]`
					}
					_, _ = fmt.Fprintf(w, `{"entities":{"Q123":{"id":"Q123","lastrevid":42,"claims":{"P4404":[{"mainsnak":{"snaktype":"value","datavalue":{"value":%q}}}],"P136":[{"rank":"normal","mainsnak":{"snaktype":"value","datavalue":{"value":{"id":"Q456"}}}%s}]}}}}`, recordings[ids[0]], references)
				case r.URL.Path == "/wiki/Special:EntityData/Q456.json":
					_, _ = fmt.Fprint(w, `{"entities":{"Q456":{"id":"Q456","lastrevid":7,"labels":{"en":{"value":"electronic"}}}}}`)
				default:
					t.Errorf("unexpected external acquisition %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			verifier, err := musicbrainz.New(musicbrainz.Config{UserAgent: "offline-integration-test", MirrorURL: server.URL, WikidataURL: server.URL, Interval: time.Nanosecond})
			if err != nil {
				t.Fatal(err)
			}
			defer verifier.Close()
			intent := enhancedIntent(1)
			intent.References, intent.Seeds = nil, core.IntentSeeds{}
			intent.Preferences.Genres = []core.IntentPreference{{Value: "electronic", Influence: core.InfluencePositive}}
			intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "electronic", Scope: "playlist"}}
			engine := New(catalog, fakes.NewSimilarityEngine(base), catalog, DefaultConfig()).WithRecordingVerifier(verifier).WithCandidateSource(&fixtureDiscovery{})
			engine.retriever = &metadataPriorityRetriever{poolRetriever{candidates: candidatesForTracks(refs(catalog, ids...)), pageSize: enhancedChannelBatch}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := engine.BuildRecommendation(ctx, ports.RecommendationRequest{Intent: intent})
			if err != nil || result.Search == nil {
				t.Fatalf("missing frozen result: err=%v", err)
			}
			if err := result.Search.Validate(); err != nil {
				t.Fatal(err)
			}
			if calls.Load() == 0 || calls.Load() > recordingVerificationLimit+2 {
				t.Fatalf("adapter unused or exceeded shortlist: requests=%d", calls.Load())
			}
			if !referenced {
				if len(result.Tracks) != 0 || result.Outcome.State == core.OutcomeFulfilled || result.Search.StopReason == "quality_target" {
					t.Fatalf("unreferenced statement or ordinary tags became decisive: outcome=%+v stop=%s", result.Outcome, result.Search.StopReason)
				}
				return
			}
			if len(result.Tracks) != 1 || result.Tracks[0].ID != ids[0] || result.Outcome.State != core.OutcomeFulfilled || result.Search.StopReason != "quality_target" || result.Search.Considered < enhancedMinimumComparisons {
				t.Fatalf("cited recording did not reach supported bounded result: ids=%v outcome=%+v stop=%s compared=%d", result.IDs(), result.Outcome, result.Search.StopReason, result.Search.Considered)
			}
			candidate, found := findCandidate(result.Search.Candidates, ids[0])
			if !found || candidate.FitTier != fitStrong {
				t.Fatalf("strong decision missing from snapshot: %+v", candidate)
			}
			foundClaim := false
			if result.Intent.Knowledge != nil {
				for _, track := range result.Intent.Knowledge.Tracks {
					for _, claim := range track.Claims {
						if track.Ref.ID == ids[0] && claim.Kind == "genre" && claim.Value == "electronic" && claim.Method == "linked_statement" {
							foundClaim = claim.Scope == "recording" && claim.RecordingID == recordings[ids[0]] && claim.EntityID == recordings[ids[0]] && claim.Source.URL == "https://www.wikidata.org/wiki/Special:EntityData/Q123.json?revision=42" && claim.Source.Revision == "42" && claim.Locator != ""
						}
					}
				}
			}
			if !foundClaim {
				t.Fatal("identity-scoped, attributed adapter evidence missing from frozen intent")
			}
		})
	}
}
