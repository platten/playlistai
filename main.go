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
	"log/slog"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/platten/playlistai/internal/app"
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
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Fprintln(os.Stdout, bridge.Version)
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "--app-update-worker" {
		if err := updater.RunWorker(os.Args[2]); err != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "--audio-worker" {
		if err := audioruntime.Run(os.Args[2]); err != nil {
			os.Exit(1)
		}
		return
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
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
