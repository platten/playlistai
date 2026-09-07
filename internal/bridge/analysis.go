package bridge

import (
	"context"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/audio"
)

func (a *API) GetAnalysisStatus(ctx context.Context) (app.AnalysisStatus, error) {
	return a.app.GetAnalysisStatus(ctx)
}
func (a *API) InspectAnalysisBundle(path string) (audio.BundleManifest, error) {
	return a.app.InspectAnalysisBundle(path)
}
func (a *API) InstallAnalysisBundle(ctx context.Context, path string) error {
	ctx, _, finish := a.operations.begin(ctx, "analysis-download")
	defer finish()
	a.operations.cancel("prompt-generation")
	a.operations.cancel("playlist-build")
	return a.app.InstallAnalysisBundle(ctx, path, NewWailsProgress())
}
func (a *API) SetAnalysisEnabled(enabled bool) error {
	if !enabled {
		a.operations.cancel("prompt-generation")
		a.operations.cancel("playlist-build")
	}
	return a.app.SetAnalysisEnabled(enabled)
}
func (a *API) ClearAnalysis(ctx context.Context) error {
	a.operations.cancel("prompt-generation")
	a.operations.cancel("playlist-build")
	return a.app.ClearAnalysis(ctx)
}
func (a *API) RemoveAnalysisModel() error {
	a.operations.cancel("prompt-generation")
	a.operations.cancel("playlist-build")
	a.operations.cancel("analysis-download")
	return a.app.RemoveAnalysisModel()
}
