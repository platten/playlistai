// Package bridge exposes application use-cases to the Wails/React frontend.
// It is deliberately thin: it maps between frontend-friendly DTOs and the
// internal ports and contains no business logic.
package bridge

import (
	"context"
	"log/slog"

	"github.com/platten/playlistai/internal/app"
)

// Version is stamped at build time via -ldflags "-X .../bridge.Version=...".
var Version = "dev"

// API is the struct bound into the Wails runtime. Every exported method becomes
// callable from TypeScript.
type API struct {
	app *app.Container
	log *slog.Logger
	ctx context.Context
}

// New creates the bound API.
func New(a *app.Container, log *slog.Logger) *API {
	if log == nil {
		log = slog.Default()
	}
	return &API{app: a, log: log}
}

// Startup is wired to options.App.OnStartup; it captures the Wails context used
// for event emission and window control.
func (a *API) Startup(ctx context.Context) {
	a.ctx = ctx
	a.log.Info("bridge startup", "version", Version)
}

// Status is the snapshot the frontend requests on load and after long tasks.
type Status struct {
	CoreReady     bool   `json:"coreReady"`     // catalog + similarity + engine wired
	LLMReady      bool   `json:"llmReady"`      // a local GGUF is configured and present
	CatalogLoaded bool   `json:"catalogLoaded"` // the embedding catalog is in memory
	ParserBackend string `json:"parserBackend"` // "llama" | "rules" | "none"
	PreviewMode   string `json:"previewMode"`   // "deezer" | "spotify" | "off"
	Version       string `json:"version"`
}

// GetStatus reports what is wired and ready. Safe to call before any milestone's
// ports are populated.
func (a *API) GetStatus() Status {
	cfg := a.app.Config()

	parser := "none"
	if a.app.Parser != nil {
		parser = a.app.Parser.Info().Backend
	}

	return Status{
		CoreReady:     a.app.Ready(),
		LLMReady:      cfg.LLMReady(),
		CatalogLoaded: a.app.Catalog != nil,
		ParserBackend: parser,
		PreviewMode:   cfg.Preview.Provider,
		Version:       Version,
	}
}
