package bridge

// PreviewResult is the outcome of GetPreviewURL. A miss (Available == false) is
// normal — many tracks have no preview anywhere — and is never an error.
type PreviewResult struct {
	URL       string `json:"url"`
	Available bool   `json:"available"`
}

// GetPreviewURL resolves a playable ~30s preview for a catalog track id, per
// the configured preview.provider. Returns a zero, unavailable result (no
// error) whenever preview is off, the catalog isn't loaded, or the id is
// unknown — the UI just hides the play control in that case.
func (a *API) GetPreviewURL(id string) (PreviewResult, error) {
	provider := a.app.PreviewProvider()
	if provider == nil || a.app.Catalog == nil {
		return PreviewResult{}, nil
	}
	meta, ok := a.app.Catalog.Meta(id)
	if !ok {
		return PreviewResult{}, nil
	}

	url, ok, err := provider.PreviewURL(a.context(), meta.Ref, meta.PreviewURL)
	if err != nil {
		return PreviewResult{}, err
	}
	return PreviewResult{URL: url, Available: ok}, nil
}

// GetPreviewProviderName returns the active preview backend's name
// ("deezer" | "spotify" | "off").
func (a *API) GetPreviewProviderName() string {
	return a.app.PreviewProviderName()
}

// SetPreviewProvider switches the preview backend and persists the choice.
func (a *API) SetPreviewProvider(provider string) error {
	return a.app.SetPreviewProvider(provider)
}
