package mbindex

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// BenchmarkRecordingSQL compares only database loading and finalization. It
// deliberately excludes JSON and XZ work to make storage tradeoffs visible.
// By default each operation writes 30,000 recordings with random UUID order,
// 6,000 distinct artists, one credit, one ISRC and two tags per recording, then
// creates the recording indexes and writes a compact on-disk SQLite database.
// MBPACK_BENCH_SQL_ROWS optionally controls dataset size for larger experiments.
func BenchmarkRecordingSQL(b *testing.B) {
	rows := 30000
	if value := os.Getenv("MBPACK_BENCH_SQL_ROWS"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1000 || parsed > 1000000 || parsed%5 != 0 {
			b.Fatalf("MBPACK_BENCH_SQL_ROWS must be a multiple of 5 between 1000 and 1000000, got %q", value)
		}
		rows = parsed
	}
	ids := make([]string, rows)
	artistIDs := make([]string, rows/5)
	rng := rand.New(rand.NewPCG(31, 97))
	for i := range ids {
		ids[i] = fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", rng.Uint32(), rng.Uint32()&0xffff, rng.Uint32()&0xffff, rng.Uint32()&0xffff, rng.Uint64()&0xffffffffffff)
	}
	copy(artistIDs, ids)
	for _, variant := range []struct {
		name      string
		cacheKiB  int
		memory    bool
		batchSize int
		dedup     bool
		staging   bool
	}{
		{name: "disk_default_single", batchSize: 1},
		{name: "disk_64MiB_single", cacheKiB: 65536, batchSize: 1},
		{name: "memory_single_export", memory: true, batchSize: 1},
		{name: "disk_64MiB_batch64", cacheKiB: 65536, batchSize: 64},
		{name: "disk_64MiB_single_dedup", cacheKiB: 65536, batchSize: 1, dedup: true},
		{name: "disk_64MiB_batch64_dedup", cacheKiB: 65536, batchSize: 64, dedup: true},
		{name: "disk_default_sorted_stage_dedup", batchSize: 1, dedup: true, staging: true},
		{name: "disk_64MiB_sorted_stage_dedup", cacheKiB: 65536, batchSize: 1, dedup: true, staging: true},
	} {
		b.Run(variant.name, func(b *testing.B) {
			b.ReportAllocs()
			var elapsed time.Duration
			for iteration := 0; iteration < b.N; iteration++ {
				b.StopTimer()
				output := filepath.Join(b.TempDir(), "recordings.sqlite")
				dsn := output
				if variant.memory {
					dsn = ":memory:"
				}
				db, err := sql.Open("sqlite", dsn)
				if err != nil {
					b.Fatal(err)
				}
				db.SetMaxOpenConns(1)
				pragmas := `PRAGMA journal_mode=OFF; PRAGMA synchronous=OFF; PRAGMA temp_store=FILE;`
				if variant.cacheKiB != 0 {
					pragmas += fmt.Sprintf("PRAGMA cache_size=-%d;", variant.cacheKiB)
				}
				if _, err = db.Exec(pragmas + buildSchema); err != nil {
					_ = db.Close()
					b.Fatal(err)
				}
				if variant.staging {
					if err = benchmarkCreateSQLStages(db); err != nil {
						_ = db.Close()
						b.Fatal(err)
					}
				}
				b.StartTimer()
				started := time.Now()
				err = benchmarkRecordingSQLLoad(db, ids, artistIDs, variant.batchSize, variant.dedup, variant.staging)
				if err == nil && variant.staging {
					err = benchmarkSortSQLStages(db)
				}
				if err == nil {
					_, err = db.Exec(`CREATE INDEX recording_title_key ON recordings(title_key); CREATE INDEX recording_artist ON recording_artists(artist_mbid,recording_mbid); CREATE INDEX recording_artist_name ON recording_artists(name_key,recording_mbid); CREATE INDEX recording_tags_key ON recording_tags(tag_key,recording_mbid);`)
				}
				if err == nil {
					if variant.memory {
						_, err = db.Exec("VACUUM INTO ?", output)
					} else {
						_, err = db.Exec("VACUUM")
					}
				}
				closeErr := db.Close()
				elapsed += time.Since(started)
				b.StopTimer()
				if err != nil {
					b.Fatal(err)
				}
				if closeErr != nil {
					b.Fatal(closeErr)
				}
				verifyBenchmarkRecordingSQL(b, output, rows, len(artistIDs))
			}
			b.ReportMetric(float64(rows*b.N)/elapsed.Seconds(), "recordings/s")
		})
	}
}

func verifyBenchmarkRecordingSQL(b *testing.B, output string, recordings, artists int) {
	b.Helper()
	db, err := sql.Open("sqlite", output)
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	for _, expected := range []struct {
		table string
		count int
	}{
		{"recordings", recordings},
		{"artists", artists},
		{"recording_artists", recordings},
		{"recording_isrcs", recordings},
		{"recording_tags", recordings * 2},
	} {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM " + expected.table).Scan(&count); err != nil {
			b.Fatal(err)
		}
		if count != expected.count {
			b.Fatalf("%s count = %d, want %d", expected.table, count, expected.count)
		}
	}
	stat, err := os.Stat(output)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(stat.Size()), "db-bytes")
}

func benchmarkRecordingSQLLoad(db *sql.DB, ids, artistIDs []string, batchSize int, dedup, staging bool) error {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	definitions := []struct {
		query   string
		columns int
	}{
		{`INSERT OR REPLACE INTO recordings(mbid,title,title_key,artist_credit,duration_ms,first_release_date,comment) VALUES`, 7},
		{`INSERT OR IGNORE INTO artists(mbid,name,name_key,sort_name,comment) VALUES`, 5},
		{`INSERT OR REPLACE INTO recording_artists(recording_mbid,position,artist_mbid,name,name_key,join_phrase) VALUES`, 6},
		{`INSERT OR IGNORE INTO recording_isrcs(recording_mbid,isrc) VALUES`, 2},
		{`INSERT OR REPLACE INTO recording_tags(recording_mbid,tag_key,tag,votes) VALUES`, 4},
	}
	writers := make([]*benchmarkSQLBatch, len(definitions))
	for i, definition := range definitions {
		if staging {
			definition.query = strings.Replace(definition.query, " INTO ", " INTO stage_", 1)
		}
		writer := &benchmarkSQLBatch{tx: tx, query: definition.query, columns: definition.columns, batchSize: batchSize, values: make([]any, 0, definition.columns*batchSize)}
		statement, err := tx.PrepareContext(ctx, writer.statement(batchSize))
		if err != nil {
			return err
		}
		defer statement.Close()
		writer.prepared = statement
		writers[i] = writer
	}
	seen := make(map[string]struct{}, len(artistIDs))
	for i, id := range ids {
		artistID := artistIDs[i%len(artistIDs)]
		if err := writers[0].add(id, "Representative Recording Title", "representative recording title", "Representative Artist", int64(231000), "2024-05-06", ""); err != nil {
			return err
		}
		if _, ok := seen[artistID]; !dedup || !ok {
			if err := writers[1].add(artistID, "Representative Artist", "representative artist", "Artist, Representative", ""); err != nil {
				return err
			}
			if dedup {
				seen[artistID] = struct{}{}
			}
		}
		if err := writers[2].add(id, 0, artistID, "Representative Artist", "representative artist", ""); err != nil {
			return err
		}
		if err := writers[3].add(id, "USXXX2400001"); err != nil {
			return err
		}
		if err := writers[4].add(id, "alternative rock", "alternative rock", 2); err != nil {
			return err
		}
		if err := writers[4].add(id, "indie rock", "indie rock", 1); err != nil {
			return err
		}
	}
	for _, writer := range writers {
		if err := writer.flush(); err != nil {
			return err
		}
	}
	return tx.Commit()
}

var benchmarkSQLStageTables = []struct {
	name     string
	key      string
	conflict string
}{
	{"recordings", "mbid", "REPLACE"},
	{"artists", "mbid", "IGNORE"},
	{"recording_artists", "recording_mbid,position", "REPLACE"},
	{"recording_isrcs", "recording_mbid,isrc", "IGNORE"},
	{"recording_tags", "recording_mbid,tag_key,tag", "REPLACE"},
}

func benchmarkCreateSQLStages(db *sql.DB) error {
	for _, table := range benchmarkSQLStageTables {
		if _, err := db.Exec("CREATE TABLE stage_" + table.name + " AS SELECT * FROM " + table.name + " WHERE 0"); err != nil {
			return err
		}
	}
	return nil
}

func benchmarkSortSQLStages(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, table := range benchmarkSQLStageTables {
		// Rowid preserves original insertion order within equal primary keys,
		// so REPLACE keeps the last input and IGNORE keeps the first input.
		query := "INSERT OR " + table.conflict + " INTO " + table.name + " SELECT * FROM stage_" + table.name + " ORDER BY " + table.key + ",rowid"
		if _, err := tx.Exec(query); err != nil {
			return err
		}
		if _, err := tx.Exec("DROP TABLE stage_" + table.name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func TestBenchmarkSQLStagingDuplicateSemantics(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "staging.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(buildSchema); err != nil {
		t.Fatal(err)
	}
	if err := benchmarkCreateSQLStages(db); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO stage_recordings VALUES('r','first','first','credit',1,'',''),('r','last','last','credit',2,'','')`,
		`INSERT INTO stage_artists VALUES('a','first','first','',''),('a','last','last','','')`,
		`INSERT INTO stage_recording_artists VALUES('r',0,'a','first','first',''),('r',0,'a','last','last','')`,
		`INSERT INTO stage_recording_isrcs VALUES('r','US123'),('r','US123')`,
		`INSERT INTO stage_recording_tags VALUES('r','rock','rock',1),('r','rock','rock',2)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := benchmarkSortSQLStages(db); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		query string
		want  string
	}{
		{`SELECT title FROM recordings WHERE mbid='r'`, "last"},
		{`SELECT name FROM artists WHERE mbid='a'`, "first"},
		{`SELECT name FROM recording_artists WHERE recording_mbid='r' AND position=0`, "last"},
		{`SELECT count(*) FROM recording_isrcs`, "1"},
		{`SELECT votes FROM recording_tags WHERE recording_mbid='r' AND tag_key='rock' AND tag='rock'`, "2"},
	} {
		var got string
		if err := db.QueryRow(check.query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != check.want {
			t.Fatalf("%s = %q, want %q", check.query, got, check.want)
		}
	}
}

type benchmarkSQLBatch struct {
	tx        *sql.Tx
	query     string
	columns   int
	batchSize int
	values    []any
	prepared  *sql.Stmt
}

func (w *benchmarkSQLBatch) statement(rows int) string {
	row := "(" + strings.TrimSuffix(strings.Repeat("?,", w.columns), ",") + ")"
	return w.query + strings.TrimSuffix(strings.Repeat(row+",", rows), ",")
}

func (w *benchmarkSQLBatch) add(values ...any) error {
	w.values = append(w.values, values...)
	if len(w.values) == w.columns*w.batchSize {
		return w.flush()
	}
	return nil
}

func (w *benchmarkSQLBatch) flush() error {
	if len(w.values) == 0 {
		return nil
	}
	var err error
	if len(w.values) == w.columns*w.batchSize {
		_, err = w.prepared.ExecContext(context.Background(), w.values...)
	} else {
		_, err = w.tx.ExecContext(context.Background(), w.statement(len(w.values)/w.columns), w.values...)
	}
	clear(w.values)
	w.values = w.values[:0]
	return err
}
