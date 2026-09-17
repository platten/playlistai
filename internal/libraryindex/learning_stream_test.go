package libraryindex

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarylearn"
	"github.com/platten/playlistai/internal/localaudio"
)

func TestFrozenStoreDiverseSampleMatchesInMemoryContract(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "frozen.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE files(id TEXT PRIMARY KEY,source_revision TEXT,status TEXT);
		CREATE TABLE jobs(file_id TEXT,source_revision TEXT,kind TEXT,state TEXT,semantic_key TEXT);
		CREATE TABLE track_metadata(file_id TEXT,source_revision TEXT,contract TEXT,data BLOB);
		CREATE TABLE mert_results(file_id TEXT,source_revision TEXT,contract TEXT,vector BLOB);`); err != nil {
		t.Fatal(err)
	}
	inputs := []struct{ id, artist, title, isrc string }{
		{"a", "Alpha", "One", "shared"},
		{"b", "Alpha", "One remaster", "shared"},
		{"c", "Alpha", "Two", ""},
		{"d", "Beta", "Three", ""},
		{"e", "Gamma", "Four", ""},
	}
	items := make([]librarylearn.SampleItem, 0, len(inputs))
	for _, input := range inputs {
		metadata := localaudio.Metadata{
			Title:         &localaudio.TagValue{Value: input.title},
			ArtistCredits: []localaudio.TagValue{{Value: input.artist}},
		}
		if input.isrc != "" {
			metadata.ISRC = &localaudio.TagValue{Value: input.isrc}
		}
		record := MetadataRecord{Probe: localaudio.ProbeResult{Metadata: metadata}}
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		for _, statement := range []struct {
			query string
			args  []any
		}{
			{`INSERT INTO files VALUES(?,?,'present')`, []any{input.id, "rev"}},
			{`INSERT INTO jobs VALUES(?,?,'metadata','completed','metadata-v1')`, []any{input.id, "rev"}},
			{`INSERT INTO jobs VALUES(?,?,'audio','completed','audio-v1')`, []any{input.id, "rev"}},
			{`INSERT INTO track_metadata VALUES(?,?,?,?)`, []any{input.id, "rev", "metadata-v1", raw}},
			{`INSERT INTO mert_results VALUES(?,?,?,X'01')`, []any{input.id, "rev", "audio-v1"}},
		} {
			if _, err := db.Exec(statement.query, statement.args...); err != nil {
				t.Fatal(err)
			}
		}
		items = append(items, sampleLearningItem(input.id, record))
	}
	store := &frozenStore{db: db}
	const seed = uint64(73)
	want := librarylearn.DiverseSample(items, 4, seed)
	got, err := store.diverseSample(ctx, t.TempDir(), 4, seed)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sample mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestAssignmentCursorPinsCanonicalRows(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, assignmentStoreName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE assignments(id TEXT PRIMARY KEY,cluster_id INTEGER,similarity REAL,alternative_cluster INTEGER,alternative_score REAL) WITHOUT ROWID;
		INSERT INTO assignments VALUES('b',2,.8,1,.7),('d',3,.9,0,.6);`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	cursor, err := openGenerationAssignments(ctx, dir, assignmentStoreName)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	if _, ok, err := cursor.For("a"); err != nil || ok {
		t.Fatalf("unexpected a assignment ok=%v err=%v", ok, err)
	}
	if value, ok, err := cursor.For("b"); err != nil || !ok || value.Cluster != 2 {
		t.Fatalf("b assignment=%+v ok=%v err=%v", value, ok, err)
	}
	if value, ok, err := cursor.For("d"); err != nil || !ok || value.Cluster != 3 {
		t.Fatalf("d assignment=%+v ok=%v err=%v", value, ok, err)
	}
}

func TestEffectiveTrainingSampleHonorsCorpusAndRAM(t *testing.T) {
	if got := effectiveTrainingSample(0, 100_000, 768, 8<<30); got != 50_000 {
		t.Fatalf("default sample=%d", got)
	}
	if got := effectiveTrainingSample(50_000, 3, 768, 8<<30); got != 3 {
		t.Fatalf("corpus bounded sample=%d", got)
	}
	want := int(((256 << 20) / 4) / int64(768*8+256))
	if got := effectiveTrainingSample(50_000, 100_000, 768, 256<<20); got != want {
		t.Fatalf("RAM bounded sample=%d want=%d", got, want)
	}
}

func TestIndexShardRowsFitsHalfScratchBudget(t *testing.T) {
	const (
		dimension = 768
		budget    = int64(64 << 20)
	)
	rows := indexShardRows(dimension, budget)
	if rows <= 0 || rows > 16_384 {
		t.Fatalf("rows=%d", rows)
	}
	if got := int64(rows) * int64(dimension*4+64); got > budget/2 {
		t.Fatalf("shard bytes=%d budget half=%d", got, budget/2)
	}
}

func TestDSPLearningTrackAggregatesWindowsWithoutMixingMissingEvidence(t *testing.T) {
	known := func(value float64) core.DSPValue { return core.DSPValue{Value: &value, State: core.FeatureKnown} }
	record := DSPRecord{Version: "dsp/v1", Sampling: "balanced/v1", Scope: "sampled_windows", Windows: []DSPWindowRecord{
		{ObservedSeconds: 1, Features: core.DSPFeatures{RMSDBFS: known(-20)}},
		{ObservedSeconds: 3, Features: core.DSPFeatures{RMSDBFS: known(-10)}},
	}}
	track := dspLearningTrack("track-a", record)
	if track.Contract.Version != record.Version || track.Contract.Sampling != record.Sampling || track.Contract.Scope != record.Scope {
		t.Fatalf("contract=%+v", track.Contract)
	}
	for _, feature := range track.Features {
		if feature.Name == "rms_dbfs" {
			if feature.Value == nil || *feature.Value != -12.5 || feature.Partial {
				t.Fatalf("RMS=%+v", feature)
			}
			return
		}
	}
	t.Fatal("RMS feature missing")
}
