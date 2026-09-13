// Package mbindex builds and reads a compact, offline MusicBrainz discovery index.
package mbindex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/sqliteuri"
)

const IndexVersion = "musicbrainz-offline/v1"

type Info struct {
	Version       string            `json:"version"`
	Snapshot      string            `json:"snapshot"`
	BuiltAt       string            `json:"builtAt"`
	CoreLicense   string            `json:"coreLicense"`
	TagsLicense   string            `json:"tagsLicense"`
	Artists       int64             `json:"artists"`
	Recordings    int64             `json:"recordings"`
	ArtistTags    int64             `json:"artistTags"`
	RecordingTags int64             `json:"recordingTags"`
	Inputs        map[string]string `json:"inputs"`
}

func (i Info) Validate() error {
	if i.Version != IndexVersion || i.Snapshot == "" || i.CoreLicense != "CC0-1.0" || i.TagsLicense != "CC-BY-NC-SA-3.0" || i.Artists <= 0 || i.Recordings <= 0 {
		return errors.New("unsupported or incomplete MusicBrainz index")
	}
	if _, err := time.Parse("20060102-150405", i.Snapshot); err != nil {
		return fmt.Errorf("invalid MusicBrainz snapshot: %w", err)
	}
	return nil
}

type Artist struct {
	MBID    string
	Name    string
	Comment string
	Votes   int
}

type Recording struct {
	MBID             string
	Title            string
	ArtistCredit     string
	DurationMS       int64
	FirstReleaseDate string
	Comment          string
	Artists          []Credit
	ISRCs            []string
	Tags             []Tag
}

type Credit struct {
	Position int
	MBID     string
	Name     string
	Join     string
}

type Tag struct {
	Name  string
	Votes int
}

type Store struct {
	db   *sql.DB
	info Info
}

func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteuri.Path(abs, url.Values{"mode": {"ro"}}))
	if err != nil {
		return nil, err
	}
	var raw string
	if err = db.QueryRow("SELECT value FROM info WHERE key='manifest'").Scan(&raw); err != nil {
		_ = db.Close()
		return nil, err
	}
	var info Info
	if err = json.Unmarshal([]byte(raw), &info); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err = info.Validate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, info: info}, nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Info() Info   { return s.info }

// ArtistsByTag returns artists whose MusicBrainz tags exactly match one of the
// normalized discovery terms. Tags nominate candidates; they do not prove that
// every recording has the requested sound.
func (s *Store) ArtistsByTag(ctx context.Context, terms []string, limit int, offset int) ([]Artist, error) {
	limit = min(1000, max(1, limit))
	if offset < 0 {
		offset = 0
	}
	keys := make([]string, 0, len(terms))
	seen := map[string]bool{}
	for _, term := range terms {
		key := core.NormalizeIdentityPart(term)
		if key != "" && !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	if len(keys) == 0 {
		return nil, nil
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
	args := make([]any, 0, len(keys)+2)
	for _, key := range keys {
		args = append(args, key)
	}
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, `SELECT a.mbid,a.name,a.comment,SUM(t.votes) FROM artist_tags t JOIN artists a ON a.mbid=t.artist_mbid WHERE t.tag_key IN (`+marks+`) GROUP BY a.mbid,a.name,a.comment ORDER BY SUM(t.votes) DESC,a.mbid LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Artist
	for rows.Next() {
		var a Artist
		if err := rows.Scan(&a.MBID, &a.Name, &a.Comment, &a.Votes); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) ArtistRecordings(ctx context.Context, artistMBID string, limit, offset int) ([]Recording, error) {
	limit = min(1000, max(1, limit))
	offset = max(0, offset)
	rows, err := s.db.QueryContext(ctx, `SELECT r.mbid,r.title,r.artist_credit,r.duration_ms,r.first_release_date,r.comment FROM recording_artists ra JOIN recordings r ON r.mbid=ra.recording_mbid WHERE ra.artist_mbid=? ORDER BY r.mbid LIMIT ? OFFSET ?`, artistMBID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recording
	for rows.Next() {
		var r Recording
		if err := rows.Scan(&r.MBID, &r.Title, &r.ArtistCredit, &r.DurationMS, &r.FirstReleaseDate, &r.Comment); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.loadRecordingDetails(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) FindRecordings(ctx context.Context, artist, title string, limit int) ([]Recording, error) {
	limit = min(100, max(1, limit))
	artistKey := core.NormalizeIdentityPart(artist)
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT r.mbid,r.title,r.artist_credit,r.duration_ms,r.first_release_date,r.comment FROM recordings r JOIN recording_artists ra ON ra.recording_mbid=r.mbid JOIN artists a ON a.mbid=ra.artist_mbid WHERE r.title_key=? AND (a.name_key=? OR ra.name_key=? OR EXISTS(SELECT 1 FROM artist_aliases aa WHERE aa.artist_mbid=a.mbid AND aa.name_key=?)) ORDER BY r.mbid LIMIT ?`, core.NormalizeIdentityPart(title), artistKey, artistKey, artistKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recording
	for rows.Next() {
		var r Recording
		if err := rows.Scan(&r.MBID, &r.Title, &r.ArtistCredit, &r.DurationMS, &r.FirstReleaseDate, &r.Comment); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.loadRecordingDetails(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) loadRecordingDetails(ctx context.Context, r *Recording) error {
	rows, err := s.db.QueryContext(ctx, `SELECT position,artist_mbid,name,join_phrase FROM recording_artists WHERE recording_mbid=? ORDER BY position`, r.MBID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var c Credit
		if err := rows.Scan(&c.Position, &c.MBID, &c.Name, &c.Join); err != nil {
			rows.Close()
			return err
		}
		r.Artists = append(r.Artists, c)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT isrc FROM recording_isrcs WHERE recording_mbid=? ORDER BY isrc`, r.MBID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			rows.Close()
			return err
		}
		r.ISRCs = append(r.ISRCs, value)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT tag,votes FROM recording_tags WHERE recording_mbid=? ORDER BY votes DESC,tag`, r.MBID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var tag Tag
		if err := rows.Scan(&tag.Name, &tag.Votes); err != nil {
			return err
		}
		r.Tags = append(r.Tags, tag)
	}
	return rows.Err()
}
