package libraryindex

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/localaudio"
)

const EffNetSamplingVersion = "two-distributed-10s-or-complete-short/v1"

func EffNetSemanticKey(runtimeID string, model audio.DiscogsModel) string {
	return runtimeID + ";" + EffNetSamplingVersion + ";" + model.Fingerprint()
}

func effNetWindows(duration float64) ([]SampleWindow, error) {
	if duration < 3 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return nil, fmt.Errorf("%w: EffNet requires at least three observed seconds", localaudio.ErrUnsupported)
	}
	return CLAPWindows(duration)
}

func (a *Analyzer) runEffNet(ctx context.Context, discoveryDone <-chan struct{}, report *AnalysisReport) error {
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
				err := a.processEffNet(workerCtx, job)
				leases.remove(job)
				if err != nil {
					if errors.Is(err, errSourceChangedAfterManifest) {
						atomic.AddInt64(&report.SkippedChanged, 1)
						a.emitJobIssue(workerCtx, job, "effnet", "source_changed_after_manifest", err, false)
						if failErr := a.State.FailJob(workerCtx, job, "source_changed_after_manifest", err.Error(), false); failErr != nil {
							cancel(failErr)
							return
						}
						continue
					}
					if errors.Is(err, errSourceRefreshed) {
						atomic.AddInt64(&report.Retried, 1)
						a.emitJobIssue(workerCtx, job, "effnet", "source_refreshed", err, true)
						continue
					}
					retry := retryableAnalysisError(workerCtx, err, job.RetryCount)
					code := classifyAnalysisError(err)
					a.emitJobIssue(workerCtx, job, "effnet", code, err, retry)
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
				atomic.AddInt64(&report.EffNetCompleted, 1)
			}
		}()
	}
	err := a.dispatchJobs(workerCtx, "effnet", discoveryDone, jobs, leases)
	close(jobs)
	workers.Wait()
	if cause := context.Cause(workerCtx); cause != nil && !errors.Is(cause, context.Canceled) {
		return errors.Join(err, cause)
	}
	return err
}

func (a *Analyzer) processEffNet(ctx context.Context, job Job) error {
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
	windows, err := effNetWindows(stored.Probe.Duration.Seconds)
	if err != nil {
		return err
	}
	var intervals []core.LibraryAudioInterval
	hash := sha256.New()
	sums := make([][]float64, len(a.EffNetModel.Heads))
	for i, head := range a.EffNetModel.Heads {
		sums[i] = make([]float64, len(head.Classes))
	}
	var covered float64
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
		var observed float64
		var scores [][]float32
		func() {
			defer clear(window.Samples)
			pcm, seconds := boundedCLAPPCM(window, requested, sampled)
			observed = seconds
			if observed < 3 {
				err = fmt.Errorf("%w: EffNet interval shorter than three seconds", localaudio.ErrUnsupported)
				return
			}
			var mono []float32
			mono, err = audio.DiscogsResample(ctx, pcm)
			if err != nil {
				return
			}
			defer clear(mono)
			if len(mono) < 3*16000 {
				err = fmt.Errorf("%w: EffNet interval shorter than three seconds", localaudio.ErrUnsupported)
				return
			}
			bytes := make([]byte, 4*len(mono))
			for i, value := range mono {
				binary.LittleEndian.PutUint32(bytes[4*i:], math.Float32bits(value))
			}
			var offset [8]byte
			binary.LittleEndian.PutUint64(offset[:], math.Float64bits(sampled.Start))
			_, _ = hash.Write(offset[:])
			_, _ = hash.Write(bytes)
			clear(bytes)
			scores, err = a.EffNet.Classify(ctx, mono)
		}()
		if err != nil {
			return err
		}
		for i, row := range scores {
			if i >= len(sums) || len(row) != len(sums[i]) {
				return errors.New("library indexer: invalid EffNet output shape")
			}
			for j, score := range row {
				sums[i][j] += float64(score) * observed
			}
		}
		intervals = append(intervals, core.LibraryAudioInterval{StartSeconds: sampled.Start, EndSeconds: sampled.Start + observed})
		covered += observed
	}
	if err := a.verifySourceRevision(ctx, job, file, path); err != nil {
		return err
	}
	heads := make([]core.MusicClassifierHead, len(a.EffNetModel.Heads))
	for i, head := range a.EffNetModel.Heads {
		head.Scores = make([]float32, len(head.Classes))
		for j, sum := range sums[i] {
			head.Scores[j] = float32(sum / covered)
		}
		heads[i] = head
	}
	incomplete := covered < stored.Probe.Duration.Seconds-0.001
	reason := ""
	if incomplete {
		reason = "sampled local recording only"
	}
	evidence := core.MusicClassifierEvidence{Version: core.MusicClassifierEvidenceVersion, Encoder: a.EffNetModel.Encoder,
		Preprocessing: audio.DiscogsPreprocessing, Runtime: audio.DiscogsRuntime, AudioSHA256: hex.EncodeToString(hash.Sum(nil)),
		Source: "local-library", SourceID: file.ID, License: "local-only derivative; source redistribution rights not established",
		Coverage: core.LibraryCLAPCoverage{CoveredSeconds: covered, Incomplete: incomplete, PartialReason: reason, Segments: intervals}, Heads: heads}
	if err := evidence.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	return a.State.CommitJob(ctx, JobResult{Job: job, Contract: job.SemanticKey, EffNetData: raw})
}
