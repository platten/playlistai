package catalog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/reco/multichannel"
)

// Exercise the production metadata adapter with an occupied temporary SQLite pool.
func TestSelectionMetadataCancellationInterruptsSQL(t *testing.T) {
	for _, mode := range []core.RecommendationMode{core.EnhancedHybrid, core.AcousticBrainzFirst, core.CLAPFirst, core.DeejAIOnly} {
		for _, stage := range []string{"Select", "Sequence"} {
			t.Run(string(mode)+"/"+stage, func(t *testing.T) {
				cat := openTestdata(t)
				meta, ok := cat.Meta("seed0001")
				if !ok {
					t.Fatal("fixture metadata missing")
				}
				intent := core.MusicIntent{Version: core.CurrentIntentVersion, Mode: core.ModeSimilar, Count: 1,
					Controls: core.IntentControls{TotalTrackCount: 1, RecommendationMode: mode}}.Normalized()
				candidates := []core.Candidate{{Track: meta.Ref, Scores: core.CandidateScores{Total: 1}}}
				cat.db.SetMaxOpenConns(1)
				conn, err := cat.db.Conn(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan error, 1)
				waits := cat.db.Stats().WaitCount
				go func() {
					var err error
					if stage == "Select" {
						_, err = multichannel.NewSelector(cat, multichannel.Config{}).Select(ctx, candidates, ports.SelectionRequest{Intent: intent, Count: 1})
					} else {
						_, err = multichannel.NewSequencer(cat, multichannel.Config{}).Sequence(ctx, ports.SequenceRequest{Intent: intent, Candidates: candidates, Seed: 1})
					}
					done <- err
				}()
				waitForCatalogQuery(t, cat.db, waits)
				cancel()
				select {
				case err := <-done:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("lost cancellation: %v", err)
					}
				case <-time.After(time.Second):
					_ = conn.Close()
					select {
					case err := <-done:
						t.Fatalf("%s still waited for SQLite connection after cancellation; released for cleanup; eventual error=%v", stage, err)
					case <-time.After(time.Second):
						t.Fatal("cleanup did not finish")
					}
				}
			})
		}
	}
}
