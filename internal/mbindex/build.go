package mbindex

import (
	"archive/tar"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ulikunitz/xz"

	"github.com/platten/playlistai/internal/core"
)

type BuildOptions struct {
	Output           string
	Snapshot         string
	ArtistArchive    string
	RecordingArchive string
	Inputs           map[string]string
	Replace          bool
	SQLiteCacheMiB   int // Per connection; zero uses DefaultSQLiteCacheMiB.
	Progress         func(BuildProgress)
}

const DefaultSQLiteCacheMiB = 64

func sqliteCacheMiB(value int) (int, error) {
	if value == 0 {
		return DefaultSQLiteCacheMiB, nil
	}
	if value < 1 || value > 4096 {
		return 0, errors.New("SQLite cache must be between 1 and 4096 MiB per database")
	}
	return value, nil
}

// BuildProgress reports JSON member bytes or finalization steps and decoded rows.
// Build invokes Progress concurrently for the artist and recording stages.
type BuildProgress struct {
	Entity string
	Done   int64
	Total  int64
	Rows   int64
}

type dumpTag struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type artistDump struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	SortName       string `json:"sort-name"`
	Disambiguation string `json:"disambiguation"`
	Aliases        []struct {
		Name     string `json:"name"`
		SortName string `json:"sort-name"`
		Locale   string `json:"locale"`
	} `json:"aliases"`
	Tags   []dumpTag `json:"tags"`
	Genres []dumpTag `json:"genres"`
}

type recordingDump struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	Disambiguation   string    `json:"disambiguation"`
	Length           int64     `json:"length"`
	FirstReleaseDate string    `json:"first-release-date"`
	Video            bool      `json:"video"`
	ISRCs            []string  `json:"isrcs"`
	Tags             []dumpTag `json:"tags"`
	Genres           []dumpTag `json:"genres"`
	ArtistCredit     []struct {
		Name       string `json:"name"`
		JoinPhrase string `json:"joinphrase"`
		Artist     struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			SortName       string `json:"sort-name"`
			Disambiguation string `json:"disambiguation"`
		} `json:"artist"`
	} `json:"artist-credit"`
}

const buildSchema = `
CREATE TABLE info(key TEXT PRIMARY KEY,value TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE artists(mbid TEXT PRIMARY KEY,name TEXT NOT NULL,name_key TEXT NOT NULL,sort_name TEXT NOT NULL,comment TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE artist_aliases(artist_mbid TEXT NOT NULL,name TEXT NOT NULL,name_key TEXT NOT NULL,sort_name TEXT NOT NULL,locale TEXT NOT NULL,PRIMARY KEY(artist_mbid,name,locale)) WITHOUT ROWID;
CREATE TABLE artist_tags(tag_key TEXT NOT NULL,artist_mbid TEXT NOT NULL,tag TEXT NOT NULL,votes INTEGER NOT NULL,PRIMARY KEY(tag_key,artist_mbid,tag)) WITHOUT ROWID;
CREATE TABLE recordings(mbid TEXT PRIMARY KEY,title TEXT NOT NULL,title_key TEXT NOT NULL,artist_credit TEXT NOT NULL,duration_ms INTEGER NOT NULL,first_release_date TEXT NOT NULL,comment TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE recording_artists(recording_mbid TEXT NOT NULL,position INTEGER NOT NULL,artist_mbid TEXT NOT NULL,name TEXT NOT NULL,name_key TEXT NOT NULL,join_phrase TEXT NOT NULL,PRIMARY KEY(recording_mbid,position)) WITHOUT ROWID;
CREATE TABLE recording_isrcs(recording_mbid TEXT NOT NULL,isrc TEXT NOT NULL,PRIMARY KEY(recording_mbid,isrc)) WITHOUT ROWID;
CREATE TABLE recording_tags(recording_mbid TEXT NOT NULL,tag_key TEXT NOT NULL,tag TEXT NOT NULL,votes INTEGER NOT NULL,PRIMARY KEY(recording_mbid,tag_key,tag)) WITHOUT ROWID;
`

func Build(ctx context.Context, o BuildOptions) (info Info, err error) {
	cacheMiB, err := sqliteCacheMiB(o.SQLiteCacheMiB)
	if err != nil {
		return info, err
	}
	if o.Output == "" || o.ArtistArchive == "" || o.RecordingArchive == "" {
		return info, errors.New("output, artist archive, and recording archive are required")
	}
	if _, err := time.Parse("20060102-150405", o.Snapshot); err != nil {
		return info, fmt.Errorf("invalid MusicBrainz snapshot: %w", err)
	}
	abs, err := filepath.Abs(o.Output)
	if err != nil {
		return info, err
	}
	if st, statErr := os.Lstat(abs); statErr == nil {
		if !o.Replace || !st.Mode().IsRegular() {
			return info, errors.New("output exists; use replace or choose another path")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return info, statErr
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return info, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".musicbrainz-build-*.sqlite")
	if err != nil {
		return info, err
	}
	tmpPath := tmp.Name()
	if err = tmp.Close(); err != nil {
		return info, err
	}
	defer os.Remove(tmpPath)
	artistTmp, err := os.CreateTemp(filepath.Dir(abs), ".musicbrainz-artists-*.sqlite")
	if err != nil {
		return info, err
	}
	artistTmpPath := artistTmp.Name()
	if err = artistTmp.Close(); err != nil {
		return info, err
	}
	defer os.Remove(artistTmpPath)

	stageCtx, cancelStages := context.WithCancel(ctx)
	defer cancelStages()
	type stageResult struct {
		name string
		err  error
	}
	results := make(chan stageResult, 2)
	var stages sync.WaitGroup
	stages.Add(2)
	go func() {
		defer stages.Done()
		results <- stageResult{"artists", buildStageWithCache(stageCtx, artistTmpPath, cacheMiB, func(db *sql.DB) error {
			return importArtists(stageCtx, db, o.ArtistArchive, o.Progress)
		})}
	}()
	go func() {
		defer stages.Done()
		results <- stageResult{"recordings", buildStageWithCache(stageCtx, tmpPath, cacheMiB, func(db *sql.DB) error {
			return importRecordings(stageCtx, db, o.RecordingArchive, o.Progress)
		})}
	}()
	go func() {
		stages.Wait()
		close(results)
	}()
	var stageErr error
	for result := range results {
		if result.err != nil && stageErr == nil {
			stageErr = fmt.Errorf("import %s: %w", result.name, result.err)
			cancelStages()
		}
	}
	if stageErr != nil {
		return info, stageErr
	}
	if o.Progress != nil {
		o.Progress(BuildProgress{Entity: "finalize", Total: 4})
	}

	db, err := sql.Open("sqlite", tmpPath)
	if err != nil {
		return info, err
	}
	defer db.Close()
	if err = configureBuildDB(ctx, db, cacheMiB); err != nil {
		return info, err
	}
	if _, err = db.ExecContext(ctx, `ATTACH DATABASE ? AS artist_stage`, artistTmpPath); err != nil {
		return info, err
	}
	if _, err = db.ExecContext(ctx, `
INSERT OR REPLACE INTO artists(mbid,name,name_key,sort_name,comment) SELECT mbid,name,name_key,sort_name,comment FROM artist_stage.artists;
INSERT OR IGNORE INTO artist_aliases(artist_mbid,name,name_key,sort_name,locale) SELECT artist_mbid,name,name_key,sort_name,locale FROM artist_stage.artist_aliases;
INSERT OR REPLACE INTO artist_tags(tag_key,artist_mbid,tag,votes) SELECT tag_key,artist_mbid,tag,votes FROM artist_stage.artist_tags;
`); err != nil {
		return info, err
	}
	if _, err = db.ExecContext(ctx, `DETACH DATABASE artist_stage`); err != nil {
		return info, err
	}
	if o.Progress != nil {
		o.Progress(BuildProgress{Entity: "finalize", Done: 1, Total: 4})
	}
	if _, err = db.ExecContext(ctx, `CREATE INDEX artist_name_key ON artists(name_key); CREATE INDEX artist_alias_key ON artist_aliases(name_key); CREATE INDEX artist_tags_artist ON artist_tags(artist_mbid); CREATE INDEX recording_title_key ON recordings(title_key); CREATE INDEX recording_artist ON recording_artists(artist_mbid,recording_mbid); CREATE INDEX recording_artist_name ON recording_artists(name_key,recording_mbid); CREATE INDEX recording_tags_key ON recording_tags(tag_key,recording_mbid);`); err != nil {
		return info, err
	}
	if o.Progress != nil {
		o.Progress(BuildProgress{Entity: "finalize", Done: 2, Total: 4})
	}
	info = Info{Version: IndexVersion, Snapshot: o.Snapshot, BuiltAt: time.Now().UTC().Format(time.RFC3339), CoreLicense: "CC0-1.0", TagsLicense: "CC-BY-NC-SA-3.0", Inputs: o.Inputs}
	for query, dest := range map[string]*int64{
		"SELECT count(*) FROM artists":        &info.Artists,
		"SELECT count(*) FROM recordings":     &info.Recordings,
		"SELECT count(*) FROM artist_tags":    &info.ArtistTags,
		"SELECT count(*) FROM recording_tags": &info.RecordingTags,
	} {
		if err = db.QueryRowContext(ctx, query).Scan(dest); err != nil {
			return info, err
		}
	}
	if err = info.Validate(); err != nil {
		return info, err
	}
	raw, _ := json.Marshal(info)
	if _, err = db.ExecContext(ctx, "INSERT INTO info(key,value) VALUES('manifest',?)", string(raw)); err != nil {
		return info, err
	}
	if o.Progress != nil {
		o.Progress(BuildProgress{Entity: "finalize", Done: 3, Total: 4})
	}
	if _, err = db.ExecContext(ctx, "PRAGMA optimize; VACUUM"); err != nil {
		return info, err
	}
	if o.Progress != nil {
		o.Progress(BuildProgress{Entity: "finalize", Done: 4, Total: 4})
	}
	if err = db.Close(); err != nil {
		return info, err
	}
	if err = publishBuild(tmpPath, abs, o.Replace); err != nil {
		return info, err
	}
	return info, nil
}

// Replacement never deletes the old output before its successor is ready.
// Rename replaces a closed destination atomically where supported; an open-file
// or filesystem error retains the old output. A no-replace build uses a hard
// link so an output that appeared during the build cannot be overwritten.
func publishBuild(staged, target string, replace bool) error {
	if replace {
		return os.Rename(staged, target)
	}
	if err := os.Link(staged, target); err != nil {
		return err
	}
	return nil // Build's deferred staging cleanup removes the other link.
}

func buildStageWithCache(ctx context.Context, path string, cacheMiB int, load func(*sql.DB) error) (err error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := db.Close(); err == nil {
			err = closeErr
		}
	}()
	if err = configureBuildDB(ctx, db, cacheMiB); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, buildSchema); err != nil {
		return err
	}
	return load(db)
}

func configureBuildDB(ctx context.Context, db *sql.DB, cacheMiB int) error {
	// PRAGMAs are connection-local; keep both stages and the reopened finalizer
	// on one connection. These disposable files are published only after success.
	db.SetMaxOpenConns(1)
	_, err := db.ExecContext(ctx, fmt.Sprintf(`PRAGMA journal_mode=OFF; PRAGMA synchronous=OFF; PRAGMA temp_store=FILE; PRAGMA cache_size=-%d;`, cacheMiB*1024))
	return err
}

func importArtists(ctx context.Context, db *sql.DB, path string, progress func(BuildProgress)) error {
	tx, err := beginImportTransaction(ctx, db)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	artist, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO artists(mbid,name,name_key,sort_name,comment) VALUES(?,?,?,?,?)`)
	if err != nil {
		return err
	}
	alias, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO artist_aliases(artist_mbid,name,name_key,sort_name,locale) VALUES(?,?,?,?,?)`)
	if err != nil {
		_ = artist.Close()
		return err
	}
	tag, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO artist_tags(tag_key,artist_mbid,tag,votes) VALUES(?,?,?,?)`)
	if err != nil {
		_ = artist.Close()
		_ = alias.Close()
		return err
	}
	defer artist.Close()
	defer alias.Close()
	defer tag.Close()
	err = readJSONDump(ctx, path, "artists", "artist", progress, func(a artistDump) error {
		if a.ID == "" || strings.TrimSpace(a.Name) == "" {
			return nil
		}
		if _, err := artist.ExecContext(ctx, a.ID, a.Name, normalize(a.Name), a.SortName, a.Disambiguation); err != nil {
			return err
		}
		for _, value := range a.Aliases {
			if value.Name != "" {
				if _, err := alias.ExecContext(ctx, a.ID, value.Name, normalize(value.Name), value.SortName, value.Locale); err != nil {
					return err
				}
			}
		}
		for _, value := range mergeTags(a.Tags, a.Genres) {
			if key := normalize(value.Name); key != "" {
				if _, err := tag.ExecContext(ctx, key, a.ID, value.Name, value.Count); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("import artists: %w", err)
	}
	return commitImportTransaction(ctx, tx)
}

func importRecordings(ctx context.Context, db *sql.DB, path string, progress func(BuildProgress)) error {
	tx, err := beginImportTransaction(ctx, db)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	prepare := func(query string) (*sql.Stmt, error) { return tx.PrepareContext(ctx, query) }
	recording, err := prepare(`INSERT OR REPLACE INTO recordings(mbid,title,title_key,artist_credit,duration_ms,first_release_date,comment) VALUES(?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer recording.Close()
	artist, err := prepare(`INSERT OR IGNORE INTO artists(mbid,name,name_key,sort_name,comment) VALUES(?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer artist.Close()
	credit, err := prepare(`INSERT OR REPLACE INTO recording_artists(recording_mbid,position,artist_mbid,name,name_key,join_phrase) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer credit.Close()
	isrc, err := prepare(`INSERT OR IGNORE INTO recording_isrcs(recording_mbid,isrc) VALUES(?,?)`)
	if err != nil {
		return err
	}
	defer isrc.Close()
	tag, err := prepare(`INSERT OR REPLACE INTO recording_tags(recording_mbid,tag_key,tag,votes) VALUES(?,?,?,?)`)
	if err != nil {
		return err
	}
	defer tag.Close()
	// An exact, bounded cache avoids repeated artist B-tree lookups and name
	// normalization. On eviction INSERT OR IGNORE still guarantees first-wins.
	seenArtists := make(map[string]struct{})
	err = readJSONDump(ctx, path, "recordings", "recording", progress, func(r recordingDump) error {
		if r.ID == "" || r.Title == "" || r.Video || len(r.ArtistCredit) == 0 {
			return nil
		}
		var combined strings.Builder
		for _, c := range r.ArtistCredit {
			combined.WriteString(c.Name)
			combined.WriteString(c.JoinPhrase)
		}
		if _, err := recording.ExecContext(ctx, r.ID, r.Title, normalize(r.Title), combined.String(), max(0, r.Length), r.FirstReleaseDate, r.Disambiguation); err != nil {
			return err
		}
		for position, c := range r.ArtistCredit {
			if c.Artist.ID == "" || c.Name == "" {
				continue
			}
			if _, seen := seenArtists[c.Artist.ID]; !seen {
				if _, err := artist.ExecContext(ctx, c.Artist.ID, c.Artist.Name, normalize(c.Artist.Name), c.Artist.SortName, c.Artist.Disambiguation); err != nil {
					return err
				}
				if len(seenArtists) >= 65536 {
					clear(seenArtists)
				}
				seenArtists[c.Artist.ID] = struct{}{}
			}
			if _, err := credit.ExecContext(ctx, r.ID, position, c.Artist.ID, c.Name, normalize(c.Name), c.JoinPhrase); err != nil {
				return err
			}
		}
		for _, value := range r.ISRCs {
			value = strings.ToUpper(strings.TrimSpace(value))
			if value != "" {
				if _, err := isrc.ExecContext(ctx, r.ID, value); err != nil {
					return err
				}
			}
		}
		for _, value := range mergeTags(r.Tags, r.Genres) {
			if key := normalize(value.Name); key != "" {
				if _, err := tag.ExecContext(ctx, r.ID, key, value.Name, value.Count); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("import recordings: %w", err)
	}
	return commitImportTransaction(ctx, tx)
}

func beginImportTransaction(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Each stage exclusively owns its database. Keep rollback with the importer's
	// deferred Rollback: BeginTx(ctx) would also start an automatic rollback on
	// cancellation. That goroutine can mark the transaction done before releasing
	// its file handle, so our Rollback and DB.Close could return before Windows
	// can remove the staging file. Reads, statements and the commit guard still
	// use the original cancellable context; only transaction cleanup is detached.
	return db.BeginTx(context.WithoutCancel(ctx), nil)
}

func commitImportTransaction(ctx context.Context, tx *sql.Tx) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

func readJSONDump[T any](ctx context.Context, archivePath, progressEntity, entity string, progress func(BuildProgress), consume func(T) error) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	xr, err := xz.NewReader(&contextReader{ctx: ctx, r: f})
	if err != nil {
		return err
	}
	tr := tar.NewReader(xr)
	wanted := "mbdump/" + entity
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("MusicBrainz archive is missing %s", wanted)
		}
		if err != nil {
			return err
		}
		member := strings.TrimPrefix(path.Clean(strings.ReplaceAll(h.Name, "\\", "/")), "./")
		if h.Typeflag != tar.TypeReg || h.Size == 0 || member != wanted {
			continue
		}
		if progress != nil {
			progress(BuildProgress{Entity: progressEntity, Total: h.Size})
		}
		return streamJSON(ctx, io.LimitReader(tr, h.Size), wanted, progressEntity, h.Size, progress, consume)
	}
}

// A small queue overlaps decompression/typed decoding with SQLite writes. Only
// retained fields cross the queue; large unused release/relationship payloads
// stay in the decoder, and the full catalog is never retained in Go memory.
func streamJSON[T any](ctx context.Context, source io.Reader, member, entity string, total int64, progress func(BuildProgress), consume func(T) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type record struct {
		value       T
		row, offset int64
	}
	records := make(chan record, 32)
	finished := make(chan error, 1)
	go func() {
		defer close(records)
		decoder := json.NewDecoder(&contextReader{ctx: ctx, r: source})
		for row := int64(1); ; row++ {
			var value T
			if err := decoder.Decode(&value); err != nil {
				if errors.Is(err, io.EOF) {
					finished <- nil
				} else {
					finished <- fmt.Errorf("%s row %d: %w", member, row, err)
				}
				return
			}
			select {
			case records <- record{value, row, decoder.InputOffset()}:
			case <-ctx.Done():
				finished <- ctx.Err()
				return
			}
		}
	}()
	var rows int64
	lastProgress := time.Now()
	for item := range records {
		err := ctx.Err()
		if err == nil {
			err = consume(item.value)
		}
		if err != nil {
			cancel()
			<-finished // Join before the caller closes the archive or database.
			return fmt.Errorf("%s row %d: %w", member, item.row, err)
		}
		rows = item.row
		if progress != nil && rows%128 == 0 && time.Since(lastProgress) >= 250*time.Millisecond {
			progress(BuildProgress{Entity: entity, Done: min(item.offset, total-1), Total: total, Rows: rows})
			lastProgress = time.Now()
		}
	}
	if err := <-finished; err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if progress != nil {
		progress(BuildProgress{Entity: entity, Done: total, Total: total, Rows: rows})
	}
	return nil
}

func mergeTags(groups ...[]dumpTag) []dumpTag {
	byKey := map[string]dumpTag{}
	for _, group := range groups {
		for _, tag := range group {
			key := normalize(tag.Name)
			if key != "" {
				if old, ok := byKey[key]; !ok || tag.Count > old.Count {
					byKey[key] = tag
				}
			}
		}
	}
	out := make([]dumpTag, 0, len(byKey))
	for _, tag := range byKey {
		out = append(out, tag)
	}
	return out
}

func normalize(value string) string { return core.NormalizeIdentityPart(value) }
