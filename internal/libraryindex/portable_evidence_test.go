package libraryindex

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/localaudio"
)

func evidenceSnapshotDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "snapshot.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE roots(id TEXT PRIMARY KEY,alias TEXT);
		CREATE TABLE files(id TEXT PRIMARY KEY,root_id TEXT,relative_path TEXT,source_revision TEXT,status TEXT);
		CREATE TABLE jobs(file_id TEXT,source_revision TEXT,kind TEXT,state TEXT,semantic_key TEXT,error_code TEXT,error_detail TEXT);
		CREATE TABLE track_metadata(file_id TEXT,source_revision TEXT,contract TEXT,data BLOB);
		CREATE TABLE dsp_results(file_id TEXT,source_revision TEXT,contract TEXT,data BLOB);
		CREATE TABLE mert_results(file_id TEXT,source_revision TEXT,contract TEXT,vector BLOB);
		CREATE TABLE clap_results(file_id TEXT,source_revision TEXT,contract TEXT,vector BLOB,data BLOB);
		INSERT INTO roots VALUES('root','library');`)
	if err != nil {
		t.Fatal(err)
	}
	return db, path
}

func TestDSPSourceAndSnapshotRetainFailedMERTResults(t *testing.T) {
	ctx := context.Background()
	db, path := evidenceSnapshotDB(t)
	value := -12.0
	record := DSPRecord{Version: "dsp/v1", Sampling: "test/v1", Scope: "sampled_windows", Windows: []DSPWindowRecord{{ObservedSeconds: 5, Features: core.DSPFeatures{RMSDBFS: core.DSPValue{Value: &value, State: core.FeatureKnown}}}}}
	raw, _ := json.Marshal(record)
	for _, item := range []struct{ id, state, revision, status string }{
		{"complete", "completed", "current", "present"},
		{"failed", "failed", "current", "present"},
		{"superseded", "superseded", "current", "present"},
		{"stale", "failed", "old", "present"},
		{"removed", "completed", "current", "missing"},
	} {
		if _, err := db.Exec("INSERT INTO files VALUES(?,'root','track.flac','current',?)", item.id, item.status); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO jobs VALUES(?,?,'audio',?,'audio/v1','','')", item.id, item.revision, item.state); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO dsp_results VALUES(?,?,'audio/v1',?)", item.id, item.revision, raw); err != nil {
			t.Fatal(err)
		}
	}
	store := &frozenStore{db: db}
	source, err := store.dspSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for {
		track, ok, err := source.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		ids = append(ids, track.TrackID)
		if track.Features[0].Value == nil || *track.Features[0].Value != value {
			t.Fatalf("DSP result changed: %+v", track)
		}
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []string{"complete", "failed"}) {
		t.Fatalf("DSP coverage=%v", ids)
	}
	first, err := snapshotSemanticDigest(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	value = -18
	raw, _ = json.Marshal(record)
	if _, err := db.Exec("UPDATE dsp_results SET data=? WHERE file_id='failed'", raw); err != nil {
		t.Fatal(err)
	}
	second, err := snapshotSemanticDigest(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("failed-MERT DSP changed without invalidating corpus generation")
	}
}

func storedCLAPFixture() CLAPRecord {
	return CLAPRecord{Model: core.AudioModelIdentity{Model: "clap", Revision: "fixture", Preprocessing: "clap/v1", Runtime: "runtime/cpu", Dimension: 2, Weights: strings.Repeat("a", 64)}, Sampling: CLAPSamplingVersion, Pooled: []float32{1, 0}, Coverage: 7, Incomplete: true, Segments: []CLAPSegmentRecord{{Index: 0, StartSeconds: 10, EndSeconds: 17, ObservedSeconds: 7, Vector: []float32{1, 0}}}}
}

func insertStoredCLAP(t *testing.T, db *sql.DB, id string, record CLAPRecord) {
	t.Helper()
	metadata := MetadataRecord{Probe: localaudio.ProbeResult{Metadata: localaudio.Metadata{Title: &localaudio.TagValue{Value: "Song"}, ArtistCredits: []localaudio.TagValue{{Value: "Artist"}}}}}
	metadataRaw, _ := json.Marshal(metadata)
	raw, _ := json.Marshal(record)
	vector := make([]byte, len(record.Pooled)*4)
	for i, value := range record.Pooled {
		binary.LittleEndian.PutUint32(vector[i*4:], math.Float32bits(value))
	}
	for _, statement := range []struct {
		text string
		args []any
	}{
		{"INSERT INTO files VALUES(?,'root','track.flac','current','present')", []any{id}},
		{"INSERT INTO jobs VALUES(?,'current','metadata','completed','metadata/v1','','')", []any{id}},
		{"INSERT INTO jobs VALUES(?,'current','clap','completed','clap/v1','','')", []any{id}},
		{"INSERT INTO track_metadata VALUES(?,'current','metadata/v1',?)", []any{id, metadataRaw}},
		{"INSERT INTO clap_results VALUES(?,'current','clap/v1',?,?)", []any{id, vector, raw}},
	} {
		if _, err := db.Exec(statement.text, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStoredCLAPExportsSegmentsWithoutInference(t *testing.T) {
	ctx := context.Background()
	db, path := evidenceSnapshotDB(t)
	record := storedCLAPFixture()
	insertStoredCLAP(t, db, "one", record)
	space, model, generation, err := clapVectorSpace(ctx, path, "test")
	if err != nil || model == nil || *model != record.Model || space.GraphSHA256 != record.Model.Weights || generation != "test-clap" {
		t.Fatalf("CLAP contract=%+v %+v %q %v", space, model, generation, err)
	}
	source, err := openFrozenPackSource(ctx, path, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	output := filepath.Join(t.TempDir(), "exported.paipack")
	_, err = librarypack.WriteSource(ctx, output, librarypack.Pack{CorpusGeneration: "corpus-test", MetadataGeneration: "metadata-test", CLAPGeneration: generation, CLAP: space, CLAPModel: model}, source, librarypack.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := librarypack.OpenManager(ctx, t.TempDir(), librarypack.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	staged, err := manager.Stage(ctx, output)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Discard(staged) }()
	e, ok, err := staged.Generation().CLAPEvidence(ctx, "one")
	if err != nil || !ok || e.CoveredSeconds != 7 || !e.Incomplete || e.PartialReason == "" || e.Segments[0].InputSeconds != 10 || e.Segments[0].Padding != "repeat" || e.Segments[0].EndSeconds != 17 {
		t.Fatalf("stored evidence=%+v available=%v err=%v", e, ok, err)
	}
}

func TestStoredCLAPInvalidCoverageOmitsOnlySegmentEvidence(t *testing.T) {
	for _, test := range []struct {
		name     string
		duration float64
		record   CLAPRecord
	}{
		{"past_duration", 16.9, storedCLAPFixture()},
		{"overlapping_windows", 15, func() CLAPRecord {
			record := storedCLAPFixture()
			record.Segments = []CLAPSegmentRecord{
				{Index: 0, StartSeconds: 0, EndSeconds: 7.51, ObservedSeconds: 7.51, InputSeconds: 10, Padding: "repeat", Validity: "valid", Vector: []float32{1, 0}},
				{Index: 1, StartSeconds: 7.5, EndSeconds: 15, ObservedSeconds: 7.5, InputSeconds: 10, Padding: "repeat", Validity: "valid", Vector: []float32{1, 0}},
			}
			record.Coverage = 15.01
			record.Incomplete = false
			return record
		}()},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			db, path := evidenceSnapshotDB(t)
			insertStoredCLAP(t, db, "one", test.record)
			metadata := MetadataRecord{Probe: localaudio.ProbeResult{Duration: localaudio.Duration{Seconds: test.duration, Provenance: "fixture", Reliable: true}, Metadata: localaudio.Metadata{Title: &localaudio.TagValue{Value: "Song"}, ArtistCredits: []localaudio.TagValue{{Value: "Artist"}}}}}
			raw, _ := json.Marshal(metadata)
			if _, err := db.Exec("UPDATE track_metadata SET data=?", raw); err != nil {
				t.Fatal(err)
			}
			source, err := openFrozenPackSource(ctx, path, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			track, ok, err := source.Next(ctx)
			if err != nil || !ok {
				t.Fatalf("track available=%v err=%v", ok, err)
			}
			if track.CLAPEvidence != nil || !reflect.DeepEqual(track.CLAP, test.record.Pooled) || !strings.Contains(string(track.Missingness), "invalid_stored_segment_coverage") {
				t.Fatalf("invalid evidence was not marked unavailable: %+v", track)
			}
			if _, ok, err := source.Next(ctx); err != nil || ok {
				t.Fatal(err)
			}
			space, model, generation, err := clapVectorSpace(ctx, path, "test")
			if err != nil {
				t.Fatal(err)
			}
			fresh, err := openFrozenPackSource(ctx, path, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			if _, err := librarypack.WriteSource(ctx, filepath.Join(t.TempDir(), "export.paipack"), librarypack.Pack{CorpusGeneration: "corpus-test", MetadataGeneration: "metadata-test", CLAPGeneration: generation, CLAP: space, CLAPModel: model}, fresh, librarypack.Limits{}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCLAPExportRejectsMixedModelIdentity(t *testing.T) {
	db, path := evidenceSnapshotDB(t)
	one := storedCLAPFixture()
	insertStoredCLAP(t, db, "one", one)
	two := one
	two.Model.Runtime = "runtime/cuda"
	insertStoredCLAP(t, db, "two", two)
	if _, _, _, err := clapVectorSpace(context.Background(), path, "test"); err == nil {
		t.Fatal("different paired model runtimes exported as one space")
	}
}

func TestCLAPPooledRecordDoesNotInventSegments(t *testing.T) {
	record := storedCLAPFixture()
	record.Segments = nil
	if record.portableEvidence() != nil {
		t.Fatal("pooled-only record gained nominal coverage")
	}
	record = storedCLAPFixture()
	record.Sampling = legacyCLAPSamplingVersion
	evidence := record.portableEvidence()
	if evidence.Segments[0].Padding != "repeat" || evidence.Segments[0].InputSeconds != 10 {
		t.Fatal("legacy producer lost its known repeat-padding contract")
	}
	record = storedCLAPFixture()
	record.Sampling = "unknown-producer"
	evidence = record.portableEvidence()
	if evidence.Segments[0].Padding != "unknown" || evidence.Segments[0].InputSeconds != 0 {
		t.Fatal("unknown producer acquired guessed padding")
	}
}
