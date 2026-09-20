package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/audioruntime"
	"github.com/platten/playlistai/internal/bridge"
	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/resolution"
)

// The production native inference service launches the current executable.
// Preserve musiccheck's worker entrypoint when that executable is a test binary.
func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "--mert-worker" {
		if err := audioruntime.RunMERT(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) == 3 && os.Args[1] == "--audio-worker" {
		if err := audioruntime.Run(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type liveAppCase struct {
	Index               int                   `json:"index"`
	Prompt              string                `json:"prompt"`
	Started             time.Time             `json:"started"`
	Milliseconds        int64                 `json:"milliseconds"`
	ParseMilliseconds   int64                 `json:"parseMilliseconds"`
	PrepareMilliseconds int64                 `json:"prepareMilliseconds"`
	BuildMilliseconds   int64                 `json:"buildMilliseconds"`
	Parser              bridge.ParserStatus   `json:"parser"`
	ParsedIntent        core.MusicIntent      `json:"parsedIntent"`
	ConsumedIntent      core.MusicIntent      `json:"consumedIntent"`
	Playlist            bridge.PlaylistResult `json:"playlist"`
	Errors              []string              `json:"errors"`
	Metrics             liveAppMetrics        `json:"metrics"`
	StopRequested       bool                  `json:"stopRequested"`
}

type liveAppMetrics struct {
	Tracks              int  `json:"tracks"`
	DistinctArtists     int  `json:"distinctArtists"`
	AtLeast10Tracks     bool `json:"atLeast10Tracks"`
	AtLeast3Artists     bool `json:"atLeast3Artists"`
	RequestedCount      int  `json:"requestedCount"`
	ExactRequestedCount bool `json:"exactRequestedCount"`
}
type liveAppReport struct {
	Started            time.Time               `json:"started"`
	Completed          bool                    `json:"completed"`
	Fixture            string                  `json:"fixture"`
	CaseCount          int                     `json:"caseCount"`
	Seed               core.RNGSeed            `json:"seed"`
	RecommendationMode core.RecommendationMode `json:"recommendationMode"`
	Platform           string                  `json:"platform"`
	GoVersion          string                  `json:"goVersion"`
	Capabilities       map[string]any          `json:"capabilities"`
	Cases              []liveAppCase           `json:"cases"`
}

// TestLiveAppPrompts is intentionally excluded from ordinary offline checks.
// Run with PLAYLISTAI_EVAL_DATA_DIR pointing at an isolated, prepared app store:
// go test ./cmd/musiccheck -run '^TestLiveAppPrompts$' -count=1 -timeout=150m -v
// Native execution requires CGO and the same installed libraries as the desktop.
// This exercises parsing and generation, not title generation or rendered UI.
func TestLiveAppPrompts(t *testing.T) {
	dir := os.Getenv("PLAYLISTAI_EVAL_DATA_DIR")
	if dir == "" {
		t.Skip("set PLAYLISTAI_EVAL_DATA_DIR to opt into real app prompt evaluation")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	defaults := config.Default()
	defaultPath, _ := filepath.Abs(defaults.DataDir)
	resolved, _ := filepath.EvalSymlinks(abs)
	defaultResolved, _ := filepath.EvalSymlinks(defaultPath)
	if abs == defaultPath || (resolved != "" && resolved == defaultResolved) || filepath.Dir(abs) == abs {
		t.Fatal("evaluation requires an isolated data directory, not the normal application store")
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"preferences.json", "prefs.json", "history.sqlite", "candidate-catalog", "music-analysis", "mert-analysis"} {
		if info, e := os.Lstat(filepath.Join(abs, name)); e == nil && info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("evaluation writable path must not symlink user state: %s", name)
		}
	}
	cfg := defaults
	if file := os.Getenv("PLAYLISTAI_EVAL_CONFIG"); file != "" {
		cfg, err = config.Load(file)
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg.DataDir = abs
	cfg.Catalog.Dir = filepath.Join(abs, "catalog")
	cfg.Enrich.CachePath = filepath.Join(abs, "evaluation-metadata.sqlite")
	for _, override := range []struct {
		env    string
		target *string
	}{{"PLAYLISTAI_EVAL_CATALOG", &cfg.Catalog.Dir}, {"PLAYLISTAI_EVAL_MODEL", &cfg.AI.ModelPath}, {"PLAYLISTAI_EVAL_RUNTIME", &cfg.AI.LlamaServerPath}} {
		if value := os.Getenv(override.env); value != "" {
			*override.target = value
		}
	}
	fixture := os.Getenv("PLAYLISTAI_EVAL_PROMPTS")
	if fixture == "" {
		fixture = filepath.Join("..", "..", "internal", "evaluation", "testdata", "varied-prompts-v1.json")
	}
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var cases []promptCase
	if err = json.Unmarshal(raw, &cases); err != nil || len(cases) == 0 {
		t.Fatalf("load prompt fixture: %v", err)
	}
	caseTimeout := 3 * time.Minute
	if value := os.Getenv("PLAYLISTAI_EVAL_CASE_TIMEOUT"); value != "" {
		caseTimeout, err = time.ParseDuration(value)
		if err != nil || caseTimeout <= 0 || caseTimeout > 10*time.Minute {
			t.Fatal("PLAYLISTAI_EVAL_CASE_TIMEOUT must be a positive duration at most10m")
		}
	}
	var stopAfter time.Duration
	if value := os.Getenv("PLAYLISTAI_EVAL_STOP_CHECKING_AFTER"); value != "" {
		stopAfter, err = time.ParseDuration(value)
		if err != nil || stopAfter <= 0 || stopAfter >= caseTimeout {
			t.Fatal("PLAYLISTAI_EVAL_STOP_CHECKING_AFTER must be positive and below the hard case timeout")
		}
	}
	if value := os.Getenv("PLAYLISTAI_EVAL_LIMIT"); value != "" {
		n, e := strconv.Atoi(value)
		if e != nil || n < 1 || n > len(cases) {
			t.Fatal("invalid PLAYLISTAI_EVAL_LIMIT")
		}
		cases = cases[:n]
	}
	logFile, err := os.OpenFile(filepath.Join(abs, "live-app.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	logger := slog.New(slog.NewJSONHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo}))
	report := liveAppReport{Started: time.Now().UTC(), Fixture: fixture, CaseCount: len(cases), Seed: "42", RecommendationMode: core.EnhancedHybrid, Platform: runtime.GOOS + "/" + runtime.GOARCH, GoVersion: runtime.Version(), Capabilities: map[string]any{"nativeInferenceCompiled": audio.NativeInferenceAvailable()}, Cases: []liveAppCase{}}
	report.Capabilities["hardCaseTimeout"] = caseTimeout.String()
	report.Capabilities["softBuildStopAfter"] = stopAfter.String()
	writeReport := func() {
		t.Helper()
		b, e := json.MarshalIndent(report, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(abs, "live-app-report.json"), append(b, '\n'), 0600); e != nil {
			t.Fatal(e)
		}
	}
	writeReport()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	container, err := app.New(ctx, cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer container.Close()
	if err = container.SetRecommendationMode(core.EnhancedHybrid); err != nil {
		t.Fatal(err)
	}
	allowRules := os.Getenv("PLAYLISTAI_EVAL_ALLOW_RULES") == "1"
	startup, stopStartup := context.WithTimeout(ctx, 3*time.Minute)
	for {
		parser := container.IntentParser().Info()
		if !container.AudioStartupPending() && parser.Ready && (parser.Backend == "llama" || allowRules) {
			break
		}
		select {
		case <-startup.Done():
			stopStartup()
			report.Capabilities["startupError"] = startup.Err().Error()
			report.Capabilities["parser"] = parser
			writeReport()
			t.Fatal("app startup did not reach requested parser/audio readiness")
		case <-time.After(250 * time.Millisecond):
		}
	}
	stopStartup()
	rt := container.Runtime()
	report.Capabilities["parser"] = container.IntentParser().Info()
	report.Capabilities["actualRecommendationMode"] = container.RecommendationMode()
	if rt.Resolver != nil {
		report.Capabilities["catalogVersion"] = rt.Resolver.CatalogVersion()
	}
	if versioned, ok := rt.Reco.(interface{ AlgorithmVersion() string }); ok {
		report.Capabilities["algorithmVersion"] = versioned.AlgorithmVersion()
	}
	status, statusErr := container.GetDiscoveryAssetStatus()
	report.Capabilities["discovery"] = status
	analysis, analysisErr := container.GetAnalysisStatus(ctx)
	report.Capabilities["analysis"] = analysis
	enhanced, enhancedErr := container.GetEnhancedAnalysisStatus(ctx)
	report.Capabilities["enhanced"] = enhanced
	for name, e := range map[string]error{"discoveryError": statusErr, "analysisError": analysisErr, "enhancedError": enhancedErr} {
		if e != nil {
			report.Capabilities[name] = e.Error()
		}
	}
	writeReport()
	if rt.Reco == nil || rt.Catalog == nil || rt.Resolver == nil || statusErr != nil || !status.Installed {
		t.Fatal("evaluation requires prepared catalog and shared discovery assets; see report capabilities")
	}
	journal, err := os.OpenFile(filepath.Join(abs, "live-app-cases.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	for i, expected := range cases {
		if ctx.Err() != nil {
			break
		}
		row := liveAppCase{Index: i + 1, Prompt: expected.Prompt, Started: time.Now().UTC(), Errors: []string{}}
		caseCtx, finish := context.WithTimeout(ctx, caseTimeout)
		// Fresh bridge instance prevents an earlier case's intent-preview cache
		// from substituting for a new parser call. Application services stay real.
		api := bridge.New(container, logger)
		parseStart := time.Now()
		preview, parseErr := api.ParseIntent(caseCtx, expected.Prompt)
		row.ParseMilliseconds = time.Since(parseStart).Milliseconds()
		row.Parser = preview.Parser
		row.ParsedIntent = preview.Intent
		if parseErr != nil {
			row.Errors = append(row.Errors, "parse: "+parseErr.Error())
		} else {
			row.Errors = append(row.Errors, checkIntent(expected, preview.Intent)...)
			if preview.Cached {
				row.Errors = append(row.Errors, "parser unexpectedly reused a cached intent")
			}
			if preview.Parser.FallbackUsed {
				row.Errors = append(row.Errors, "parser fallback: "+preview.Parser.FallbackReason)
			}
			if !allowRules && preview.Parser.Backend != "llama" {
				row.Errors = append(row.Errors, "requested local language model was not used")
			}
			intent := preview.Intent
			intent.Seed = "42"
			intent.Controls.RecommendationMode = core.EnhancedHybrid
			intent.VerificationPolicy = core.BestAvailable
			prepareStart := time.Now()
			prepareErr := resolution.SpellingConfirmationError(preview.ResolutionIssues)
			if prepareErr == nil && container.Knowledge != nil {
				if knowledge, ok := container.Knowledge.(ports.IterativeMusicKnowledge); ok {
					intent, prepareErr = knowledge.PrepareMusic(caseCtx, intent, rt.Catalog, rt.Resolver, ports.NopProgress{})
				} else {
					intent, prepareErr = container.Knowledge.ResolveMusic(caseCtx, intent, rt.Catalog, rt.Resolver, ports.NopProgress{})
				}
			}
			if prepareErr == nil {
				intent, _ = resolution.Apply(rt.Resolver, intent)
			}
			row.PrepareMilliseconds = time.Since(prepareStart).Milliseconds()
			row.ConsumedIntent = intent
			if prepareErr != nil {
				row.Errors = append(row.Errors, "prepare: "+prepareErr.Error())
			} else {
				buildStart := time.Now()
				generationID := fmt.Sprintf("live-eval-%d-%d", report.Started.UnixNano(), i+1)
				finishStop := liveAppSoftStop(caseCtx, stopAfter, func() { api.StopAndKeepCheckedTracks(generationID) })
				playlist, buildErr := api.BuildPlaylist(caseCtx, bridge.BuildPlaylistRequest{Version: core.CurrentIntentVersion, Intent: intent, Seed: "42", GenerationID: generationID})
				row.StopRequested = finishStop()
				if row.StopRequested {
					row.Errors = append(row.Errors, "evaluation soft stop requested; not an uninterrupted completion")
				}
				row.BuildMilliseconds = time.Since(buildStart).Milliseconds()
				row.Playlist = playlist
				if buildErr != nil {
					row.Errors = append(row.Errors, "build: "+buildErr.Error())
				} else {
					row.ConsumedIntent = playlist.Intent
					row.Errors = append(row.Errors, checkIntent(expected, playlist.Intent)...)
					if playlist.Intent.Controls.RecommendationMode != core.EnhancedHybrid || playlist.Seed != "42" {
						row.Errors = append(row.Errors, "actual mode or fixed seed changed")
					}
				}
				plain := core.Playlist{Intent: playlist.Intent, Seed: playlist.Seed, Outcome: playlist.Outcome, AudioEvidence: playlist.AudioEvidence}
				for _, track := range playlist.Tracks {
					plain.Tracks = append(plain.Tracks, core.TrackRef{ID: track.ID, Artist: track.Artist, Title: track.Title})
				}
				row.Errors = append(row.Errors, checkPlaylist(plain, 10, intent.Controls.TotalTrackCount)...)
				row.Errors = append(row.Errors, checkVariety(plain, expected, 3)...)
				if plain.Outcome.State != core.OutcomeFulfilled {
					row.Errors = append(row.Errors, fmt.Sprintf("semantic outcome not fulfilled: %s", plain.Outcome.State))
				}
				for _, track := range plain.Tracks {
					for _, excluded := range expected.ExcludedArtists {
						if strings.Contains(strings.ToLower(track.Display()), strings.ToLower(excluded)) {
							row.Errors = append(row.Errors, "excluded artist in output")
						}
					}
				}
				if expected.Destination != "" && len(plain.Tracks) > 0 && !strings.EqualFold(plain.Tracks[len(plain.Tracks)-1].Artist, expected.Destination) {
					row.Errors = append(row.Errors, "wrong final artist")
				}
				if expected.Meaning != nil && expected.Meaning.Start != "" && len(plain.Tracks) > 0 && !strings.EqualFold(plain.Tracks[0].Artist, expected.Meaning.Start) {
					row.Errors = append(row.Errors, "wrong starting artist")
				}
			}
		}
		finish()
		artists := map[string]bool{}
		for _, track := range row.Playlist.Tracks {
			if artist := core.NormalizeIdentityPart(track.Artist); artist != "" {
				artists[artist] = true
			}
		}
		row.Metrics = liveAppMetrics{Tracks: len(row.Playlist.Tracks), DistinctArtists: len(artists), AtLeast10Tracks: len(row.Playlist.Tracks) >= 10, AtLeast3Artists: len(artists) >= 3, RequestedCount: row.ConsumedIntent.Controls.TotalTrackCount, ExactRequestedCount: row.ConsumedIntent.Controls.TotalTrackCount > 0 && len(row.Playlist.Tracks) == row.ConsumedIntent.Controls.TotalTrackCount}
		row.Milliseconds = time.Since(row.Started).Milliseconds()
		report.Cases = append(report.Cases, row)
		if err = json.NewEncoder(journal).Encode(row); err != nil {
			t.Fatal(err)
		}
		if err = journal.Sync(); err != nil {
			t.Fatal(err)
		}
		writeReport()
		logger.Info("evaluation case completed", "index", row.Index, "tracks", len(row.Playlist.Tracks), "outcome", row.Playlist.Outcome.State, "errors", row.Errors, "milliseconds", row.Milliseconds)
		t.Logf("case %d/%d tracks=%d outcome=%s errors=%v duration=%s", i+1, len(cases), len(row.Playlist.Tracks), row.Playlist.Outcome.State, row.Errors, time.Duration(row.Milliseconds)*time.Millisecond)
	}
	report.Completed = ctx.Err() == nil && len(report.Cases) == len(cases)
	writeReport()
	if ctx.Err() != nil {
		t.Errorf("evaluation interrupted: %v", ctx.Err())
	}
	for _, row := range report.Cases {
		if len(row.Errors) > 0 {
			t.Errorf("case %d %q: %v", row.Index, row.Prompt, row.Errors)
		}
	}
}

// Join the timer goroutine before inspecting its result or releasing the API.
func liveAppSoftStop(ctx context.Context, after time.Duration, stop func()) func() bool {
	if after <= 0 {
		return func() bool { return false }
	}
	done, joined := make(chan struct{}), make(chan struct{})
	requested := false
	go func() {
		defer close(joined)
		timer := time.NewTimer(after)
		defer timer.Stop()
		select {
		case <-done:
		case <-ctx.Done():
		case <-timer.C:
			requested = true
			stop()
		}
	}()
	return func() bool {
		close(done)
		<-joined
		return requested
	}
}

func TestLiveAppSoftStop(t *testing.T) {
	t.Run("fires and joins", func(t *testing.T) {
		called := make(chan struct{})
		finish := liveAppSoftStop(context.Background(), time.Millisecond, func() { close(called) })
		select {
		case <-called:
		case <-time.After(time.Second):
			t.Fatal("soft stop did not fire")
		}
		if !finish() {
			t.Fatal("stop not recorded")
		}
	})
	t.Run("completion cancels timer", func(t *testing.T) {
		finish := liveAppSoftStop(context.Background(), time.Hour, func() { t.Error("unexpected stop") })
		if finish() {
			t.Fatal("unexpected stop flag")
		}
	})
	t.Run("parent cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		finish := liveAppSoftStop(ctx, time.Hour, func() { t.Error("unexpected stop") })
		cancel()
		if finish() {
			t.Fatal("parent cancellation is not a soft stop")
		}
	})
}
