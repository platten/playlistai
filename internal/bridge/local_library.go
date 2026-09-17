package bridge

import (
	"context"
	"errors"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	appcore "github.com/platten/playlistai/internal/app"
)

const localLibraryImportOperation = "local-library-import"

type LocalLibraryImportResult struct {
	Canceled bool                       `json:"canceled"`
	Status   appcore.LocalLibraryStatus `json:"status"`
}

func (a *API) GetLocalLibraryStatus() (appcore.LocalLibraryStatus, error) {
	return a.app.LocalLibraryStatus()
}

// ChooseLocalLibraryPack asks for a pack and imports it into app-managed
// storage. Dismissing the native picker is a successful canceled result.
func (a *API) ChooseLocalLibraryPack(ctx context.Context) (LocalLibraryImportResult, error) {
	path, canceled, err := chooseLocalLibraryPath(func() (string, error) {
		appInstance := application.Get()
		if appInstance == nil || appInstance.Dialog == nil {
			return "", errors.New("native file picker is unavailable")
		}
		return appInstance.Dialog.OpenFile().
			SetTitle("Import Playlist AI library pack").
			CanChooseFiles(true).
			CanChooseDirectories(false).
			AddFilter("Playlist AI library packs", "*.paipack").
			PromptForSingleSelection()
	})
	if err != nil {
		return LocalLibraryImportResult{}, err
	}
	if canceled {
		return LocalLibraryImportResult{Canceled: true}, nil
	}
	return a.ImportLocalLibraryPack(ctx, path)
}

func chooseLocalLibraryPath(pick func() (string, error)) (path string, canceled bool, err error) {
	path, err = pick()
	if err != nil {
		return "", false, err
	}
	if path == "" {
		return "", true, nil
	}
	return path, false, nil
}

// ImportLocalLibraryPack is the headless/testable counterpart to the picker.
// The app copies and verifies the pack; it never mutates the selected file.
func (a *API) ImportLocalLibraryPack(ctx context.Context, path string) (LocalLibraryImportResult, error) {
	if strings.TrimSpace(path) == "" {
		return LocalLibraryImportResult{}, errors.New("local library pack path is required")
	}
	ctx, current, finish := a.operations.begin(ctx, localLibraryImportOperation)
	defer finish()
	status, err := a.app.ImportLocalLibrary(ctx, path)
	if err != nil {
		return LocalLibraryImportResult{}, err
	}
	if !current() {
		return LocalLibraryImportResult{}, context.Canceled
	}
	return LocalLibraryImportResult{Status: status}, nil
}

func (a *API) CancelLocalLibraryImport() {
	a.operations.cancel(localLibraryImportOperation)
}

func (a *API) SetLocalLibraryMode(mode string) (appcore.LocalLibraryStatus, error) {
	return a.app.SetLocalLibraryMode(appcore.LocalLibraryMode(mode))
}

func (a *API) SetLocalLibraryRoot(alias, path string) (appcore.LocalLibraryStatus, error) {
	return a.app.SetLocalLibraryRoot(alias, path)
}

func (a *API) RemoveLocalLibrary(ctx context.Context) (appcore.LocalLibraryStatus, error) {
	a.operations.cancel(localLibraryImportOperation)
	return a.app.RemoveLocalLibrary(ctx)
}
