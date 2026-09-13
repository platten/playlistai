package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	_ "modernc.org/sqlite"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/sqliteuri"
)

// DynamicCatalog overlays a bounded persistent set of preview-resolved tracks
// on the immutable Deej-AI catalog. Its row/vector methods deliberately expose
// only the base catalog so the two identity spaces cannot corrupt dense search.
type DynamicCatalog struct {
	base     ports.Catalog
	resolver ports.ReferenceResolver
	db       *sql.DB
	mu       sync.RWMutex
	tracks   map[string]core.TrackMeta
}

func OpenDynamic(base ports.Catalog, resolver ports.ReferenceResolver, path string) (*DynamicCatalog, error) {
	if base == nil || resolver == nil {
		return nil, errors.New("catalog: dynamic overlay requires a base catalog and resolver")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn, err := sqliteuri.Writable(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	d := &DynamicCatalog{base: base, resolver: resolver, db: db, tracks: map[string]core.TrackMeta{}}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS dynamic_tracks (
		id TEXT PRIMARY KEY, artist TEXT NOT NULL, title TEXT NOT NULL,
		preview_url TEXT NOT NULL, album TEXT NOT NULL DEFAULT '',
		duration_ms INTEGER NOT NULL DEFAULT 0, duration_source TEXT NOT NULL DEFAULT '',
		recording_id TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL
	)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	rows, err := db.Query(`SELECT id, artist, title, preview_url, album, duration_ms, duration_source, recording_id FROM dynamic_tracks`)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, artist, title, preview, album, source, recording string
		var duration int64
		if err := rows.Scan(&id, &artist, &title, &preview, &album, &duration, &source, &recording); err != nil {
			_ = db.Close()
			return nil, err
		}
		meta := core.TrackMeta{Ref: core.TrackRef{ID: id, Artist: artist, Title: title}, PreviewURL: preview, Album: album, AlbumReliable: album != ""}
		if duration > 0 {
			meta.FullRecordingDuration = &core.RecordingDuration{Milliseconds: duration, Source: source, RecordingID: recording}
		}
		d.tracks[id] = meta
	}
	if err := rows.Err(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return d, nil
}

func (d *DynamicCatalog) Close() error { return d.db.Close() }

func (d *DynamicCatalog) RegisterDynamicTrack(meta core.TrackMeta) error {
	id := strings.TrimSpace(meta.Ref.ID)
	if !strings.HasPrefix(id, "deezer:") || strings.TrimSpace(meta.Ref.Artist) == "" || strings.TrimSpace(meta.Ref.Title) == "" || strings.TrimSpace(meta.PreviewURL) == "" {
		return errors.New("catalog: invalid dynamic track")
	}
	var duration int64
	var source, recording string
	if meta.FullRecordingDuration != nil {
		duration, source, recording = meta.FullRecordingDuration.Milliseconds, meta.FullRecordingDuration.Source, meta.FullRecordingDuration.RecordingID
	}
	_, err := d.db.Exec(`INSERT INTO dynamic_tracks(id,artist,title,preview_url,album,duration_ms,duration_source,recording_id,updated_at)
		VALUES(?,?,?,?,?,?,?,?,unixepoch()) ON CONFLICT(id) DO UPDATE SET artist=excluded.artist,title=excluded.title,
		preview_url=excluded.preview_url,album=excluded.album,duration_ms=excluded.duration_ms,
		duration_source=excluded.duration_source,recording_id=excluded.recording_id,updated_at=excluded.updated_at`,
		id, meta.Ref.Artist, meta.Ref.Title, "", meta.Album, duration, source, recording)
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.tracks[id] = meta
	d.mu.Unlock()
	return nil
}

func (d *DynamicCatalog) Len() int                                   { return d.base.Len() }
func (d *DynamicCatalog) Dim() int                                   { return d.base.Dim() }
func (d *DynamicCatalog) ID(row int) string                          { return d.base.ID(row) }
func (d *DynamicCatalog) RowOf(id string) (int, bool)                { return d.base.RowOf(id) }
func (d *DynamicCatalog) VectorsByRow(row int) (ports.Vectors, bool) { return d.base.VectorsByRow(row) }
func (d *DynamicCatalog) Vectors(id string) (ports.Vectors, bool)    { return d.base.Vectors(id) }
func (d *DynamicCatalog) RawRow(row int) ([]int8, []int8, bool)      { return d.base.RawRow(row) }

func (d *DynamicCatalog) Meta(id string) (core.TrackMeta, bool) {
	if meta, ok := d.base.Meta(id); ok {
		return meta, true
	}
	d.mu.RLock()
	meta, ok := d.tracks[id]
	d.mu.RUnlock()
	return meta, ok
}

func (d *DynamicCatalog) Resolve(query string, max int) []core.TrackRef {
	out := d.base.Resolve(query, max)
	if max <= 0 || len(out) >= max {
		return out
	}
	q := core.NormalizeIdentityPart(query)
	d.mu.RLock()
	defer d.mu.RUnlock()
	ids := make([]string, 0, len(d.tracks))
	for id := range d.tracks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		meta := d.tracks[id]
		if q == "" || strings.Contains(core.NormalizeIdentityPart(meta.Ref.Display()), q) {
			out = append(out, meta.Ref)
			if len(out) == max {
				break
			}
		}
	}
	return out
}

func (d *DynamicCatalog) CatalogVersion() string { return d.resolver.CatalogVersion() }

func (d *DynamicCatalog) ResolveReference(ref core.IntentReference) core.ReferenceResolution {
	if strings.HasPrefix(ref.TrackID, "deezer:") {
		if meta, ok := d.Meta(ref.TrackID); ok {
			candidate := core.ResolutionCandidate{Kind: ref.Kind, EntityID: ref.TrackID, Artist: meta.Ref.Artist, Title: meta.Ref.Title, Confidence: 1,
				Evidence:        []core.ResolutionEvidence{{Match: "id", NormalizedQuery: core.NormalizeIdentityPart(ref.Query), MatchedText: meta.Ref.Display()}},
				Representatives: []core.WeightedTrack{{TrackID: ref.TrackID, Weight: 1}}}
			return core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: d.CatalogVersion(), Selected: &candidate}
		}
	}
	return d.resolver.ResolveReference(ref)
}

func (d *DynamicCatalog) ArtistRecordings(ctx context.Context, artist string) ([]core.TrackRef, error) {
	source, ok := d.base.(ports.ArtistRecordingCatalog)
	if !ok {
		return nil, fmt.Errorf("catalog: base does not support artist recordings")
	}
	return source.ArtistRecordings(ctx, artist)
}

var _ ports.Catalog = (*DynamicCatalog)(nil)
var _ ports.ReferenceResolver = (*DynamicCatalog)(nil)
var _ ports.DynamicTrackCatalog = (*DynamicCatalog)(nil)
var _ ports.ArtistRecordingCatalog = (*DynamicCatalog)(nil)
