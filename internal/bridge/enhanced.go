package bridge

import (
	"context"

	"github.com/platten/playlistai/internal/app"
)

func (a *API) GetEnhancedAnalysisStatus(ctx context.Context) (app.EnhancedAnalysisStatus, error) {
	return a.app.GetEnhancedAnalysisStatus(ctx)
}
func (a *API) SetEnhancedAnalysisEnabled(enabled bool) error {
	a.cancelRecommendationWork()
	a.operations.cancel("enhanced-analysis")
	return a.app.SetEnhancedAnalysisEnabled(enabled)
}
func (a *API) InstallMERT(ctx context.Context, directory string) error {
	ctx, _, finish := a.operations.begin(ctx, "mert-install")
	defer finish()
	a.cancelRecommendationWork()
	a.operations.cancel("enhanced-analysis")
	return a.app.InstallMERT(ctx, directory, NewWailsProgress())
}
func (a *API) InstallRecommendedMERT(ctx context.Context) error {
	ctx, _, finish := a.operations.begin(ctx, "mert-install")
	defer finish()
	a.cancelRecommendationWork()
	a.operations.cancel("enhanced-analysis")
	return a.app.InstallRecommendedMERT(ctx, NewWailsProgress())
}
func (a *API) RemoveMERT() error {
	a.cancelRecommendationWork()
	a.operations.cancel("mert-install")
	a.operations.cancel("enhanced-analysis")
	return a.app.RemoveMERT()
}
func (a *API) ClearEnhancedAnalysis(ctx context.Context) error {
	a.cancelRecommendationWork()
	a.operations.cancel("enhanced-analysis")
	return a.app.ClearEnhancedAnalysis(ctx)
}
func (a *API) AnalyzeEnhancedTracks(ctx context.Context, ids []string, liked bool) (app.EnhancedAnalysisReport, error) {
	ctx, _, finish := a.operations.begin(ctx, "enhanced-analysis")
	defer finish()
	return a.app.AnalyzeEnhancedTracks(ctx, ids, liked, NewWailsProgress())
}
