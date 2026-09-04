// Command playlistai is the Wails v2 desktop shell for Playlist AI.
//
// The Go module lays code out under internal/; this file stays at the module
// root because the Wails toolchain builds the package in the working directory.
package main

import (
	"context"
	"embed"
	"log/slog"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/bridge"
	"github.com/platten/playlistai/internal/config"
)

//go:embed all:frontend/dist
var assets embed.FS

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

	api := bridge.New(container, log)

	return wails.Run(&options.App{
		Title:            "Playlist AI",
		Width:            1180,
		Height:           800,
		MinWidth:         920,
		MinHeight:        640,
		BackgroundColour: &options.RGBA{R: 15, G: 15, B: 18, A: 1},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        api.Startup,
		Bind:             []any{api},
	})
}
