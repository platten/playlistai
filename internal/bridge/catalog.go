package bridge

// Catalog-facing bridge methods. All are safe to call before the catalog is
// loaded; they return zero values or a clear error.

// CatalogInfo reports local recommendation data availability during setup.
type CatalogInfo struct {
	Loaded     bool `json:"loaded"`
	TrackCount int  `json:"trackCount"`
	Dim        int  `json:"dim"`
	// Configured reports whether any catalog source exists: a local archive
	// (Bundled), catalog.archive_url, catalog.manifest_url, or a pre-populated
	// catalog.dir. DownloadCatalog fails immediately when this is false.
	Configured bool `json:"configured"`
	// Bundled reports that a real catalog.tar.zst is staged next to the app —
	// setup is a local decompress, no download.
	Bundled bool `json:"bundled"`
	// AutoSetup reports that EnsureCatalog can get the catalog with no user
	// action (a bundled archive, or catalog.archive_url is set) — the
	// first-run gate runs it automatically on launch. When false but
	// Configured is true (manifest_url only), the user triggers it with a button.
	AutoSetup bool `json:"autoSetup"`
}

// GetCatalogInfo reports whether the embedding catalog is loaded, its size,
// and whether/how a source is configured.
func (a *API) GetCatalogInfo() CatalogInfo {
	bundled := a.app.CatalogBundled()
	cat := a.app.Config().Catalog
	hasArchive := cat.ArchiveURL != ""
	autoSetup := bundled || hasArchive

	if a.app.Catalog == nil {
		return CatalogInfo{
			Configured: autoSetup || cat.ManifestURL != "",
			Bundled:    bundled,
			AutoSetup:  autoSetup,
		}
	}
	return CatalogInfo{
		Loaded:     true,
		TrackCount: a.app.Catalog.Len(),
		Dim:        a.app.Catalog.Dim(),
		Configured: true,
		Bundled:    bundled,
		AutoSetup:  autoSetup,
	}
}

// DownloadCatalog gets the catalog onto disk and loads it, emitting
// playlistai:progress events under op "catalog". Blocks until done; the
// frontend awaits it while listening for progress. Uses whichever source is
// configured (bundled archive → catalog.archive_url download → manifest_url);
// see app.Container.EnsureCatalog. Returns a descriptive error if none is set.
func (a *API) DownloadCatalog() error {
	if a.app.Catalog != nil {
		return nil
	}
	return a.app.EnsureCatalog(a.context(), NewWailsProgress())
}
