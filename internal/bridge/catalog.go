package bridge

import "github.com/platten/playlistai/internal/ports"

// Catalog-facing bridge methods. All are safe to call before the catalog is
// loaded; they return zero values or a clear error.

// CatalogInfo is the snapshot the Catalog screen polls.
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

// SimilarResult is the payload for SimilarTracks.
type SimilarResult struct {
	Seed TrackHit   `json:"seed"`
	Hits []TrackHit `json:"hits"`
}

// SimilarTracks returns the catalog tracks most similar to a seed track, blended
// between the two embedding spaces by creativity (0 = playlist co-occurrence,
// 1 = pure audio). The seed itself is excluded. Empty when the catalog or
// similarity engine is not ready.
func (a *API) SimilarTracks(id string, k int, creativity float64) SimilarResult {
	res := SimilarResult{Hits: []TrackHit{}}
	if a.app.Catalog == nil || a.app.Sim == nil {
		return res
	}
	if k <= 0 {
		k = 25
	}
	creativity = clamp01(creativity)

	meta, ok := a.app.Catalog.Meta(id)
	if !ok {
		return res
	}
	res.Seed = TrackHit{ID: id, Artist: meta.Ref.Artist, Title: meta.Ref.Title}

	v, ok := a.app.Catalog.Vectors(id)
	if !ok {
		return res
	}

	matches := a.app.Sim.Search(ports.SimilarityQuery{
		AudioSum: v.Audio,
		TrackSum: v.Track,
		Weights:  [2]float32{float32(creativity), float32(1 - creativity)},
		K:        k + 1,
		Exclude:  map[string]struct{}{id: {}},
	})
	for _, m := range matches {
		mm, ok := a.app.Catalog.Meta(m.ID)
		if !ok {
			continue
		}
		res.Hits = append(res.Hits, TrackHit{ID: m.ID, Artist: mm.Ref.Artist, Title: mm.Ref.Title})
		if len(res.Hits) >= k {
			break
		}
	}
	return res
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
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
