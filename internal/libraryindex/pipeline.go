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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/localaudio"
)

type AnalysisOptions struct {
	Metadata  bool
	Audio     bool
	Profile   SamplingProfile
	Integrity IntegrityPolicy
}

type IntegrityPolicy string

const (
	IntegrityFull     IntegrityPolicy = "full"
	IntegrityDeferred IntegrityPolicy = "deferred"
)

type AnalysisReport struct {
	MetadataCompleted int64           `json:"metadataCompleted"`
	AudioCompleted    int64           `json:"audioCompleted"`
	Failed            int64           `json:"failed"`
	Retried           int64           `json:"retried"`
	SkippedChanged    int64           `json:"skippedChanged"`
	MERTReused        int64           `json:"mertReused"`
	DSPReused         int64           `json:"dspReused"`
	WindowsDecoded    int64           `json:"windowsDecoded"`
	Timings           AnalysisTimings `json:"timings"`
}

type AnalysisTimings struct {
	Decode         time.Duration `json:"decode"`
	DSP            time.Duration `json:"dsp"`
	MERTPreprocess time.Duration `json:"mertPreprocess"`
	MERTWait       time.Duration `json:"mertWait"`
	MERTInference  time.Duration `json:"mertInference"`
}

type Analyzer struct {
	State     *State
	Runtime   *localaudio.Runtime
	MERT      *audio.MERTWorkerPool
	Plan      ResourcePlan
	Admission *Admission
	Profile   SamplingProfile
	Integrity IntegrityPolicy
	OnFile    func(FileActivity)
	OnIssue   func(ProcessingIssue)
	// StopAdmission closes on graceful shutdown. Dispatchers stop claiming new
	// jobs while workers drain every job already admitted to their queues.
	StopAdmission <-chan struct{}
	// FreezeManifest prevents a file changed after the scan/diff barrier from
	// being admitted again during this run. The next scan observes and queues it.
	FreezeManifest bool
	DiffEpoch      int64
	stageOnce      sync.Once
	dspSlots       chan struct{}
	reuseMu        sync.Mutex
	reuseFlights   map[string]*reuseFlight
}

type reuseFlight struct{ done chan struct{} }

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
	Probe            localaudio.ProbeResult `json:"probe"`
	Contract         string                 `json:"contract"`
	Unsupported      string                 `json:"unsupported,omitempty"`
	Integrity        IntegrityRecord        `json:"integrity"`
	AudioFingerprint FingerprintRecord      `json:"audioFingerprint"`
}

type IntegrityRecord struct {
	Status string `json:"status"`
	Method string `json:"method,omitempty"`
	Error  string `json:"error,omitempty"`
}

type FingerprintRecord struct {
	Status string                       `json:"status"`
	Value  *localaudio.AudioFingerprint `json:"value,omitempty"`
	Error  string                       `json:"error,omitempty"`
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
	Device             string                           `json:"device"`
	ReusedRecording    string                           `json:"reusedRecording,omitempty"`
}

func (a *Analyzer) Run(ctx context.Context, options AnalysisOptions, discoveryDone <-chan struct{}) (AnalysisReport, error) {
	var report AnalysisReport
	if a.State == nil || a.Runtime == nil || a.Admission == nil {
		return report, errors.New("library indexer: analyzer is not configured")
	}
	if options.Profile == "" {
		options.Profile = ProfileBalanced
	}
	if options.Integrity == "" {
		options.Integrity = IntegrityFull
	}
	if options.Integrity != IntegrityFull && options.Integrity != IntegrityDeferred {
		return report, errors.New("library indexer: integrity policy must be full or deferred")
	}
	a.Integrity = options.Integrity
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
				if !errors.Is(err, ErrShutdownRequested) {
					cancel(err)
				}
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
				if !errors.Is(err, ErrShutdownRequested) {
					cancel(err)
				}
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
	fingerprintRecord := FingerprintRecord{Status: "unavailable"}
	if probeErr == nil {
		embeddedFingerprint, fingerprintTagged, embeddedErr := localaudio.EmbeddedAudioFingerprint(probe.Metadata)
		acoustIDTagged := probe.Metadata.AcoustID != nil && strings.TrimSpace(probe.Metadata.AcoustID.Value) != ""
		fingerprintEstablishedIntegrity := false
		switch {
		case fingerprintTagged && embeddedErr == nil:
			fingerprintRecord = FingerprintRecord{Status: "available", Value: &embeddedFingerprint}
		case fingerprintTagged:
			fingerprintRecord = FingerprintRecord{Status: "invalid_embedded", Error: boundedAnalysisDetail(embeddedErr.Error())}
			a.emitIssue(NewProcessingIssue("metadata", file.RootAlias, file.RelativePath, "embedded_fingerprint_invalid", embeddedErr, false))
		case acoustIDTagged:
			fingerprintRecord = FingerprintRecord{Status: "not_generated_embedded_acoustid"}
		default:
			release, err = a.Admission.Acquire(ctx, Reservation{CPU: 1, SourceIO: 1, Memory: 8 << 20, Files: 4})
			if err != nil {
				return err
			}
			fingerprint, fingerprintErr := a.Runtime.AudioFingerprint(ctx, probe)
			release()
			if errors.Is(fingerprintErr, localaudio.ErrSourceChanged) {
				if a.FreezeManifest {
					return errors.Join(errSourceChangedAfterManifest, fingerprintErr)
				}
				if verifyErr := a.verifySourceRevision(ctx, job, file, path); verifyErr != nil {
					return verifyErr
				}
				return fingerprintErr
			}
			if errors.Is(fingerprintErr, context.Canceled) || errors.Is(fingerprintErr, context.DeadlineExceeded) || errors.Is(fingerprintErr, localaudio.ErrOutputLimit) || errors.Is(fingerprintErr, localaudio.ErrProcessStalled) {
				return fingerprintErr
			}
			if fingerprintErr == nil {
				fingerprintRecord = FingerprintRecord{Status: "available", Value: &fingerprint}
				fingerprintEstablishedIntegrity = localaudio.RequiresIntegrityValidation(probe)
			} else {
				fingerprintRecord.Error = boundedAnalysisDetail(fingerprintErr.Error())
				a.emitIssue(NewProcessingIssue("metadata", file.RootAlias, file.RelativePath, "fingerprint_unavailable", fingerprintErr, false))
			}
		}
		if fingerprintEstablishedIntegrity {
			integrity = IntegrityRecord{Status: "valid", Method: localaudio.IntegrityValidationVersion}
		} else if localaudio.RequiresIntegrityValidation(probe) && a.Integrity == IntegrityDeferred {
			integrity = IntegrityRecord{Status: "deferred", Method: "sampled-audio-on-analysis/v1"}
		} else if localaudio.RequiresIntegrityValidation(probe) {
			integrity, err = a.validateMetadataIntegrity(ctx, job, file, probe)
			if err != nil {
				return err
			}
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
	raw, err := json.Marshal(MetadataRecord{Probe: probe, Contract: job.SemanticKey, Unsupported: unsupported, Integrity: integrity, AudioFingerprint: fingerprintRecord})
	if err != nil {
		return err
	}
	return a.State.CommitJob(ctx, JobResult{Job: job, Contract: job.SemanticKey, Metadata: raw})
}

func (a *Analyzer) validateMetadataIntegrity(ctx context.Context, job Job, file FileRecord, probe localaudio.ProbeResult) (IntegrityRecord, error) {
	release, err := a.Admission.Acquire(ctx, Reservation{CPU: 1, SourceIO: 1, Memory: 8 << 20, Files: 4})
	if err != nil {
		return IntegrityRecord{}, err
	}
	integrityErr := a.Runtime.ValidateIntegrity(ctx, probe)
	release()
	if errors.Is(integrityErr, localaudio.ErrSourceChanged) {
		if a.FreezeManifest {
			return IntegrityRecord{}, errors.Join(errSourceChangedAfterManifest, integrityErr)
		}
		return IntegrityRecord{}, integrityErr
	}
	if integrityErr == nil {
		return IntegrityRecord{Status: "valid", Method: localaudio.IntegrityValidationVersion}, nil
	}
	if !errors.Is(integrityErr, localaudio.ErrCorrupt) {
		return IntegrityRecord{}, integrityErr
	}
	a.emitIssue(NewProcessingIssue("metadata", file.RootAlias, file.RelativePath, "corrupt_media", integrityErr, false))
	return IntegrityRecord{Status: "corrupt", Method: localaudio.IntegrityValidationVersion, Error: boundedAnalysisDetail(integrityErr.Error())}, nil
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
				err := a.processAudio(workerCtx, job, profile, report)
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
		if shutdownRequested(a.StopAdmission) {
			return ErrShutdownRequested
		}
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
			case <-a.StopAdmission:
				return ErrShutdownRequested
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

func (a *Analyzer) processAudio(ctx context.Context, job Job, profile SamplingProfile, report *AnalysisReport) error {
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
	recordingKey := metadataRecordingIdentity(stored)
	reusable, reused, flight, err := a.claimReusableMERT(ctx, job.FileID, recordingKey, job.SemanticKey)
	if err != nil {
		return err
	}
	if flight != nil {
		defer a.finishReuseFlight(recordingKey, job.SemanticKey, flight)
	}
	if reused {
		atomic.AddInt64(&report.MERTReused, 1)
	}
	probe := stored.Probe
	probe.Path = path
	dspContract := DSPSemanticKey(probe.ProbeRuntimeID, profile)
	cachedDSP, dspReused, err := a.State.CachedDSP(ctx, job.FileID, job.SourceRevision, dspContract)
	if err != nil {
		return err
	}
	if dspReused {
		atomic.AddInt64(&report.DSPReused, 1)
	}
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
	mert := MERTRecord{Model: a.MERT.Identity(), LocalPreprocessing: audio.MERTLocalPreprocessingVersion, Sampling: SamplingVersion + ";profile=" + string(profile), InferenceWorkers: a.Plan.InferenceWorkers, InferenceThreads: a.Plan.InferenceThreads, Device: a.MERT.Device()}
	if reused {
		mert.ReusedRecording = recordingKey
	}
	sums := make([]float64, audio.MERTDimension)
	var mertErr error
	for _, requested := range decodeWindows {
		if reused && dspReused {
			break
		}
		decodeReserve := int64(math.Ceil(requested.Duration.Seconds())) * int64(stored.Probe.SelectedStream.SampleRate) * int64(stored.Probe.SelectedStream.Channels) * 4
		decodeLease, acquireErr := a.Admission.AcquireLease(ctx, Reservation{CPU: 1, SourceIO: 1, Memory: 64 << 20, Files: 4, PCMBytes: decodeReserve})
		if acquireErr != nil {
			return acquireErr
		}
		decodeStarted := time.Now()
		window, decodeErr := a.Runtime.DecodeWindow(ctx, probe, requested)
		addAnalysisDuration(&report.Timings.Decode, time.Since(decodeStarted))
		if releaseErr := decodeLease.ReleasePart(Reservation{CPU: 1, SourceIO: 1, Files: 4}); decodeErr == nil {
			decodeErr = releaseErr
		}
		if decodeErr != nil {
			decodeLease.Release()
			if a.FreezeManifest && errors.Is(decodeErr, localaudio.ErrSourceChanged) {
				return errors.Join(errSourceChangedAfterManifest, decodeErr)
			}
			return decodeErr
		}
		atomic.AddInt64(&report.WindowsDecoded, 1)
		pcm := audio.DecodedPCM{Samples: window.Samples, SampleRate: window.SampleRate, Channels: window.Channels}
		dspWork := func() error {
			if dspReused {
				return nil
			}
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
			started := time.Now()
			features, err := audio.MeasureLocalDSP(ctx, pcm)
			addAnalysisDuration(&report.Timings.DSP, time.Since(started))
			if err != nil {
				return err
			}
			dsp.Windows = append(dsp.Windows, DSPWindowRecord{Index: window.Index, StartSeconds: window.RequestedStart.Seconds(), ObservedSeconds: window.ObservedDuration.Seconds(), SampleRate: window.SampleRate, Channels: window.Channels, Features: features})
			return nil
		}
		mertWork := func() error {
			if reused {
				return nil
			}
			releaseCPU, err := a.Admission.Acquire(ctx, Reservation{CPU: 1})
			if err != nil {
				return err
			}
			preprocessStarted := time.Now()
			if !hasMERTSignal(pcm) {
				releaseCPU()
				return errors.New("library indexer: silent or degenerate MERT window")
			}
			ratio := downmixPowerRatio(pcm)
			resampled, err := audio.MERTResampleLocal(ctx, pcm)
			releaseCPU()
			addAnalysisDuration(&report.Timings.MERTPreprocess, time.Since(preprocessStarted))
			if err != nil {
				return err
			}
			defer clear(resampled)
			if len(resampled) < 400 {
				return errors.New("library indexer: insufficient observed MERT samples")
			}
			waitStarted := time.Now()
			worker, releaseWorker, err := a.MERT.Acquire(ctx)
			addAnalysisDuration(&report.Timings.MERTWait, time.Since(waitStarted))
			if err != nil {
				return err
			}
			defer releaseWorker()
			cpuUnits := max(1, a.Plan.InferenceThreads)
			if _, cuda := audio.MERTCUDADeviceIndex(a.MERT.Device()); cuda {
				cpuUnits = 1
			}
			releaseInferenceCPU, err := a.Admission.Acquire(ctx, Reservation{CPU: cpuUnits})
			if err != nil {
				return err
			}
			inferenceStarted := time.Now()
			vector, err := worker.EmbedAudio(ctx, resampled)
			releaseInferenceCPU()
			addAnalysisDuration(&report.Timings.MERTInference, time.Since(inferenceStarted))
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
				clear(window.Samples)
				decodeLease.Release()
				return err
			}
			if mertErr == nil {
				mertErr = mertWork()
			}
			clear(window.Samples)
			decodeLease.Release()
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
			clear(window.Samples)
			decodeLease.Release()
			return dspErr
		}
		clear(window.Samples)
		decodeLease.Release()
	}
	if err := a.verifySourceRevision(ctx, job, file, path); err != nil {
		return err
	}
	dspRaw := cachedDSP
	if !dspReused {
		dspRaw, err = json.Marshal(dsp)
		if err != nil {
			return err
		}
	}
	if mertErr != nil && !reused {
		if err := a.State.CommitPartialDSP(ctx, job, job.SemanticKey, dspContract, dspRaw); err != nil {
			return errors.Join(mertErr, err)
		}
		return mertErr
	}
	var vectorRaw []byte
	if reused {
		if reusable.Dimension != audio.MERTDimension || len(reusable.Vector) != audio.MERTDimension*4 {
			return errors.New("library indexer: incompatible reusable MERT result")
		}
		vectorRaw = append([]byte(nil), reusable.Vector...)
	} else {
		vector, normalizeErr := normalizeSums(sums)
		if normalizeErr != nil {
			if commitErr := a.State.CommitPartialDSP(ctx, job, job.SemanticKey, dspContract, dspRaw); commitErr != nil {
				return errors.Join(normalizeErr, commitErr)
			}
			return normalizeErr
		}
		vectorRaw = float32Bytes(vector)
		clear(vector)
	}
	mertRaw, err := json.Marshal(mert)
	if err != nil {
		return err
	}
	return a.State.CommitJob(ctx, JobResult{Job: job, Contract: job.SemanticKey, DSP: dspRaw, DSPCacheContract: dspContract, Vector: vectorRaw, MERTData: mertRaw, Dimension: audio.MERTDimension})
}

func addAnalysisDuration(target *time.Duration, value time.Duration) {
	atomic.AddInt64((*int64)(target), int64(value))
}

func (a *Analyzer) claimReusableMERT(ctx context.Context, fileID, recordingKey, contract string) (ReusableMERT, bool, *reuseFlight, error) {
	if recordingKey == "" {
		return ReusableMERT{}, false, nil, nil
	}
	flightKey := contract + "\x00" + recordingKey
	for {
		cached, found, err := a.State.ReusableMERTForRecording(ctx, fileID, recordingKey, contract)
		if err != nil || found {
			return cached, found, nil, err
		}
		a.reuseMu.Lock()
		if a.reuseFlights == nil {
			a.reuseFlights = make(map[string]*reuseFlight)
		}
		if existing := a.reuseFlights[flightKey]; existing != nil {
			a.reuseMu.Unlock()
			select {
			case <-existing.done:
				continue
			case <-ctx.Done():
				return ReusableMERT{}, false, nil, ctx.Err()
			}
		}
		flight := &reuseFlight{done: make(chan struct{})}
		a.reuseFlights[flightKey] = flight
		a.reuseMu.Unlock()
		return ReusableMERT{}, false, flight, nil
	}
}

func (a *Analyzer) finishReuseFlight(recordingKey, contract string, flight *reuseFlight) {
	key := contract + "\x00" + recordingKey
	a.reuseMu.Lock()
	if a.reuseFlights[key] == flight {
		delete(a.reuseFlights, key)
		close(flight.done)
	}
	a.reuseMu.Unlock()
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

func DSPSemanticKey(runtimeID string, profile SamplingProfile) string {
	return audio.LocalDSPVersion + ";" + runtimeID + ";" + SamplingVersion + ";profile=" + string(profile)
}

func MetadataSemanticKey(runtimeID string) string {
	return MetadataSemanticKeyForPolicy(runtimeID, IntegrityFull)
}

func MetadataSemanticKeyForPolicy(runtimeID string, policy IntegrityPolicy) string {
	if policy == "" {
		policy = IntegrityFull
	}
	base := fmt.Sprintf("ffprobe-json-tags/v4;%s;%s;%s;%s", localaudio.AudioFingerprintVersion, localaudio.EmbeddedAudioFingerprintVersion, localaudio.IntegrityValidationVersion, runtimeID)
	if policy == IntegrityFull {
		return base
	}
	return base + ";integrity=" + string(policy)
}

func boundedAnalysisDetail(value string) string {
	const maximum = 4096
	if len(value) > maximum {
		return value[:maximum]
	}
	return value
}
