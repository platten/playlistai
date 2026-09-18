package libraryindex

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/localaudio"
)

func TestFingerprintGenerationOnlyWhenIdentityTagsAreMissing(t *testing.T) {
	for _, test := range []struct {
		name     string
		metadata localaudio.Metadata
		generate bool
		status   string
	}{
		{name: "embedded fingerprint", metadata: localaudio.Metadata{AcoustIDFingerprint: &localaudio.TagValue{Value: "AQADtNQYhYkYnGhw7Xtagged"}}, status: "available"},
		{name: "embedded AcoustID", metadata: localaudio.Metadata{AcoustID: &localaudio.TagValue{Value: "11111111-2222-3333-4444-555555555555"}}, status: "not_generated_embedded_acoustid"},
		{name: "missing", generate: true, status: "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			record, generate, err := fingerprintFromEmbeddedTags(test.metadata)
			if err != nil || generate != test.generate || record.Status != test.status {
				t.Fatalf("record=%+v generate=%v err=%v", record, generate, err)
			}
		})
	}
}

func TestAcquireDSPSlotDoesNotRetainCPUWhileWaiting(t *testing.T) {
	plan := ResourcePlan{HeavyWorkers: 1, IOWorkers: 1, MaxRAM: 1 << 30, MaxOpenFiles: 8, DSPWorkers: 1}
	analyzer := &Analyzer{Plan: plan, Admission: NewAdmission(plan, 1024)}
	defer analyzer.Admission.Close()
	analyzer.initializeStageLimits()
	analyzer.dspSlots <- struct{}{}
	type dspResult struct {
		release func()
		err     error
	}
	resultCh := make(chan dspResult, 1)
	go func() {
		release, _, _, err := analyzer.acquireDSPSlot(context.Background())
		resultCh <- dspResult{release: release, err: err}
	}()
	time.Sleep(10 * time.Millisecond)
	used, _ := analyzer.Admission.Usage()
	if used.CPU != 0 {
		t.Fatalf("DSP waiter retained %d CPU slots", used.CPU)
	}
	<-analyzer.dspSlots
	result := <-resultCh
	if result.err != nil {
		t.Fatal(result.err)
	}
	used, _ = analyzer.Admission.Usage()
	if used.CPU != 1 {
		t.Fatalf("admitted DSP work holds %d CPU slots", used.CPU)
	}
	result.release()
	used, _ = analyzer.Admission.Usage()
	if used.CPU != 0 {
		t.Fatalf("DSP release leaked CPU: %+v", used)
	}
}

func TestAcquireDSPSlotCancellationReleasesStageSlot(t *testing.T) {
	plan := ResourcePlan{HeavyWorkers: 1, IOWorkers: 1, MaxRAM: 1 << 30, MaxOpenFiles: 8, DSPWorkers: 1}
	analyzer := &Analyzer{Plan: plan, Admission: NewAdmission(plan, 1024)}
	defer analyzer.Admission.Close()
	analyzer.initializeStageLimits()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if release, _, _, err := analyzer.acquireDSPSlot(ctx); release != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled DSP slot acquire: release=%v err=%v", release != nil, err)
	}
	if len(analyzer.dspSlots) != 0 {
		t.Fatal("canceled DSP acquire leaked a stage slot")
	}
}

func TestDecodedPCMReservationUsesFractionalDurationAndRejectsOverflow(t *testing.T) {
	got, err := decodedPCMReservation(localaudio.Window{Duration: 1500 * time.Millisecond}, 44_100, 2)
	if err != nil || got != (66_150+4096)*2*4 {
		t.Fatalf("reservation=%d err=%v", got, err)
	}
	if _, err := decodedPCMReservation(localaudio.Window{Duration: time.Duration(math.MaxInt64)}, math.MaxInt, math.MaxInt); err == nil {
		t.Fatal("overflowing channel reservation accepted")
	}
}

func TestRecordTrackTimingMergesMetadataAndAudioStages(t *testing.T) {
	var report AnalysisReport
	analyzer := &Analyzer{}
	analyzer.recordTrackTiming(&report, TrackAnalysisTiming{FileID: "track", Total: time.Second, Timings: AnalysisTimings{Integrity: time.Second, SourceRead: time.Second}})
	analyzer.recordTrackTiming(&report, TrackAnalysisTiming{FileID: "track", Windows: 6, Total: 2 * time.Second, Timings: AnalysisTimings{Decode: 2 * time.Second, SourceRead: 2 * time.Second}})
	if len(report.TrackTimings) != 1 || report.TrackTimings[0].Windows != 6 || report.TrackTimings[0].Total != 3*time.Second || report.TrackTimings[0].Timings.SourceRead != 3*time.Second {
		t.Fatalf("merged track timings=%+v", report.TrackTimings)
	}
	if report.Timings.Integrity != time.Second || report.Timings.Decode != 2*time.Second || report.Timings.SourceRead != 3*time.Second {
		t.Fatalf("aggregate timings=%+v", report.Timings)
	}
}

func TestRunDecodedWindowsBuffersTrackAndReleasesPCMPerWindow(t *testing.T) {
	plan := ResourcePlan{HeavyWorkers: 2, IOWorkers: 1, MaxRAM: 1 << 30, MaxOpenFiles: 8}
	admission := NewAdmission(plan, 24)
	defer admission.Close()
	windows := []localaudio.Window{{Index: 0, Duration: time.Second}, {Index: 1, Duration: time.Second}, {Index: 2, Duration: time.Second}}
	reservations := []int64{8, 8, 8}
	events := make([]string, 0, 6)
	buffers := make([][]float32, 0, 3)
	processed := 0
	result, err := runDecodedWindows(context.Background(), admission, BufferingTrack, windows, reservations,
		func(_ context.Context, requested localaudio.Window) (localaudio.PCMWindow, error) {
			used, _ := admission.Usage()
			if used.SourceIO != 1 || used.PCMBytes != 24 {
				t.Fatalf("decode usage=%+v", used)
			}
			events = append(events, fmt.Sprintf("d%d", requested.Index))
			samples := []float32{float32(requested.Index + 1), 1}
			buffers = append(buffers, samples)
			return localaudio.PCMWindow{Index: requested.Index, Samples: samples, SampleRate: 1, Channels: 1}, nil
		}, func(window localaudio.PCMWindow) error {
			used, _ := admission.Usage()
			wantPCM := int64(24 - processed*8)
			if used.SourceIO != 0 || used.CPU != 0 || used.PCMBytes != wantPCM {
				t.Fatalf("process %d usage=%+v want PCM=%d", processed, used, wantPCM)
			}
			events = append(events, fmt.Sprintf("p%d", window.Index))
			processed++
			return nil
		})
	if err != nil || !result.TrackBuffered || result.WindowFallback || result.Windows != 3 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if got := strings.Join(events, ","); got != "d0,d1,d2,p0,p1,p2" {
		t.Fatalf("events=%s", got)
	}
	assertClearedBuffers(t, buffers)
	if used, _ := admission.Usage(); used != (AdmissionUsage{}) {
		t.Fatalf("buffered pipeline leaked resources: %+v", used)
	}
}

func TestRunDecodedWindowsFallsBackAndMatchesBufferedOrder(t *testing.T) {
	windows := []localaudio.Window{{Index: 0, Duration: time.Second}, {Index: 1, Duration: time.Second}, {Index: 2, Duration: time.Second}}
	reservations := []int64{8, 8, 8}
	run := func(budget int64) (decodedWindowsResult, []float32, []string, error) {
		plan := ResourcePlan{HeavyWorkers: 2, IOWorkers: 1, MaxRAM: 1 << 30, MaxOpenFiles: 8}
		admission := NewAdmission(plan, budget)
		defer admission.Close()
		var output []float32
		var events []string
		result, err := runDecodedWindows(context.Background(), admission, BufferingTrack, windows, reservations,
			func(_ context.Context, requested localaudio.Window) (localaudio.PCMWindow, error) {
				events = append(events, fmt.Sprintf("d%d", requested.Index))
				return localaudio.PCMWindow{Index: requested.Index, Samples: []float32{float32(requested.Index + 1), 2}}, nil
			}, func(window localaudio.PCMWindow) error {
				events = append(events, fmt.Sprintf("p%d", window.Index))
				output = append(output, float32(window.Index), window.Samples[0]+window.Samples[1])
				return nil
			})
		if used, _ := admission.Usage(); used != (AdmissionUsage{}) {
			t.Fatalf("pipeline leaked resources: %+v", used)
		}
		return result, output, events, err
	}
	buffered, bufferedOutput, bufferedEvents, err := run(24)
	if err != nil || !buffered.TrackBuffered {
		t.Fatalf("buffered result=%+v err=%v", buffered, err)
	}
	fallback, fallbackOutput, fallbackEvents, err := run(16)
	if err != nil || !fallback.WindowFallback || fallback.TrackBuffered {
		t.Fatalf("fallback result=%+v err=%v", fallback, err)
	}
	if !slices.Equal(bufferedOutput, fallbackOutput) {
		t.Fatalf("buffered=%v fallback=%v", bufferedOutput, fallbackOutput)
	}
	if strings.Join(bufferedEvents, ",") != "d0,d1,d2,p0,p1,p2" || strings.Join(fallbackEvents, ",") != "d0,p0,d1,p1,d2,p2" {
		t.Fatalf("buffered events=%v fallback events=%v", bufferedEvents, fallbackEvents)
	}
}

func TestRunDecodedWindowsCleansPartialDecodeAndProcessFailures(t *testing.T) {
	processFailure := errors.New("process failed")
	decodeFailure := localaudio.ErrSourceChanged
	for _, test := range []struct {
		name        string
		decodeFail  int
		processFail int
		want        error
	}{
		{name: "partial decode", decodeFail: 1, processFail: -1, want: decodeFailure},
		{name: "process", decodeFail: -1, processFail: 0, want: processFailure},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := ResourcePlan{HeavyWorkers: 2, IOWorkers: 1, MaxRAM: 1 << 30, MaxOpenFiles: 8}
			admission := NewAdmission(plan, 24)
			defer admission.Close()
			windows := []localaudio.Window{{Index: 0, Duration: time.Second}, {Index: 1, Duration: time.Second}, {Index: 2, Duration: time.Second}}
			var buffers [][]float32
			_, err := runDecodedWindows(context.Background(), admission, BufferingTrack, windows, []int64{8, 8, 8},
				func(_ context.Context, requested localaudio.Window) (localaudio.PCMWindow, error) {
					if requested.Index == test.decodeFail {
						return localaudio.PCMWindow{}, decodeFailure
					}
					samples := []float32{1, 2}
					buffers = append(buffers, samples)
					return localaudio.PCMWindow{Index: requested.Index, Samples: samples}, nil
				}, func(window localaudio.PCMWindow) error {
					if window.Index == test.processFail {
						return processFailure
					}
					return nil
				})
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want %v", err, test.want)
			}
			assertClearedBuffers(t, buffers)
			if used, _ := admission.Usage(); used != (AdmissionUsage{}) {
				t.Fatalf("failure leaked resources: %+v", used)
			}
		})
	}
}

func TestRunDecodedWindowsCancellationWhileWaitingDoesNotLeak(t *testing.T) {
	plan := ResourcePlan{HeavyWorkers: 1, IOWorkers: 1, MaxRAM: 1 << 30, MaxOpenFiles: 8}
	admission := NewAdmission(plan, 8)
	defer admission.Close()
	hold, err := admission.Acquire(context.Background(), Reservation{SourceIO: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := runDecodedWindows(ctx, admission, BufferingWindow, []localaudio.Window{{Index: 0, Duration: time.Second}}, []int64{8},
			func(context.Context, localaudio.Window) (localaudio.PCMWindow, error) {
				return localaudio.PCMWindow{Samples: []float32{1, 2}}, nil
			}, func(localaudio.PCMWindow) error { return nil })
		done <- err
	}()
	for admission.Stats().Queued == 0 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled pipeline error=%v", err)
	}
	hold()
	if used, _ := admission.Usage(); used != (AdmissionUsage{}) {
		t.Fatalf("canceled pipeline leaked resources: %+v", used)
	}
}

func assertClearedBuffers(t *testing.T, buffers [][]float32) {
	t.Helper()
	for _, samples := range buffers {
		for _, sample := range samples {
			if sample != 0 {
				t.Fatalf("released PCM was not cleared: %v", samples)
			}
		}
	}
}
