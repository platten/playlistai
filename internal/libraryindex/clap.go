package libraryindex

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/localaudio"
)

const CLAPSamplingVersion = "two-distributed-10s-thirds-or-edges/v1"

type CLAPSegmentRecord struct {
	Index           int       `json:"index"`
	StartSeconds    float64   `json:"startSeconds"`
	EndSeconds      float64   `json:"endSeconds"`
	ObservedSeconds float64   `json:"observedSeconds"`
	InputSeconds    float64   `json:"inputSeconds,omitempty"`
	Padding         string    `json:"padding,omitempty"`
	Validity        string    `json:"validity,omitempty"`
	Reason          string    `json:"reason,omitempty"`
	Vector          []float32 `json:"vector"`
}

type CLAPRecord struct {
	Model         core.AudioModelIdentity `json:"model"`
	Sampling      string                  `json:"sampling"`
	Device        string                  `json:"device"`
	Segments      []CLAPSegmentRecord     `json:"segments"`
	Pooled        []float32               `json:"pooled"`
	Coverage      float64                 `json:"coveredSeconds"`
	Incomplete    bool                    `json:"incomplete,omitempty"`
	PartialReason string                  `json:"partialReason,omitempty"`
}

// portableEvidence retains measured intervals from stored analysis. Older
// records lack explicit padding fields; only this known producer's 10-second
// repeat-padding contract permits reconstructing them, never the manifest's
// nominal sampling policy or a pooled vector alone.
func (r CLAPRecord) portableEvidence() *librarypack.CLAPEvidence {
	if len(r.Segments) == 0 {
		return nil
	}
	model := r.Model
	e := &librarypack.CLAPEvidence{Model: &model, Sampling: r.Sampling, Scope: librarypack.CLAPEvidenceScope, CoveredSeconds: r.Coverage, Incomplete: r.Incomplete, PartialReason: r.PartialReason}
	if e.Incomplete && e.PartialReason == "" {
		e.PartialReason = "observed_audio_shorter_than_requested"
	}
	for _, s := range r.Segments {
		segment := librarypack.CLAPSegment{Index: s.Index, StartSeconds: s.StartSeconds, EndSeconds: s.EndSeconds, ObservedSeconds: s.ObservedSeconds, InputSeconds: s.InputSeconds, Padding: s.Padding, Validity: s.Validity, Reason: s.Reason, Vector: append([]float32(nil), s.Vector...)}
		if segment.Validity == "" && len(segment.Vector) > 0 {
			segment.Validity = "valid"
		}
		if segment.Padding == "" {
			segment.Padding = "unknown"
			if r.Sampling == CLAPSamplingVersion && segment.Validity == "valid" && segment.ObservedSeconds > 0 && segment.ObservedSeconds <= 10 {
				segment.InputSeconds, segment.Padding = 10, "none"
				if segment.ObservedSeconds < 10 {
					segment.Padding = "repeat"
				}
			}
		}
		e.Segments = append(e.Segments, segment)
	}
	return e
}

func CLAPWindows(duration float64) ([]SampleWindow, error) {
	if math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 {
		return nil, errors.New("library indexer: exact positive duration required for CLAP sampling")
	}
	if duration <= 10 {
		return []SampleWindow{{Index: 0, Duration: duration}}, nil
	}
	if duration < 20 {
		first := duration / 2
		return []SampleWindow{{Index: 0, Duration: first}, {Index: 1, Start: first, Duration: duration - first}}, nil
	}
	if duration < 30 {
		return []SampleWindow{{Index: 0, Duration: 10}, {Index: 1, Start: duration - 10, Duration: 10}}, nil
	}
	first := math.Max(0, math.Min(duration-10, duration/3-5))
	second := math.Max(first+10, math.Min(duration-10, 2*duration/3-5))
	return []SampleWindow{{Index: 0, Start: first, Duration: 10}, {Index: 1, Start: second, Duration: 10}}, nil
}

func CLAPSemanticKey(runtimeID string, modelID string) string {
	return audio.PreprocessingVersion + ";local-pcm/v1;" + runtimeID + ";" + CLAPSamplingVersion + ";mean-duration-pool/v1;" + modelID
}

func (a *Analyzer) runCLAP(ctx context.Context, discoveryDone <-chan struct{}, report *AnalysisReport) error {
	workerCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	leases := newJobLeaseTracker()
	heartbeatDone := make(chan struct{})
	go a.heartbeatJobs(workerCtx, leases, cancel, heartbeatDone)
	defer func() { cancel(nil); <-heartbeatDone }()
	jobs := make(chan Job, a.Plan.QueueDepth)
	var workers sync.WaitGroup
	for range max(1, a.Plan.DecodeWorkers) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				err := a.processCLAP(workerCtx, job)
				leases.remove(job)
				if err != nil {
					retry := retryableAnalysisError(workerCtx, err, job.RetryCount)
					code := classifyAnalysisError(err)
					a.emitJobIssue(workerCtx, job, "clap", code, err, retry)
					if failErr := a.State.FailJob(workerCtx, job, code, err.Error(), retry); failErr != nil {
						cancel(failErr)
						return
					}
					if retry {
						atomic.AddInt64(&report.Retried, 1)
					} else {
						atomic.AddInt64(&report.Failed, 1)
					}
					continue
				}
				atomic.AddInt64(&report.CLAPCompleted, 1)
			}
		}()
	}
	err := a.dispatchJobs(workerCtx, "clap", discoveryDone, jobs, leases)
	close(jobs)
	workers.Wait()
	if cause := context.Cause(workerCtx); cause != nil && !errors.Is(cause, context.Canceled) {
		return errors.Join(err, cause)
	}
	return err
}

func (a *Analyzer) processCLAP(ctx context.Context, job Job) error {
	file, err := a.State.File(ctx, job.FileID)
	if err != nil {
		return err
	}
	path, err := file.AbsolutePath()
	if err != nil {
		return err
	}
	if a.OnFile != nil {
		a.OnFile(FileActivity{RelativePath: file.RelativePath, Size: file.Size, Extension: file.Extension})
	}
	if err := a.verifySourceRevision(ctx, job, file, path); err != nil {
		return err
	}
	metadata, revision, err := a.State.Metadata(ctx, job.FileID)
	if err != nil {
		return err
	}
	if revision != job.SourceRevision {
		return errSourceChangedAfterManifest
	}
	var stored MetadataRecord
	if err := json.Unmarshal(metadata, &stored); err != nil {
		return err
	}
	if stored.Unsupported != "" {
		return fmt.Errorf("%w: %s", localaudio.ErrUnsupported, stored.Unsupported)
	}
	stored.Probe.Path = path
	windows, err := CLAPWindows(stored.Probe.Duration.Seconds)
	if err != nil {
		return err
	}
	record := CLAPRecord{Model: a.CLAP.Identity(), Sampling: CLAPSamplingVersion, Device: a.CLAPDevice}
	sums := make([]float64, 512)
	var weight float64
	for _, sampled := range windows {
		requested := localaudio.Window{Index: sampled.Index, Start: time.Duration(sampled.Start * float64(time.Second)), Duration: time.Duration(sampled.Duration * float64(time.Second))}
		reservation, err := decodedPCMReservation(requested, stored.Probe.SelectedStream.SampleRate, stored.Probe.SelectedStream.Channels)
		if err != nil {
			return err
		}
		release, err := a.Admission.Acquire(ctx, Reservation{CPU: 1, SourceIO: 1, Memory: reservation, PCMBytes: reservation, Files: 4})
		if err != nil {
			return err
		}
		window, decodeErr := a.Runtime.DecodeWindow(ctx, stored.Probe, requested)
		release()
		if decodeErr != nil {
			return decodeErr
		}
		func() {
			defer clear(window.Samples)
			pcm := audio.DecodedPCM{Samples: window.Samples, SampleRate: window.SampleRate, Channels: window.Channels}
			var samples []float32
			samples, err = audio.CLAPResampleLocal(ctx, pcm)
			if err != nil {
				return
			}
			defer clear(samples)
			segment := make([]float32, audio.SegmentSamples)
			copy(segment, samples)
			if len(samples) > 0 && len(samples) < len(segment) {
				for i := len(samples); i < len(segment); i++ {
					segment[i] = samples[i%len(samples)]
				}
			}
			var vector []float32
			vector, err = a.CLAP.EmbedAudio(ctx, segment)
			clear(segment)
			if err != nil {
				return
			}
			observed := math.Min(window.ObservedDuration.Seconds(), 10)
			owned := append([]float32(nil), vector...)
			clear(vector)
			padding := "none"
			if observed < 10 {
				padding = "repeat"
			}
			record.Segments = append(record.Segments, CLAPSegmentRecord{Index: sampled.Index, StartSeconds: sampled.Start, EndSeconds: sampled.Start + observed, ObservedSeconds: observed, InputSeconds: 10, Padding: padding, Validity: "valid", Vector: owned})
			for i, value := range owned {
				sums[i] += float64(value) * observed
			}
			weight += observed
		}()
		if err != nil {
			return err
		}
	}
	if weight <= 0 {
		return errors.New("library indexer: CLAP produced no observed evidence")
	}
	var norm float64
	for _, value := range sums {
		norm += value * value
	}
	if norm <= 1e-12 || math.IsNaN(norm) || math.IsInf(norm, 0) {
		return errors.New("library indexer: degenerate CLAP aggregate")
	}
	record.Pooled = make([]float32, 512)
	vectorRaw := make([]byte, 512*4)
	for i, value := range sums {
		record.Pooled[i] = float32(value / math.Sqrt(norm))
		binary.LittleEndian.PutUint32(vectorRaw[i*4:], math.Float32bits(record.Pooled[i]))
	}
	record.Coverage, record.Incomplete = weight, weight < math.Min(20, stored.Probe.Duration.Seconds)
	if record.Incomplete {
		record.PartialReason = "observed_audio_shorter_than_requested"
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return a.State.CommitJob(ctx, JobResult{Job: job, Contract: job.SemanticKey, Vector: vectorRaw, CLAPData: raw, Dimension: 512})
}
