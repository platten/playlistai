package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/indexerbundle"
	"github.com/platten/playlistai/internal/libraryindex"
	"github.com/platten/playlistai/internal/localaudio"
	"github.com/platten/playlistai/internal/modelpack"
)

const usage = `usage: playlist-indexer <command> [options]

commands:
  run       scan, analyze, drain durable commits, then optionally export
  scan      update the durable file inventory
  analyze   process pending metadata/audio jobs
  status    inspect state without taking the coordinator lock
  doctor    validate resources, codec runtime, state, and model
  model     setup or import the separately licensed MERT model
  fit       fit deterministic unsupervised library resources
  neighbors query a fitted local library index
  export    publish an atomic .paipack
  bench     run opt-in isolated local benchmarks
`

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("empty value")
	}
	*s = append(*s, value)
	return nil
}

type commonFlags struct {
	config, state, runtimeDir, concurrency, workers, ioProfile, maxRAM, shutdown string
	jsonOutput, offline, acceptModel, retryFailed, noProgress                    bool
	followDirectorySymlinks                                                      bool
	seed                                                                         int64
	scanWorkers, metadataWorkers, decodeWorkers, dspWorkers                      int
	inferenceWorkers, inferenceThreads, fitWorkers, indexWorkers                 int
	ioWorkers, queueDepth, maxOpenFiles                                          int
}

func addCommon(flags *flag.FlagSet, values *commonFlags) {
	if values.state == "" {
		values.state = defaultStateDir()
	}
	if values.concurrency == "" {
		values.concurrency = "auto"
	}
	if values.workers == "" {
		values.workers = "auto"
	}
	if values.ioProfile == "" {
		values.ioProfile = "auto"
	}
	if values.seed == 0 {
		values.seed = 42
	}
	flags.StringVar(&values.config, "config", values.config, "optional JSON configuration; CLI flags take precedence")
	flags.StringVar(&values.state, "state", values.state, "local state directory")
	flags.StringVar(&values.runtimeDir, "runtime-dir", values.runtimeDir, "absolute verified codec runtime directory")
	flags.StringVar(&values.concurrency, "concurrency", values.concurrency, "auto, manual, or serial")
	flags.StringVar(&values.workers, "workers", values.workers, "global CPU-heavy budget: auto or positive integer")
	flags.IntVar(&values.scanWorkers, "scan-workers", values.scanWorkers, "directory enumeration ceiling")
	flags.IntVar(&values.metadataWorkers, "metadata-workers", values.metadataWorkers, "probe/metadata ceiling")
	flags.IntVar(&values.decodeWorkers, "decode-workers", values.decodeWorkers, "local decode ceiling")
	flags.IntVar(&values.dspWorkers, "dsp-workers", values.dspWorkers, "DSP ceiling")
	flags.IntVar(&values.inferenceWorkers, "inference-workers", values.inferenceWorkers, "warm MERT session ceiling")
	flags.IntVar(&values.inferenceThreads, "inference-threads", values.inferenceThreads, "native intra-operation threads per MERT session")
	flags.IntVar(&values.fitWorkers, "fit-workers", values.fitWorkers, "learning CPU-task ceiling")
	flags.IntVar(&values.indexWorkers, "index-workers", values.indexWorkers, "index CPU-task ceiling")
	flags.IntVar(&values.ioWorkers, "io-workers", values.ioWorkers, "aggregate source-tree I/O ceiling")
	flags.StringVar(&values.ioProfile, "io-profile", values.ioProfile, "auto, hdd, nas, or ssd")
	flags.IntVar(&values.queueDepth, "queue-depth", values.queueDepth, "small descriptor queue depth")
	flags.StringVar(&values.maxRAM, "max-ram", values.maxRAM, "admission target, e.g. 8GiB")
	flags.IntVar(&values.maxOpenFiles, "max-open-files", values.maxOpenFiles, "owned source/runtime descriptor ceiling")
	flags.StringVar(&values.shutdown, "shutdown-timeout", values.shutdown, "graceful drain duration")
	flags.Int64Var(&values.seed, "seed", values.seed, "deterministic learning seed")
	flags.BoolVar(&values.jsonOutput, "json", false, "machine-readable stdout")
	flags.BoolVar(&values.offline, "offline", false, "forbid network asset setup")
	flags.BoolVar(&values.acceptModel, "accept-model-license", false, "accept the MERT CC-BY-NC-4.0 license")
	flags.BoolVar(&values.retryFailed, "retry-failed", false, "requeue bounded recoverable failures")
	flags.BoolVar(&values.noProgress, "no-progress", false, "disable the interactive PTerm progress bar")
	flags.BoolVar(&values.followDirectorySymlinks, "follow-directory-symlinks", values.followDirectorySymlinks, "follow directory symlinks encountered below a source root")
}

type commonConfig struct {
	State                   string `json:"state"`
	RuntimeDir              string `json:"runtimeDir"`
	Concurrency             string `json:"concurrency"`
	Workers                 string `json:"workers"`
	IOProfile               string `json:"ioProfile"`
	MaxRAM                  string `json:"maxRam"`
	Shutdown                string `json:"shutdownTimeout"`
	ScanWorkers             int    `json:"scanWorkers"`
	MetadataWorkers         int    `json:"metadataWorkers"`
	DecodeWorkers           int    `json:"decodeWorkers"`
	DSPWorkers              int    `json:"dspWorkers"`
	InferenceWorkers        int    `json:"inferenceWorkers"`
	InferenceThreads        int    `json:"inferenceThreads"`
	FitWorkers              int    `json:"fitWorkers"`
	IndexWorkers            int    `json:"indexWorkers"`
	IOWorkers               int    `json:"ioWorkers"`
	QueueDepth              int    `json:"queueDepth"`
	MaxOpenFiles            int    `json:"maxOpenFiles"`
	Seed                    int64  `json:"seed"`
	FollowDirectorySymlinks *bool  `json:"followDirectorySymlinks"`
}

func (c *commonFlags) preloadConfig(args []string) error {
	path := ""
	for i, arg := range args {
		if arg == "--config" && i+1 < len(args) {
			path = args[i+1]
		}
		if strings.HasPrefix(arg, "--config=") {
			path = strings.TrimPrefix(arg, "--config=")
		}
	}
	if path == "" {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var cfg commonConfig
	if err := decoder.Decode(&cfg); err != nil {
		return fmt.Errorf("indexer config: %w", err)
	}
	c.config, c.state, c.runtimeDir, c.concurrency, c.workers, c.ioProfile, c.maxRAM, c.shutdown = path, cfg.State, cfg.RuntimeDir, cfg.Concurrency, cfg.Workers, cfg.IOProfile, cfg.MaxRAM, cfg.Shutdown
	c.scanWorkers, c.metadataWorkers, c.decodeWorkers, c.dspWorkers = cfg.ScanWorkers, cfg.MetadataWorkers, cfg.DecodeWorkers, cfg.DSPWorkers
	c.inferenceWorkers, c.inferenceThreads, c.fitWorkers, c.indexWorkers = cfg.InferenceWorkers, cfg.InferenceThreads, cfg.FitWorkers, cfg.IndexWorkers
	c.ioWorkers, c.queueDepth, c.maxOpenFiles, c.seed = cfg.IOWorkers, cfg.QueueDepth, cfg.MaxOpenFiles, cfg.Seed
	if cfg.FollowDirectorySymlinks != nil {
		c.followDirectorySymlinks = *cfg.FollowDirectorySymlinks
	}
	return nil
}

func (c commonFlags) plan() (libraryindex.ResourcePlan, error) {
	return c.resolvePlan(false)
}

func (c commonFlags) metadataPlan() (libraryindex.ResourcePlan, error) {
	return c.resolvePlan(true)
}

func (c commonFlags) resolvePlan(metadataOnly bool) (libraryindex.ResourcePlan, error) {
	mode := libraryindex.ConcurrencyMode(strings.ToLower(c.concurrency))
	ioProfile := libraryindex.IOProfile(strings.ToLower(c.ioProfile))
	over := libraryindex.ResourceOverrides{Mode: mode, IOProfile: ioProfile, ScanWorkers: c.scanWorkers, MetadataWorkers: c.metadataWorkers,
		DecodeWorkers: c.decodeWorkers, DSPWorkers: c.dspWorkers, InferenceWorkers: c.inferenceWorkers, InferenceThreads: c.inferenceThreads,
		FitWorkers: c.fitWorkers, IndexWorkers: c.indexWorkers, IOWorkers: c.ioWorkers, QueueDepth: c.queueDepth, MaxOpenFiles: c.maxOpenFiles}
	if c.workers == "" || strings.EqualFold(c.workers, "auto") {
		over.WorkersAuto = true
	} else {
		value, err := strconv.Atoi(c.workers)
		if err != nil || value <= 0 {
			return libraryindex.ResourcePlan{}, errors.New("--workers accepts only auto or a positive integer")
		}
		over.Workers = value
	}
	var err error
	if c.maxRAM != "" {
		over.MaxRAM, err = parseSize(c.maxRAM)
		if err != nil {
			return libraryindex.ResourcePlan{}, err
		}
	}
	if c.shutdown != "" {
		over.ShutdownTimeout, err = time.ParseDuration(c.shutdown)
		if err != nil || over.ShutdownTimeout <= 0 {
			return libraryindex.ResourcePlan{}, errors.New("--shutdown-timeout requires a positive Go duration")
		}
	}
	if metadataOnly {
		plan, err := libraryindex.ResolveMetadataResourcePlan(over)
		if err == nil {
			resolvedShutdownNanos.Store(int64(plan.ShutdownTimeout))
		}
		return plan, err
	}
	plan, err := libraryindex.ResolveResourcePlan(over)
	if err == nil {
		resolvedShutdownNanos.Store(int64(plan.ShutdownTimeout))
	}
	return plan, err
}

func execute(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 1, errors.New("command is required")
	}
	switch args[0] {
	case "run", "scan", "analyze":
		return runPipelineCommand(ctx, args[0], args[1:], stdout, stderr)
	case "status":
		return runStatus(ctx, args[1:], stdout)
	case "doctor":
		return runDoctor(ctx, args[1:], stdout)
	case "model":
		return runModel(ctx, args[1:], stdout, stderr)
	case "fit", "neighbors", "export", "bench":
		return runLearningCommand(ctx, args, stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0, nil
	default:
		return 1, fmt.Errorf("unknown command %q", args[0])
	}
}

func runPipelineCommand(ctx context.Context, command string, args []string, stdout, stderr io.Writer) (code int, runErr error) {
	flags := flag.NewFlagSet("playlist-indexer "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	common := commonFlags{followDirectorySymlinks: true}
	if err := common.preloadConfig(args); err != nil {
		return 1, err
	}
	addCommon(flags, &common)
	var roots, rootAliases, appendRoots, exclusions stringList
	flags.Var(&roots, "root", "source root; repeatable")
	flags.Var(&rootAliases, "root-alias", "stable logical root as ALIAS=PATH; repeatable")
	flags.Var(&appendRoots, "append-root", "additional stable source root as ALIAS=PATH; repeatable")
	flags.Var(&exclusions, "exclude", "root-relative excluded subtree; repeatable")
	profile := flags.String("profile", "balanced", "fast, balanced, or deep")
	analysis := flags.String("analysis", "audio", "metadata or audio")
	device := flags.String("device", "cpu", "cpu or auto")
	modelBundle := flags.String("model-bundle", "", "verified local prepared MERT bundle")
	outPath := flags.String("out", "", "portable .paipack output; run only")
	trainingSample := flags.Int("training-sample", 0, "maximum deterministic MERT training sample")
	clusters := flags.Int("clusters", 0, "spherical cluster count; zero uses the recorded heuristic")
	refit := flags.Bool("refit", false, "force a new compatible learning generation")
	if err := flags.Parse(args); err != nil {
		return 1, err
	}
	if flags.NArg() != 0 {
		return 1, errors.New("unexpected positional arguments")
	}
	if *device != "cpu" && *device != "auto" {
		return 1, errors.New("only the validated CPU backend is available")
	}
	metadataOnly := *analysis == "metadata"
	if !metadataOnly && *analysis != "audio" {
		return 1, errors.New("--analysis must be metadata or audio")
	}
	plan, err := common.resolvePlan(metadataOnly)
	if err != nil {
		return 1, err
	}
	if err := plan.ValidateForAnalysis(metadataOnly); err != nil {
		return 1, err
	}
	if command != "analyze" && len(roots)+len(rootAliases)+len(appendRoots) == 0 {
		return 1, errors.New("at least one --root, --root-alias, or --append-root is required")
	}
	if gracefulStopRequested(ctx) {
		return 130, libraryindex.ErrShutdownRequested
	}
	codec, err := resolveCodec(ctx, common)
	if err != nil {
		return 1, err
	}
	state, err := libraryindex.OpenState(ctx, common.state, command, max(2, plan.HeavyWorkers))
	if err != nil {
		return 1, err
	}
	defer func() {
		if closeErr := state.Close(); closeErr != nil {
			code, runErr = 1, fmt.Errorf("close durable state: %w", closeErr)
		}
	}()
	issues, err := openIssueRecorder(filepath.Dir(state.Path()))
	if err != nil {
		return 1, fmt.Errorf("open state issue log: %w", err)
	}
	defer func() {
		if runErr != nil && !errors.Is(runErr, libraryindex.ErrShutdownRequested) && !errors.Is(runErr, context.Canceled) {
			issues.Record(libraryindex.NewProcessingIssue("run", "", "", "fatal", runErr, false))
		}
		if closeErr := issues.Close(); closeErr != nil && runErr == nil {
			code, runErr = 1, fmt.Errorf("close state issue log: %w", closeErr)
		}
	}()
	if common.retryFailed {
		requeued, retryErr := state.RetryFailed(ctx)
		if retryErr != nil {
			return 1, retryErr
		}
		fmt.Fprintf(stderr, "requeued failed jobs: %d\n", requeued)
	}
	var pool *audio.MERTWorkerPool
	if !metadataOnly {
		bundleDir, manifest, err := ensureModel(ctx, common, *modelBundle, stderr)
		if err != nil {
			return 1, err
		}
		executable, err := os.Executable()
		if err != nil {
			return 1, err
		}
		warmStart := time.Now()
		pool, plan, err = warmMERTPool(ctx, executable, bundleDir, manifest, plan, stderr, issues.Record)
		if err != nil {
			return 1, fmt.Errorf("warm MERT workers: %w", err)
		}
		defer pool.Close()
		fmt.Fprintf(stderr, "MERT sessions warmed: count=%d threads=%d rss=%d elapsed=%s\n", pool.Parallelism(), plan.InferenceThreads, pool.ResidentBytes(), time.Since(warmStart).Round(time.Millisecond))
	}
	if gracefulStopRequested(ctx) {
		return 130, libraryindex.ErrShutdownRequested
	}
	fmt.Fprintln(stderr, "effective resources:", plan.Summary())
	profileValue := libraryindex.SamplingProfile(*profile)
	semanticJobs := map[string]string{"metadata": libraryindex.MetadataSemanticKey(codec.ID())}
	if !metadataOnly {
		semanticJobs["audio"] = libraryindex.AudioSemanticKey(codec.ID(), pool.Identity(), profileValue)
	}
	analysisOptions := libraryindex.AnalysisOptions{Metadata: true, Audio: !metadataOnly, Profile: profileValue}
	analyzer := &libraryindex.Analyzer{State: state, Runtime: codec, MERT: pool, Plan: plan, Admission: libraryindex.NewAdmission(plan, plan.MaxRAM/4), Profile: profileValue, StopAdmission: gracefulStopFromContext(ctx)}
	defer analyzer.Admission.Close()
	var initialPhase string
	switch command {
	case "scan":
		initialPhase = "Scanning"
	case "analyze":
		initialPhase = "Analyzing"
	default:
		initialPhase = "Scanning directory inventory"
	}
	progressMode := progressJobs
	if command == "scan" {
		progressMode = progressScan
	}
	progressReader := &scopedProgressReader{state: state}
	progress := startPipelineProgress(ctx, progressReader, semanticJobs, stderr, common.noProgress, initialPhase, progressMode)
	analyzer.OnFile = progress.SetCurrentFile
	progressComplete := false
	defer func() { progress.Stop(progressComplete) }()
	analyzer.OnIssue = issues.Record
	var scanReport libraryindex.ScanReport
	var scanErr error
	if command != "analyze" {
		resolvedRoots := make([]libraryindex.Root, 0, len(roots)+len(rootAliases)+len(appendRoots))
		seenAliases := make(map[string]struct{}, len(roots)+len(rootAliases)+len(appendRoots))
		for _, namedRoots := range []struct {
			flag       string
			items      []string
			additional bool
		}{{flag: "--root-alias", items: rootAliases}, {flag: "--append-root", items: appendRoots, additional: true}} {
			for _, specification := range namedRoots.items {
				alias, rootPath, parseErr := parseNamedRoot(namedRoots.flag, specification)
				if parseErr != nil {
					scanErr = parseErr
					break
				}
				if _, duplicate := seenAliases[alias]; duplicate {
					scanErr = fmt.Errorf("duplicate root alias %q", alias)
					break
				}
				seenAliases[alias] = struct{}{}
				var root libraryindex.Root
				var rootErr error
				if namedRoots.additional {
					root, rootErr = state.EnsureAdditionalRoot(ctx, rootPath, alias)
				} else {
					root, rootErr = state.EnsureRoot(ctx, rootPath, alias)
				}
				if rootErr != nil {
					scanErr = rootErr
					break
				}
				resolvedRoots = append(resolvedRoots, root)
			}
			if scanErr != nil {
				break
			}
		}
		for i, rootPath := range roots {
			if scanErr != nil {
				break
			}
			alias := defaultRootAlias(rootPath)
			if len(roots) > 1 {
				alias = fmt.Sprintf("%s-%d", alias, i+1)
			}
			if _, duplicate := seenAliases[alias]; duplicate {
				scanErr = fmt.Errorf("duplicate root alias %q; use --root-alias to disambiguate", alias)
				break
			}
			seenAliases[alias] = struct{}{}
			root, rootErr := state.EnsureRoot(ctx, rootPath, alias)
			if rootErr != nil {
				scanErr = rootErr
				break
			}
			resolvedRoots = append(resolvedRoots, root)
		}
		if scanErr == nil {
			scanReport, scanErr = state.Scan(ctx, libraryindex.ScanOptions{Roots: resolvedRoots, Workers: plan.ScanWorkers, QueueDepth: plan.QueueDepth, FollowSymlinks: common.followDirectorySymlinks, Exclusions: exclusions, SemanticJobs: semanticJobs, Admission: analyzer.Admission, OnFile: progress.SetCurrentFile, OnDirectory: progress.SetCurrentDirectory, OnIssue: issues.Record, OnEpoch: progressReader.SetScanEpoch, StopAdmission: gracefulStopFromContext(ctx)})
			if scanErr == nil {
				progress.SetPhase("Building scan manifest and diff")
				scanReport.Manifest, scanErr = state.WriteScanManifest(ctx, scanReport.Epoch, semanticJobs)
				if scanErr == nil && command != "scan" {
					progressReader.FreezeEpoch(scanReport.Epoch)
				}
			}
		}
	}
	if scanErr != nil {
		if errors.Is(scanErr, libraryindex.ErrShutdownRequested) {
			return 130, scanErr
		}
		return 1, scanErr
	}
	if gracefulStopRequested(ctx) {
		return 130, libraryindex.ErrShutdownRequested
	}
	if err := issues.Err(); err != nil {
		return 1, fmt.Errorf("write state issue log: %w", err)
	}
	var analysisReport libraryindex.AnalysisReport
	if command != "scan" {
		progress.SetPhase("Analyzing manifest diff")
		analyzer.FreezeManifest = command != "analyze"
		if command != "analyze" {
			analyzer.DiffEpoch = scanReport.Epoch
		}
		discoveryDone := make(chan struct{})
		close(discoveryDone)
		analysisReport, err = analyzer.Run(ctx, analysisOptions, discoveryDone)
		if err != nil {
			if errors.Is(err, libraryindex.ErrShutdownRequested) {
				return 130, err
			}
			return 1, err
		}
		if err := issues.Err(); err != nil {
			return 1, fmt.Errorf("write state issue log: %w", err)
		}
	}
	var fitResult libraryindex.FitResult
	var packManifest any
	if command == "run" && *outPath != "" {
		if gracefulStopRequested(ctx) {
			return 130, libraryindex.ErrShutdownRequested
		}
		progress.SetCurrentOperation("Library fitting (no source file)")
		progress.SetPhase("Fitting library")
		fitResult, err = state.Fit(ctx, libraryindex.FitOptions{Seed: uint64(common.seed), TrainingSample: *trainingSample, Clusters: *clusters, Refit: *refit, Plan: plan})
		if err != nil {
			return 1, err
		}
		if gracefulStopRequested(ctx) {
			return 130, libraryindex.ErrShutdownRequested
		}
		progress.SetCurrentOperation("Library export (no source file)")
		progress.SetPhase("Exporting library pack")
		manifest, err := state.ExportPack(ctx, *outPath)
		if err != nil {
			return 1, err
		}
		packManifest = manifest
	}
	if gracefulStopRequested(ctx) {
		return 130, libraryindex.ErrShutdownRequested
	}
	progressComplete = true
	progress.Stop(true)
	result := map[string]any{"command": command, "resources": plan, "scan": scanReport, "analysis": analysisReport, "fit": fitResult, "pack": packManifest}
	if common.jsonOutput {
		return completionCode(scanReport.Errors + analysisReport.Failed + analysisReport.SkippedChanged), json.NewEncoder(stdout).Encode(result)
	}
	fmt.Fprintf(stdout, "files=%d queued=%d metadata=%d analyzed=%d skipped_changed=%d failed=%d\n", scanReport.AudioFiles, scanReport.Manifest.DiffCount, analysisReport.MetadataCompleted, analysisReport.AudioCompleted, analysisReport.SkippedChanged, analysisReport.Failed)
	return completionCode(scanReport.Errors + analysisReport.Failed + analysisReport.SkippedChanged), nil
}

func parseNamedRoot(flagName, specification string) (string, string, error) {
	alias, rootPath, ok := strings.Cut(specification, "=")
	alias, rootPath = strings.TrimSpace(alias), strings.TrimSpace(rootPath)
	if !ok || alias == "" || rootPath == "" {
		return "", "", fmt.Errorf("%s must use ALIAS=PATH", flagName)
	}
	return alias, rootPath, nil
}

func defaultRootAlias(path string) string {
	base := filepath.Base(filepath.Clean(path))
	var alias strings.Builder
	for _, character := range base {
		allowed := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '.' || character == '_' || character == ':' || character == '-'
		if allowed {
			alias.WriteRune(character)
		} else if alias.Len() > 0 && !strings.HasSuffix(alias.String(), "-") {
			alias.WriteByte('-')
		}
		if alias.Len() >= 96 {
			break
		}
	}
	value := strings.Trim(alias.String(), ".-_")
	if value == "" || value[0] == ':' {
		return "root"
	}
	return value
}

func warmMERTPool(ctx context.Context, executable, bundleDir string, manifest audio.MERTBundleManifest, plan libraryindex.ResourcePlan, stderr io.Writer, onIssue func(libraryindex.ProcessingIssue)) (*audio.MERTWorkerPool, libraryindex.ResourcePlan, error) {
	makePool := func(count int) *audio.MERTWorkerPool {
		return audio.NewMERTWorkerPool(&audio.MERTWorker{Executable: executable, BundleDir: bundleDir, Model: manifest.Model, InferenceThreads: plan.InferenceThreads}, count)
	}
	if plan.Mode == libraryindex.ConcurrencyAuto && plan.InferenceWorkers > 1 {
		probe := makePool(1)
		if err := warmMERTWithRetries(ctx, probe, stderr, onIssue); err != nil {
			_ = probe.Close()
			return nil, plan, err
		}
		measured := probe.ResidentBytes()
		headroom := int64(1 << 30)
		if measured > 0 && measured*int64(plan.InferenceWorkers)+headroom > plan.MaxRAM {
			plan.InferenceWorkers = 1
			fmt.Fprintf(stderr, "automatic MERT fallback: one session (measured session RSS=%d, target=%d)\n", measured, plan.MaxRAM)
			plan.ResidentMERTBytes = probe.ResidentBytes()
			return probe, plan, nil
		}
		_ = probe.Close()
	}
	pool := makePool(plan.InferenceWorkers)
	if err := warmMERTWithRetries(ctx, pool, stderr, onIssue); err != nil {
		_ = pool.Close()
		return nil, plan, err
	}
	if measured := pool.ResidentBytes(); measured > 0 && measured+(1<<30) > plan.MaxRAM {
		_ = pool.Close()
		return nil, plan, fmt.Errorf("measured MERT worker RSS %d plus safety headroom exceeds --max-ram %d", measured, plan.MaxRAM)
	}
	plan.ResidentMERTBytes = pool.ResidentBytes()
	return pool, plan, nil
}

type mertPoolWarmer interface {
	Warm(context.Context) error
}

func warmMERTWithRetries(ctx context.Context, pool mertPoolWarmer, stderr io.Writer, onIssue func(libraryindex.ProcessingIssue)) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = pool.Warm(ctx)
		if err == nil || ctx.Err() != nil || !errors.Is(err, audio.ErrNativeWorker) {
			return err
		}
		if onIssue != nil {
			onIssue(libraryindex.NewProcessingIssue("mert_warmup", "", "", "native_worker_restart", err, attempt < 2))
		}
		if attempt < 2 {
			fmt.Fprintf(stderr, "MERT warmup worker failed; restarting native sessions (attempt %d/3)\n", attempt+2)
		}
	}
	return err
}

func completionCode(failures int64) int {
	if failures > 0 {
		return 2
	}
	return 0
}

func ensureModel(ctx context.Context, common commonFlags, localBundle string, stderr io.Writer) (string, audio.MERTBundleManifest, error) {
	manager := &audio.MERTBundleManager{Directory: filepath.Join(common.state, "runtime", "mert")}
	if dir, manifest, err := manager.ActiveContext(ctx); err == nil {
		return dir, manifest, nil
	}
	if !common.acceptModel {
		return "", audio.MERTBundleManifest{}, errors.New("MERT is separately licensed CC-BY-NC-4.0; use --accept-model-license with setup/import or run")
	}
	if localBundle != "" {
		dir, err := manager.InstallLocal(ctx, localBundle, nil)
		if err != nil {
			return "", audio.MERTBundleManifest{}, err
		}
		manifest, err := audio.ReadMERTBundleContext(ctx, dir)
		return dir, manifest, err
	}
	if bundle, openErr := indexerbundle.OpenSelf(); openErr == nil {
		defer bundle.Close()
		runtimeRoot := filepath.Join(common.state, "runtime")
		if err := os.MkdirAll(runtimeRoot, 0o700); err != nil {
			return "", audio.MERTBundleManifest{}, err
		}
		stage, err := os.MkdirTemp(runtimeRoot, ".embedded-mert-")
		if err != nil {
			return "", audio.MERTBundleManifest{}, err
		}
		defer os.RemoveAll(stage)
		if err := bundle.Extract("mert", stage); err == nil {
			dir, err := manager.InstallLocal(ctx, stage, nil)
			if err != nil {
				return "", audio.MERTBundleManifest{}, err
			}
			manifest, err := audio.ReadMERTBundleContext(ctx, dir)
			return dir, manifest, err
		} else if common.offline {
			return "", audio.MERTBundleManifest{}, errors.New("offline executable does not contain a MERT payload; build with indexerpack --model or use --model-bundle")
		}
	}
	if common.offline {
		return "", audio.MERTBundleManifest{}, errors.New("offline mode requires an embedded or --model-bundle MERT pack")
	}
	distribution, err := modelpack.RecommendedMERT(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", audio.MERTBundleManifest{}, err
	}
	fmt.Fprintf(stderr, "downloading pinned MERT pack (%d bytes) after explicit license consent\n", distribution.DownloadBytes)
	cache := filepath.Join(common.state, "runtime", "downloads", distribution.SHA256)
	if err := os.MkdirAll(filepath.Join(common.state, "runtime"), 0o700); err != nil {
		return "", audio.MERTBundleManifest{}, err
	}
	stage, err := os.MkdirTemp(filepath.Join(common.state, "runtime"), ".mert-unpack-")
	if err != nil {
		return "", audio.MERTBundleManifest{}, err
	}
	defer os.RemoveAll(stage)
	destination := filepath.Join(stage, "files")
	if err := modelpack.FetchPinned(ctx, distribution.URL, distribution.SHA256, cache, destination, nil); err != nil {
		return "", audio.MERTBundleManifest{}, err
	}
	dir, err := manager.InstallLocal(ctx, destination, nil)
	if err != nil {
		return "", audio.MERTBundleManifest{}, err
	}
	manifest, err := audio.ReadMERTBundleContext(ctx, dir)
	return dir, manifest, err
}

func resolveCodec(ctx context.Context, common commonFlags) (*localaudio.Runtime, error) {
	runtimeDir := common.runtimeDir
	if runtimeDir == "" {
		runtimeDir = os.Getenv("PLAYLIST_INDEXER_CODEC_RUNTIME")
	}
	if runtimeDir != "" {
		absolute, err := filepath.Abs(runtimeDir)
		if err != nil {
			return nil, err
		}
		return localaudio.OpenRuntime(absolute)
	}
	bundle, err := indexerbundle.OpenSelf()
	if err != nil {
		return nil, errors.New("codec runtime is not installed; use the packaged executable or provide --runtime-dir")
	}
	defer bundle.Close()
	payload, err := bundle.Sub("codec")
	if err != nil {
		return nil, errors.New("packaged executable does not contain its codec payload")
	}
	runtimeRoot, err := filepath.Abs(filepath.Join(common.state, "runtime", "codec"))
	if err != nil {
		return nil, err
	}
	codec, err := localaudio.InstallPayload(ctx, payload, runtimeRoot)
	if err != nil {
		return nil, fmt.Errorf("extract codec runtime (if this filesystem is mounted noexec, choose an executable --runtime-dir): %w", err)
	}
	return codec, nil
}

func runStatus(ctx context.Context, args []string, stdout io.Writer) (int, error) {
	flags := flag.NewFlagSet("playlist-indexer status", flag.ContinueOnError)
	state := flags.String("state", defaultStateDir(), "local state directory")
	jsonOutput := flags.Bool("json", false, "machine-readable stdout")
	if err := flags.Parse(args); err != nil {
		return 1, err
	}
	status, err := libraryindex.ReadStatus(ctx, *state)
	if err != nil {
		return 1, err
	}
	if *jsonOutput {
		return 0, json.NewEncoder(stdout).Encode(status)
	}
	fmt.Fprintf(stdout, "files=%d present=%d queued_files=%d active_files=%d failed_files=%d metadata=%d dsp=%d mert=%d\n", status.Files, status.Present, status.QueuedFiles, status.ActiveFiles, status.FailedFiles, status.Metadata, status.DSP, status.MERT)
	return 0, nil
}

func runDoctor(ctx context.Context, args []string, stdout io.Writer) (int, error) {
	flags := flag.NewFlagSet("playlist-indexer doctor", flag.ContinueOnError)
	var common commonFlags
	if err := common.preloadConfig(args); err != nil {
		return 1, err
	}
	addCommon(flags, &common)
	if err := flags.Parse(args); err != nil {
		return 1, err
	}
	plan, err := common.plan()
	if err != nil {
		return 1, err
	}
	report := map[string]any{"resources": plan, "go": runtime.Version(), "platform": runtime.GOOS + "/" + runtime.GOARCH, "codecReady": false, "mertReady": false}
	if codec, openErr := resolveCodec(ctx, common); openErr == nil {
		report["codecReady"], report["codecRuntime"] = true, codec.Manifest()
	} else {
		report["codecError"] = openErr.Error()
	}
	manager := &audio.MERTBundleManager{Directory: filepath.Join(common.state, "runtime", "mert")}
	if _, manifest, openErr := manager.ActiveContext(ctx); openErr == nil {
		report["mertReady"], report["mertModel"] = true, manifest.Model
	} else {
		report["mertError"] = openErr.Error()
	}
	return 0, json.NewEncoder(stdout).Encode(report)
}

func runModel(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	if len(args) == 0 || args[0] != "setup" && args[0] != "import" && args[0] != "status" {
		return 1, errors.New("usage: playlist-indexer model setup|import|status [options]")
	}
	command := args[0]
	flags := flag.NewFlagSet("playlist-indexer model "+command, flag.ContinueOnError)
	var common commonFlags
	if err := common.preloadConfig(args[1:]); err != nil {
		return 1, err
	}
	addCommon(flags, &common)
	source := flags.String("source", "", "prepared local MERT pack; import only")
	if err := flags.Parse(args[1:]); err != nil {
		return 1, err
	}
	if command == "status" {
		manager := &audio.MERTBundleManager{Directory: filepath.Join(common.state, "runtime", "mert")}
		dir, manifest, err := manager.ActiveContext(ctx)
		if err != nil {
			return 1, err
		}
		return 0, json.NewEncoder(stdout).Encode(map[string]any{"installed": true, "directory": dir, "model": manifest.Model, "license": manifest.License})
	}
	if command == "import" && *source == "" {
		return 1, errors.New("model import requires --source")
	}
	dir, manifest, err := ensureModel(ctx, common, *source, stderr)
	if err != nil {
		return 1, err
	}
	return 0, json.NewEncoder(stdout).Encode(map[string]any{"installed": true, "directory": dir, "model": manifest.Model, "license": manifest.License})
}

func defaultStateDir() string {
	if data, err := os.UserHomeDir(); err == nil {
		return filepath.Join(data, ".local", "share", "playlist-indexer")
	}
	return "playlist-indexer-state"
}

func parseSize(value string) (int64, error) {
	value = strings.TrimSpace(value)
	units := []struct {
		suffix string
		factor int64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"GB", 1000 * 1000 * 1000}, {"MB", 1000 * 1000}, {"KB", 1000}, {"B", 1}}
	for _, unit := range units {
		if strings.HasSuffix(value, unit.suffix) {
			n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(value, unit.suffix)), 10, 64)
			if err != nil || n <= 0 || n > (1<<63-1)/unit.factor {
				return 0, fmt.Errorf("invalid size %q", value)
			}
			return n * unit.factor, nil
		}
	}
	return 0, fmt.Errorf("size %q requires B, KiB, MiB, GiB, KB, MB, or GB", value)
}
