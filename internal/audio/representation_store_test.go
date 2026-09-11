package audio

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func storedRepresentation() core.AudioRepresentation {
	a := core.AudioRepresentation{
		TrackID: "track", CatalogVersion: "catalog", TrackKey: "artist/title",
		Identity:    core.PreviewIdentity{Status: core.ResolutionResolved, Provider: "deezer", ProviderID: "123"},
		AudioSHA256: strings.Repeat("a", 64),
		Model: core.AudioRepresentationIdentity{Model: "audio-only-fixture", Revision: "revision", Preprocessing: "prep/v1",
			Runtime: "fixture/v1", Dimension: 2, WeightsSHA256: strings.Repeat("b", 64), Pooling: "mean-l2/v1"},
		Segments: []core.AudioRepresentationSegment{{StartSeconds: 2, EndSeconds: 7, Vector: []float32{1, 0}},
			{StartSeconds: 7, EndSeconds: 9, Vector: []float32{1, 0}}},
		Pooled: []float32{1, 0}, Coverage: core.PreviewCoverage{Available: true, Source: "deezer", StartSeconds: 2, EndSeconds: 9, CoveredSeconds: 7},
		AnalyzedAt: "2026-09-11T12:00:00Z",
	}
	a.ID = Fingerprint(a)
	return a
}

func TestRepresentationStoreRoundTripAndIsolation(t *testing.T) {
	parent, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	store, ctx := parent.Representations(), context.Background()
	a := storedRepresentation()
	if _, ok, err := store.Find(ctx, a.CatalogVersion, a.TrackID, a.TrackKey, a.Model); ok || err != nil {
		t.Fatalf("missing row: %v %v", ok, err)
	}
	for range 2 {
		if err := store.Put(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := store.Find(ctx, a.CatalogVersion, a.TrackID, a.TrackKey, a.Model)
	if err != nil || !ok || !reflect.DeepEqual(a, got) {
		t.Fatalf("round trip: %+v %v %v", got, ok, err)
	}
	got.Pooled[0] = 0
	got.Segments[0].Vector[0] = 0
	again, ok, err := store.Find(ctx, a.CatalogVersion, a.TrackID, a.TrackKey, a.Model)
	if err != nil || !ok || !reflect.DeepEqual(a, again) {
		t.Fatal("caller mutated persisted evidence")
	}
	u, err := store.Usage(ctx)
	if err != nil || u.Records != 1 || u.Bytes == 0 {
		t.Fatalf("idempotence: %+v %v", u, err)
	}
	clap := validStoredAnalysis()
	if err := parent.Put(ctx, clap); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := parent.Find(ctx, clap.CatalogVersion, clap.TrackID, clap.TrackKey, clap.Model); !ok || err != nil {
		t.Fatalf("CLAP deleted: %v", err)
	}
	u, err = store.Usage(ctx)
	if err != nil || u.Records != 0 || u.Bytes != 0 {
		t.Fatalf("clear: %+v %v", u, err)
	}
	if err := store.Put(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := parent.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Find(ctx, a.CatalogVersion, a.TrackID, a.TrackKey, a.Model); !ok || err != nil {
		t.Fatalf("CLAP clear deleted representation: %v", err)
	}
}

func TestRepresentationCacheIdentity(t *testing.T) {
	parent, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	store, ctx := parent.Representations(), context.Background()
	a := storedRepresentation()
	if err := store.Put(ctx, a); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		name   string
		mutate func(*core.AudioRepresentation)
	}{
		{"model", func(a *core.AudioRepresentation) { a.Model.Model += "x" }},
		{"revision", func(a *core.AudioRepresentation) { a.Model.Revision += "x" }},
		{"preprocessing", func(a *core.AudioRepresentation) { a.Model.Preprocessing += "x" }},
		{"runtime", func(a *core.AudioRepresentation) { a.Model.Runtime += "x" }},
		{"dimension", func(a *core.AudioRepresentation) { a.Model.Dimension++ }},
		{"weights", func(a *core.AudioRepresentation) { a.Model.WeightsSHA256 = strings.Repeat("c", 64) }},
		{"pooling", func(a *core.AudioRepresentation) { a.Model.Pooling += "x" }},
		{"catalog", func(a *core.AudioRepresentation) { a.CatalogVersion += "x" }},
		{"track", func(a *core.AudioRepresentation) { a.TrackID += "x" }},
		{"recording key", func(a *core.AudioRepresentation) { a.TrackKey += "x" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			b := a
			change.mutate(&b)
			if _, ok, err := store.Find(ctx, b.CatalogVersion, b.TrackID, b.TrackKey, b.Model); ok || err != nil {
				t.Fatalf("incompatible hit: %v %v", ok, err)
			}
		})
	}
}

func TestRepresentationRejectsInvalidEvidence(t *testing.T) {
	parent, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	for _, tc := range []struct {
		name   string
		mutate func(*core.AudioRepresentation)
	}{
		{"missing weights", func(a *core.AudioRepresentation) { a.Model.WeightsSHA256 = "" }},
		{"missing pooling", func(a *core.AudioRepresentation) { a.Model.Pooling = "" }},
		{"invalid hash", func(a *core.AudioRepresentation) { a.AudioSHA256 = "unknown" }},
		{"unresolved", func(a *core.AudioRepresentation) { a.Identity.Status = core.ResolutionAmbiguous }},
		{"wrong provider", func(a *core.AudioRepresentation) { a.Identity.Provider = "other" }},
		{"bad time", func(a *core.AudioRepresentation) { a.AnalyzedAt = "yesterday" }},
		{"missing pooled", func(a *core.AudioRepresentation) { a.Pooled = nil }},
		{"zero pooled", func(a *core.AudioRepresentation) { a.Pooled = []float32{0, 0} }},
		{"infinite pooled", func(a *core.AudioRepresentation) { a.Pooled[0] = float32(math.Inf(1)) }},
		{"nonunit segment", func(a *core.AudioRepresentation) { a.Segments[0].Vector[0] = 2 }},
		{"dimension mismatch", func(a *core.AudioRepresentation) { a.Model.Dimension = 3 }},
		{"missing segments", func(a *core.AudioRepresentation) { a.Segments = nil }},
		{"unknown coverage", func(a *core.AudioRepresentation) { a.Coverage.Available = false }},
		{"wrong coverage source", func(a *core.AudioRepresentation) { a.Coverage.Source = "other" }},
		{"nan coverage", func(a *core.AudioRepresentation) { a.Coverage.CoveredSeconds = math.NaN() }},
		{"padding counted", func(a *core.AudioRepresentation) { a.Coverage.CoveredSeconds = 10 }},
		{"overlap", func(a *core.AudioRepresentation) { a.Segments[1].StartSeconds = 6 }},
		{"negative segment", func(a *core.AudioRepresentation) { a.Segments[0].StartSeconds = -1 }},
		{"nan segment", func(a *core.AudioRepresentation) { a.Segments[0].EndSeconds = math.NaN() }},
		{"segment outside coverage", func(a *core.AudioRepresentation) { a.Segments[1].EndSeconds = 10 }},
		{"incorrect coverage start", func(a *core.AudioRepresentation) { a.Coverage.StartSeconds = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := storedRepresentation()
			tc.mutate(&a)
			a.ID = ""
			a.ID = Fingerprint(a)
			if err := parent.Representations().Put(context.Background(), a); err == nil {
				t.Fatal("invalid evidence persisted")
			}
		})
	}
	a := storedRepresentation()
	a.PreviewOffsetKnown = true // valid field change still requires a new fingerprint
	if err := parent.Representations().Put(context.Background(), a); err == nil {
		t.Fatal("tampered identity accepted")
	}
}

func TestRepresentationCorruptionAndCancellation(t *testing.T) {
	for _, damage := range []string{"json", "vector", "identity", "query"} {
		t.Run(damage, func(t *testing.T) {
			parent, err := OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer parent.Close()
			store, ctx := parent.Representations(), context.Background()
			a := storedRepresentation()
			if err := store.Put(ctx, a); err != nil {
				t.Fatal(err)
			}
			b := storedRepresentation()
			switch damage {
			case "vector":
				b.Pooled = nil
			case "identity":
				b.AnalyzedAt = "2026-09-12T12:00:00Z"
			case "query":
				b.TrackID = "different"
				b.ID = ""
				b.ID = Fingerprint(b)
			}
			raw, err := json.Marshal(b)
			if err != nil {
				t.Fatal(err)
			}
			if damage == "json" {
				raw = []byte("{")
			}
			if _, err := parent.db.Exec("UPDATE audio_representation SET data=?", string(raw)); err != nil {
				t.Fatal(err)
			}
			if _, ok, err := store.Find(ctx, a.CatalogVersion, a.TrackID, a.TrackKey, a.Model); ok || err == nil {
				t.Fatal("corrupt cache trusted")
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if err := store.Put(canceled, a); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, _, err := store.Find(canceled, a.CatalogVersion, a.TrackID, a.TrackKey, a.Model); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := store.Usage(canceled); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err := store.Clear(canceled); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func TestRepresentationMigrationPreservesLegacyDatabase(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "audio-analysis.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE analysis (id TEXT PRIMARY KEY, catalog TEXT NOT NULL, track TEXT NOT NULL,
track_key TEXT NOT NULL, model TEXT NOT NULL, data TEXT NOT NULL);
CREATE TABLE assessment (analysis_id TEXT NOT NULL, intent TEXT NOT NULL, policy TEXT NOT NULL, data TEXT NOT NULL,
PRIMARY KEY(analysis_id,intent,policy));`)
	if err != nil {
		t.Fatal(err)
	}
	a := validStoredAnalysis()
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("INSERT INTO analysis VALUES(?,?,?,?,?,?)", a.ID, a.CatalogVersion, a.TrackID, a.TrackKey, Fingerprint(a.Model), string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		parent, err := OpenStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		got, ok, err := parent.Find(context.Background(), a.CatalogVersion, a.TrackID, a.TrackKey, a.Model)
		if !ok || err != nil || !reflect.DeepEqual(got, a) {
			t.Fatalf("legacy row changed: %v %v", ok, err)
		}
		if err := parent.Representations().Put(context.Background(), storedRepresentation()); err != nil {
			t.Fatal(err)
		}
		if err := parent.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
