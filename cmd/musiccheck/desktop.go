package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/bridge"
	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/evaluation"
	"github.com/platten/playlistai/internal/logging"
	"github.com/platten/playlistai/internal/ports"
)

type desktopEvaluationOptions struct {
	Mode                                                                                     core.RecommendationMode
	DataDir, Config, Catalog, Model, Runtime, Output, Replay, Split, Variant, CacheCondition string
	Diagnostics                                                                              string
	CaseTimeout, CleanupTimeout                                                              time.Duration
}

type desktopEvaluationRun struct {
	FamilyID               string                `json:"familyId"`
	Prompt                 string                `json:"prompt"`
	InputMode              string                `json:"inputMode"`
	Milliseconds           int64                 `json:"milliseconds"`
	Result                 bridge.GenerateResult `json:"result"`
	Error                  string                `json:"error,omitempty"`
	GenerationLimitSeconds int64                 `json:"generationLimitSeconds"`
	TimedOut               bool                  `json:"timedOut"`
	Parser                 *bridge.ParserStatus  `json:"parser"`
	ParserFindings         []string              `json:"parserFindings"`
	InterpretationFindings []string              `json:"interpretationFindings"`
	ConstraintFindings     []string              `json:"constraintFindings"`
	Completeness           desktopCompleteness   `json:"completeness"`
}

type desktopCompleteness struct {
	Requested       int  `json:"requested"`
	Returned        int  `json:"returned"`
	DistinctArtists int  `json:"distinctArtists"`
	ExactCount      bool `json:"exactCount"`
}

type desktopEvaluationReport struct {
	Version       int                       `json:"version"`
	Started       time.Time                 `json:"started"`
	Completed     bool                      `json:"completed"`
	Platform      string                    `json:"platform"`
	GoVersion     string                    `json:"goVersion"`
	Identities    runIdentities             `json:"identities"`
	Executable    artifactIdentity          `json:"executable"`
	Capabilities  map[string]any            `json:"capabilities"`
	Runs          []desktopEvaluationRun    `json:"runs"`
	ListeningRuns []evaluation.RelevanceRun `json:"listeningRuns"`
}

// This invokes app.New and the bridge operation used by the desktop, including
// its configured packed CLAP query encoder, preview providers and source policy.
// Evaluation never acknowledges exposure or writes into the normal app store.
func runDesktopEvaluation(ctx context.Context, options desktopEvaluationOptions, cases []promptCase) (runErr error) {
	if options.Mode == "" {
		options.Mode = core.EnhancedHybrid
	}
	if options.Mode != core.EnhancedHybrid && options.Mode != core.Automatic {
		return fmt.Errorf("desktop evaluation supports enhanced_hybrid or automatic")
	}
	if options.CaseTimeout == 0 {
		options.CaseTimeout = ports.EnhancedGenerationLimit
		if options.Mode == core.Automatic {
			options.CaseTimeout = ports.AutomaticGenerationLimit
		}
	}
	if options.Split != "development" && options.Split != "heldout" {
		return fmt.Errorf("eval-split must be development or heldout")
	}
	if options.Variant == "" || (options.CacheCondition != "cold" && options.CacheCondition != "warm" && options.CacheCondition != "unknown") {
		return fmt.Errorf("variant required; cache-condition must be cold, warm, or unknown")
	}
	selected := desktopCases(cases, options.Split)
	if len(selected) == 0 {
		return fmt.Errorf("no cases in requested evaluation split")
	}
	if err := validateDesktopDiagnostics(options, len(selected)); err != nil {
		return err
	}
	if options.Diagnostics != "" {
		file, err := os.OpenFile(options.Diagnostics, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		store := &logging.Store{}
		store.SetDebug(true)
		ctx = logging.WithDiagnostics(ctx, store)
		// Export after ordinary errors/cancellation as well as success. A killed
		// process cannot flush this memory-only store. Existing store bounds apply.
		defer func() {
			encoder := json.NewEncoder(file)
			encoder.SetIndent("", "  ")
			writeErr := encoder.Encode(store.Read(0))
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				runErr = errors.Join(runErr, writeErr, closeErr)
			}
		}()
	}
	root, err := isolatedEvaluationDirectory(options.DataDir)
	if err != nil {
		return err
	}
	cfg := config.Default()
	if options.Config != "" {
		cfg, err = config.Load(options.Config)
		if err != nil {
			return err
		}
	}
	cfg.DataDir, cfg.Enrich.CachePath = root, filepath.Join(root, "evaluation-metadata.sqlite")
	if options.Catalog != "" {
		cfg.Catalog.Dir = options.Catalog
	} else {
		cfg.Catalog.Dir = filepath.Join(root, "catalog")
	}
	if options.Model != "" {
		cfg.AI.ModelPath = options.Model
	}
	if options.Runtime != "" {
		cfg.AI.LlamaServerPath = options.Runtime
	}
	prior := map[string]desktopEvaluationRun{}
	if options.Replay != "" {
		raw, readErr := os.ReadFile(options.Replay)
		if readErr != nil {
			return readErr
		}
		var report desktopEvaluationReport
		if err = json.Unmarshal(raw, &report); err != nil {
			return err
		}
		if report.Version != 1 {
			return fmt.Errorf("replay requires a desktop evaluation report")
		}
		for _, row := range report.Runs {
			prior[row.FamilyID] = row
		}
	}
	logFile, err := os.OpenFile(filepath.Join(root, "evaluation.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	logger := slog.New(slog.NewJSONHandler(logFile, nil))
	container, err := app.New(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer container.Close()
	if err = container.SetRecommendationMode(options.Mode); err != nil {
		return err
	}
	startup, cancelStartup := context.WithTimeout(ctx, 3*time.Minute)
	defer cancelStartup()
	prefs := config.LoadPrefs(root)
	requestedParser := "rules"
	if !prefs.ModelDisabled && (prefs.ModelPath != "" || cfg.AI.ModelPath != "") {
		requestedParser = "llama"
	}
	for !desktopReady(container.AudioStartupPending(), container.IntentParser().Info(), requestedParser) {
		select {
		case <-startup.Done():
			return fmt.Errorf("desktop evaluation startup: requested %s, active %s: %w", requestedParser, container.IntentParser().Info().Backend, startup.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	report := desktopEvaluationReport{Version: 1, Started: time.Now().UTC(), Platform: runtime.GOOS + "/" + runtime.GOARCH, GoVersion: runtime.Version(), Capabilities: map[string]any{"parser": container.IntentParser().Info(), "requestedParser": requestedParser, "nativeInferenceCompiled": audio.NativeInferenceAvailable(), "generationLimitSeconds": int64(options.CaseTimeout / time.Second)}, Runs: []desktopEvaluationRun{}, ListeningRuns: []evaluation.RelevanceRun{}}
	discovery, discoveryErr := container.GetDiscoveryAssetStatus()
	analysis, analysisErr := container.GetAnalysisStatus(ctx)
	enhanced, enhancedErr := container.GetEnhancedAnalysisStatus(ctx)
	report.Capabilities["discovery"], report.Capabilities["analysis"], report.Capabilities["enhanced"] = discovery, analysis, enhanced
	report.Capabilities["recommendationConfig"] = cfg.Recommendation
	report.Capabilities["recommendationMode"] = options.Mode
	library, libraryErr := container.LocalLibraryStatus()
	if libraryErr != nil {
		return libraryErr
	}
	report.Capabilities["library"] = library
	for key, value := range map[string]error{"discoveryError": discoveryErr, "analysisError": analysisErr, "enhancedError": enhancedErr} {
		if value != nil {
			report.Capabilities[key] = value.Error()
		}
	}
	identityOpts := identityOptions{}
	if !prefs.ModelDisabled {
		identityOpts.Model = cfg.AI.ModelPath
		if prefs.ModelPath != "" {
			identityOpts.Model = prefs.ModelPath
		}
	}
	clapManager := audio.BundleManager{Directory: filepath.Join(root, "music-analysis")}
	if dir, _, activeErr := clapManager.ActiveStartupContext(ctx); activeErr == nil {
		identityOpts.CLAPBundle = dir
	}
	mertManager := audio.MERTBundleManager{Directory: filepath.Join(root, "mert-analysis")}
	if dir, _, activeErr := mertManager.ActiveStartupContext(ctx); activeErr == nil {
		identityOpts.MERTBundle = dir
	}
	report.Identities, err = collectRunIdentities(ctx, identityOpts)
	if err != nil {
		return err
	}
	report.Identities.Discovery = discoveryIdentity{Version: discovery.Version, ManifestDigest: discovery.ManifestDigest, PackIDs: append([]string(nil), discovery.PackIDs...), Tracks: discovery.Tracks}
	if executable, executableErr := os.Executable(); executableErr == nil {
		report.Executable, err = identifyFile(ctx, executable)
		if err != nil {
			return err
		}
	}
	if rt := container.Runtime(); rt.Resolver != nil {
		report.Identities.CatalogVersion = rt.Resolver.CatalogVersion()
	}
	report.Capabilities["parser"] = container.IntentParser().Info()
	write := func() error { return writeDesktopReport(options.Output, report) }
	if err = write(); err != nil {
		return err
	}
	for _, item := range selected {
		if err = ctx.Err(); err != nil {
			return err
		}
		api := bridge.New(container, logger)
		row := desktopEvaluationRun{FamilyID: item.FamilyID, Prompt: item.Prompt, InputMode: "raw", GenerationLimitSeconds: int64(options.CaseTimeout / time.Second)}
		started := time.Now()
		caseCtx, cancel := context.WithTimeout(ctx, options.CaseTimeout)
		if item.RequiredLibraryMode != "" && (string(library.Mode) != item.RequiredLibraryMode || !library.Installed) {
			err = fmt.Errorf("family requires a prepared %s store; source settings were preserved", item.RequiredLibraryMode)
		} else if options.Replay == "" {
			row.Result, err = api.GenerateFromPrompt(caseCtx, item.Prompt)
		} else {
			row.InputMode = "frozen"
			previous, ok := prior[item.FamilyID]
			if !ok || previous.Prompt != item.Prompt || previous.Result.Playlist.Intent.Version == 0 {
				err = fmt.Errorf("no matching frozen resolved intent")
			} else {
				// A frozen-intent evaluation intentionally regenerates under the
				// current policy; do not pass the prior saved-result replay token.
				request := bridge.BuildPlaylistRequest{Version: core.CurrentIntentVersion, Intent: previous.Result.Playlist.Intent, Seed: previous.Result.Playlist.Seed}
				request.Intent.Controls.RecommendationMode = options.Mode
				row.Result.Request = request
				row.Result.Playlist, err = api.BuildPlaylist(caseCtx, request)
			}
		}
		row.TimedOut = caseCtx.Err() == context.DeadlineExceeded
		cancel()
		row.Milliseconds = time.Since(started).Milliseconds()
		if err != nil {
			row.Error = err.Error()
		}
		populateDesktopFindings(&row, item, requestedParser)
		report.Runs = append(report.Runs, row)
		listening := evaluation.RelevanceRun{FamilyID: item.FamilyID, Prompt: item.Prompt, Split: options.Split, Variant: options.Variant, InputMode: row.InputMode, CacheCondition: options.CacheCondition, Requested: row.Result.Playlist.Intent.Controls.TotalTrackCount, Milliseconds: row.Milliseconds, GenerationLimitMilliseconds: options.CaseTimeout.Milliseconds(), Tracks: []evaluation.BlindTrack{}, Error: row.Error}
		if listening.Requested < 1 {
			listening.Requested = max(item.Count, 1)
		}
		for _, track := range row.Result.Playlist.Tracks {
			listening.Tracks = append(listening.Tracks, evaluation.BlindTrack{ID: track.ID, Artist: track.Artist, Title: track.Title})
		}
		if snapshot := row.Result.Playlist.Search; snapshot != nil {
			listening.Candidates = []evaluation.BlindTrack{}
			for _, candidate := range snapshot.Candidates {
				track := candidate.Track
				listening.Candidates = append(listening.Candidates, evaluation.BlindTrack{ID: track.ID, Artist: track.Artist, Title: track.Title})
			}
		}
		report.ListeningRuns = append(report.ListeningRuns, listening)
		if err = write(); err != nil {
			return err
		}
		fmt.Printf("%s: mode=%s tracks=%d elapsed=%dms error=%s\n", item.FamilyID, row.InputMode, len(listening.Tracks), row.Milliseconds, row.Error)
	}
	report.Completed = true
	if err = write(); err != nil {
		return err
	}
	for _, row := range report.Runs {
		if desktopCaseFailed(row) {
			return fmt.Errorf("one or more desktop operations or deterministic assertions failed; see %s", options.Output)
		}
	}
	return nil
}

func desktopReady(audioPending bool, parser ports.ParserInfo, requested string) bool {
	return !audioPending && parser.Ready && parser.Backend == requested
}

func desktopCases(cases []promptCase, split string) []promptCase {
	var selected []promptCase
	for _, item := range cases {
		itemSplit := item.Split
		if itemSplit == "" {
			itemSplit = "development"
		}
		if itemSplit != split {
			continue
		}
		if item.FamilyID == "" {
			item.FamilyID = audio.Fingerprint(item.Prompt)
		}
		selected = append(selected, item)
	}
	return selected
}

func validateDesktopFlags(flags *flag.FlagSet) error {
	allowed := map[string]bool{"mode": true, "app-data-dir": true, "app-config": true, "catalog": true, "model": true, "runtime": true, "prompts": true, "output": true, "diagnostics": true, "case": true, "replay": true, "eval-split": true, "variant": true, "cache-condition": true, "case-timeout": true, "cleanup-timeout": true, "supervised-child": true, "cancel-file": true}
	var unsupported []string
	flags.Visit(func(value *flag.Flag) {
		if !allowed[value.Name] {
			unsupported = append(unsupported, "-"+value.Name)
		}
	})
	if len(unsupported) > 0 {
		return fmt.Errorf("app evaluation does not use these standalone flags: %s", strings.Join(unsupported, ", "))
	}
	return nil
}

func validateDesktopDiagnostics(options desktopEvaluationOptions, selected int) error {
	if options.Diagnostics == "" {
		return nil
	}
	if selected != 1 {
		return fmt.Errorf("desktop diagnostics require exactly one selected case")
	}
	// Resolve existing parent directories as well as lexical aliases before the
	// supervisor writes its report. Diagnostics must never replace run artifacts.
	canonical := func(path string) (string, error) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
			return resolved, nil
		}
		if parent, err := filepath.EvalSymlinks(filepath.Dir(absolute)); err == nil {
			absolute = filepath.Join(parent, filepath.Base(absolute))
		}
		return absolute, nil
	}
	diagnostics, err := canonical(options.Diagnostics)
	if err != nil {
		return err
	}
	dataDir, err := canonical(options.DataDir)
	if err != nil {
		return err
	}
	if diagnostics == dataDir || strings.HasPrefix(diagnostics, dataDir+string(filepath.Separator)) {
		return fmt.Errorf("desktop diagnostics must be outside the isolated app-data-dir")
	}
	output, err := canonical(options.Output)
	if err != nil {
		return err
	}
	if diagnostics == output || diagnostics == output+".listening.json" || diagnostics == output+".cases" || strings.HasPrefix(diagnostics, output+".cases"+string(filepath.Separator)) {
		return fmt.Errorf("diagnostics must not collide with the desktop report or its case artifacts")
	}
	if _, err = os.Lstat(options.Diagnostics); !os.IsNotExist(err) {
		if err == nil {
			return fmt.Errorf("desktop diagnostics require a new output file")
		}
		return err
	}
	return nil
}

func populateDesktopFindings(row *desktopEvaluationRun, expected promptCase, requestedParser string) {
	row.ParserFindings, row.InterpretationFindings, row.ConstraintFindings = []string{}, []string{}, []string{}
	if row.InputMode == "raw" {
		parser := row.Result.Status.Parser
		if parser.Backend == "" {
			parser = row.Result.Playlist.Status.Parser
		}
		if parser.Backend == "" {
			row.ParserFindings = append(row.ParserFindings, "actual parser unavailable in returned operation status")
		} else {
			row.Parser = &parser
			if parser.Backend != requestedParser {
				row.ParserFindings = append(row.ParserFindings, "requested parser was not used")
			}
			if parser.FallbackUsed {
				row.ParserFindings = append(row.ParserFindings, "parser fallback: "+parser.FallbackReason)
			}
		}
	}
	playlist := row.Result.Playlist
	intent := playlist.Intent
	if intent.Version == 0 {
		intent = row.Result.Request.Intent
	}
	if intent.Version == 0 {
		row.InterpretationFindings = append(row.InterpretationFindings, "resolved intent unavailable")
	} else {
		row.InterpretationFindings = append(row.InterpretationFindings, checkIntent(expected, intent)...)
	}
	plain := core.Playlist{Intent: intent}
	artists := map[string]bool{}
	for _, track := range playlist.Tracks {
		plain.Tracks = append(plain.Tracks, core.TrackRef{ID: track.ID, Artist: track.Artist, Title: track.Title})
		if key := core.NormalizeIdentityPart(track.Artist); key != "" {
			artists[key] = true
		}
		for _, excluded := range expected.ExcludedArtists {
			if strings.EqualFold(track.Artist, excluded) {
				row.ConstraintFindings = append(row.ConstraintFindings, "excluded artist in output: "+track.Artist)
			}
		}
	}
	requested := expected.Count
	if requested == 0 {
		requested = intent.Controls.TotalTrackCount
	}
	row.Completeness = desktopCompleteness{Requested: requested, Returned: len(plain.Tracks), DistinctArtists: len(artists), ExactCount: requested > 0 && requested == len(plain.Tracks)}
	row.ConstraintFindings = append(row.ConstraintFindings, checkPlaylist(plain, 0, requested)...)
	if len(plain.Tracks) > 0 {
		row.ConstraintFindings = append(row.ConstraintFindings, checkVariety(plain, expected, min(3, len(plain.Tracks)))...)
		if expected.Destination != "" && !strings.EqualFold(plain.Tracks[len(plain.Tracks)-1].Artist, expected.Destination) {
			row.ConstraintFindings = append(row.ConstraintFindings, "wrong final artist")
		}
		if expected.Meaning != nil && expected.Meaning.Start != "" && !strings.EqualFold(plain.Tracks[0].Artist, expected.Meaning.Start) {
			row.ConstraintFindings = append(row.ConstraintFindings, "wrong starting artist")
		}
	}
}

func desktopCaseFailed(row desktopEvaluationRun) bool {
	return row.Error != "" || row.TimedOut || len(row.ParserFindings)+len(row.InterpretationFindings)+len(row.ConstraintFindings) > 0
}

func writeDesktopReport(path string, report desktopEvaluationReport) error {
	if err := writeDesktopJSON(path, report); err != nil {
		return err
	}
	return writeDesktopJSON(path+".listening.json", struct {
		ListeningRuns []evaluation.RelevanceRun `json:"listeningRuns"`
	}{report.ListeningRuns})
}

func writeDesktopJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".evaluation-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err = file.Write(append(raw, '\n')); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func isolatedEvaluationDirectory(path string) (string, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	defaultRoot, _ := filepath.Abs(config.Default().DataDir)
	if root == defaultRoot || filepath.Dir(root) == root {
		return "", fmt.Errorf("app evaluation requires an isolated directory, not normal user state")
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	defaultResolved, _ := filepath.EvalSymlinks(defaultRoot)
	if root != resolved || root == defaultRoot || root == defaultResolved || filepath.Dir(root) == root {
		return "", fmt.Errorf("app evaluation requires an isolated directory, not normal or linked user state")
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("evaluation store must not contain symlinks: %s", path)
		}
		return nil
	})
	return root, err
}
