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
	catalog := a.runtime().Catalog
	if catalog == nil {
		return nil, errors.New("catalog not loaded")
	}
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
// window it shows a native Save dialog; otherwise (and when the dialog is
// dismissed with no window) it falls back to <DataDir>/exports/<name>.csv.
func (a *API) ExportCSV(name string, tracks []ExportTrackDTO) (ExportSaveResult, error) {
	exp, ok := a.app.Exporter("csv")
	if !ok {
		return ExportSaveResult{}, errors.New("csv exporter not wired")
	}

	res, err := exp.Export(a.context(), a.exportRequest(name, tracks), NewWailsProgress())
	if err != nil {
		return ExportSaveResult{}, err
	}

	target, canceled, err := a.chooseCSVPath(res.Location)
	if err != nil {
		return ExportSaveResult{}, err
	}
	if canceled {
		return ExportSaveResult{Canceled: true, Count: res.Count}, nil
	}
	// The OS save dialog lets the user type a name with no extension (or the
	// wrong one); make sure what lands on disk is still a .csv.
	target = soundiizcsv.EnsureCSVExt(target)

	if err := os.WriteFile(target, res.Data, 0o644); err != nil {
		return ExportSaveResult{}, fmt.Errorf("write %s: %w", target, err)
	}
	a.log.Info("exported CSV", "path", target, "tracks", res.Count)
	return ExportSaveResult{Path: target, Count: res.Count}, nil
}

// chooseCSVPath asks the OS for a save location, falling back to the data dir.
func (a *API) chooseCSVPath(suggestedName string) (path string, canceled bool, err error) {
	if appInst := application.Get(); appInst != nil && appInst.Dialog != nil {
		chosen, derr := appInst.Dialog.SaveFile().
			SetFilename(suggestedName).
			SetMessage("Save playlist CSV").
			PromptForSingleSelection()
		if derr != nil {
			return "", false, derr
		}
		if chosen == "" {
			return "", true, nil
		}
		return chosen, false, nil
	}

	dir := filepath.Join(a.app.Config().DataDir, "exports")
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return "", false, mkErr
	}
	return filepath.Join(dir, suggestedName), false, nil
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

	opened := true
	if oerr := browser.OpenURL(res.Location); oerr != nil {
		opened = false
		a.log.Warn("soundiiz handoff ready but no browser could be launched", "err", oerr, "url", res.Location)
	}
	a.log.Info("soundiiz handoff ready", "url", res.Location, "tracks", res.Count, "browserOpened", opened)
	return SoundiizHandoffResult{URL: res.Location, Count: res.Count, Opened: opened}, nil
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
