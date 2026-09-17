package libraryindex

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/localaudio"
)

type AnalysisOptions struct {
	Metadata bool
	Audio    bool
	Profile  SamplingProfile
}

type AnalysisReport struct {
	MetadataCompleted int64 `json:"metadataCompleted"`
	AudioCompleted    int64 `json:"audioCompleted"`
	Failed            int64 `json:"failed"`
	Retried           int64 `json:"retried"`
	SkippedChanged    int64 `json:"skippedChanged"`
}

type Analyzer struct {
	State     *State
	Runtime   *localaudio.Runtime
	MERT      *audio.MERTWorkerPool
	Plan      ResourcePlan
	Admission *Admission
	Profile   SamplingProfile
	OnFile    func(FileActivity)
	OnIssue   func(ProcessingIssue)
	// FreezeManifest prevents a file changed after the scan/diff barrier from
	// being admitted again during this run. The next scan observes and queues it.
	FreezeManifest bool
	DiffEpoch      int64
	stageOnce      sync.Once
	dspSlots       chan struct{}
}

const (
	jobLeaseDuration   = 5 * time.Minute
	jobHeartbeatPeriod = time.Minute
)

var errSourceRefreshed = errors.New("library indexer: source revision refreshed")
var errSourceChangedAfterManifest = errors.New("library indexer: source changed after scan manifest")

type jobLeaseTracker struct {
	mu   sync.Mutex
	jobs map[int64]Job
}

func newJobLeaseTracker() *jobLeaseTracker {
	return &jobLeaseTracker{jobs: make(map[int64]Job)}
}

func (t *jobLeaseTracker) add(jobs ...Job) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, job := range jobs {
		t.jobs[job.ID] = job
	}
}

func (t *jobLeaseTracker) remove(job Job) {
	t.mu.Lock()
	delete(t.jobs, job.ID)
	t.mu.Unlock()
}

func (t *jobLeaseTracker) snapshot() []Job {
	t.mu.Lock()
	defer t.mu.Unlock()
	jobs := make([]Job, 0, len(t.jobs))
	for _, job := range t.jobs {
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	return jobs
}

func (a *Analyzer) initializeStageLimits() {
	a.stageOnce.Do(func() { a.dspSlots = make(chan struct{}, max(1, a.Plan.DSPWorkers)) })
}

type MetadataRecord struct {
	Probe       localaudio.ProbeResult `json:"probe"`
	Contract    string                 `json:"contract"`
	Unsupported string                 `json:"unsupported,omitempty"`
	Integrity   IntegrityRecord        `json:"integrity"`
}

type IntegrityRecord struct {
	Status string `json:"status"`
	Method string `json:"method,omitempty"`
	Error  string `json:"error,omitempty"`
}

type DSPWindowRecord struct {
	Index           int              `json:"index"`
	StartSeconds    float64          `json:"startSeconds"`
	ObservedSeconds float64          `json:"observedSeconds"`
	SampleRate      int              `json:"sampleRate"`
	Channels        int              `json:"channels"`
	Features        core.DSPFeatures `json:"features"`
}

type DSPRecord struct {
	Version  string            `json:"version"`
	Sampling string            `json:"sampling"`
	Scope    string            `json:"scope"`
	Windows  []DSPWindowRecord `json:"windows"`
}

type MERTSegmentRecord struct {
	Index                     int     `json:"index"`
	StartSeconds              float64 `json:"startSeconds"`
	ObservedSeconds           float64 `json:"observedSeconds"`
	DownmixCancellationRatio  float64 `json:"downmixCancellationRatio"`
	SevereDownmixCancellation bool    `json:"severeDownmixCancellation"`
}

type MERTRecord struct {
	Model              core.AudioRepresentationIdentity `json:"model"`
	LocalPreprocessing string                           `json:"localPreprocessing"`
	Sampling           string                           `json:"sampling"`
	Segments           []MERTSegmentRecord              `json:"segments"`
	InferenceWorkers   int                              `json:"inferenceWorkers"`
	InferenceThreads   int                              `json:"inferenceThreads"`
}

func (a *Analyzer) Run(ctx context.Context, options AnalysisOptions, discoveryDone <-chan struct{}) (AnalysisReport, error) {
	var report AnalysisReport
	if a.State == nil || a.Runtime == nil || a.Admission == nil {
		return report, errors.New("library indexer: analyzer is not configured")
	}
	if options.Profile == "" {
		options.Profile = ProfileBalanced
	}
	a.initializeStageLimits()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var wg sync.WaitGroup
	fatal := make(chan error, 2)
	if options.Metadata {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.runMetadata(ctx, discoveryDone, &report); err != nil {
				fatal <- err
				cancel(err)
			}
		}()
	}
	if options.Audio {
		if a.MERT == nil {
			return report, errors.New("library indexer: MERT model is required for audio analysis")
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.runAudio(ctx, discoveryDone, &report, options.Profile); err != nil {
				fatal <- err
				cancel(err)
			}
		}()
	}
	wg.Wait()
	close(fatal)
	var errs []error
	for err := range fatal {
		errs = append(errs, err)
	}
	return report, errors.Join(errs...)
}

func (a *Analyzer) runMetadata(ctx context.Context, discoveryDone <-chan struct{}, report *AnalysisReport) error {
	workerCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	leases := newJobLeaseTracker()
	heartbeatDone := make(chan struct{})
	go a.heartbeatJobs(workerCtx, leases, cancel, heartbeatDone)
	defer func() { cancel(nil); <-heartbeatDone }()
	jobs := make(chan Job, a.Plan.QueueDepth)
	var workers sync.WaitGroup
	for range a.Plan.MetadataWorkers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				err := a.processMetadata(workerCtx, job)
				leases.remove(job)
				if err != nil {
					if errors.Is(err, errSourceChangedAfterManifest) {
						atomic.AddInt64(&report.SkippedChanged, 1)
						a.emitJobIssue(workerCtx, job, "metadata", "source_changed_after_manifest", err, false)
						if failErr := a.State.FailJob(workerCtx, job, "source_changed_after_manifest", err.Error(), false); failErr != nil {
							cancel(failErr)
							return
						}
						continue
					}
					if errors.Is(err, errSourceRefreshed) {
						atomic.AddInt64(&report.Retried, 1)
						a.emitJobIssue(workerCtx, job, "metadata", "source_refreshed", err, true)
						continue
					}
					retry := retryableAnalysisError(workerCtx, err, job.RetryCount)
					code := classifyAnalysisError(err)
					a.emitJobIssue(workerCtx, job, "metadata", code, err, retry)
					if retry {
						atomic.AddInt64(&report.Retried, 1)
					} else {
						atomic.AddInt64(&report.Failed, 1)
					}
					if failErr := a.State.FailJob(workerCtx, job, code, err.Error(), retry); failErr != nil {
						cancel(failErr)
						return
					}
					continue
				}
				atomic.AddInt64(&report.MetadataCompleted, 1)
			}
		}()
	}
	err := a.dispatchJobs(workerCtx, "metadata", discoveryDone, jobs, leases)
	close(jobs)
	workers.Wait()
	if cause := context.Cause(workerCtx); cause != nil && !errors.Is(cause, context.Canceled) {
		return errors.Join(err, cause)
	}
	return err
}

func (a *Analyzer) processMetadata(ctx context.Context, job Job) error {
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
	release, err := a.Admission.Acquire(ctx, Reservation{CPU: 1, SourceIO: 1, Memory: 64 << 20, Files: 4})
	if err != nil {
		return err
	}
	probe, probeErr := a.Runtime.Probe(ctx, path)
	release()
	if probeErr != nil {
		if errors.Is(probeErr, localaudio.ErrSourceChanged) {
			if a.FreezeManifest {
				return errors.Join(errSourceChangedAfterManifest, probeErr)
			}
			if verifyErr := a.verifySourceRevision(ctx, job, file, path); verifyErr != nil {
				return verifyErr
			}
		}
		if !errors.Is(probeErr, localaudio.ErrUnsupported) {
			return probeErr
		}
	}
	if err := a.verifySourceRevision(ctx, job, file, path); err != nil {
		return err
	}
	integrity := IntegrityRecord{Status: "not_required"}
	if probeErr == nil && localaudio.RequiresIntegrityValidation(probe) {
		integrity = IntegrityRecord{Status: "valid", Method: localaudio.IntegrityValidationVersion}
		release, err = a.Admission.Acquire(ctx, Reservation{CPU: 1, SourceIO: 1, Memory: 8 << 20, Files: 4})
		if err != nil {
			return err
		}
		validationErr := a.Runtime.ValidateIntegrity(ctx, probe)
		release()
		if errors.Is(validationErr, localaudio.ErrSourceChanged) {
			if a.FreezeManifest {
				return errors.Join(errSourceChangedAfterManifest, validationErr)
			}
			if verifyErr := a.verifySourceRevision(ctx, job, file, path); verifyErr != nil {
				return verifyErr
			}
			return validationErr
		}
		if validationErr != nil {
			if !errors.Is(validationErr, localaudio.ErrCorrupt) {
				return validationErr
			}
			integrity.Status = "corrupt"
			integrity.Error = boundedAnalysisDetail(validationErr.Error())
			a.emitIssue(NewProcessingIssue("metadata", file.RootAlias, file.RelativePath, "corrupt_media", validationErr, false))
		}
		if err := a.verifySourceRevision(ctx, job, file, path); err != nil {
			return err
		}
	}
	probe.Path = "" // persistent metadata never needs an absolute source path
	unsupported := ""
	if probeErr != nil {
		unsupported = probeErr.Error()
		a.emitIssue(NewProcessingIssue("metadata", file.RootAlias, file.RelativePath, "unsupported", probeErr, false))
	}
	raw, err := json.Marshal(MetadataRecord{Probe: probe, Contract: job.SemanticKey, Unsupported: unsupported, Integrity: integrity})
	if err != nil {
		return err
	}
	return a.State.CommitJob(ctx, JobResult{Job: job, Contract: job.SemanticKey, Metadata: raw})
}

func (a *Analyzer) runAudio(ctx context.Context, discoveryDone <-chan struct{}, report *AnalysisReport, profile SamplingProfile) error {
	workerCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	leases := newJobLeaseTracker()
	heartbeatDone := make(chan struct{})
	go a.heartbeatJobs(workerCtx, leases, cancel, heartbeatDone)
	defer func() { cancel(nil); <-heartbeatDone }()
	jobs := make(chan Job, a.Plan.QueueDepth)
	var workers sync.WaitGroup
	for range a.Plan.DecodeWorkers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				err := a.processAudio(workerCtx, job, profile)
				leases.remove(job)
				if err != nil {
					if errors.Is(err, errSourceChangedAfterManifest) {
						atomic.AddInt64(&report.SkippedChanged, 1)
						a.emitJobIssue(workerCtx, job, "audio", "source_changed_after_manifest", err, false)
						if failErr := a.State.FailJob(workerCtx, job, "source_changed_after_manifest", err.Error(), false); failErr != nil {
							cancel(failErr)
							return
						}
						continue
					}
					if errors.Is(err, errSourceRefreshed) {
						atomic.AddInt64(&report.Retried, 1)
						a.emitJobIssue(workerCtx, job, "audio", "source_refreshed", err, true)
						continue
					}
					retry := retryableAnalysisError(workerCtx, err, job.RetryCount)
					code := classifyAnalysisError(err)
					a.emitJobIssue(workerCtx, job, "audio", code, err, retry)
					if retry {
						atomic.AddInt64(&report.Retried, 1)
					} else {
						atomic.AddInt64(&report.Failed, 1)
					}
					if failErr := a.State.FailJob(workerCtx, job, code, err.Error(), retry); failErr != nil {
						cancel(failErr)
						return
					}
					continue
				}
				atomic.AddInt64(&report.AudioCompleted, 1)
			}
		}()
	}
	err := a.dispatchJobs(workerCtx, "audio", discoveryDone, jobs, leases)
	close(jobs)
	workers.Wait()
	if cause := context.Cause(workerCtx); cause != nil && !errors.Is(cause, context.Canceled) {
		return errors.Join(err, cause)
	}
	return err
}

func (a *Analyzer) dispatchJobs(ctx context.Context, kind string, discoveryDone <-chan struct{}, out chan<- Job, leases *jobLeaseTracker) error {
	discoveryComplete := false
	for {
		if !discoveryComplete {
			select {
			case <-discoveryDone:
				discoveryComplete = true
			default:
			}
		}
		var claimed []Job
		var err error
		if a.DiffEpoch > 0 {
			claimed, err = a.State.ClaimScanDiffJobs(ctx, a.DiffEpoch, kind, max(1, min(a.Plan.QueueDepth, 64)), jobLeaseDuration)
		} else {
			claimed, err = a.State.ClaimJobs(ctx, kind, max(1, min(a.Plan.QueueDepth, 64)), jobLeaseDuration)
		}
		if err != nil {
			return err
		}
		if len(claimed) == 0 {
			if discoveryComplete {
				if kind == "audio" {
					metadataPending, metadataLeased, err := a.jobCounts(ctx, "metadata")
					if err != nil {
						return err
					}
					if metadataPending == 0 && metadataLeased == 0 {
						if err := a.finalizeBlockedAudio(ctx); err != nil {
							return err
						}
					}
				}
				pending, leased, err := a.jobCounts(ctx, kind)
				if err != nil {
					return err
				}
				if pending == 0 && leased == 0 {
					return nil
				}
			}
			select {
			case <-ctx.Done():
				return context.Cause(ctx)
			case <-time.After(20 * time.Millisecond):
			}
			continue
		}
		leases.add(claimed...)
		for _, job := range claimed {
			select {
			case out <- job:
			case <-ctx.Done():
				leases.remove(job)
				return context.Cause(ctx)
			}
		}
	}
}

func (a *Analyzer) jobCounts(ctx context.Context, kind string) (int64, int64, error) {
	if a.DiffEpoch > 0 {
		return a.State.ScanDiffJobCounts(ctx, a.DiffEpoch, kind)
	}
	return a.State.JobCounts(ctx, kind)
}

func (a *Analyzer) finalizeBlockedAudio(ctx context.Context) error {
	if a.DiffEpoch > 0 {
		return a.State.FinalizeBlockedScanDiffAudio(ctx, a.DiffEpoch)
	}
	return a.State.FinalizeBlockedAudio(ctx)
}

func (a *Analyzer) heartbeatJobs(ctx context.Context, leases *jobLeaseTracker, cancel context.CancelCauseFunc, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(jobHeartbeatPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.State.RenewJobs(ctx, leases.snapshot(), jobLeaseDuration); err != nil {
				cancel(err)
				return
			}
		}
	}
}

func (a *Analyzer) verifySourceRevision(ctx context.Context, job Job, file FileRecord, path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if a.FreezeManifest {
			return fmt.Errorf("%w: %v", errSourceChangedAfterManifest, err)
		}
		return err
	}
	if !info.Mode().IsRegular() {
		if a.FreezeManifest {
			return fmt.Errorf("%w: source is no longer a regular file", errSourceChangedAfterManifest)
		}
		return errors.New("library indexer: source is no longer a regular file")
	}
	device, inode := fileIdentity(info)
	observed := sourceRevision(info.Size(), info.ModTime().UnixNano(), device, inode)
	if observed == job.SourceRevision {
		return nil
	}
	if a.FreezeManifest {
		return fmt.Errorf("%w: expected size %d, observed size %d", errSourceChangedAfterManifest, file.Size, info.Size())
	}
	refreshed, err := a.State.RefreshFileRevision(ctx, job.FileID, job.SourceRevision, info.Size(), info.ModTime().UnixNano(), device, inode)
	if err != nil {
		return err
	}
	if refreshed {
		return errSourceRefreshed
	}
	return localaudio.ErrSourceChanged
}

func (a *Analyzer) emitJobIssue(ctx context.Context, job Job, stage, code string, err error, retryable bool) {
	file, fileErr := a.State.File(ctx, job.FileID)
	if fileErr != nil {
		a.emitIssue(NewProcessingIssue(stage, "", "", code, errors.Join(err, fileErr), retryable))
		return
	}
	a.emitIssue(NewProcessingIssue(stage, file.RootAlias, file.RelativePath, code, err, retryable))
}

func (a *Analyzer) emitIssue(issue ProcessingIssue) {
	if a.OnIssue != nil {
		a.OnIssue(issue)
	}
}

func (a *Analyzer) processAudio(ctx context.Context, job Job, profile SamplingProfile) error {
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
		return localaudio.ErrSourceChanged
	}
	var stored MetadataRecord
	if err := json.Unmarshal(metadata, &stored); err != nil {
		return err
	}
	if stored.Unsupported != "" {
		return fmt.Errorf("%w: %s", localaudio.ErrUnsupported, stored.Unsupported)
	}
	if stored.Integrity.Status == "corrupt" {
		return fmt.Errorf("%w: %s", localaudio.ErrCorrupt, stored.Integrity.Error)
	}
	probe := stored.Probe
	probe.Path = path
	windows, err := samplingForProbe(probe, profile)
	if err != nil {
		return err
	}
	decodeWindows := make([]localaudio.Window, len(windows))
	for i, window := range windows {
		decodeWindows[i] = localaudio.Window{Index: window.Index, Start: time.Duration(window.Start * float64(time.Second)), Duration: time.Duration(window.Duration * float64(time.Second))}
	}
	var dsp DSPRecord
	dsp.Version, dsp.Sampling, dsp.Scope = audio.LocalDSPVersion, SamplingVersion+";profile="+string(profile), "sampled_windows"
	mert := MERTRecord{Model: a.MERT.Identity(), LocalPreprocessing: audio.MERTLocalPreprocessingVersion, Sampling: SamplingVersion + ";profile=" + string(profile), InferenceWorkers: a.Plan.InferenceWorkers, InferenceThreads: a.Plan.InferenceThreads}
	sums := make([]float64, audio.MERTDimension)
	var mertErr error
	var decodeReserve int64
	for _, window := range decodeWindows {
		decodeReserve += int64(math.Ceil(window.Duration.Seconds())) * int64(stored.Probe.SelectedStream.SampleRate) * int64(stored.Probe.SelectedStream.Channels) * 4
	}
	decodeLease, err := a.Admission.AcquireLease(ctx, Reservation{CPU: 1, SourceIO: 1, Memory: 64 << 20, Files: 4, PCMBytes: decodeReserve})
	if err != nil {
		return err
	}
	defer decodeLease.Release()
	decoded := make([]localaudio.PCMWindow, 0, len(decodeWindows))
	err = a.Runtime.DecodeWindows(ctx, probe, decodeWindows, func(window localaudio.PCMWindow) error {
		window.Samples = append([]float32(nil), window.Samples...)
		decoded = append(decoded, window)
		return nil
	})
	// Decoder ownership ends here. Keep only the explicitly reserved immutable
	// PCM bytes; source I/O, descriptors, and decode CPU become available before
	// DSP or inference can back up.
	if releaseErr := decodeLease.ReleasePart(Reservation{CPU: 1, SourceIO: 1, Files: 4}); err == nil {
		err = releaseErr
	}
	if err != nil {
		for i := range decoded {
			clear(decoded[i].Samples)
		}
		if a.FreezeManifest && errors.Is(err, localaudio.ErrSourceChanged) {
			return errors.Join(errSourceChangedAfterManifest, err)
		}
		return err
	}
	defer func() {
		for i := range decoded {
			clear(decoded[i].Samples)
			decoded[i].Samples = nil
		}
	}()
	for _, window := range decoded {
		pcm := audio.DecodedPCM{Samples: window.Samples, SampleRate: window.SampleRate, Channels: window.Channels}
		dspWork := func() error {
			releaseCPU, err := a.Admission.Acquire(ctx, Reservation{CPU: 1})
			if err != nil {
				return err
			}
			defer releaseCPU()
			select {
			case a.dspSlots <- struct{}{}:
				defer func() { <-a.dspSlots }()
			case <-ctx.Done():
				return ctx.Err()
			}
			features, err := audio.MeasureLocalDSP(ctx, pcm)
			if err != nil {
				return err
			}
			dsp.Windows = append(dsp.Windows, DSPWindowRecord{Index: window.Index, StartSeconds: window.RequestedStart.Seconds(), ObservedSeconds: window.ObservedDuration.Seconds(), SampleRate: window.SampleRate, Channels: window.Channels, Features: features})
			return nil
		}
		mertWork := func() error {
			releaseCPU, err := a.Admission.Acquire(ctx, Reservation{CPU: max(1, a.Plan.InferenceThreads)})
			if err != nil {
				return err
			}
			defer releaseCPU()
			if !hasMERTSignal(pcm) {
				return errors.New("library indexer: silent or degenerate MERT window")
			}
			ratio := downmixPowerRatio(pcm)
			resampled, err := audio.MERTResampleLocal(ctx, pcm)
			if err != nil {
				return err
			}
			defer clear(resampled)
			if len(resampled) < 400 {
				return errors.New("library indexer: insufficient observed MERT samples")
			}
			vector, err := a.MERT.EmbedAudio(ctx, resampled)
			if err != nil {
				return err
			}
			defer clear(vector)
			if len(vector) != audio.MERTDimension {
				return errors.New("library indexer: invalid MERT dimension")
			}
			weight := float64(len(resampled))
			for i, value := range vector {
				if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
					return errors.New("library indexer: nonfinite MERT output")
				}
				sums[i] += float64(value) * weight
			}
			mert.Segments = append(mert.Segments, MERTSegmentRecord{Index: window.Index, StartSeconds: window.RequestedStart.Seconds(), ObservedSeconds: window.ObservedDuration.Seconds(), DownmixCancellationRatio: ratio, SevereDownmixCancellation: ratio < 0.01})
			return nil
		}
		if a.Plan.Mode == ConcurrencySerial || a.Plan.HeavyWorkers < 2 {
			if err := dspWork(); err != nil {
				return err
			}
			if mertErr == nil {
				mertErr = mertWork()
			}
			continue
		}
		var dspErr, windowMERTErr error
		var branchWG sync.WaitGroup
		branchWG.Add(1)
		go func() {
			defer branchWG.Done()
			dspErr = dspWork()
		}()
		if mertErr == nil {
			branchWG.Add(1)
			go func() {
				defer branchWG.Done()
				windowMERTErr = mertWork()
			}()
		}
		branchWG.Wait()
		if mertErr == nil && windowMERTErr != nil {
			mertErr = windowMERTErr
		}
		if dspErr != nil {
			return dspErr
		}
	}
	if err := a.verifySourceRevision(ctx, job, file, path); err != nil {
		return err
	}
	dspRaw, err := json.Marshal(dsp)
	if err != nil {
		return err
	}
	if mertErr != nil {
		if err := a.State.CommitPartialDSP(ctx, job, job.SemanticKey, dspRaw); err != nil {
			return errors.Join(mertErr, err)
		}
		return mertErr
	}
	vector, err := normalizeSums(sums)
	if err != nil {
		if commitErr := a.State.CommitPartialDSP(ctx, job, job.SemanticKey, dspRaw); commitErr != nil {
			return errors.Join(err, commitErr)
		}
		return err
	}
	mertRaw, err := json.Marshal(mert)
	if err != nil {
		return err
	}
	return a.State.CommitJob(ctx, JobResult{Job: job, Contract: job.SemanticKey, DSP: dspRaw, Vector: float32Bytes(vector), MERTData: mertRaw, Dimension: len(vector)})
}

func hasMERTSignal(pcm audio.DecodedPCM) bool {
	if pcm.Channels < 1 || len(pcm.Samples) < pcm.Channels*2 {
		return false
	}
	frames := len(pcm.Samples) / pcm.Channels
	var sum, squares float64
	for frame := range frames {
		var mono float64
		for channel := range pcm.Channels {
			mono += float64(pcm.Samples[frame*pcm.Channels+channel])
		}
		mono /= float64(pcm.Channels)
		sum += mono
		squares += mono * mono
	}
	mean := sum / float64(frames)
	variance := squares/float64(frames) - mean*mean
	return variance > 1e-12
}

func samplingForProbe(probe localaudio.ProbeResult, profile SamplingProfile) ([]SampleWindow, error) {
	if probe.Duration.Seconds <= 0 {
		return nil, errors.New("library indexer: duration unavailable for bounded sampling")
	}
	if probe.Duration.Reliable {
		return SamplingWindows(probe.Duration.Seconds, profile)
	}
	count := 6
	switch profile {
	case ProfileFast:
		count = 3
	case ProfileDeep:
		count = 12
	}
	windows := make([]SampleWindow, 0, count)
	for i := 0; i < count; i++ {
		start := float64(i * 5)
		if start >= probe.Duration.Seconds {
			break
		}
		windows = append(windows, SampleWindow{Index: i, Start: start, Duration: math.Min(5, probe.Duration.Seconds-start)})
	}
	return windows, nil
}

func normalizeSums(sums []float64) ([]float32, error) {
	var norm float64
	for _, value := range sums {
		norm += value * value
	}
	if norm <= 1e-12 || math.IsNaN(norm) || math.IsInf(norm, 0) {
		return nil, errors.New("library indexer: degenerate pooled MERT output")
	}
	vector := make([]float32, len(sums))
	norm = math.Sqrt(norm)
	for i := range sums {
		vector[i] = float32(sums[i] / norm)
	}
	return vector, nil
}

func float32Bytes(vector []float32) []byte {
	raw := make([]byte, len(vector)*4)
	for i, value := range vector {
		binary.LittleEndian.PutUint32(raw[i*4:], math.Float32bits(value))
	}
	return raw
}

func downmixPowerRatio(pcm audio.DecodedPCM) float64 {
	if pcm.Channels <= 1 {
		return 1
	}
	var channelPower, monoPower float64
	frames := len(pcm.Samples) / pcm.Channels
	for frame := range frames {
		var mono float64
		for channel := 0; channel < pcm.Channels; channel++ {
			value := float64(pcm.Samples[frame*pcm.Channels+channel])
			channelPower += value * value / float64(pcm.Channels)
			mono += value / float64(pcm.Channels)
		}
		monoPower += mono * mono
	}
	if channelPower <= 1e-16 {
		return 1
	}
	return monoPower / channelPower
}

func classifyAnalysisError(err error) string {
	switch {
	case errors.Is(err, audio.ErrNativeWorker):
		return "native_worker_transient"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "canceled"
	case errors.Is(err, localaudio.ErrSourceChanged):
		return "source_changed"
	case errors.Is(err, localaudio.ErrCorrupt):
		return "corrupt_media"
	case errors.Is(err, localaudio.ErrProcessStalled):
		return "decoder_stalled"
	case errors.Is(err, localaudio.ErrUnsupported):
		return "unsupported"
	case errors.Is(err, sql.ErrNoRows):
		return "missing_prerequisite"
	default:
		return "analysis_failed"
	}
}

func retryableAnalysisError(parent context.Context, err error, retries int) bool {
	if retries >= 2 || parent.Err() != nil {
		return false
	}
	return errors.Is(err, localaudio.ErrSourceChanged) || errors.Is(err, localaudio.ErrProcessStalled) || errors.Is(err, audio.ErrNativeWorker) || errors.Is(err, context.DeadlineExceeded)
}

func AudioSemanticKey(runtimeID string, model core.AudioRepresentationIdentity, profile SamplingProfile) string {
	return audio.LocalDSPVersion + ";" + audio.MERTLocalPreprocessingVersion + ";" + audio.MERTPoolingVersion + ";" + runtimeID + ";" + string(profile) + ";" + model.WeightsSHA256
}

func MetadataSemanticKey(runtimeID string) string {
	return fmt.Sprintf("ffprobe-json-tags/v2;%s;%s", localaudio.IntegrityValidationVersion, runtimeID)
}

func boundedAnalysisDetail(value string) string {
	const maximum = 4096
	if len(value) > maximum {
		return value[:maximum]
	}
	return value
}
