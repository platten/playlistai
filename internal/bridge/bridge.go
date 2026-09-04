// Package bridge exposes application use-cases to the Wails v3 frontend as a
// Service. It is deliberately thin: it maps between frontend-friendly DTOs and
// the internal ports and contains no business logic.
package bridge

import (
	"context"
	"log/slog"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/platten/playlistai/internal/app"
)

// Version is stamped at build time via -ldflags "-X .../bridge.Version=...".
var Version = "dev"

// API is registered with application.NewService; every exported method becomes
// callable from TypeScript via the generated bindings.
type API struct {
	app *app.Container
	log *slog.Logger
	ctx context.Context
}

// New creates the service.
func New(a *app.Container, log *slog.Logger) *API {
	if log == nil {
		log = slog.Default()
	}
	return &API{app: a, log: log, ctx: context.Background()}
}

// ServiceName implements application.ServiceName.
func (a *API) ServiceName() string { return "playlistai.bridge" }

// ServiceStartup implements application.ServiceStartup.
func (a *API) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	a.ctx = ctx
	a.log.Info("bridge service startup", "version", Version)
	return nil
}

// context returns the service context, falling back to Background.
func (a *API) context() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

// ServiceShutdown implements application.ServiceShutdown.
func (a *API) ServiceShutdown() error { return nil }

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
