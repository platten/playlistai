// Command playlistai is the Wails v3 desktop shell for Playlist AI.
//
// Code lives under internal/; this file stays at the module root because the
// Wails toolchain (Taskfile + `wails3`) builds the package in the working
// directory.
package main

import (
	"context"
	"embed"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/audioruntime"
	"github.com/platten/playlistai/internal/bridge"
	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/logging"
	"github.com/platten/playlistai/internal/updater"
)

// The frontend build output is embedded into the binary. `wails3 dev` serves
// the Vite dev server instead; production builds serve this FS.
//
//go:embed all:frontend/dist
var assets embed.FS

func init() {
	// Registered events get a strongly-typed binding on the frontend.
	application.RegisterEvent[bridge.ProgressEvent](bridge.ProgressEventName)
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := dispatch(os.Args[1:], os.Stdout, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// dispatch keeps headless worker/packaging commands ahead of desktop startup.
// Returning errors here makes exit policy explicit without terminating callers
// that exercise command validation in tests.
func dispatch(args []string, out io.Writer, log *slog.Logger) error {
	// Packaging gate: no GUI, network, models or user-data access required.
	if len(args) == 1 && args[0] == "--check-audio-worker" {
		_, err := audio.RecommendedBundle()
		return err
	}
	if len(args) == 1 && args[0] == "--version" {
		_, err := fmt.Fprintln(out, bridge.Version)
		return err
	}
	if len(args) == 2 && args[0] == "--app-update-worker" {
		return updater.RunWorker(args[1])
	}
	if len(args) == 2 && args[0] == "--audio-worker" {
		return audioruntime.Run(args[1])
	}
	if len(args) == 2 && args[0] == "--mert-worker" {
		return audioruntime.RunMERT(args[1])
	}
	return run(log)
}

func run(log *slog.Logger) error {
	return runWithHost(log, launchDesktop)
}

// runWithHost owns non-GUI initialization and teardown. The native host is the
// sole boundary replaced by tests; real stores/configuration are still used.
func runWithHost(log *slog.Logger, host func(*app.Container, *slog.Logger, *logging.Store) error) error {
	logs := &logging.Store{}
	log = slog.New(logging.NewHandler(log.Handler(), logs))
	cfg := config.Default()
	if p := os.Getenv("PLAYLISTAI_CONFIG"); p != "" {
		loaded, err := config.Load(p)
		if err != nil {
			return err
		}
		cfg = loaded
	}

	container, err := app.New(context.Background(), cfg, log)
	if err != nil {
		return err
	}
	defer func() { _ = container.Close() }()
	return host(container, log, logs)
}

func launchDesktop(container *app.Container, log *slog.Logger, logs *logging.Store) error {
	wapp := application.New(application.Options{
		Name:        "Playlist AI",
		Description: "Local-first playlist recommendations over the Deej-AI embedding catalog.",
		LogLevel:    slog.LevelInfo,
		Logger:      log,
		Services: []application.Service{
			application.NewService(bridge.NewWithLogs(container, log, logs)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	wapp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "Playlist AI",
		Width:            1280,
		Height:           820,
		MinWidth:         1040,
		MinHeight:        640,
		BackgroundColour: application.NewRGB(15, 15, 18),
		URL:              "/",
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 44,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
	})

	return wapp.Run()
}
