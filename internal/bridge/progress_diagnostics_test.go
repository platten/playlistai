package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/logging"
	"github.com/platten/playlistai/internal/ports"
)

type progressFailureEngine struct {
	failure error
	cancel  context.CancelFunc
}

func (e progressFailureEngine) AlgorithmVersion() string { return "progress-failure-fixture/v1" }
func (e progressFailureEngine) Build(context.Context, core.MusicIntent) (core.Playlist, error) {
	return core.Playlist{}, e.failure
}
func (e progressFailureEngine) BuildRecommendation(ctx context.Context, request ports.RecommendationRequest) (core.Playlist, error) {
	request.Progress.Report("generation", 2, 10, "private progress note")
	ports.ReportSearch(request.Progress, core.SearchProgress{Stage: "verifying", CandidatesConsidered: 9, CandidatesEligible: 2})
	request.OnChecked(core.TrackRef{ID: "private-track", Artist: "private-artist", Title: "private-title"})
	request.OnSuggested(core.TrackRef{ID: "private-suggestion"})
	if e.cancel != nil {
		e.cancel()
		// Context cancellation must not prevent retaining the last breadcrumb.
		request.Progress.Report("generation", 2, 10, "private canceled note")
		return core.Playlist{}, ctx.Err()
	}
	return core.Playlist{}, e.failure
}

func TestHeadlessProgressDiagnosticsRetainedAfterGenerationFailure(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, canceled := range []bool{false, true} {
			t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled]+"/"+map[bool]string{false: "error", true: "canceled"}[canceled], func(t *testing.T) {
				api := New(newLoadedContainer(t), nil)
				store := &logging.Store{}
				store.SetDebug(enabled)
				ctx, cancel := context.WithCancel(logging.WithDiagnostics(context.Background(), store))
				defer cancel()
				failure := errors.New("fixture recommendation failure")
				engine := progressFailureEngine{failure: failure}
				if canceled {
					engine.cancel = cancel
					failure = context.Canceled
				}
				useRecommendationEngine(api, engine)
				result, err := api.GenerateFromPrompt(ctx, "1 track like Justice")
				if !errors.Is(err, failure) || len(result.Playlist.Tracks) != 0 {
					t.Fatalf("generation semantics changed: result=%+v error=%v", result.Playlist, err)
				}
				entries := store.Read(0)
				if !enabled {
					if len(entries) != 0 {
						t.Fatalf("opt-out retained %d diagnostics", len(entries))
					}
					return
				}
				var checked, suggested, phase, generationError bool
				for _, entry := range entries {
					generationError = generationError || strings.Contains(entry.Text, `event="generation.error"`)
					if !strings.Contains(entry.Text, `event="generation.progress"`) {
						continue
					}
					if strings.Contains(entry.Text, "private-") || strings.Contains(entry.Text, "private ") || strings.Contains(entry.Text, `"note"`) {
						t.Fatalf("progress included sensitive payload: %s", entry.Text)
					}
					_, raw, ok := strings.Cut(entry.Text, " data=")
					var event struct {
						core.SearchProgress
						Checked, Suggested bool
					}
					if !ok || json.Unmarshal([]byte(raw), &event) != nil {
						t.Fatalf("invalid progress diagnostic: %s", entry.Text)
					}
					checked, suggested = checked || event.Checked, suggested || event.Suggested
					phase = phase || event.Stage == "verifying" && event.CandidatesConsidered == 9 && event.CandidatesEligible == 2
				}
				if !checked || !suggested || !phase || !generationError {
					t.Fatalf("missing failure progress: checked=%v suggested=%v phase=%v error=%v", checked, suggested, phase, generationError)
				}
			})
		}
	}
}

func TestHeadlessProgressDiagnosticsBoundedConcurrentAndOptOut(t *testing.T) {
	store := &logging.Store{}
	store.SetDebug(true)
	ctx, cancel := context.WithCancel(logging.WithDiagnostics(context.Background(), store))
	ctx = context.WithValue(ctx, generationKey{}, &liveGeneration{ID: "generation-fixture", started: time.Now().Add(-time.Second)})
	progress := generationProgress(ctx)
	cancel()
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 600 {
				progress.Search(core.SearchProgress{Stage: "checking"})
			}
		}()
	}
	workers.Wait()
	progress.Search(core.SearchProgress{Stage: "assembling", CandidatesConsidered: 42})
	entries := store.Read(0)
	if len(entries) != 2000 || entries[0].ID <= 1 || !strings.Contains(entries[len(entries)-1].Text, `"candidatesConsidered":42`) {
		t.Fatalf("bounded store lost latest progress: count=%d", len(entries))
	}
	_, raw, _ := strings.Cut(entries[len(entries)-1].Text, " data=")
	var event core.SearchProgress
	if err := json.Unmarshal([]byte(raw), &event); err != nil || event.ElapsedMilliseconds < 1000 {
		t.Fatalf("generation elapsed time missing: %+v err=%v", event, err)
	}
	store.SetDebug(false)
	progress.Report("generation", 0, 0, "private after opt-out")
	if len(store.Read(0)) != 0 {
		t.Fatal("opt-out did not clear and stop progress diagnostics")
	}
}
