package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/platten/playlistai/internal/core"
)

// RuntimeVersion retains discovery data, not the operator's release archive.
const RuntimeVersion = "discogs-runtime/v2"

// Compact creates a runtime-only index without modifying the full source index.
// Text is dictionary encoded and the candidate table's primary key is its only
// index. Track ordinals follow stable hash order, allowing indexed sampling
// without storing a second sampling key/index on every membership.
func Compact(ctx context.Context, source, dest string) (info Info, err error) {
	s, err := Open(source)
	if err != nil {
		return info, err
	}
	info = s.Info()
	_ = s.Close()
	if info.Version != Version {
		return info, errors.New("compaction requires the full v1 import")
	}
	if info.ComposerVersion != "" && info.ComposerVersion != ComposerVersion {
		return info, errors.New("unsupported composer metadata extension")
	}
	if _, err := os.Lstat(dest); !errors.Is(err, os.ErrNotExist) {
		return info, errors.New("compact output already exists or cannot be inspected")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return info, err
	}
	f, err := os.CreateTemp(filepath.Dir(dest), ".runtime-build-*.sqlite")
	if err != nil {
		return info, err
	}
	tmp := f.Name()
	_ = f.Close()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	db, err := sql.Open("sqlite", tmp)
	if err != nil {
		return info, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	abs, err := filepath.Abs(source)
	if err != nil {
		return info, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	if _, err = db.ExecContext(ctx, "ATTACH DATABASE ? AS source", u.String()+"?mode=ro"); err != nil {
		return info, err
	}
	_, err = db.ExecContext(ctx, `PRAGMA page_size=4096; PRAGMA journal_mode=DELETE; PRAGMA synchronous=NORMAL;
CREATE TABLE info(key TEXT PRIMARY KEY,value TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE genre_keys(g INTEGER PRIMARY KEY,name TEXT NOT NULL,key TEXT NOT NULL UNIQUE);
CREATE TABLE recordings(t INTEGER PRIMARY KEY,id TEXT NOT NULL);
CREATE TABLE credit_sets(c INTEGER PRIMARY KEY,names TEXT NOT NULL);
CREATE TABLE candidates(g INTEGER,t INTEGER,a INTEGER,r INTEGER,c INTEGER,PRIMARY KEY(g,t,a)) WITHOUT ROWID;
INSERT INTO recordings SELECT row_number() OVER(ORDER BY k,track_id),track_id FROM (SELECT track_id,min(sample_key) k FROM source.genre_tracks GROUP BY track_id);
CREATE UNIQUE INDEX recording_join ON recordings(id);
INSERT INTO credit_sets(names) SELECT DISTINCT credits FROM source.genre_tracks ORDER BY credits;
CREATE UNIQUE INDEX credit_join ON credit_sets(names);`)
	if err != nil {
		return info, err
	}
	// Go's normalization preserves Unicode case behavior used at lookup time.
	rows, err := db.QueryContext(ctx, "SELECT name FROM source.tags ORDER BY name")
	if err != nil {
		return info, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			_ = rows.Close()
			return info, err
		}
		names = append(names, name)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return info, err
	}
	_ = rows.Close()
	for _, name := range names {
		if _, err = db.ExecContext(ctx, "INSERT OR IGNORE INTO genre_keys(name,key) VALUES(?,?)", name, core.NormalizeIdentityPart(name)); err != nil {
			return info, err
		}
	}
	// Signed release IDs replace repeated URLs; negative IDs identify masters.
	var invalid int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM source.genre_tracks WHERE source NOT GLOB 'https://www.discogs.com/release/[0-9]*' AND source NOT GLOB 'https://www.discogs.com/master/[0-9]*'`).Scan(&invalid); err != nil {
		return info, err
	}
	if invalid != 0 {
		return info, fmt.Errorf("unsupported provenance URLs: %d", invalid)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO candidates
SELECT g.g,t.t,s.artist_id,CASE WHEN s.source LIKE '%/release/%' THEN CAST(substr(s.source,33) AS INTEGER) ELSE -CAST(substr(s.source,32) AS INTEGER) END,c.c
FROM source.genre_tracks s JOIN genre_keys g ON g.key=s.tag JOIN recordings t ON t.id=s.track_id JOIN credit_sets c ON c.names=s.credits;
DROP INDEX recording_join; DROP INDEX credit_join;`)
	if err != nil {
		return info, err
	}
	if info.ComposerVersion == ComposerVersion {
		// Only credited tracks need this dictionary. This also preserves credits
		// for matched recordings without genre tags, absent from candidates.
		_, err = db.ExecContext(ctx, `CREATE TABLE composer_tracks(t INTEGER PRIMARY KEY,id TEXT NOT NULL UNIQUE);
CREATE TABLE composer_values(c INTEGER PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE composer_links(t INTEGER,c INTEGER,r INTEGER,PRIMARY KEY(t,c,r)) WITHOUT ROWID;
INSERT INTO composer_tracks(id) SELECT DISTINCT track_id FROM source.composer_credits ORDER BY track_id;
INSERT INTO composer_values(value) SELECT DISTINCT value FROM source.composer_credits ORDER BY value;
CREATE UNIQUE INDEX composer_value_join ON composer_values(value);
INSERT INTO composer_links SELECT t.t,v.c,CASE WHEN s.source LIKE '%/release/%' THEN CAST(substr(s.source,33) AS INTEGER) ELSE -CAST(substr(s.source,32) AS INTEGER) END
FROM source.composer_credits s JOIN composer_tracks t ON t.id=s.track_id JOIN composer_values v ON v.value=s.value;
DROP INDEX composer_value_join;`)
		if err != nil {
			return info, err
		}
		if err = db.QueryRowContext(ctx, "SELECT count(*) FROM composer_links WHERE r=0").Scan(&invalid); err != nil {
			return info, err
		}
		if invalid != 0 {
			return info, errors.New("invalid composer provenance in compact index")
		}
		if err = db.QueryRowContext(ctx, "SELECT count(*) FROM composer_links").Scan(&info.ComposerCredits); err != nil {
			return info, err
		}
	}
	if _, err = db.ExecContext(ctx, "DETACH DATABASE source"); err != nil {
		return info, err
	}
	var bad int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM candidates WHERE r=0").Scan(&bad); err != nil {
		return info, err
	}
	if bad != 0 {
		return info, errors.New("invalid release identities in compact index")
	}
	info.Version = RuntimeVersion
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM recordings").Scan(&info.Tracks); err != nil {
		return info, err
	}
	if info.Tracks == 0 {
		return info, errors.New("source index contains no genre discovery tracks")
	}
	info.Entities = 0 // archival release/master rows are intentionally omitted
	raw, _ := json.Marshal(info)
	if _, err = db.ExecContext(ctx, "INSERT INTO info VALUES('manifest',?)", string(raw)); err != nil {
		return info, err
	}
	if _, err = db.ExecContext(ctx, "VACUUM; ANALYZE;"); err != nil {
		return info, err
	}
	if err = db.Close(); err != nil {
		return info, err
	}
	if err = ctx.Err(); err != nil {
		return info, err
	}
	err = publishIndex(tmp, dest, false)
	return info, err
}
