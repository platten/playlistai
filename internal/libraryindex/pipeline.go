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
	CLAP      bool
	Profile   SamplingProfile
	Integrity IntegrityPolicy
}

type IntegrityPolicy string

const (
	IntegrityFull     IntegrityPolicy = "full"
	IntegrityDeferred IntegrityPolicy = "deferred"
)

type AnalysisReport struct {
	MetadataCompleted     int64                 `json:"metadataCompleted"`
	AudioCompleted        int64                 `json:"audioCompleted"`
	CLAPCompleted         int64                 `json:"clapCompleted"`
	Failed                int64                 `json:"failed"`
	Retried               int64                 `json:"retried"`
	SkippedChanged        int64                 `json:"skippedChanged"`
	MERTReused            int64                 `json:"mertReused"`
	DSPReused             int64                 `json:"dspReused"`
	MERTCacheLoaded       int64                 `json:"mertCacheLoaded"`
	MERTOutages           int64                 `json:"mertOutages"`
	MERTDeferred          int64                 `json:"mertDeferred"`
	WindowsDecoded        int64                 `json:"windowsDecoded"`
	TracksBuffered        int64                 `json:"tracksBuffered"`
	WindowFallbacks       int64                 `json:"windowFallbacks"`
	Timings               AnalysisTimings       `json:"timings"`
	AdmissionWaits        AdmissionWaitCounters `json:"admissionWaits"`
	StageTrace            []StageTraceEvent     `json:"stageTrace,omitempty"`
	TraceDropped          uint64                `json:"traceDropped,omitempty"`
	NativeFailures        int64                 `json:"nativeFailures"`
	WorkerRestarts        int64                 `json:"workerRestarts"`
	HealthChecks          int64                 `json:"healthChecks"`
	WorkerRestartDuration time.Duration         `json:"workerRestartDuration"`
	HealthCheckDuration   time.Duration         `json:"healthCheckDuration"`
	MERTOutageDuration    time.Duration         `json:"mertOutageDuration"`
	TrackTimings          []TrackAnalysisTiming `json:"trackTimings,omitempty"`
}

type AnalysisTimings struct {
	CPUAdmission      time.Duration `json:"cpuAdmission"`
	SourceIOAdmission time.Duration `json:"sourceIoAdmission"`
	PCMAdmission      time.Duration `json:"pcmAdmission"`
	SourceRead        time.Duration `json:"sourceRead"`
	Probe             time.Duration `json:"probe"`
	Fingerprint       time.Duration `json:"fingerprint"`
	Integrity         time.Duration `json:"integrity"`
	Decode            time.Duration `json:"decode"`
	DSPSlotWait       time.Duration `json:"dspSlotWait"`
	DSP               time.Duration `json:"dsp"`
	Downmix           time.Duration `json:"downmix"`
	ResamplingKernel  time.Duration `json:"resamplingKernel"`
	MERTPreprocess    time.Duration `json:"mertPreprocess"`
	MERTWait          time.Duration `json:"mertWait"`
	WorkerPreprocess  time.Duration `json:"workerPreprocess"`
	IPC               time.Duration `json:"ipc"`
	ONNXExecution     time.Duration `json:"onnxExecution"`
	CUDAExecution     time.Duration `json:"cudaExecution"`
	MERTInference     time.Duration `json:"mertInference"`
	Commit            time.Duration `json:"commit"`
}

type TrackAnalysisTiming struct {
	FileID  string          `json:"fileId"`
	Windows int             `json:"windows"`
	Total   time.Duration   `json:"total"`
	Timings AnalysisTimings `json:"timings"`
}

type Analyzer struct {
	State      *State
	Runtime    *localaudio.Runtime
	MERT       *audio.MERTWorkerPool
	CLAP       *audio.WorkerPool
	CLAPDevice string
	Plan       ResourcePlan
	Admission  *Admission
	Profile    SamplingProfile
	Integrity  IntegrityPolicy
	OnFile     func(FileActivity)
	OnIssue    func(ProcessingIssue)
	// OnMERTHealth reports MERT outages and recoveries. Audio analysis is
	// paused between the two calls.
	OnMERTHealth func(healthy bool, err error)
	// StopAdmission closes on graceful shutdown. Dispatchers stop claiming new
	// jobs while workers drain every job already admitted to their queues.
	StopAdmission <-chan struct{}
	// FreezeManifest prevents a file changed after the scan/diff barrier from
	// being admitted again during this run. The next scan observes and queues it.
	FreezeManifest bool
	DiffEpoch      int64
	stageOnce      sync.Once
	dspSlots       chan struct{}
	reuseCache     *mertReuseCache
	mertHealth     *mertSupervisor
	timingMu       sync.Mutex
	timingReport   *AnalysisReport
	timingIndex    map[string]int
	Trace          *StageTrace
}

const (
	jobLeaseDuration   = 5 * time.Minute
	jobHeartbeatPeriod = time.Minute
	claimIdleMinimum   = 20 * time.Millisecond
	claimIdleMaximum   = 500 * time.Millisecond
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
	if options.Audio && a.MERT == nil {
		return report, errors.New("library indexer: MERT model is required for audio analysis")
	}
	if options.CLAP && a.CLAP == nil {
		return report, errors.New("library indexer: CLAP model is required for CLAP analysis")
	}
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
	if options.CLAP {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.runCLAP(ctx, discoveryDone, &report); err != nil {
				fatal <- err
				if !errors.Is(err, ErrShutdownRequested) {
					cancel(err)
				}
			}
		}()
	}
	wg.Wait()
	report.StageTrace, report.TraceDropped = a.Trace.Snapshot()
	report.AdmissionWaits = a.Admission.Stats()
	sort.Slice(report.TrackTimings, func(i, j int) bool { return report.TrackTimings[i].FileID < report.TrackTimings[j].FileID })
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
				err := a.processMetadata(workerCtx, job, report)
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

func (a *Analyzer) processMetadata(ctx context.Context, job Job, report *AnalysisReport) (processErr error) {
	trackStarted := time.Now()
	track := TrackAnalysisTiming{FileID: job.FileID}
	defer func() {
		track.Total = time.Since(trackStarted)
		a.recordTrackTiming(report, track)
	}()
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
	admissionStarted := time.Now()
	release, err := a.Admission.Acquire(ctx, Reservation{CPU: 1, SourceIO: 1, Memory: 64 << 20, Files: 4})
	admissionWait := time.Since(admissionStarted)
	track.Timings.CPUAdmission += admissionWait
	track.Timings.SourceIOAdmission += admissionWait
	if err != nil {
		return err
	}
	probeStarted := time.Now()
	probe, probeErr := a.Runtime.Probe(ctx, path)
	probeDuration := time.Since(probeStarted)
	track.Timings.Probe += probeDuration
	track.Timings.SourceRead += probeDuration
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
		var generateFingerprint bool
		var embeddedErr error
		fingerprintRecord, generateFingerprint, embeddedErr = fingerprintFromEmbeddedTags(probe.Metadata)
		fingerprintEstablishedIntegrity := false
		if embeddedErr != nil {
			a.emitIssue(NewProcessingIssue("metadata", file.RootAlias, file.RelativePath, "embedded_fingerprint_invalid", embeddedErr, false))
		}
		if generateFingerprint {
			admissionStarted = time.Now()
			release, err = a.Admission.Acquire(ctx, Reservation{CPU: 1, SourceIO: 1, Memory: 8 << 20, Files: 4})
			admissionWait = time.Since(admissionStarted)
			track.Timings.CPUAdmission += admissionWait
			track.Timings.SourceIOAdmission += admissionWait
			if err != nil {
				return err
			}
			fingerprintStarted := time.Now()
			fingerprint, fingerprintErr := a.Runtime.AudioFingerprint(ctx, probe)
			fingerprintDuration := time.Since(fingerprintStarted)
			track.Timings.Fingerprint += fingerprintDuration
			track.Timings.SourceRead += fingerprintDuration
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
			integrity, err = a.validateMetadataIntegrity(ctx, job, file, probe, &track.Timings)
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
	commitStarted := time.Now()
	commitErr := a.State.CommitJob(ctx, JobResult{Job: job, Contract: job.SemanticKey, Metadata: raw})
	track.Timings.Commit += time.Since(commitStarted)
	return commitErr
}

// fingerprintFromEmbeddedTags makes fingerprint generation an explicit
// missing-data operation. A valid embedded Chromaprint value is copied exactly;
// an AcoustID ID also suppresses generation, and an invalid embedded value is
// reported rather than silently replaced.
func fingerprintFromEmbeddedTags(metadata localaudio.Metadata) (FingerprintRecord, bool, error) {
	embedded, tagged, err := localaudio.EmbeddedAudioFingerprint(metadata)
	switch {
	case tagged && err == nil:
		return FingerprintRecord{Status: "available", Value: &embedded}, false, nil
	case tagged:
		return FingerprintRecord{Status: "invalid_embedded", Error: boundedAnalysisDetail(err.Error())}, false, err
	case metadata.AcoustID != nil && strings.TrimSpace(metadata.AcoustID.Value) != "":
		return FingerprintRecord{Status: "not_generated_embedded_acoustid"}, false, nil
	default:
		return FingerprintRecord{Status: "unavailable"}, true, nil
	}
}

func (a *Analyzer) validateMetadataIntegrity(ctx context.Context, job Job, file FileRecord, probe localaudio.ProbeResult, timings *AnalysisTimings) (IntegrityRecord, error) {
	admissionStarted := time.Now()
	release, err := a.Admission.Acquire(ctx, Reservation{CPU: 1, SourceIO: 1, Memory: 8 << 20, Files: 4})
	admissionWait := time.Since(admissionStarted)
	timings.CPUAdmission += admissionWait
	timings.SourceIOAdmission += admissionWait
	if err != nil {
		return IntegrityRecord{}, err
	}
	integrityStarted := time.Now()
	integrityErr := a.Runtime.ValidateIntegrity(ctx, probe)
	integrityDuration := time.Since(integrityStarted)
	timings.Integrity += integrityDuration
	timings.SourceRead += integrityDuration
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
	contract := AudioSemanticKey(a.Runtime.ID(), a.MERT.Identity(), profile)
	seed, err := a.State.LoadReusableMERT(ctx, contract)
	if err != nil {
		return err
	}
	a.reuseCache = newMERTReuseCache(seed)
	report.MERTCacheLoaded = int64(len(seed))
	a.mertHealth = newMERTSupervisor(workerCtx, a.probeMERT, a.reportMERTHealth)
	a.mertHealth.start()
	defer func() {
		a.mertHealth.stop()
		report.MERTOutages = a.mertHealth.outages.Load()
		report.MERTOutageDuration = a.mertHealth.outageDuration()
		stats := a.MERT.Diagnostics()
		report.NativeFailures, report.WorkerRestarts = stats.NativeFailures, stats.Restarts
		report.HealthChecks, report.HealthCheckDuration = stats.HealthChecks, stats.HealthDuration
		report.WorkerRestartDuration = stats.RestartDuration
	}()
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
				if errors.Is(err, errMERTUnavailable) {
					// The outage, not this track, stopped the work: requeue it
					// without spending one of its retries.
					atomic.AddInt64(&report.MERTDeferred, 1)
					if releaseErr := a.State.ReleaseJob(workerCtx, job); releaseErr != nil {
						cancel(releaseErr)
						return
					}
					continue
				}
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
	err = a.dispatchJobs(workerCtx, "audio", discoveryDone, jobs, leases)
	close(jobs)
	workers.Wait()
	if cause := context.Cause(workerCtx); cause != nil && !errors.Is(cause, context.Canceled) {
		return errors.Join(err, cause)
	}
	return err
}

func (a *Analyzer) dispatchJobs(ctx context.Context, kind string, discoveryDone <-chan struct{}, out chan<- Job, leases *jobLeaseTracker) error {
	discoveryComplete := false
	idle := claimIdleMinimum
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
		claimStarted := time.Now()
		if a.DiffEpoch > 0 {
			claimed, err = a.State.ClaimScanDiffJobs(ctx, a.DiffEpoch, kind, max(1, min(a.Plan.QueueDepth, 64)), jobLeaseDuration)
		} else {
			claimed, err = a.State.ClaimJobs(ctx, kind, max(1, min(a.Plan.QueueDepth, 64)), jobLeaseDuration)
		}
		a.trace("claim_"+kind, claimStarted, len(claimed))
		if err != nil {
			return err
		}
		if len(claimed) == 0 {
			// An empty claim that walked a long blocked range sleeps at least as
			// long as it ran, bounding its share of a read connection.
			claimElapsed := time.Since(claimStarted)
			if discoveryComplete {
				pending, leased, err := a.jobCounts(ctx, kind)
				if err != nil {
					return err
				}
				// Pending audio that cannot be claimed is blocked on metadata. Once
				// metadata is exhausted it can never become eligible, so fail it.
				// Checking pending first keeps the in-flight tail from rerunning
				// the finalization query on every poll.
				if (kind == "audio" || kind == "clap") && pending > 0 {
					metadataPending, metadataLeased, err := a.jobCounts(ctx, "metadata")
					if err != nil {
						return err
					}
					if metadataPending == 0 && metadataLeased == 0 {
						if err := a.finalizeBlockedKind(ctx, kind); err != nil {
							return err
						}
						if pending, leased, err = a.jobCounts(ctx, kind); err != nil {
							return err
						}
					}
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
			case <-time.After(max(idle, claimElapsed)):
			}
			// Empty polls back off so a stage waiting on its prerequisite or on
			// in-flight leases does not keep the state database busy.
			idle = min(2*idle, claimIdleMaximum)
			continue
		}
		idle = claimIdleMinimum
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

func (a *Analyzer) probeMERT(ctx context.Context, wait bool) (bool, error) {
	started := time.Now()
	defer func() { a.trace("health_check", started, 0) }()
	if wait {
		return true, a.MERT.ValidateAll(ctx)
	}
	worker, release, ok := a.MERT.TryAcquire()
	if !ok {
		return false, nil
	}
	defer release()
	return true, a.MERT.Validate(ctx, worker)
}

// acquireMERT rechecks availability after both independently blocking waits.
// Deferral never retains a worker or CPU reservation.
func (a *Analyzer) acquireMERT(ctx context.Context, timings *AnalysisTimings) (*audio.MERTWorker, func(), func(), error) {
	available := func(worker *audio.MERTWorker) bool {
		return (a.mertHealth == nil || a.mertHealth.healthy()) && (worker == nil || !a.MERT.Quarantined(worker))
	}
	if !available(nil) {
		return nil, nil, nil, errMERTUnavailable
	}
	started := time.Now()
	a.trace("session_queue", started, 0)
	worker, releaseWorker, err := a.MERT.Acquire(ctx)
	timings.MERTWait += time.Since(started)
	a.trace("session_wait", started, 0)
	if err != nil {
		return nil, nil, nil, err
	}
	if !available(worker) {
		releaseWorker()
		return nil, nil, nil, errMERTUnavailable
	}
	cpuUnits := max(1, a.Plan.InferenceThreads)
	if _, cuda := audio.MERTCUDADeviceIndex(a.MERT.Device()); cuda {
		cpuUnits = 1
	}
	started = time.Now()
	releaseCPU, err := a.Admission.Acquire(ctx, Reservation{CPU: cpuUnits})
	timings.CPUAdmission += time.Since(started)
	a.trace("inference_cpu_wait", started, 0)
	if err != nil {
		releaseWorker()
		return nil, nil, nil, err
	}
	release := func() { releaseCPU(); releaseWorker() }
	if !available(worker) {
		release()
		return nil, nil, nil, errMERTUnavailable
	}
	return worker, releaseCPU, release, nil
}

func (a *Analyzer) reportMERTHealth(healthy bool, err error) {
	if healthy {
		a.emitIssue(NewProcessingIssue("audio", "", "", "mert_recovered", nil, false))
	} else {
		a.emitIssue(NewProcessingIssue("audio", "", "", "mert_unavailable", fmt.Errorf("%s: %w", mertOutageIssueDetail, err), true))
	}
	if a.OnMERTHealth != nil {
		a.OnMERTHealth(healthy, err)
	}
}

func (a *Analyzer) finalizeBlockedKind(ctx context.Context, kind string) error {
	if a.DiffEpoch > 0 {
		return a.State.FinalizeBlockedScanDiffKind(ctx, a.DiffEpoch, kind)
	}
	return a.State.FinalizeBlockedKind(ctx, kind)
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

func (a *Analyzer) processAudio(ctx context.Context, job Job, profile SamplingProfile, report *AnalysisReport) (processErr error) {
	if a.mertHealth != nil {
		if err := a.mertHealth.wait(ctx, a.StopAdmission); err != nil {
			return err
		}
	}
	trackStarted := time.Now()
	track := TrackAnalysisTiming{FileID: job.FileID}
	defer func() {
		track.Total = time.Since(trackStarted)
		a.recordTrackTiming(report, track)
	}()
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
	reusable, reused, flight, err := a.claimReusableMERT(ctx, recordingKey, job.SemanticKey)
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

	processWindow := func(window localaudio.PCMWindow) error {
		pcm := audio.DecodedPCM{Samples: window.Samples, SampleRate: window.SampleRate, Channels: window.Channels}
		dspWork := func() (AnalysisTimings, error) {
			var timings AnalysisTimings
			if dspReused {
				return timings, nil
			}
			releaseCPUAndSlot, slotWait, cpuWait, err := a.acquireDSPSlot(ctx)
			timings.DSPSlotWait += slotWait
			timings.CPUAdmission += cpuWait
			if err != nil {
				return timings, err
			}
			defer releaseCPUAndSlot()
			started := time.Now()
			features, err := audio.MeasureLocalDSP(ctx, pcm)
			timings.DSP += time.Since(started)
			a.trace("dsp", started, 1)
			if err != nil {
				return timings, err
			}
			dsp.Windows = append(dsp.Windows, DSPWindowRecord{Index: window.Index, StartSeconds: window.RequestedStart.Seconds(), ObservedSeconds: window.ObservedDuration.Seconds(), SampleRate: window.SampleRate, Channels: window.Channels, Features: features})
			return timings, nil
		}
		mertWork := func() (AnalysisTimings, error) {
			var timings AnalysisTimings
			if reused {
				return timings, nil
			}
			cpuStarted := time.Now()
			releaseCPU, err := a.Admission.Acquire(ctx, Reservation{CPU: 1})
			timings.CPUAdmission += time.Since(cpuStarted)
			if err != nil {
				return timings, err
			}
			preprocessStarted := time.Now()
			resampled, metrics, err := audio.MERTResampleLocalWithMetrics(ctx, pcm)
			releaseCPU()
			timings.MERTPreprocess += time.Since(preprocessStarted)
			a.trace("mert_preprocess", preprocessStarted, 1)
			timings.Downmix += metrics.DownmixDuration
			timings.ResamplingKernel += metrics.ResampleDuration
			if err != nil {
				return timings, err
			}
			defer clear(resampled)
			if !metrics.HasSignal {
				return timings, errors.New("library indexer: silent or degenerate MERT window")
			}
			if len(resampled) < 400 {
				return timings, errors.New("library indexer: insufficient observed MERT samples")
			}

			a.Trace.Ready(1)
			worker, releaseInferenceCPU, releaseInference, err := a.acquireMERT(ctx, &timings)
			a.Trace.Ready(-1)
			if err != nil {
				return timings, err
			}
			defer releaseInference()
			_, cuda := audio.MERTCUDADeviceIndex(a.MERT.Device())
			a.Trace.Dispatch(worker)
			inferenceStarted := time.Now()
			vector, workerTimings, err := worker.EmbedAudioWithTimings(ctx, resampled)
			inferenceDuration := time.Since(inferenceStarted)
			releaseInferenceCPU()
			a.Trace.Finished(worker)
			a.trace("inference_request", inferenceStarted, 1)
			a.trace("onnx_execution", time.Now().Add(-workerTimings.Execution), 1)
			timings.MERTInference += inferenceDuration
			timings.WorkerPreprocess += workerTimings.Preprocessing
			timings.ONNXExecution += workerTimings.Execution
			ipc := inferenceDuration - workerTimings.Preprocessing - workerTimings.Execution
			if ipc > 0 {
				timings.IPC += ipc
			}
			if cuda {
				timings.CUDAExecution += workerTimings.Execution
			}
			if err != nil {
				// Health-check the same worker, which also restarts it. A healthy
				// restart leaves the failure with this track and its retries.
				if errors.Is(err, audio.ErrNativeWorker) {
					a.MERT.Quarantine(worker)
					if a.mertHealth != nil && a.mertHealth.confirmOutage(ctx, func(ctx context.Context) error { return a.MERT.Validate(ctx, worker) }) {
						return timings, errors.Join(errMERTUnavailable, err)
					}
				}
				return timings, err
			}
			if a.mertHealth != nil {
				a.mertHealth.succeeded()
			}
			defer clear(vector)
			if len(vector) != audio.MERTDimension {
				return timings, errors.New("library indexer: invalid MERT dimension")
			}
			if err := accumulateMERT(sums, vector, float64(len(resampled))); err != nil {
				return timings, err
			}

			ratio := metrics.DownmixCancellationRatio
			mert.Segments = append(mert.Segments, MERTSegmentRecord{Index: window.Index, StartSeconds: window.RequestedStart.Seconds(), ObservedSeconds: window.ObservedDuration.Seconds(), DownmixCancellationRatio: ratio, SevereDownmixCancellation: ratio < 0.01})
			return timings, nil
		}

		if a.Plan.Mode == ConcurrencySerial || a.Plan.HeavyWorkers < 2 {
			dspTimings, err := dspWork()
			mergeAnalysisTimings(&track.Timings, dspTimings)
			if err != nil {
				return err
			}
			if mertErr == nil {
				var mertTimings AnalysisTimings
				mertTimings, mertErr = mertWork()
				mergeAnalysisTimings(&track.Timings, mertTimings)
			}
			return nil
		}
		var dspTimings, mertTimings AnalysisTimings
		var dspErr, windowMERTErr error
		var branchWG sync.WaitGroup
		branchWG.Add(1)
		go func() {
			defer branchWG.Done()
			dspTimings, dspErr = dspWork()
		}()
		if mertErr == nil {
			branchWG.Add(1)
			go func() {
				defer branchWG.Done()
				mertTimings, windowMERTErr = mertWork()
			}()
		}
		branchWG.Wait()
		mergeAnalysisTimings(&track.Timings, dspTimings)
		mergeAnalysisTimings(&track.Timings, mertTimings)
		if mertErr == nil && windowMERTErr != nil {
			mertErr = windowMERTErr
		}
		return dspErr
	}

	reservations := make([]int64, len(decodeWindows))
	var totalPCM int64
	for i, requested := range decodeWindows {
		reserve, reserveErr := decodedPCMReservation(requested, stored.Probe.SelectedStream.SampleRate, stored.Probe.SelectedStream.Channels)
		if reserveErr != nil {
			return reserveErr
		}
		if totalPCM > math.MaxInt64-reserve {
			return errors.New("library indexer: track PCM reservation overflow")
		}
		reservations[i] = reserve
		totalPCM += reserve
	}
	if !reused || !dspReused {
		decodeResult, decodeErr := runDecodedWindows(ctx, a.Admission, a.Plan.BufferingMode, decodeWindows, reservations,
			func(ctx context.Context, requested localaudio.Window) (localaudio.PCMWindow, error) {
				started := time.Now()
				window, err := a.Runtime.DecodeWindow(ctx, probe, requested)
				a.trace("decode", started, 1)
				return window, err
			}, processWindow)
		mergeAnalysisTimings(&track.Timings, decodeResult.Timings)
		track.Windows += decodeResult.Windows
		atomic.AddInt64(&report.WindowsDecoded, int64(decodeResult.Windows))
		if decodeResult.TrackBuffered {
			atomic.AddInt64(&report.TracksBuffered, 1)
		}
		if decodeResult.WindowFallback {
			atomic.AddInt64(&report.WindowFallbacks, 1)
		}

		if decodeErr != nil {
			if a.FreezeManifest && errors.Is(decodeErr, localaudio.ErrSourceChanged) {
				return errors.Join(errSourceChangedAfterManifest, decodeErr)
			}
			return decodeErr
		}
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
		commitStarted := time.Now()
		err := a.State.CommitPartialDSP(ctx, job, job.SemanticKey, dspContract, dspRaw)
		track.Timings.Commit += time.Since(commitStarted)
		if err != nil {
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
			commitStarted := time.Now()
			commitErr := a.State.CommitPartialDSP(ctx, job, job.SemanticKey, dspContract, dspRaw)
			track.Timings.Commit += time.Since(commitStarted)
			if commitErr != nil {
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
	commitStarted := time.Now()
	commitErr := a.State.CommitJob(ctx, JobResult{Job: job, Contract: job.SemanticKey, DSP: dspRaw, DSPCacheContract: dspContract, Vector: vectorRaw, MERTData: mertRaw, Dimension: audio.MERTDimension})
	track.Timings.Commit += time.Since(commitStarted)
	if commitErr == nil && !reused && recordingKey != "" {
		a.reuseCache.store(recordingKey, job.SemanticKey, ReusableMERT{Vector: vectorRaw, Dimension: audio.MERTDimension})
	}
	return commitErr
}

func (a *Analyzer) acquireDSPSlot(ctx context.Context) (func(), time.Duration, time.Duration, error) {
	slotStarted := time.Now()
	select {
	case a.dspSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, time.Since(slotStarted), 0, ctx.Err()
	}
	slotWait := time.Since(slotStarted)
	cpuStarted := time.Now()
	releaseCPU, err := a.Admission.Acquire(ctx, Reservation{CPU: 1})
	cpuWait := time.Since(cpuStarted)
	if err != nil {
		<-a.dspSlots
		return nil, slotWait, cpuWait, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			releaseCPU()
			<-a.dspSlots
		})
	}, slotWait, cpuWait, nil
}

func decodedPCMReservation(window localaudio.Window, sampleRate, channels int) (int64, error) {
	reservation, err := localaudio.DecodedPCMReservationBytes(window, sampleRate, channels)
	if err != nil {
		return 0, fmt.Errorf("library indexer: PCM reservation: %w", err)
	}
	return reservation, nil
}

type decodedWindowsResult struct {
	Timings        AnalysisTimings
	Windows        int
	TrackBuffered  bool
	WindowFallback bool
}

func runDecodedWindows(ctx context.Context, admission *Admission, mode BufferingMode, windows []localaudio.Window, reservations []int64, decode func(context.Context, localaudio.Window) (localaudio.PCMWindow, error), process func(localaudio.PCMWindow) error) (decodedWindowsResult, error) {
	var result decodedWindowsResult
	if admission == nil || decode == nil || process == nil || len(windows) == 0 || len(windows) != len(reservations) {
		return result, errors.New("library indexer: decoded-window pipeline is not configured")
	}
	var totalPCM int64
	for _, reservation := range reservations {
		if reservation <= 0 || totalPCM > math.MaxInt64-reservation {
			return result, errors.New("library indexer: invalid track PCM reservation")
		}
		totalPCM += reservation
	}
	_, capacity := admission.Usage()
	bufferTrack := mode == BufferingTrack && totalPCM <= capacity.PCMBytes
	result.TrackBuffered = bufferTrack
	result.WindowFallback = mode == BufferingTrack && !bufferTrack
	if bufferTrack {
		acquireStarted := time.Now()
		lease, err := admission.AcquireLease(ctx, Reservation{CPU: 1, SourceIO: 1, Memory: 64 << 20, Files: 4, PCMBytes: totalPCM})
		waited := time.Since(acquireStarted)
		result.Timings.CPUAdmission += waited
		result.Timings.SourceIOAdmission += waited
		result.Timings.PCMAdmission += waited
		if err != nil {
			return result, err
		}
		decoded := make([]localaudio.PCMWindow, 0, len(windows))
		defer func() {
			for i := range decoded {
				clear(decoded[i].Samples)
			}
			lease.Release()
		}()
		for i, requested := range windows {
			decodeStarted := time.Now()
			window, decodeErr := decode(ctx, requested)
			decodeDuration := time.Since(decodeStarted)
			result.Timings.Decode += decodeDuration
			result.Timings.SourceRead += decodeDuration
			if decodeErr != nil {
				return result, decodeErr
			}
			if int64(len(window.Samples)) > reservations[i]/4 {
				clear(window.Samples)
				return result, errors.New("library indexer: decoder exceeded reserved PCM")
			}
			decoded = append(decoded, window)
			result.Windows++
		}
		if err := lease.ReleasePart(Reservation{CPU: 1, SourceIO: 1, Memory: 64 << 20, Files: 4}); err != nil {
			return result, err
		}
		for i := range decoded {
			processErr := process(decoded[i])
			clear(decoded[i].Samples)
			decoded[i].Samples = nil
			releaseErr := lease.ReleasePart(Reservation{PCMBytes: reservations[i]})
			if processErr != nil || releaseErr != nil {
				return result, errors.Join(processErr, releaseErr)
			}
		}
		return result, nil
	}
	for i, requested := range windows {
		acquireStarted := time.Now()
		lease, err := admission.AcquireLease(ctx, Reservation{CPU: 1, SourceIO: 1, Memory: 64 << 20, Files: 4, PCMBytes: reservations[i]})
		waited := time.Since(acquireStarted)
		result.Timings.CPUAdmission += waited
		result.Timings.SourceIOAdmission += waited
		result.Timings.PCMAdmission += waited
		if err != nil {
			return result, err
		}
		decodeStarted := time.Now()
		window, decodeErr := decode(ctx, requested)
		decodeDuration := time.Since(decodeStarted)
		result.Timings.Decode += decodeDuration
		result.Timings.SourceRead += decodeDuration
		if releaseErr := lease.ReleasePart(Reservation{CPU: 1, SourceIO: 1, Memory: 64 << 20, Files: 4}); decodeErr == nil {
			decodeErr = releaseErr
		}
		if decodeErr != nil {
			clear(window.Samples)
			lease.Release()
			return result, decodeErr
		}
		if int64(len(window.Samples)) > reservations[i]/4 {
			clear(window.Samples)
			lease.Release()
			return result, errors.New("library indexer: decoder exceeded reserved PCM")
		}
		result.Windows++
		processErr := process(window)
		clear(window.Samples)
		releaseErr := lease.ReleasePart(Reservation{PCMBytes: reservations[i]})
		lease.Release()
		if processErr != nil || releaseErr != nil {
			return result, errors.Join(processErr, releaseErr)
		}
	}
	return result, nil
}

func mergeAnalysisTimings(target *AnalysisTimings, value AnalysisTimings) {
	target.CPUAdmission += value.CPUAdmission
	target.SourceIOAdmission += value.SourceIOAdmission
	target.PCMAdmission += value.PCMAdmission
	target.SourceRead += value.SourceRead
	target.Probe += value.Probe
	target.Fingerprint += value.Fingerprint
	target.Integrity += value.Integrity
	target.Decode += value.Decode
	target.DSPSlotWait += value.DSPSlotWait
	target.DSP += value.DSP
	target.Downmix += value.Downmix
	target.ResamplingKernel += value.ResamplingKernel
	target.MERTPreprocess += value.MERTPreprocess
	target.MERTWait += value.MERTWait
	target.WorkerPreprocess += value.WorkerPreprocess
	target.IPC += value.IPC
	target.ONNXExecution += value.ONNXExecution
	target.CUDAExecution += value.CUDAExecution
	target.MERTInference += value.MERTInference
	target.Commit += value.Commit
}

func (a *Analyzer) recordTrackTiming(report *AnalysisReport, track TrackAnalysisTiming) {
	a.timingMu.Lock()
	defer a.timingMu.Unlock()
	if a.timingReport != report {
		a.timingReport = report
		a.timingIndex = make(map[string]int, len(report.TrackTimings))
		for i := range report.TrackTimings {
			a.timingIndex[report.TrackTimings[i].FileID] = i
		}
	}
	if i, ok := a.timingIndex[track.FileID]; ok {
		report.TrackTimings[i].Windows += track.Windows
		report.TrackTimings[i].Total += track.Total
		mergeAnalysisTimings(&report.TrackTimings[i].Timings, track.Timings)
		mergeAnalysisTimings(&report.Timings, track.Timings)
		return
	}
	a.timingIndex[track.FileID] = len(report.TrackTimings)
	report.TrackTimings = append(report.TrackTimings, track)
	mergeAnalysisTimings(&report.Timings, track.Timings)
}

func (a *Analyzer) claimReusableMERT(ctx context.Context, recordingKey, contract string) (ReusableMERT, bool, *reuseFlight, error) {
	if a.reuseCache == nil {
		return ReusableMERT{}, false, nil, errors.New("library indexer: MERT reuse cache is not initialized")
	}
	return a.reuseCache.claim(ctx, recordingKey, contract)
}

func (a *Analyzer) finishReuseFlight(recordingKey, contract string, flight *reuseFlight) {
	if a.reuseCache != nil {
		a.reuseCache.finish(recordingKey, contract, flight)
	}
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
	return audio.LocalDSPVersion + ";" + audio.MERTLocalPreprocessingVersion + ";" + audio.MERTPoolingVersion + ";" + runtimeID + ";" + SamplingVersion + ";" + string(profile) + ";" + model.WeightsSHA256
}

// Validate the entire window before changing the pooled result. A malformed
// worker response must not leave a prefix of its vector in the accumulator.
func accumulateMERT(sums []float64, vector []float32, weight float64) error {
	if len(sums) != len(vector) || len(vector) == 0 || weight <= 0 || math.IsNaN(weight) || math.IsInf(weight, 0) {
		return errors.New("library indexer: invalid MERT pooling input")
	}
	for i, value := range vector {
		next := sums[i] + float64(value)*weight
		if math.IsNaN(next) || math.IsInf(next, 0) {
			return errors.New("library indexer: nonfinite MERT output")
		}
	}
	for i, value := range vector {
		sums[i] += float64(value) * weight
	}
	return nil
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
