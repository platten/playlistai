package bridge

import "github.com/platten/playlistai/internal/ports"

// Catalog-facing bridge methods. All are safe to call before the catalog is
// loaded; they return zero values or a clear error.

// CatalogInfo is the snapshot the Catalog screen polls.
type CatalogInfo struct {
	Loaded     bool `json:"loaded"`
	TrackCount int  `json:"trackCount"`
	Dim        int  `json:"dim"`
	// Configured reports whether catalog.manifest_url (or a pre-populated
	// catalog.dir) is set at all. This project does not ship or host a
	// catalog itself — see docs/CATALOG.md — so a fresh install is
	// unconfigured until an operator points it at a self-hosted one.
	// DownloadCatalog fails immediately when this is false; the UI should
	// explain that rather than offer a download that's guaranteed to error.
	Configured bool `json:"configured"`
}

// GetCatalogInfo reports whether the embedding catalog is loaded, its size,
// and whether a source is configured at all.
func (a *API) GetCatalogInfo() CatalogInfo {
	if a.app.Catalog == nil {
		return CatalogInfo{Configured: a.app.Config().Catalog.ManifestURL != ""}
	}
	return CatalogInfo{
		Loaded:     true,
		TrackCount: a.app.Catalog.Len(),
		Dim:        a.app.Catalog.Dim(),
		Configured: true,
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
