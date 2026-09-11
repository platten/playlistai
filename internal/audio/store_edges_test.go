package audio

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func validStoredAnalysis() core.AudioAnalysis {
	a := core.AudioAnalysis{
		TrackID: "track", TrackKey: "artist/title", CatalogVersion: "catalog",
		Model:       core.AudioModelIdentity{Model: "fixture", Revision: "1", Preprocessing: PreprocessingVersion, Runtime: "fixture", Dimension: 2},
		Identity:    core.PreviewIdentity{Provider: "deezer", ProviderID: "track", Status: core.ResolutionResolved},
		AudioSHA256: strings.Repeat("0", 64),
		Segments:    []core.AudioSegment{{StartSeconds: 0, EndSeconds: 10, Embedding: []float32{1, 0}}},
	}
	a.ID = Fingerprint(a)
	return a
}

func TestAnalysisStoreRejectsCorruptOrUnverifiedEvidence(t *testing.T) {
	for _, mutate := range []struct {
		name  string
		apply func(*core.AudioAnalysis)
	}{
		{"unresolved", func(a *core.AudioAnalysis) { a.Identity.Status = core.ResolutionAmbiguous }},
		{"bad hash", func(a *core.AudioAnalysis) { a.AudioSHA256 = "not-hex" }},
		{"negative time", func(a *core.AudioAnalysis) { a.Segments[0].StartSeconds = -1 }},
		{"nonfinite time", func(a *core.AudioAnalysis) { a.Segments[0].EndSeconds = math.NaN() }},
		{"wrong dimension", func(a *core.AudioAnalysis) { a.Model.Dimension = 3 }},
		{"nonfinite vector", func(a *core.AudioAnalysis) { a.Segments[0].Embedding[0] = float32(math.Inf(1)) }},
		{"zero vector", func(a *core.AudioAnalysis) { a.Segments[0].Embedding[0] = 0 }},
		{"wrong identity", func(a *core.AudioAnalysis) { a.ID = "tampered" }},
		{"invalid sampling", func(a *core.AudioAnalysis) { a.Sampling = &core.AudioSampling{Policy: "unknown"} }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			store, err := OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			a := validStoredAnalysis()
			mutate.apply(&a)
			if err := store.Put(context.Background(), a); err == nil {
				t.Fatal("invalid derived evidence was persisted")
			}
			usage, err := store.Usage(context.Background())
			if err != nil || usage.Records != 0 {
				t.Fatalf("invalid row survived: %+v %v", usage, err)
			}
		})
	}
	if Fingerprint(math.NaN()) != "" {
		t.Fatal("nonserializable evidence received an identity")
	}
}

func TestAnalysisStoreRevalidatesPersistedPayloadAndQueryIdentity(t *testing.T) {
	for _, damage := range []string{"json", "validation", "identity"} {
		t.Run(damage, func(t *testing.T) {
			store, err := OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			a := validStoredAnalysis()
			if err := store.Put(context.Background(), a); err != nil {
				t.Fatal(err)
			}
			raw := []byte("{")
			if damage != "json" {
				changed := a
				if damage == "validation" {
					changed.Identity.Status = core.ResolutionAmbiguous
				} else {
					changed.TrackID = "other"
				}
				raw, err = json.Marshal(changed)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.db.Exec("UPDATE analysis SET data=?", string(raw)); err != nil {
				t.Fatal(err)
			}
			if _, ok, err := store.Find(context.Background(), a.CatalogVersion, a.TrackID, a.TrackKey, a.Model); err == nil || ok {
				t.Fatal("corrupt persisted row was trusted")
			}
		})
	}
}

func TestAnalysisStorageFailuresNeverCreateOrphanAssessments(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(file, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if store, err := OpenStore(file); err == nil {
		store.Close()
		t.Fatal("file accepted as data directory")
	}
	if err := os.WriteFile(filepath.Join(dir, "audio-analysis.sqlite"), []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if store, err := OpenStore(dir); err == nil {
		store.Close()
		t.Fatal("corrupt database opened")
	}
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	assessment := core.AudioAssessment{AnalysisID: "missing", IntentFingerprint: "intent", PolicyVersion: "policy"}
	if err := store.PutAssessment(ctx, assessment); err != nil {
		t.Fatal(err)
	}
	usage, err := store.Usage(ctx)
	if err != nil || usage.Assessments != 0 {
		t.Fatalf("orphan assessment persisted: %+v %v", usage, err)
	}
	if err := store.PutAssessment(ctx, core.AudioAssessment{}); err == nil {
		t.Fatal("incomplete assessment accepted")
	}
	assessment.Clauses = []core.AudioClauseAssessment{{Score: math.NaN()}}
	if err := store.PutAssessment(ctx, assessment); err == nil {
		t.Fatal("nonfinite assessment accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	a := validStoredAnalysis()
	if _, _, err := store.Find(canceled, a.CatalogVersion, a.TrackID, a.TrackKey, a.Model); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := store.Put(canceled, a); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
