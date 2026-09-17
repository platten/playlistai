// Package libraryindex implements the headless local-library analyzer and its
// portable catalog format. It deliberately has no dependency on Wails or the
// desktop application composition root.
package libraryindex

import (
	"errors"
	"fmt"
	"runtime"
	"time"
)

type ConcurrencyMode string

const (
	ConcurrencyAuto   ConcurrencyMode = "auto"
	ConcurrencyManual ConcurrencyMode = "manual"
	ConcurrencySerial ConcurrencyMode = "serial"
)

type IOProfile string

const (
	IOAuto IOProfile = "auto"
	IOHDD  IOProfile = "hdd"
	IONAS  IOProfile = "nas"
	IOSSD  IOProfile = "ssd"
)

// ResourceOverrides contains user/config values. Zero means that automatic
// planning supplies the value. Only WorkersAuto distinguishes an explicitly
// supplied literal "auto" from an omitted value.
type ResourceOverrides struct {
	Mode             ConcurrencyMode
	Workers          int
	WorkersAuto      bool
	ScanWorkers      int
	MetadataWorkers  int
	DecodeWorkers    int
	DSPWorkers       int
	InferenceWorkers int
	InferenceThreads int
	FitWorkers       int
	IndexWorkers     int
	IOWorkers        int
	IOProfile        IOProfile
	QueueDepth       int
	MaxRAM           int64
	MaxOpenFiles     int
	ShutdownTimeout  time.Duration
}

// ResourcePlan is the complete allocation exposed in status and persisted in
// run diagnostics. Stage values are ceilings sharing HeavyWorkers; they never
// multiply the global CPU admission budget.
type ResourcePlan struct {
	Mode                  ConcurrencyMode `json:"mode"`
	EffectiveCPUSlots     int             `json:"effectiveCpuSlots"`
	CPUQuota              float64         `json:"cpuQuota,omitempty"`
	CPUQuotaSource        string          `json:"cpuQuotaSource"`
	HeavyWorkers          int             `json:"workers"`
	ScanWorkers           int             `json:"scanWorkers"`
	MetadataWorkers       int             `json:"metadataWorkers"`
	DecodeWorkers         int             `json:"decodeWorkers"`
	DSPWorkers            int             `json:"dspWorkers"`
	InferenceWorkers      int             `json:"inferenceWorkers"`
	InferenceThreads      int             `json:"inferenceThreads"`
	FitWorkers            int             `json:"fitWorkers"`
	IndexWorkers          int             `json:"indexWorkers"`
	IOWorkers             int             `json:"ioWorkers"`
	IOProfile             IOProfile       `json:"ioProfile"`
	QueueDepth            int             `json:"queueDepth"`
	MaxRAM                int64           `json:"maxRamBytes"`
	MaxOpenFiles          int             `json:"maxOpenFiles"`
	ShutdownTimeout       time.Duration   `json:"shutdownTimeout"`
	EstimatedSessionBytes int64           `json:"estimatedSessionBytes"`
	ResidentMERTBytes     int64           `json:"residentMertBytes,omitempty"`
}

type hostCapacity struct {
	CPUSlots     int
	CPUQuota     float64
	CPUSource    string
	AvailableRAM int64
	OpenFileSoft int
	GOMAXPROCS   int
}

const (
	defaultQueueDepth       = 16
	defaultShutdownTimeout  = 30 * time.Second
	minimumOperationBytes   = 256 << 20
	defaultMERTSessionBytes = 768 << 20
	openFileHeadroom        = 64
)

func ResolveResourcePlan(over ResourceOverrides) (ResourcePlan, error) {
	return resolveResourcePlan(over, detectHostCapacity())
}

func resolveResourcePlan(over ResourceOverrides, host hostCapacity) (ResourcePlan, error) {
	return resolveResourcePlanFor(over, host, true)
}

func ResolveMetadataResourcePlan(over ResourceOverrides) (ResourcePlan, error) {
	if over.InferenceWorkers > 0 || over.InferenceThreads > 0 {
		return ResourcePlan{}, errors.New("library indexer: inference overrides are invalid for metadata-only analysis")
	}
	return resolveResourcePlanFor(over, detectHostCapacity(), false)
}

func resolveResourcePlanFor(over ResourceOverrides, host hostCapacity, reserveInference bool) (ResourcePlan, error) {
	if over.Mode == "" {
		over.Mode = ConcurrencyAuto
	}
	if over.IOProfile == "" {
		over.IOProfile = IOAuto
	}
	if over.Mode != ConcurrencyAuto && over.Mode != ConcurrencyManual && over.Mode != ConcurrencySerial {
		return ResourcePlan{}, fmt.Errorf("library indexer: invalid concurrency mode %q", over.Mode)
	}
	if over.IOProfile != IOAuto && over.IOProfile != IOHDD && over.IOProfile != IONAS && over.IOProfile != IOSSD {
		return ResourcePlan{}, fmt.Errorf("library indexer: invalid I/O profile %q", over.IOProfile)
	}
	for name, value := range map[string]int{
		"workers": over.Workers, "scan-workers": over.ScanWorkers, "metadata-workers": over.MetadataWorkers,
		"decode-workers": over.DecodeWorkers, "dsp-workers": over.DSPWorkers, "inference-workers": over.InferenceWorkers,
		"inference-threads": over.InferenceThreads, "fit-workers": over.FitWorkers, "index-workers": over.IndexWorkers,
		"io-workers": over.IOWorkers, "queue-depth": over.QueueDepth, "max-open-files": over.MaxOpenFiles,
	} {
		if value < 0 {
			return ResourcePlan{}, fmt.Errorf("library indexer: --%s must be positive", name)
		}
	}
	if over.MaxRAM < 0 || over.ShutdownTimeout < 0 {
		return ResourcePlan{}, errors.New("library indexer: memory and duration limits must be positive")
	}
	if host.CPUSlots < 1 {
		host.CPUSlots = max(1, host.GOMAXPROCS)
	}
	if host.AvailableRAM <= 0 {
		host.AvailableRAM = 2 << 30
	}
	if host.OpenFileSoft <= 0 {
		host.OpenFileSoft = 1024
	}

	heavy := host.CPUSlots
	if heavy >= 4 {
		heavy-- // leave one effective slot for writer/control/UI inspection
	}
	if over.Workers > 0 {
		heavy = min(over.Workers, host.CPUSlots)
	}
	if over.Mode == ConcurrencySerial {
		heavy = 1
		if serialConflict(over) != "" {
			return ResourcePlan{}, fmt.Errorf("library indexer: serial mode conflicts with --%s", serialConflict(over))
		}
	}
	heavy = max(1, heavy)
	maxRAM := over.MaxRAM
	if maxRAM == 0 {
		// Admission uses no more than 75% of currently available/cgroup memory.
		maxRAM = host.AvailableRAM * 3 / 4
	} else if host.AvailableRAM > 0 && maxRAM > host.AvailableRAM {
		return ResourcePlan{}, fmt.Errorf("library indexer: max RAM %d exceeds currently available/cgroup memory %d", maxRAM, host.AvailableRAM)
	}
	if maxRAM < minimumOperationBytes {
		return ResourcePlan{}, fmt.Errorf("library indexer: max RAM %d is below the minimum operation reservation %d", maxRAM, minimumOperationBytes)
	}
	maxOpen := over.MaxOpenFiles
	if maxOpen == 0 {
		maxOpen = min(512, max(32, host.OpenFileSoft-openFileHeadroom))
	}
	if maxOpen+openFileHeadroom > host.OpenFileSoft {
		return ResourcePlan{}, fmt.Errorf("library indexer: max open files %d leaves no descriptor headroom under soft limit %d", maxOpen, host.OpenFileSoft)
	}
	queueDepth := over.QueueDepth
	if queueDepth == 0 {
		queueDepth = defaultQueueDepth
	}
	shutdown := over.ShutdownTimeout
	if shutdown == 0 {
		shutdown = defaultShutdownTimeout
	}
	ioWorkers := over.IOWorkers
	if ioWorkers == 0 {
		ioWorkers = 2
		if over.IOProfile == IOSSD {
			ioWorkers = min(4, heavy)
		}
	}
	ioWorkers = max(1, ioWorkers)
	stageDefault := max(1, min(2, heavy))
	choose := func(v, fallback int) int {
		if v > 0 {
			return v
		}
		return fallback
	}
	inferenceWorkers := over.InferenceWorkers
	if !reserveInference {
		inferenceWorkers = 0
	} else if inferenceWorkers == 0 {
		inferenceWorkers = 1
		if heavy >= 4 && maxRAM >= 2*defaultMERTSessionBytes+minimumOperationBytes {
			inferenceWorkers = 2
		}
	}
	inferenceThreads := over.InferenceThreads
	if !reserveInference {
		inferenceThreads = 0
	} else if inferenceThreads == 0 {
		inferenceThreads = max(1, heavy/(inferenceWorkers+1))
	}
	if over.Mode == ConcurrencySerial {
		ioWorkers = 1
		if reserveInference {
			inferenceWorkers, inferenceThreads = 1, 1
		}
		stageDefault = 1
	}
	if reserveInference && (inferenceThreads > heavy || inferenceWorkers*inferenceThreads > heavy) {
		return ResourcePlan{}, fmt.Errorf("library indexer: inference reservation %d workers x %d threads exceeds global CPU budget %d", inferenceWorkers, inferenceThreads, heavy)
	}
	if reserveInference && int64(inferenceWorkers)*defaultMERTSessionBytes+minimumOperationBytes > maxRAM {
		return ResourcePlan{}, fmt.Errorf("library indexer: %d inference workers cannot fit the %d-byte admission target", inferenceWorkers, maxRAM)
	}
	plan := ResourcePlan{
		Mode: over.Mode, EffectiveCPUSlots: host.CPUSlots, CPUQuota: host.CPUQuota, CPUQuotaSource: host.CPUSource,
		HeavyWorkers: heavy, ScanWorkers: choose(over.ScanWorkers, min(2, ioWorkers)),
		MetadataWorkers: choose(over.MetadataWorkers, stageDefault), DecodeWorkers: choose(over.DecodeWorkers, stageDefault),
		DSPWorkers: choose(over.DSPWorkers, stageDefault), InferenceWorkers: inferenceWorkers,
		InferenceThreads: inferenceThreads, FitWorkers: choose(over.FitWorkers, heavy), IndexWorkers: choose(over.IndexWorkers, heavy),
		IOWorkers: ioWorkers, IOProfile: over.IOProfile, QueueDepth: queueDepth, MaxRAM: maxRAM,
		MaxOpenFiles: maxOpen, ShutdownTimeout: shutdown, EstimatedSessionBytes: defaultMERTSessionBytes,
	}
	return plan, nil
}

func serialConflict(over ResourceOverrides) string {
	checks := []struct {
		name string
		v    int
	}{{"workers", over.Workers}, {"scan-workers", over.ScanWorkers}, {"metadata-workers", over.MetadataWorkers},
		{"decode-workers", over.DecodeWorkers}, {"dsp-workers", over.DSPWorkers}, {"inference-workers", over.InferenceWorkers},
		{"inference-threads", over.InferenceThreads}, {"fit-workers", over.FitWorkers}, {"index-workers", over.IndexWorkers}, {"io-workers", over.IOWorkers}}
	for _, check := range checks {
		if check.v > 1 {
			return check.name
		}
	}
	return ""
}

func (p ResourcePlan) ValidateForAnalysis(metadataOnly bool) error {
	if metadataOnly {
		return nil
	}
	if p.InferenceWorkers < 1 || p.InferenceThreads < 1 {
		return errors.New("library indexer: requested audio analysis has no inference capacity")
	}
	return nil
}

func (p ResourcePlan) Summary() string {
	return fmt.Sprintf("mode=%s cpu=%d workers=%d io=%d(%s) scan=%d metadata=%d decode=%d dsp=%d inference=%dx%d fit=%d index=%d queue=%d ram=%d open=%d",
		p.Mode, p.EffectiveCPUSlots, p.HeavyWorkers, p.IOWorkers, p.IOProfile, p.ScanWorkers, p.MetadataWorkers,
		p.DecodeWorkers, p.DSPWorkers, p.InferenceWorkers, p.InferenceThreads, p.FitWorkers, p.IndexWorkers,
		p.QueueDepth, p.MaxRAM, p.MaxOpenFiles)
}

func runtimeCPUSlots() int { return max(1, runtime.GOMAXPROCS(0)) }
