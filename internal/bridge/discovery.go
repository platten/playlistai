package bridge

import (
	"context"
	"errors"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/discoveryasset"
)

func (a *API) GetDiscoveryAssetStatus() (app.DiscoveryAssetStatus, error) {
	return a.app.GetDiscoveryAssetStatus()
}

type DiscoveryImportResult struct {
	Canceled bool                     `json:"canceled"`
	Status   app.DiscoveryAssetStatus `json:"status"`
}

func (a *API) ChooseDiscoveryPack(ctx context.Context) (DiscoveryImportResult, error) {
	instance := application.Get()
	if instance == nil || instance.Dialog == nil {
		return DiscoveryImportResult{}, errors.New("native file picker is unavailable")
	}
	path, err := instance.Dialog.OpenFile().SetTitle("Use a local music discovery pack").CanChooseFiles(true).CanChooseDirectories(false).AddFilter("Playlist AI packs", "*.paipack").PromptForSingleSelection()
	if err != nil {
		return DiscoveryImportResult{}, err
	}
	if path == "" {
		return DiscoveryImportResult{Canceled: true}, nil
	}
	return a.ImportDiscoveryPack(ctx, path)
}

func (a *API) ImportDiscoveryPack(ctx context.Context, path string) (DiscoveryImportResult, error) {
	ctx, current, finish := a.operations.begin(ctx, "discovery-data")
	defer finish()
	status, err := a.app.ImportDiscoveryAsset(ctx, path, NewWailsProgress())
	if err != nil {
		return DiscoveryImportResult{}, err
	}
	if !current() {
		return DiscoveryImportResult{}, context.Canceled
	}
	a.intentCache.clear()
	return DiscoveryImportResult{Status: status}, nil
}

type DiscoveryArchiveResult struct {
	Canceled bool                         `json:"canceled"`
	Archive  discoveryasset.ArchiveResult `json:"archive"`
}

func (a *API) ChooseDiscoveryArchiveFolder(ctx context.Context) (DiscoveryArchiveResult, error) {
	instance := application.Get()
	if instance == nil || instance.Dialog == nil {
		return DiscoveryArchiveResult{}, errors.New("native folder picker is unavailable")
	}
	path, err := instance.Dialog.OpenFile().SetTitle("Save discovery manifest and download parts").CanChooseFiles(false).CanChooseDirectories(true).PromptForSingleSelection()
	if err != nil {
		return DiscoveryArchiveResult{}, err
	}
	if path == "" {
		return DiscoveryArchiveResult{Canceled: true}, nil
	}
	return a.SaveDiscoveryArchive(ctx, path)
}

func (a *API) SaveDiscoveryArchive(ctx context.Context, parent string) (DiscoveryArchiveResult, error) {
	ctx, current, finish := a.operations.begin(ctx, "discovery-archive")
	defer finish()
	archive, err := a.app.SaveDiscoveryArchive(ctx, parent, NewWailsProgress())
	if err != nil {
		return DiscoveryArchiveResult{}, err
	}
	if !current() {
		return DiscoveryArchiveResult{}, context.Canceled
	}
	return DiscoveryArchiveResult{Archive: archive}, nil
}

func (a *API) CancelDiscoveryArchive() { a.operations.cancel("discovery-archive") }

func (a *API) InstallDiscoveryAsset(ctx context.Context) (app.DiscoveryAssetStatus, error) {
	ctx, current, finish := a.operations.begin(ctx, "discovery-data")
	defer finish()
	status, err := a.app.InstallDiscoveryAsset(ctx, NewWailsProgress())
	if err != nil {
		return status, err
	}
	if !current() {
		return status, context.Canceled
	}
	a.intentCache.clear()
	return status, nil
}

func (a *API) CancelDiscoveryAssetInstall() { a.operations.cancel("discovery-data") }

func (a *API) CheckDiscoveryAssetUpdate(ctx context.Context) (app.DiscoveryAssetUpdate, error) {
	return a.app.CheckDiscoveryAssetUpdate(ctx)
}
