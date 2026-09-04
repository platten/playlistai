package bridge

// Catalog-facing bridge methods. All are safe to call before the catalog is
// loaded; they return zero values or a clear error.

// CatalogInfo is the snapshot the Catalog screen polls.
type CatalogInfo struct {
	Loaded     bool `json:"loaded"`
	TrackCount int  `json:"trackCount"`
	Dim        int  `json:"dim"`
}

// GetCatalogInfo reports whether the embedding catalog is loaded and its size.
func (a *API) GetCatalogInfo() CatalogInfo {
	if a.app.Catalog == nil {
		return CatalogInfo{}
	}
	return CatalogInfo{
		Loaded:     true,
		TrackCount: a.app.Catalog.Len(),
		Dim:        a.app.Catalog.Dim(),
	}
}

// TrackHit is a catalog search result.
type TrackHit struct {
	ID     string `json:"id"`
	Artist string `json:"artist"`
	Title  string `json:"title"`
}

// SearchCatalog runs a token-substring search over "Artist - Title". Returns an
// empty list when the catalog is not loaded or the query is empty.
func (a *API) SearchCatalog(query string, limit int) []TrackHit {
	if a.app.Catalog == nil {
		return []TrackHit{}
	}
	if limit <= 0 {
		limit = 50
	}
	refs := a.app.Catalog.Resolve(query, limit)
	hits := make([]TrackHit, 0, len(refs))
	for _, r := range refs {
		hits = append(hits, TrackHit{ID: r.ID, Artist: r.Artist, Title: r.Title})
	}
	return hits
}

// DownloadCatalog fetches the catalog (with resume + checksum) and loads it,
// emitting playlistai:progress events under op "catalog". Blocks until done; the
// frontend awaits it while listening for progress. Returns a descriptive error
// if no catalog manifest is configured.
func (a *API) DownloadCatalog() error {
	if a.app.Catalog != nil {
		return nil
	}
	return a.app.EnsureCatalog(a.context(), NewWailsProgress())
}
