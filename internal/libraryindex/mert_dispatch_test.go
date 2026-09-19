package libraryindex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/localaudio"
)

func TestMERTDispatchRechecksOutageAfterResourceWait(t *testing.T) {
	for _, resource := range []string{"session", "cpu", "quarantined"} {
		t.Run(resource, func(t *testing.T) {
			plan := ResourcePlan{HeavyWorkers: 1, IOWorkers: 1, MaxRAM: 1 << 30, MaxOpenFiles: 8, InferenceThreads: 1}
			pool := audio.NewMERTWorkerPool(&audio.MERTWorker{}, 1)
			a := &Analyzer{MERT: pool, Plan: plan, Admission: NewAdmission(plan, 1024), Trace: NewStageTrace(100)}
			defer a.Admission.Close()
			a.mertHealth = newMERTSupervisor(context.Background(), func(ctx context.Context, _ bool) (bool, error) { <-ctx.Done(); return false, ctx.Err() }, nil)
			defer a.mertHealth.stop()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var releaseBlock func()
			if resource != "cpu" {
				worker, release, err := pool.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if resource == "quarantined" {
					pool.Quarantine(worker)
				}
				releaseBlock = release
			} else {
				release, err := a.Admission.Acquire(ctx, Reservation{CPU: 1})
				if err != nil {
					t.Fatal(err)
				}
				releaseBlock = release
			}
			defer releaseBlock()
			result := make(chan error, 1)
			go func() {
				var timings AnalysisTimings
				_, _, release, err := a.acquireMERT(ctx, &timings)
				if release != nil {
					release()
				}
				result <- err
			}()
			for {
				queued := a.Admission.Stats().Queued > 0
				if resource != "cpu" {
					events, _ := a.Trace.Snapshot()
					for _, event := range events {
						if event.Stage == "session_queue" {
							queued = true
						}
					}
				}
				if queued {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("dispatch never waited")
				case <-time.After(time.Millisecond):
				}
			}
			if resource != "quarantined" {
				a.mertHealth.markDown(errors.New("unavailable"))
			}
			releaseBlock()
			select {
			case err := <-result:
				if !errors.Is(err, errMERTUnavailable) {
					t.Fatalf("dispatch=%v", err)
				}
			case <-ctx.Done():
				t.Fatal("dispatch did not defer")
			}
			used, _ := a.Admission.Usage()
			if used.CPU != 0 {
				t.Fatalf("CPU leaked: %+v", used)
			}
			_, release, ok := pool.TryAcquire()
			if !ok {
				t.Fatal("worker reservation leaked")
			}
			release()
		})
	}
}

func TestMERTDispatchCancellationReleasesSession(t *testing.T) {
	plan := ResourcePlan{HeavyWorkers: 1, MaxRAM: 1 << 30, InferenceThreads: 1}
	a := &Analyzer{MERT: audio.NewMERTWorkerPool(&audio.MERTWorker{}, 1), Plan: plan, Admission: NewAdmission(plan, 1024)}
	defer a.Admission.Close()
	releaseCPU, err := a.Admission.Acquire(context.Background(), Reservation{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer releaseCPU()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		var timings AnalysisTimings
		_, _, release, err := a.acquireMERT(ctx, &timings)
		if release != nil {
			release()
		}
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for a.Admission.Stats().Queued == 0 {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("did not wait")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	_, release, ok := a.MERT.TryAcquire()
	if !ok {
		t.Fatal("session leaked")
	}
	release()
}

func TestTimingAggregationAcrossReports(t *testing.T) {
	a := &Analyzer{}
	first, second := &AnalysisReport{}, &AnalysisReport{}
	for _, report := range []*AnalysisReport{first, second} {
		a.recordTrackTiming(report, TrackAnalysisTiming{FileID: "b", Windows: 1, Total: time.Second, Timings: AnalysisTimings{ONNXExecution: time.Millisecond}})
		a.recordTrackTiming(report, TrackAnalysisTiming{FileID: "a", Windows: 2})
		a.recordTrackTiming(report, TrackAnalysisTiming{FileID: "b", Windows: 3, Total: time.Second, Timings: AnalysisTimings{ONNXExecution: time.Millisecond}})
		if len(report.TrackTimings) != 2 || report.TrackTimings[0].Windows != 4 || report.TrackTimings[0].Total != 2*time.Second || report.Timings.ONNXExecution != 2*time.Millisecond {
			t.Fatalf("timings=%+v", report)
		}
	}
}

func BenchmarkRecordTrackTiming(b *testing.B) {
	for _, count := range []int{1000, 100000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			a := &Analyzer{}
			report := &AnalysisReport{}
			for i := 0; i < count; i++ {
				a.recordTrackTiming(report, TrackAnalysisTiming{FileID: fmt.Sprint(i)})
			}
			last := TrackAnalysisTiming{FileID: fmt.Sprint(count - 1), Windows: 1}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				a.recordTrackTiming(report, last)
			}
		})
	}
}

// Observing creation of the processing context gives a deterministic worker
// startup barrier: rejecting an invalid configuration must happen before that
// lifetime exists, rather than racing an OnFile assertion against a goroutine.
type analysisStartupContext struct {
	context.Context
	registrations atomic.Int32
}

func (ctx *analysisStartupContext) Done() <-chan struct{} {
	ctx.registrations.Add(1)
	return ctx.Context.Done()
}

func TestAnalyzerRejectsMissingMERTBeforeStartingMetadata(t *testing.T) {
	state, err := OpenState(context.Background(), t.TempDir(), "missing-mert", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	plan := ResourcePlan{HeavyWorkers: 1, MetadataWorkers: 1, DecodeWorkers: 1, QueueDepth: 1, MaxRAM: 1 << 30}
	admission := NewAdmission(plan, 1024)
	defer admission.Close()
	analyzer := &Analyzer{State: state, Runtime: &localaudio.Runtime{}, Plan: plan, Admission: admission, Trace: NewStageTrace(10)}
	ctx := &analysisStartupContext{Context: context.Background()}
	discoveryDone := make(chan struct{})
	close(discoveryDone)
	report, err := analyzer.Run(ctx, AnalysisOptions{Metadata: true, Audio: true}, discoveryDone)
	if err == nil || !strings.Contains(err.Error(), "MERT model is required") {
		t.Fatalf("missing model error=%v", err)
	}
	if ctx.registrations.Load() != 0 {
		t.Fatal("invalid analysis started a processing lifetime before validating its model")
	}
	if report.MetadataCompleted != 0 || admission.Stats().Requests != 0 {
		t.Fatalf("invalid analysis performed work: report=%+v admission=%+v", report, admission.Stats())
	}
	if events, _ := analyzer.Trace.Snapshot(); len(events) != 0 {
		t.Fatalf("invalid analysis dispatched work: %+v", events)
	}
}
