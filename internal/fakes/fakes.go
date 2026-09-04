// Package fakes provides in-memory implementations of the ports interfaces for
// use in tests. They are deterministic and have no external dependencies.
package fakes

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// ---------------------------------------------------------------------------
// Catalog
// ---------------------------------------------------------------------------

// Catalog is a small map-backed ports.Catalog. Construct with NewCatalog.
type Catalog struct {
	dim   int
	ids   []string
	rowOf map[string]int
	meta  map[string]core.TrackMeta
	vecs  map[string]ports.Vectors
}

// CatalogTrack is one row to seed a fake Catalog with.
type CatalogTrack struct {
	ID         string
	Display    string // "Artist - Title"
	PreviewURL string
	Audio      []float32
	Track      []float32
}

// NewCatalog builds a Catalog from rows. All Audio/Track slices must have length
// dim.
func NewCatalog(dim int, rows ...CatalogTrack) *Catalog {
	c := &Catalog{
		dim:   dim,
		rowOf: make(map[string]int, len(rows)),
		meta:  make(map[string]core.TrackMeta, len(rows)),
		vecs:  make(map[string]ports.Vectors, len(rows)),
	}
	for i, r := range rows {
		c.ids = append(c.ids, r.ID)
		c.rowOf[r.ID] = i
		c.meta[r.ID] = core.TrackMeta{
			Ref:        core.ParseDisplay(r.ID, r.Display),
			PreviewURL: r.PreviewURL,
		}
		c.vecs[r.ID] = ports.Vectors{Audio: r.Audio, Track: r.Track}
	}
	return c
}

func (c *Catalog) Len() int { return len(c.ids) }
func (c *Catalog) Dim() int { return c.dim }

func (c *Catalog) ID(row int) string {
	if row < 0 || row >= len(c.ids) {
		return ""
	}
	return c.ids[row]
}

func (c *Catalog) RowOf(id string) (int, bool) {
	r, ok := c.rowOf[id]
	return r, ok
}

func (c *Catalog) Meta(id string) (core.TrackMeta, bool) {
	m, ok := c.meta[id]
	return m, ok
}

func (c *Catalog) VectorsByRow(row int) (ports.Vectors, bool) {
	if row < 0 || row >= len(c.ids) {
		return ports.Vectors{}, false
	}
	return c.Vectors(c.ids[row])
}

func (c *Catalog) Vectors(id string) (ports.Vectors, bool) {
	v, ok := c.vecs[id]
	return v, ok
}

// Resolve does a naive case-insensitive substring match over the display string.
func (c *Catalog) Resolve(query string, max int) []core.TrackRef {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []core.TrackRef
	for _, id := range c.ids {
		ref := c.meta[id].Ref
		if q == "" || strings.Contains(strings.ToLower(ref.Display()), q) {
			out = append(out, ref)
			if max > 0 && len(out) >= max {
				break
			}
		}
	}
	return out
}

var _ ports.Catalog = (*Catalog)(nil)

// ---------------------------------------------------------------------------
// IntentParser
// ---------------------------------------------------------------------------

// IntentParser returns a fixed intent (or one produced by Fn) and echoes the
// prompt into Seeds.Queries when neither is set.
type IntentParser struct {
	Fixed core.MusicIntent
	Fn    func(ports.IntentInput) core.MusicIntent
	Err   error
	Meta  ports.ParserInfo
}

func (p *IntentParser) Parse(_ context.Context, in ports.IntentInput) (core.MusicIntent, error) {
	if p.Err != nil {
		return core.MusicIntent{}, p.Err
	}
	if p.Fn != nil {
		return p.Fn(in).Normalized(), nil
	}
	if p.Fixed.Count != 0 || len(p.Fixed.Seeds.Queries) != 0 || len(p.Fixed.Seeds.TrackIDs) != 0 {
		return p.Fixed.Normalized(), nil
	}
	return core.MusicIntent{Seeds: core.IntentSeeds{Queries: []string{in.Prompt}}}.Normalized(), nil
}

func (p *IntentParser) Info() ports.ParserInfo {
	if p.Meta.Name != "" {
		return p.Meta
	}
	return ports.ParserInfo{Name: "fake", Backend: "rules", Ready: true}
}

var _ ports.IntentParser = (*IntentParser)(nil)

// ---------------------------------------------------------------------------
// SimilarityEngine
// ---------------------------------------------------------------------------

// SimilarityEngine ranks a fixed set of tracks by blended cosine similarity to
// the query. Build with NewSimilarityEngine from a fake Catalog.
type SimilarityEngine struct {
	ids   []string
	audio [][]float32
	track [][]float32
}

// NewSimilarityEngine snapshots every track's vectors from c.
func NewSimilarityEngine(c *Catalog) *SimilarityEngine {
	s := &SimilarityEngine{}
	for _, id := range c.ids {
		v := c.vecs[id]
		s.ids = append(s.ids, id)
		s.audio = append(s.audio, v.Audio)
		s.track = append(s.track, v.Track)
	}
	return s
}

func (s *SimilarityEngine) Len() int { return len(s.ids) }

func (s *SimilarityEngine) Search(q ports.SimilarityQuery) []ports.Match {
	matches := make([]ports.Match, 0, len(s.ids))
	for i, id := range s.ids {
		if _, skip := q.Exclude[id]; skip {
			continue
		}
		score := q.Weights[0]*cosine(q.AudioSum, s.audio[i]) + q.Weights[1]*cosine(q.TrackSum, s.track[i])
		matches = append(matches, ports.Match{ID: id, Row: i, Score: score})
	}
	sort.SliceStable(matches, func(a, b int) bool { return matches[a].Score > matches[b].Score })
	if q.K > 0 && len(matches) > q.K {
		matches = matches[:q.K]
	}
	return matches
}

func cosine(a, b []float32) float32 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float32
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (sqrt32(na) * sqrt32(nb))
}

var _ ports.SimilarityEngine = (*SimilarityEngine)(nil)

// ---------------------------------------------------------------------------
// RecommendationEngine
// ---------------------------------------------------------------------------

// RecommendationEngine returns Playlist (or Err). If neither is set it resolves
// the intent's first query against Catalog and returns up to intent.Count of
// those tracks.
type RecommendationEngine struct {
	Playlist core.Playlist
	Err      error
	Catalog  *Catalog
}

func (r *RecommendationEngine) Build(_ context.Context, intent core.MusicIntent) (core.Playlist, error) {
	if r.Err != nil {
		return core.Playlist{}, r.Err
	}
	if len(r.Playlist.Tracks) != 0 {
		return r.Playlist, nil
	}
	intent = intent.Normalized()
	if r.Catalog == nil || (len(intent.Seeds.Queries) == 0 && len(intent.Seeds.TrackIDs) == 0) {
		return core.Playlist{}, core.ErrNoSeeds
	}
	var refs []core.TrackRef
	for _, id := range intent.Seeds.TrackIDs {
		if m, ok := r.Catalog.Meta(id); ok {
			refs = append(refs, m.Ref)
		}
	}
	for _, q := range intent.Seeds.Queries {
		refs = append(refs, r.Catalog.Resolve(q, intent.Count)...)
	}
	if len(refs) == 0 {
		return core.Playlist{}, core.ErrNoSeeds
	}
	if len(refs) > intent.Count {
		refs = refs[:intent.Count]
	}
	return core.Playlist{Tracks: refs, Mode: intent.Mode, Intent: intent}, nil
}

var _ ports.RecommendationEngine = (*RecommendationEngine)(nil)

// ---------------------------------------------------------------------------
// Enricher
// ---------------------------------------------------------------------------

// Enricher marks every track Matched with a synthetic ISRC and reports progress.
type Enricher struct {
	Err error
}

func (e *Enricher) Enrich(_ context.Context, refs []core.TrackRef, p ports.Progress) ([]core.EnrichedTrack, error) {
	if e.Err != nil {
		return nil, e.Err
	}
	if p == nil {
		p = ports.NopProgress{}
	}
	out := make([]core.EnrichedTrack, 0, len(refs))
	for i, r := range refs {
		out = append(out, core.EnrichedTrack{
			Ref:        r,
			Matched:    true,
			ISRC:       "FAKE0000000" + pad4(i),
			MatchScore: 100,
		})
		p.Report("enrich", int64(i+1), int64(len(refs)), r.Display())
	}
	return out, nil
}

func (e *Enricher) Name() string { return "fake" }

var _ ports.Enricher = (*Enricher)(nil)

// ---------------------------------------------------------------------------
// Exporter
// ---------------------------------------------------------------------------

// Exporter records the last request and returns a canned result.
type Exporter struct {
	Last    ports.ExportRequest
	Result  ports.ExportResult
	Err     error
	IsReady bool
}

func (x *Exporter) Export(_ context.Context, req ports.ExportRequest, p ports.Progress) (ports.ExportResult, error) {
	x.Last = req
	if x.Err != nil {
		return ports.ExportResult{}, x.Err
	}
	if p == nil {
		p = ports.NopProgress{}
	}
	p.Report("export", int64(len(req.Tracks)), int64(len(req.Tracks)), req.Name)
	res := x.Result
	if res.Count == 0 {
		res.Count = len(req.Tracks)
	}
	if res.Kind == "" {
		res.Kind = "csv"
	}
	return res, nil
}

func (x *Exporter) Name() string    { return "fake" }
func (x *Exporter) Available() bool { return x.IsReady }

var _ ports.Exporter = (*Exporter)(nil)

// ---------------------------------------------------------------------------
// PreviewProvider
// ---------------------------------------------------------------------------

// PreviewProvider returns URL for every track unless Miss is true, in which case
// it falls back to the bundled URL.
type PreviewProvider struct {
	URL  string
	Miss bool
	Err  error
}

func (pp *PreviewProvider) PreviewURL(_ context.Context, _ core.TrackRef, bundledURL string) (string, bool, error) {
	if pp.Err != nil {
		return "", false, pp.Err
	}
	if pp.Miss {
		if bundledURL != "" {
			return bundledURL, true, nil
		}
		return "", false, nil
	}
	if pp.URL != "" {
		return pp.URL, true, nil
	}
	return "https://example.invalid/preview.mp3", true, nil
}

func (pp *PreviewProvider) Name() string { return "fake" }

var _ ports.PreviewProvider = (*PreviewProvider)(nil)

// ---------------------------------------------------------------------------
// Progress
// ---------------------------------------------------------------------------

// RecordingProgress captures every Report call for assertions.
type RecordingProgress struct {
	mu   sync.Mutex
	Rows []ProgressRow
}

// ProgressRow is one captured Report.
type ProgressRow struct {
	Op    string
	Done  int64
	Total int64
	Note  string
}

func (r *RecordingProgress) Report(op string, done, total int64, note string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Rows = append(r.Rows, ProgressRow{Op: op, Done: done, Total: total, Note: note})
}

// Snapshot returns a copy of the captured rows.
func (r *RecordingProgress) Snapshot() []ProgressRow {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ProgressRow, len(r.Rows))
	copy(out, r.Rows)
	return out
}

var _ ports.Progress = (*RecordingProgress)(nil)

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

func sqrt32(x float32) float32 {
	if x <= 0 {
		return 0
	}
	return float32(math.Sqrt(float64(x)))
}

func pad4(i int) string {
	const digits = "0123456789"
	b := []byte("0000")
	for p := 3; p >= 0 && i > 0; p-- {
		b[p] = digits[i%10]
		i /= 10
	}
	return string(b)
}
