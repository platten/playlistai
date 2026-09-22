package bridge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/platten/playlistai/internal/browser"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/export/soundiizcsv"
	"github.com/platten/playlistai/internal/export/soundiizhandoff"
	"github.com/platten/playlistai/internal/ports"
)

// ExportTrackDTO contains the local track details shown for export.
type ExportTrackDTO struct {
	ID     string `json:"id"`
	Artist string `json:"artist"`
	Title  string `json:"title"`
	Album  string `json:"album"`
}

// PrepareExport reads local track details without enrichment or network access.
// Preserve playlist order and duplicates; unknown IDs are skipped.
func (a *API) PrepareExport(trackIDs []string) ([]ExportTrackDTO, error) {
	ctx, release := a.app.OperationContext(a.context())
	defer release()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runtime := a.runtime()
	if runtime.Catalog == nil {
		return nil, errors.New("catalog not loaded")
	}
	catalog, releaseCatalog, err := a.app.PinFeedbackCatalogFor(ctx, runtime)
	if err != nil {
		// Preserve base-catalog export if an optional local generation is
		// temporarily unreadable. Its IDs remain honest unknowns and are skipped.
		catalog, releaseCatalog = runtime.Catalog, func() {}
	}
	defer releaseCatalog()
	out := make([]ExportTrackDTO, 0, len(trackIDs))
	for _, id := range trackIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if m, ok := catalog.Meta(id); ok {
			out = append(out, ExportTrackDTO{ID: m.Ref.ID, Artist: m.Ref.Artist, Title: m.Ref.Title, Album: m.Album})
		}
	}
	return out, nil
}

func (a *API) exportRequest(name string, tracks []ExportTrackDTO) ports.ExportRequest {
	req := ports.ExportRequest{Name: name, Tracks: make([]core.EnrichedTrack, 0, len(tracks))}
	for _, t := range tracks {
		req.Tracks = append(req.Tracks, core.EnrichedTrack{Ref: core.TrackRef{ID: t.ID, Artist: t.Artist, Title: t.Title}, Album: t.Album})
	}
	return req
}

// ExportSaveResult reports where a file export landed.
type ExportSaveResult struct {
	Path     string `json:"path"`     // absolute path the CSV was written to
	Count    int    `json:"count"`    // tracks written
	Canceled bool   `json:"canceled"` // user dismissed the save dialog
}

// ExportCSV writes the playlist as a Soundiiz-compatible CSV. When the app has a
// window it shows a native Save dialog; without one it falls back to
// <DataDir>/exports/<name>.csv without overwriting an existing file.
func (a *API) ExportCSV(name string, tracks []ExportTrackDTO) (ExportSaveResult, error) {
	exp, ok := a.app.Exporter("csv")
	if !ok {
		return ExportSaveResult{}, errors.New("csv exporter not wired")
	}

	res, err := exp.Export(a.context(), a.exportRequest(name, tracks), NewWailsProgress())
	if err != nil {
		return ExportSaveResult{}, err
	}

	target, canceled, overwrite, err := a.chooseCSVPath(res.Location)
	if err != nil {
		return ExportSaveResult{}, err
	}
	if canceled {
		return ExportSaveResult{Canceled: true, Count: res.Count}, nil
	}
	if err := writeCSVFile(target, res.Data, overwrite); err != nil {
		return ExportSaveResult{}, fmt.Errorf("write %s: %w", target, err)
	}
	a.log.Info("exported CSV", "path", target, "tracks", res.Count)
	return ExportSaveResult{Path: target, Count: res.Count}, nil
}

// chooseCSVPath asks the OS for a save location, falling back to the data dir.
func (a *API) chooseCSVPath(suggestedName string) (path string, canceled, overwrite bool, err error) {
	if appInst := application.Get(); appInst != nil && appInst.Dialog != nil {
		return chooseCSVTarget(suggestedName, func(name, dir, message string) (string, error) {
			return appInst.Dialog.SaveFile().SetFilename(name).SetDirectory(dir).
				AddFilter("CSV playlists", "*.csv").SetMessage(message).PromptForSingleSelection()
		})
	}

	dir := filepath.Join(a.app.Config().DataDir, "exports")
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return "", false, false, mkErr
	}
	// Without a native confirmation the publication always uses no-replace.
	return filepath.Join(dir, soundiizcsv.EnsureCSVExt(suggestedName)), false, false, nil
}

// Wails does not report whether its native picker actually asked to overwrite.
// Any existing destination therefore needs another prompt after we observe it;
// only the same exact path and file identity can inherit that confirmation.
// Headless exports never overwrite an existing file without a dialog.
func chooseCSVTarget(name string, pick func(name, directory, message string) (string, error)) (path string, canceled, overwrite bool, err error) {
	dir, message := "", "Save playlist CSV"
	var expectedTarget string
	var expectedFile os.FileInfo
	for attempts := 0; attempts < 10; attempts++ {
		selected, err := pick(name, dir, message)
		if err != nil {
			return "", false, false, err
		}
		if selected == "" {
			return "", true, false, nil
		}
		target := soundiizcsv.EnsureCSVExt(selected)
		info, statErr := csvTargetIdentity(target)
		if os.IsNotExist(statErr) {
			return target, false, false, nil
		}
		if statErr != nil {
			return "", false, false, statErr
		}
		if selected == target && target == expectedTarget && expectedFile != nil && os.SameFile(expectedFile, info) {
			return target, false, true, nil
		}
		// A file may have appeared after the picker accepted an absent path.
		// Observe its identity before using the native overwrite/close handling
		// for a second prompt. Normalization or a changed identity needs the same
		// check; message-dialog close callbacks vary by platform.
		expectedTarget, expectedFile = target, info
		name, dir = filepath.Base(target), filepath.Dir(target)
		message = "The CSV destination already exists. Confirm this filename to replace it, or choose another name."
	}
	return "", false, false, errors.New("the CSV destination kept changing; choose a new filename and try again")
}

func csvTargetIdentity(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("CSV destination must be a regular file")
	}
	// On Windows Lstat's identity is resolved lazily by SameFile using its
	// saved path. Stat on an open handle captures the current file ID now,
	// before a subsequent picker can replace the file at that path.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("CSV destination must be a regular file")
	}
	return info, err
}

// Write and sync the complete export before publishing it. The platform helper
// refuses to replace an unconfirmed destination, including a file created after
// the dialog closes. Rename replaces only an explicitly approved file.
func writeCSVFile(target string, data []byte, overwrite bool) error {
	return publishCSVFile(target, data, overwrite, os.Rename)
}

func publishCSVFile(target string, data []byte, overwrite bool, replace func(string, string) error) error {
	file, err := os.CreateTemp(filepath.Dir(target), ".playlist-export-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if overwrite {
		return replace(file.Name(), target)
	}
	return publishCSVNoReplace(file.Name(), target)
}

// SoundiizHandoffResult is the outcome of OpenSoundiizHandoff.
type SoundiizHandoffResult struct {
	URL    string `json:"url"`    // validated Soundiiz share URL
	Count  int    `json:"count"`  // tracks handed off
	Opened bool   `json:"opened"` // a browser was launched; if false, the UI must show the URL to copy
}

// OpenSoundiizHandoff posts the playlist to Soundiiz's tokenless import endpoint,
// validates the returned share URL, opens it in the user's browser, and returns
// it. Only the playlist name and the track/artist names leave the machine.
func (a *API) OpenSoundiizHandoff(name string, tracks []ExportTrackDTO) (SoundiizHandoffResult, error) {
	exp, ok := a.app.Exporter("soundiiz-handoff")
	if !ok {
		return SoundiizHandoffResult{}, errors.New("soundiiz exporter not wired")
	}

	res, err := exp.Export(a.context(), a.exportRequest(name, tracks), NewWailsProgress())
	if err != nil {
		return SoundiizHandoffResult{}, err
	}

	return a.presentSoundiizHandoff(res, browser.OpenURL), nil
}

func (a *API) presentSoundiizHandoff(res ports.ExportResult, open func(string) error) SoundiizHandoffResult {
	opened := true
	if oerr := open(res.Location); oerr != nil {
		opened = false
		// Opener errors can contain the full URL. Keep the share capability out
		// of ordinary logs; it is returned only to the requesting export UI.
		a.log.Warn("soundiiz handoff ready but no browser could be launched")
	}
	a.log.Info("soundiiz handoff ready", "tracks", res.Count, "browserOpened", opened)
	return SoundiizHandoffResult{URL: res.Location, Count: res.Count, Opened: opened}
}

// OpenExternalURL re-opens an already-issued Soundiiz share URL in the browser.
// It re-validates against the fixed Soundiiz import prefix so the frontend can
// never drive the system opener with an arbitrary URL.
func (a *API) OpenExternalURL(raw string) error {
	if !strings.HasPrefix(raw, soundiizhandoff.SharePrefix) {
		return errors.New("refusing to open a non-Soundiiz URL")
	}
	return browser.OpenURL(raw)
}
