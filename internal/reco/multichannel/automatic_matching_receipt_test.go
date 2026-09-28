package multichannel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
)

// This opt-in fixed synthetic receipt compares policy/coverage and scheduling;
// its scores are fixtures and never evidence of improved musical quality.
func TestAutomaticMatchingPolicyReceipt(t *testing.T) {
	if os.Getenv("PLAYLISTAI_MATCHING_RECEIPT") != "1" {
		t.Skip("opt-in matched synthetic policy receipt")
	}
	for _, strength := range []string{"essential", "required", ""} {
		c, intent, pool := automaticFixture(t, 2)
		intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "instrumentation", Value: "soft piano", Scope: "playlist", Strength: strength}}
		execution := intent
		execution.Controls.RecommendationMode = core.EnhancedHybrid
		clause := audio.Clauses(execution)[0]
		for id, score := range map[string]float64{"0": .2, "1": .8, "2": .6, "3": .1} {
			c.observations[id] = core.AudioAssessment{TrackID: id, AnalysisID: "prepared:" + id, ModelFingerprint: "synthetic-clap/v1", LibraryCoverage: &core.LibraryCLAPCoverage{CoveredSeconds: 30}, Clauses: []core.AudioClauseAssessment{{Clause: clause, Score: score, ScoreAvailable: true, State: core.EvidenceUnknown}}}
		}
		start := time.Now()
		got, err := NewAutomatic(c, nil, pool, DefaultConfig()).WithEnricher(automaticBlockingEnricher{}).Build(func() context.Context {
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			t.Cleanup(cancel)
			return ctx
		}(), intent)
		if err != nil {
			t.Fatal(err)
		}
		receipt := struct {
			Algorithm, Policy, Strength, Seed string
			Requested, Returned               int
			IDs                               []string
			Outcome, Stop                     string
			ElapsedUS                         int64
		}{Algorithm: AutomaticAlgorithmVersion, Policy: core.AutomaticFitPolicyVersion, Strength: strength, Seed: string(intent.Seed), Requested: 2, Returned: len(got.Tracks), Outcome: string(got.Outcome.State), Stop: got.Search.StopReason, ElapsedUS: time.Since(start).Microseconds()}
		for _, track := range got.Tracks {
			receipt.IDs = append(receipt.IDs, track.ID)
		}
		data, _ := json.Marshal(receipt)
		fmt.Println("MATCHING_POLICY_RECEIPT " + string(data))
	}
}
