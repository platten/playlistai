package bridge

import (
	"path/filepath"

	"github.com/platten/playlistai/internal/intent/modelmgr"
)

// ModelInfo is one curated catalog entry for the Settings screen.
type ModelInfo struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Params      string `json:"params"`
	Quant       string `json:"quant"`
	SizeApprox  int64  `json:"sizeApprox"`
	LicenseName string `json:"licenseName"`
	LicenseURL  string `json:"licenseUrl"`
	RAMGB       int    `json:"ramGb"`
	Recommended bool   `json:"recommended"`
	// Verified reports whether size + sha256 are pinned in the manifest, so
	// Download checks the file against them rather than trusting the host.
	Verified bool `json:"verified"`
}

// ModelStatus is the active-parser snapshot for the Settings screen.
type ModelStatus struct {
	Backend    string `json:"backend"` // "rules" | "llama"
	Ready      bool   `json:"ready"`
	ModelID    string `json:"modelId"`
	ModelPath  string `json:"modelPath"`
	ModelLabel string `json:"modelLabel"`
}

// GetModelCatalog returns the built-in list of downloadable models.
func (a *API) GetModelCatalog() []ModelInfo {
	src := modelmgr.Catalog()
	out := make([]ModelInfo, 0, len(src))
	for _, m := range src {
		out = append(out, ModelInfo{
			ID: m.ID, Label: m.Label, Params: m.Params, Quant: m.Quant,
			SizeApprox: m.SizeApprox, LicenseName: m.LicenseName, LicenseURL: m.LicenseURL,
			RAMGB: m.RAMGB, Recommended: m.Recommended, Verified: m.Verified(),
		})
	}
	return out
}

// GetModelStatus reports which parser is active and, if it's the local model,
// which one.
func (a *API) GetModelStatus() ModelStatus {
	info := a.app.IntentParser().Info()
	path, id := a.app.CurrentModel()

	label := ""
	if m, ok := modelmgr.Get(id); ok {
		label = m.Label
	} else if path != "" {
		label = filepath.Base(path)
	}

	return ModelStatus{
		Backend:    info.Backend,
		Ready:      info.Ready,
		ModelID:    id,
		ModelPath:  path,
		ModelLabel: label,
	}
}

// DownloadModel fetches a catalog model (progress under op "model") and switches
// to it. Blocks until the model is downloaded and llama-server is healthy.
func (a *API) DownloadModel(id string) error {
	return a.app.DownloadModel(a.context(), id, NewWailsProgress())
}

// UseModelFile points the local parser at a GGUF the user already has.
func (a *API) UseModelFile(path string) error {
	return a.app.SetModel(a.context(), path, "")
}

// ClearModel stops the local model and reverts to the rules parser.
func (a *API) ClearModel() error {
	return a.app.ClearModel()
}
