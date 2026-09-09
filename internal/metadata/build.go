package metadata

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/core"
)

type Input struct{ Path, SHA256 string }
type BuildOptions struct {
	Output, Date, CatalogVersion string
	Tracks                       []core.TrackRef
	Inputs                       []Input
	Progress                     func(int64)
	Replace                      bool // preserve previous index as a hard-linked backup before replacement
	Workers                      int  // 0 selects a bounded CPU-aware default; 1 disables parallel decoding
}
type credit struct {
	ID   int64  `xml:"id"`
	Name string `xml:"name"`
}
type xmlTrack struct {
	Title        string              `xml:"title"`
	Position     string              `xml:"position"`
	Artists      []credit            `xml:"artists>artist"`
	ExtraArtists []composerXMLCredit `xml:"extraartists>artist"`
}
type entity struct {
	ID           int64               `xml:"id,attr"`
	Title        string              `xml:"title"`
	Year         string              `xml:"year"`
	Released     string              `xml:"released"`
	MainRelease  int64               `xml:"main_release"`
	MasterID     int64               `xml:"master_id"`
	Artists      []credit            `xml:"artists>artist"`
	Genres       []string            `xml:"genres>genre"`
	Styles       []string            `xml:"styles>style"`
	Tracks       []xmlTrack          `xml:"tracklist>track"`
	ExtraArtists []composerXMLCredit `xml:"extraartists>artist"`
}
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

// Statements live for one transaction and are closed by Commit/Rollback.
// Reusing their compiled SQL avoids preparing millions of identical inserts.
type batchWriter struct {
	*sql.Tx
	statements map[string]*sql.Stmt
}

func (b *batchWriter) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	stmt := b.statements[query]
	if stmt == nil {
		var err error
		stmt, err = b.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}
		b.statements[query] = stmt
	}
	return stmt.ExecContext(ctx, args...)
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// Build streams compressed XML and publishes only after every supplied dump
// reaches EOF with a matching publisher SHA-256. No raw XML is retained in DB.
func Build(ctx context.Context, o BuildOptions) (info Info, err error) {
	workers, err := ImportWorkers(o.Workers)
	if err != nil {
		return info, err
	}
	if o.Output == "" || o.CatalogVersion == "" || len(o.Inputs) == 0 {
		return info, errors.New("output, catalog version and checksum-verified inputs required")
	}
	if _, err := time.Parse("20060102", o.Date); err != nil {
		return info, errors.New("valid snapshot date YYYYMMDD required")
	}
	if st, err := os.Lstat(o.Output); err == nil {
		if !o.Replace || !st.Mode().IsRegular() {
			return info, errors.New("output exists; choose a new path or explicitly replace a regular index file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return info, err
	}
	if err := os.MkdirAll(filepath.Dir(o.Output), 0700); err != nil {
		return info, err
	}
	lock, err := os.OpenFile(o.Output+".build.lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return info, fmt.Errorf("acquire build lock (another build may be active): %w", err)
	}
	defer func() { _ = lock.Close(); _ = os.Remove(lock.Name()) }()
	if _, err := fmt.Fprintf(lock, "pid=%d started=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339)); err != nil {
		return info, err
	}
	f, err := os.CreateTemp(filepath.Dir(o.Output), ".discogs-build-*.sqlite")
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
	_, err = db.ExecContext(ctx, `PRAGMA journal_mode=DELETE; PRAGMA synchronous=NORMAL;
CREATE TABLE info(key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE tags(name TEXT PRIMARY KEY);
CREATE TABLE entities(source TEXT PRIMARY KEY,title TEXT,title_key TEXT,master_id INTEGER,main_release INTEGER,release_date TEXT,master_year TEXT,total_tracks INTEGER,matched_tracks INTEGER);
CREATE TABLE entity_tags(entity TEXT,kind TEXT,value TEXT,PRIMARY KEY(entity,kind,value));
CREATE TABLE entity_tracks(entity TEXT,track_id TEXT,artist_id INTEGER,artist_key TEXT,position INTEGER,credits TEXT,PRIMARY KEY(entity,track_id,artist_id));
CREATE TABLE genre_tracks(tag TEXT,track_id TEXT,artist_id INTEGER,source TEXT,credits TEXT,sample_key INTEGER,PRIMARY KEY(tag,artist_id,track_id));
CREATE TABLE composer_credits(track_id TEXT,source TEXT,value TEXT,PRIMARY KEY(track_id,source,value)) WITHOUT ROWID;`)
	if err != nil {
		return info, err
	}
	lookup := make(map[string]string, len(o.Tracks))
	for _, t := range o.Tracks {
		key := core.ProvisionalRecordingKey(t)
		if prior := lookup[key]; prior == "" || t.ID < prior {
			lookup[key] = t.ID
		}
	}
	info = Info{Version: Version, Date: o.Date, Catalog: o.CatalogVersion, License: "CC0-1.0", Inputs: map[string]string{}}
	tags := map[string]bool{}
	masters := map[int64]bool{}
	for _, input := range o.Inputs {
		if len(input.SHA256) != 64 {
			return info, errors.New("each input requires its publisher SHA-256")
		}
		if _, err := hex.DecodeString(input.SHA256); err != nil {
			return info, err
		}
		if err = importXML(ctx, db, input, lookup, tags, masters, &info, o.Progress, workers); err != nil {
			return info, err
		}
		info.Inputs[filepath.Base(input.Path)] = strings.ToLower(input.SHA256)
	}
	_, err = db.ExecContext(ctx, `CREATE INDEX entity_title ON entities(title_key); CREATE INDEX entity_artist ON entity_tracks(artist_key); CREATE INDEX entity_recording ON entity_tracks(track_id); CREATE INDEX genre_sample ON genre_tracks(tag,sample_key,artist_id,track_id); ANALYZE;`)
	if err != nil {
		return info, err
	}
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM entities").Scan(&info.Entities); err != nil {
		return info, err
	}
	if err = db.QueryRowContext(ctx, "SELECT count(DISTINCT track_id) FROM entity_tracks").Scan(&info.Tracks); err != nil {
		return info, err
	}
	if info.Tracks == 0 {
		return info, errors.New("no catalog recordings matched; dataset not published")
	}
	info.ComposerVersion = ComposerVersion
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM composer_credits").Scan(&info.ComposerCredits); err != nil {
		return info, err
	}
	raw, _ := json.Marshal(info)
	if _, err = db.ExecContext(ctx, "INSERT INTO info VALUES('manifest',?)", string(raw)); err != nil {
		return info, err
	}
	if err = db.Close(); err != nil {
		return info, err
	}
	if err = ctx.Err(); err != nil {
		return info, err
	}
	err = publishIndex(tmp, o.Output, o.Replace)
	return info, err
}

func publishIndex(tmp, dest string, replace bool) error {
	st, err := os.Lstat(dest)
	if errors.Is(err, os.ErrNotExist) {
		// Exclusive publication: unlike Rename on Unix, Link cannot overwrite a
		// file that appears between the existence check and publication.
		if err := os.Link(tmp, dest); err != nil {
			return err
		}
		return os.Remove(tmp)
	}
	if err != nil {
		return err
	}
	if !replace || !st.Mode().IsRegular() {
		return errors.New("output exists; refusing to overwrite")
	}
	backup := dest + ".backup-" + time.Now().UTC().Format("20060102T150405.000000000")
	if err := os.Link(dest, backup); err != nil {
		return fmt.Errorf("preserve previous index: %w", err)
	}
	// Both names stay on the same filesystem. If replacement fails (e.g. a
	// Windows reader still holds the file), the original and backup remain.
	if err := os.Rename(tmp, dest); err != nil {
		return fmt.Errorf("install verified index (backup at %s): %w", backup, err)
	}
	return nil
}

func importXML(ctx context.Context, db *sql.DB, in Input, lookup map[string]string, tags map[string]bool, masters map[int64]bool, info *Info, progress func(int64), workers int) error {
	f, err := os.Open(in.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	hash := sha256.New()
	raw := io.TeeReader(contextReader{ctx, f}, hash)
	gz, err := gzip.NewReader(raw)
	if err != nil {
		return err
	}
	defer gz.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	writer := &batchWriter{Tx: tx, statements: map[string]*sql.Stmt{}}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	err = processEntities(ctx, gz, workers, lookup, func(kind string, e preparedEntity) error {
		info.Scanned++
		if err = saveEntity(ctx, writer, kind, e, tags, masters); err != nil {
			return err
		}
		if info.Scanned%10000 == 0 {
			if err = tx.Commit(); err != nil {
				return err
			}
			tx, err = db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			writer = &batchWriter{Tx: tx, statements: map[string]*sql.Stmt{}}
			if progress != nil {
				progress(info.Scanned)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Consume any compressed remainder before comparing the complete file hash.
	if _, err = io.Copy(io.Discard, raw); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(in.SHA256) {
		return fmt.Errorf("checksum mismatch: %s", filepath.Base(in.Path))
	}
	return tx.Commit()
}

type member struct {
	id       string
	artist   credit
	position int
	credits  string
}

type preparedEntity struct {
	entity
	tags      []string
	matches   []member
	composers []preparedComposer
}

// Matching is read-only and independent of the ordered writer's master/tag state.
func prepareEntity(e entity, lookup map[string]string) preparedEntity {
	tags := append(append([]string(nil), e.Genres...), e.Styles...)
	sort.Strings(tags)
	var matches []member
	for pos, t := range e.Tracks {
		if strings.TrimSpace(t.Title) == "" || strings.TrimSpace(t.Position) == "" {
			continue
		} // exclude headings/index tracks
		credits := t.Artists
		if len(credits) == 0 {
			credits = e.Artists
		}
		var names []string
		for _, a := range credits {
			names = append(names, artistName(a.Name))
		}
		rawCredits, _ := json.Marshal(names)
		for _, a := range credits {
			if a.ID <= 0 || artistName(a.Name) == "Various" {
				continue
			}
			key := core.ProvisionalRecordingKey(core.TrackRef{Artist: artistName(a.Name), Title: t.Title})
			if id := lookup[key]; id != "" {
				matches = append(matches, member{id, a, pos, string(rawCredits)})
			}
		}
	}
	return preparedEntity{entity: e, tags: tags, matches: matches, composers: prepareComposers(e, matches)}
}

func saveEntity(ctx context.Context, tx *batchWriter, kind string, e preparedEntity, knownTags map[string]bool, masters map[int64]bool) error {
	if e.ID <= 0 {
		return nil
	}
	for _, tag := range e.tags {
		if !knownTags[tag] {
			if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO tags VALUES(?)", tag); err != nil {
				return err
			}
			knownTags[tag] = true
		}
	}
	matches := e.matches
	if len(matches) == 0 && (kind != "master" || !masters[e.ID]) {
		return nil
	}
	if kind == "release" && e.MasterID > 0 {
		masters[e.MasterID] = true
	}
	source := fmt.Sprintf("https://www.discogs.com/%s/%d", kind, e.ID)
	for _, c := range e.composers {
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO composer_credits VALUES(?,?,?)", c.trackID, source, c.value); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO entities VALUES(?,?,?,?,?,?,?,?,?)", source, e.Title, core.NormalizeIdentityPart(e.Title), e.MasterID, e.MainRelease, e.Released, e.Year, len(e.Tracks), len(matches)); err != nil {
		return err
	}
	for _, group := range []struct {
		kind   string
		values []string
	}{{"genre", e.Genres}, {"style", e.Styles}} {
		for _, value := range group.values {
			if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO entity_tags VALUES(?,?,?)", source, group.kind, value); err != nil {
				return err
			}
		}
	}
	for _, m := range matches {
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO entity_tracks VALUES(?,?,?,?,?,?)", source, m.id, m.artist.ID, core.NormalizeIdentityPart(artistName(m.artist.Name)), m.position, m.credits); err != nil {
			return err
		}
		for _, tag := range e.tags {
			hash := fnv.New32a()
			_, _ = hash.Write([]byte(m.id))
			if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO genre_tracks VALUES(?,?,?,?,?,?)", core.NormalizeIdentityPart(tag), m.id, m.artist.ID, source, m.credits, hash.Sum32()); err != nil {
				return err
			}
		}
	}
	return nil
}
