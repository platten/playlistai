package bridge

import "github.com/platten/playlistai/internal/updater"

// ResetAssets leaves the app closed to new generation until its next launch.
func (a *API) ResetAssets() error {
	a.operations.cancelAll()
	a.updates.Cancel()
	a.intentCache.clear()
	if err := a.app.ResetAssets(); err != nil {
		return err
	}
	return updater.ClearPreviousExecutables()
}
