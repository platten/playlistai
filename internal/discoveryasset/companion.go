package discoveryasset

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/sqliteuri"
)

var musicalTags = map[string]string{
	"genre": "genre", "style": "style", "mood": "mood", "bpm": "tempo", "tbpm": "tempo", "fbpm": "tempo",
	"key": "key", "tkey": "key", "initialkey": "key", "language": "language", "language_2_letter": "language",
	"date": "edition_date", "year": "edition_date", "originaldate": "original_release_date", "originalreleasedate": "original_release_date", "original_release_date": "original_release_date",
	"artist": "artist_credit", "artists": "artist_credit", "albumartist": "album_artist", "album_artist": "album_artist", "composer": "composer", "work": "work", "movement": "movement",
	"track": "track_number", "tracknumber": "track_number", "disc": "disc_number", "discnumber": "disc_number", "releasetype": "release_type", "musicbrainz_album_type": "release_type",
	"musicbrainz_artistid": "artist_mbid", "musicbrainz_albumartistid": "album_artist_mbid", "musicbrainz_albumid": "album_mbid", "musicbrainz_releasegroupid": "release_group_mbid",
}

func tagKey(s string) string {
	return strings.ToLower(strings.NewReplacer(" ", "_", "-", "_").Replace(strings.TrimSpace(s)))
}
func safeTags(raw json.RawMessage) map[string][]string {
	var input map[string]json.RawMessage
	out := map[string][]string{}
	if json.Unmarshal(raw, &input) != nil {
		return out
	}
	for k, v := range input {
		if musicalTags[tagKey(k)] == "" {
			continue
		}
		var value string
		var values []string
		if json.Unmarshal(v, &value) == nil {
			values = []string{value}
		} else if json.Unmarshal(v, &values) != nil {
			continue
		}
		for _, value = range values {
			if strings.TrimSpace(value) != "" {
				out[k] = append(out[k], value)
			}
		}
	}
	return out
}
func publicTags(raw json.RawMessage) json.RawMessage {
	tags := map[string]any{}
	for k, v := range safeTags(raw) {
		if len(v) == 1 {
			tags[k] = v[0]
		} else {
			tags[k] = v
		}
	}
	b, _ := json.Marshal(tags)
	return b
}
func createCompanion(ctx context.Context, path string, m Manifest, packs [][]librarypack.Track) error {
	return createCompanionSources(ctx, path, m, func(i int, yield func(librarypack.Track) error) error {
		for _, t := range packs[i] {
			if e := yield(t); e != nil {
				return e
			}
		}
		return nil
	})
}

// createCompanionSources streams annotations; full vector/DSP payloads never
// accumulate in memory when activating an uncurated hosted or local pack.
func createCompanionSources(ctx context.Context, path string, m Manifest, next func(int, func(librarypack.Track) error) error) error {
	abs, e := filepath.Abs(path)
	if e != nil {
		return e
	}
	db, e := sql.Open("sqlite", sqliteuri.Path(abs, url.Values{}))
	if e != nil {
		return e
	}
	defer db.Close()
	_, e = db.ExecContext(ctx, `PRAGMA journal_mode=DELETE; PRAGMA synchronous=FULL; PRAGMA user_version=1;
CREATE TABLE packs(pack_id TEXT PRIMARY KEY,sha256 TEXT NOT NULL);
CREATE TABLE recordings(pack_id TEXT NOT NULL,track_id TEXT NOT NULL,artist TEXT NOT NULL,album TEXT NOT NULL,original_year INTEGER,PRIMARY KEY(pack_id,track_id));
CREATE TABLE annotations(pack_id TEXT NOT NULL,track_id TEXT NOT NULL,kind TEXT NOT NULL,value TEXT NOT NULL,source_key TEXT NOT NULL,origin TEXT NOT NULL);
CREATE INDEX annotations_lookup ON annotations(kind,value,pack_id,track_id);
CREATE TABLE profiles(pack_id TEXT NOT NULL,artist TEXT NOT NULL,album TEXT NOT NULL,period TEXT NOT NULL,kind TEXT NOT NULL,value TEXT NOT NULL,recordings INTEGER NOT NULL,albums INTEGER NOT NULL);`)
	if e != nil {
		return e
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback() }()
	for i := range m.Packs {
		f := m.Packs[i]
		if _, e = tx.ExecContext(ctx, "INSERT INTO packs VALUES(?,?)", f.PackID, f.SHA256); e != nil {
			return e
		}
		e = next(i, func(t librarypack.Track) error {
			tags := safeTags(t.RawTags)
			keys := make([]string, 0, len(tags))
			for k := range tags {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			var year any
			years := map[int]bool{}
			for _, k := range keys {
				for _, v := range tags[k] {
					if musicalTags[tagKey(k)] == "original_release_date" && len(v) >= 4 {
						if y, err := strconv.Atoi(v[:4]); err == nil && y >= 1000 && y <= 2100 {
							years[y] = true
						}
					}
				}
			}
			if len(years) == 1 {
				for y := range years {
					year = y
				}
			}
			if _, e = tx.ExecContext(ctx, "INSERT INTO recordings VALUES(?,?,?,?,?)", f.PackID, t.ID, t.Artist, t.Album, year); e != nil {
				return e
			}
			for _, k := range keys {
				for _, v := range tags[k] {
					if _, e = tx.ExecContext(ctx, "INSERT INTO annotations VALUES(?,?,?,?,?,?)", f.PackID, t.ID, musicalTags[tagKey(k)], strings.ToLower(strings.TrimSpace(v)), k, "embedded_tag"); e != nil {
						return e
					}
				}
			}
			return nil
		})
		if e != nil {
			return e
		}
	}
	// Artist-wide, album-specific, and reliably original-dated period distributions.
	for _, scope := range []struct{ album, period, filter string }{{"''", "''", ""}, {"r.album", "''", " AND r.album<>''"}, {"''", "CAST((r.original_year/10)*10 AS TEXT)", " AND r.original_year IS NOT NULL"}} {
		_, e = tx.ExecContext(ctx, `INSERT INTO profiles SELECT r.pack_id,r.artist,`+scope.album+`,`+scope.period+`,a.kind,a.value,COUNT(DISTINCT r.track_id),COUNT(DISTINCT NULLIF(r.album,'')) FROM recordings r JOIN annotations a USING(pack_id,track_id) WHERE a.kind IN ('genre','style','mood','language')`+scope.filter+` GROUP BY r.pack_id,r.artist,`+scope.album+`,`+scope.period+`,a.kind,a.value`)
		if e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, "CREATE INDEX profiles_lookup ON profiles(pack_id,artist,album,period)"); e != nil {
		return e
	}
	return tx.Commit()
}
func verifyCompanion(ctx context.Context, path string, m Manifest) error {
	u, e := sqliteuri.ReadOnly(path, true)
	if e != nil {
		return e
	}
	db, e := sql.Open("sqlite", u)
	if e != nil {
		return e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var version int
	if e = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); e != nil || version != 1 {
		return errors.New("discoveryasset: unsupported companion schema")
	}
	var integrity string
	if e = db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); e != nil || integrity != "ok" {
		return errors.New("discoveryasset: corrupt companion index")
	}
	rows, e := db.QueryContext(ctx, "SELECT pack_id,sha256 FROM packs")
	if e != nil {
		return e
	}
	expected := map[string]string{}
	for _, f := range m.Packs {
		expected[f.PackID] = f.SHA256
	}
	for rows.Next() {
		var id, hash string
		if e = rows.Scan(&id, &hash); e != nil {
			rows.Close()
			return e
		}
		if expected[id] != hash {
			rows.Close()
			return errors.New("discoveryasset: companion pack binding mismatch")
		}
		delete(expected, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if len(expected) != 0 {
		return errors.New("discoveryasset: incomplete companion pack bindings")
	}
	// Require all query-facing columns, even when the corpus has no annotations.
	for _, query := range []string{"SELECT pack_id,track_id,artist,album,original_year FROM recordings LIMIT 0", "SELECT pack_id,track_id,kind,value,source_key,origin FROM annotations LIMIT 0", "SELECT pack_id,artist,album,period,kind,value,recordings,albums FROM profiles LIMIT 0"} {
		r, err := db.QueryContext(ctx, query)
		if err != nil {
			return err
		}
		r.Close()
	}
	return nil
}
