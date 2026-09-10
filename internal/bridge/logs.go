package bridge

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/logging"
)

var logWindowMu sync.Mutex

// NewWithLogs connects the desktop session log viewer.
func NewWithLogs(a *app.Container, log *slog.Logger, logs *logging.Store) *API {
	api := New(a, log)
	api.logs = logs
	if logs != nil {
		logs.SetDebug(a.DebugLogging())
	}
	return api
}

func (a *API) diagnosticContext(ctx context.Context) context.Context {
	return logging.WithDiagnostics(ctx, a.logs)
}

// GetDebugLogging reports the persisted opt-in diagnostic setting.
func (a *API) GetDebugLogging() bool {
	if a.logs != nil {
		return a.logs.DebugEnabled()
	}
	return a.app.DebugLogging()
}

// SetDebugLogging persists and applies detailed session diagnostics. Turning
// it off also clears detailed records already retained in memory.
func (a *API) SetDebugLogging(enabled bool) error {
	if err := a.app.SetDebugLogging(enabled); err != nil {
		return err
	}
	if a.logs != nil {
		a.logs.SetDebug(enabled)
	}
	return nil
}

// GetLogs returns retained session records newer than the given cursor.
func (a *API) GetLogs(after uint64) []logging.Entry {
	if a.logs == nil {
		return []logging.Entry{}
	}
	return a.logs.Read(after)
}

// OpenLogWindow opens or focuses the separate log viewer, preserving Settings.
func (a *API) OpenLogWindow() error {
	logWindowMu.Lock()
	defer logWindowMu.Unlock()
	desktop := application.Get()
	if desktop == nil {
		return errors.New("log window is available in the desktop app")
	}
	if window, ok := desktop.Window.GetByName("logs"); ok {
		window.Show()
		window.Focus()
		return nil
	}
	desktop.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "logs", Title: "Playlist AI — Application logs",
		Width: 960, Height: 640, MinWidth: 420, MinHeight: 320, URL: "/?window=logs",
	})
	return nil
}

// CloseLogWindow closes only the viewer and returns focus to the main window.
func (a *API) CloseLogWindow() {
	desktop := application.Get()
	if desktop == nil {
		return
	}
	if window, ok := desktop.Window.GetByName("logs"); ok {
		window.Close()
	}
	if window, ok := desktop.Window.GetByName("main"); ok {
		window.Show()
		window.Focus()
	}
}
