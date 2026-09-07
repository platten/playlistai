package bridge

import (
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
	return api
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
