package bridge

import (
	"context"

	"github.com/platten/playlistai/internal/app"
)

func (a *API) GetIntentAssistStatus() app.IntentAssistStatus { return a.app.GetIntentAssistStatus() }

func (a *API) InstallIntentModels(ctx context.Context) error {
	ctx, _, finish := a.operations.begin(ctx, "intent-models")
	defer finish()
	a.cancelRecommendationWork()
	return a.app.InstallIntentModels(ctx, NewWailsProgress())
}

func (a *API) SetIntentAssistEnabled(enabled bool) error {
	a.cancelRecommendationWork()
	return a.app.SetIntentAssistEnabled(enabled)
}

func (a *API) InstallIntentExtractor(ctx context.Context, directory string) error {
	ctx, _, finish := a.operations.begin(ctx, "intent-extractor")
	defer finish()
	a.cancelRecommendationWork()
	return a.app.InstallIntentExtractor(ctx, directory)
}
