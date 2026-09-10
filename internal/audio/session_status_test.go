package audio

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
)

func TestSessionShouldStopMatchesSnapshotFlags(t *testing.T) {
	for _, state := range []string{"active", "stopped", "budget", "canceled", "no-context"} {
		t.Run(state, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stop := make(chan struct{})
			s := &Session{ctx: ctx, stop: stop, started: time.Now()}
			switch state {
			case "stopped":
				close(stop)
			case "budget":
				s.snapshot.BudgetExhausted = true
			case "canceled":
				cancel()
			case "no-context":
				s.ctx = nil
			}
			got := s.ShouldStop()
			want := state != "active" && state != "no-context"
			snapshot := s.Snapshot()
			if got != want || got != (snapshot.Stopped || snapshot.BudgetExhausted) {
				t.Fatal("status poll changed stop/budget semantics")
			}
		})
	}
}

func BenchmarkSessionStopPolling(b *testing.B) {
	s := &Session{ctx: context.Background(), started: time.Now()}
	for i := 0; i < 200; i++ {
		s.snapshot.Assessments = append(s.snapshot.Assessments, core.AudioAssessment{TrackID: fmt.Sprint(i), AnalysisID: "derived-feature-id", Clauses: []core.AudioClauseAssessment{{Clause: core.AudioClause{Kind: "genre", Text: "electronic"}}}})
	}
	b.Run("full_snapshot", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			snapshot := s.Snapshot()
			if snapshot.Stopped || snapshot.BudgetExhausted {
				b.Fatal("unexpected stop")
			}
		}
	})
	b.Run("status_only", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if s.ShouldStop() {
				b.Fatal("unexpected stop")
			}
		}
	})
}
