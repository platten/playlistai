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

func (a *API) GetRecommendedAnalysisBundle() (audio.BundleManifest, error) {
	return audio.RecommendedBundle()
}

func (a *API) InstallRecommendedAnalysisBundle(ctx context.Context) error {
	ctx, _, finish := a.operations.begin(ctx, "analysis-download")
	defer finish()
	a.cancelRecommendationWork()
	return a.app.InstallRecommendedAnalysisBundle(ctx, NewWailsProgress())
}
func (a *API) InstallAnalysisBundle(ctx context.Context, path string) error {
	ctx, _, finish := a.operations.begin(ctx, "analysis-download")
	defer finish()
	a.cancelRecommendationWork()
	return a.app.InstallAnalysisBundle(ctx, path, NewWailsProgress())
}
func (a *API) SetAnalysisEnabled(enabled bool) error {
	if !enabled {
		a.cancelRecommendationWork()
	}
	return a.app.SetAnalysisEnabled(enabled)
}
func (a *API) ClearAnalysis(ctx context.Context) error {
	a.cancelRecommendationWork()
	return a.app.ClearAnalysis(ctx)
}
func (a *API) RemoveAnalysisModel() error {
	a.cancelRecommendationWork()
	a.operations.cancel("analysis-download")
	return a.app.RemoveAnalysisModel()
}
