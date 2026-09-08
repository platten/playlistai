package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/httpretry"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/ports"
)

func TestInstrumentalPromptDiscoversCatalogSeedsAcrossPages(t *testing.T) {
	var entries []fakes.CatalogTrack
	for i := 0; i < 5; i++ {
		entries = append(entries, fakes.CatalogTrack{ID: fmt.Sprint(i), Display: fmt.Sprintf("Fixture %d - Recording", i), Audio: []float32{1, 0}, Track: []float32{1, 0}})
	}
	cat := fakes.NewCatalog(2, entries...)
	c, calls := seedTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/2/recording" || r.URL.Query().Get("query") != `tag:"instrumental"` || r.URL.Query().Get("limit") != "100" {
			t.Errorf("unexpected lookup %s", r.URL)
			http.NotFound(w, r)
			return
		}
		var rows []map[string]any
		count := 100
		if r.URL.Query().Get("offset") == "100" {
			count = 5
		}
		for i := 0; i < count; i++ {
			artist := fmt.Sprintf("Fixture %d", i)
			if count == 100 {
				artist = "Not in catalog"
			}
			rows = append(rows, map[string]any{"id": fmt.Sprint(i), "title": "Recording", "artist-credit": []map[string]any{{"name": artist}}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"count": 105, "recordings": rows})
	})
	intent, err := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: "Instrumental, no vocals"})
	if err != nil {
		t.Fatal(err)
	}
	selected := map[string]bool{}
	for seed := 1; seed <= 6; seed++ {
		intent.Seed = core.RNGSeed(fmt.Sprint(seed))
		got, err := c.ResolveMusic(context.Background(), intent, cat, cat, nil)
		if err != nil || len(got.InferredAnchors) != 3 || len(got.Knowledge.Candidates) != 5 || !core.WantsInstrumental(got) {
			t.Fatalf("%+v %v", got, err)
		}
		selected[got.InferredAnchors[0].Reference.TrackID] = true
		replay, err := c.ResolveMusic(context.Background(), got, cat, cat, nil)
		if err != nil || replay.Knowledge.ID != got.Knowledge.ID {
			t.Fatal("snapshot changed on replay")
		}
	}
	if calls.Load() != 2 || len(selected) < 2 {
		t.Fatalf("cache/randomization failed: %d %+v", calls.Load(), selected)
	}
}

func TestInstrumentalLookupFallbackAndMissingEvidence(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(fmt.Sprint(local), func(t *testing.T) {
			c, calls := seedTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/ws/2/recording":
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(http.StatusServiceUnavailable)
				case "/search":
					if r.URL.Query().Get("q") != `track:"instrumental"` {
						t.Error("private prompt or incorrect search")
					}
					_, _ = fmt.Fprint(w, `{"data":[{"id":1,"title":"Song (Live)","artist":{"name":"Fixture"}}]}`)
				default:
					t.Errorf("unexpected lookup %s", r.URL)
				}
			})
			var entries []fakes.CatalogTrack
			if local {
				entries = append(entries, fakes.CatalogTrack{ID: "candidate", Display: "Fixture - Song (Instrumental)", Audio: []float32{1, 0}, Track: []float32{1, 0}})
			}
			cat := fakes.NewCatalog(2, entries...)
			intent, err := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: "Instrumental, no vocals"})
			if err != nil {
				t.Fatal(err)
			}
			got, err := c.ResolveMusic(context.Background(), intent, cat, cat, nil)
			if err != nil {
				t.Fatal(err)
			}
			wantCalls := int32(httpretry.MaxAttempts + 1)
			if local {
				wantCalls += httpretry.MaxAttempts
			} // optional recording enrichment of the local proposal
			if (len(got.Knowledge.Candidates) > 0) != local || calls.Load() != wantCalls {
				t.Fatalf("fallback mismatch: %+v calls=%d", got.Knowledge, calls.Load())
			}
			if !strings.Contains(strings.Join(got.Knowledge.Notices, " "), "503") {
				t.Fatal("provider failure hidden")
			}
			if local && (got.InferredAnchors[0].Reference.TrackID != "candidate" || got.InferredAnchors[0].Suitability.State == core.EvidenceMatch || got.Seed.IsZero()) {
				t.Fatal("unverified candidate promoted or replay seed missing")
			}
		})
	}
}
