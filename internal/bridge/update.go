package bridge

import (
	"context"
	"fmt"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/platten/playlistai/internal/browser"
	"github.com/platten/playlistai/internal/updater"
)

// CheckForUpdate runs once per desktop session; network failures are logged and
// never block startup or playlist generation.
func (a *API) CheckForUpdate(ctx context.Context) (updater.Offer, error) {
	offer, err := a.updates.Check(ctx)
	if err != nil {
		a.log.Warn("application update check failed", "err", err)
	}
	return offer, nil
}

// InstallUpdate only installs the release previously checked by the backend.
func (a *API) InstallUpdate(ctx context.Context) error {
	desktop := application.Get()
	if desktop == nil {
		return fmt.Errorf("application updates require the desktop app")
	}
	progress := NewWailsProgress()
	if err := a.updates.Install(ctx, func(done, total int64, note string) { progress.Report("app-update", done, total, note) }); err != nil {
		a.log.Error("application update failed", "err", err)
		return err
	}
	a.log.Info("application update verified; exiting for replacement")
	go desktop.Quit()
	return nil
}

func (a *API) CancelUpdate() { a.updates.Cancel() }

func (a *API) OpenUpdateReleasePage() error {
	return browser.OpenURL(a.updates.ReleaseURL())
}
