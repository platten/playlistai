// Command playlistai is the Wails v3 desktop shell for Playlist AI.
//
// Code lives under internal/; this file stays at the module root because the
// Wails toolchain (Taskfile + `wails3`) builds the package in the working
// directory.
package main

import (
	"context"
	"embed"
	"log/slog"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/bridge"
	"github.com/platten/playlistai/internal/config"
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
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
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
		Services: []application.Service{
			application.NewService(bridge.New(container, log)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	wapp.Window.NewWithOptions(application.WebviewWindowOptions{
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
