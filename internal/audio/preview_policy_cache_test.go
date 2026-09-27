package audio

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestOldPreviewPolicyIsCacheMissWithoutDeletingHistory(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	clap, dsp, mert := validStoredAnalysis(), validStoredDSP(), storedRepresentation()
	clap.Identity.PolicyVersion, dsp.Identity.PolicyVersion, mert.Identity.PolicyVersion = "", "", ""
	clap.ID, dsp.ID, mert.ID = "", "", ""
	clap.ID, dsp.ID, mert.ID = Fingerprint(clap), Fingerprint(dsp), Fingerprint(mert)
	// Old structurally valid source rows remain storable and hash-valid for
	// immutable history. Only fresh lookup/search eligibility changes.
	if err := store.Put(ctx, clap); err != nil {
		t.Fatal(err)
	}
	if err := store.DSP().Put(ctx, dsp); err != nil {
		t.Fatal(err)
	}
	if err := store.Representations().Put(ctx, mert); err != nil {
		t.Fatal(err)
	}
	if _, hit, err := store.Find(ctx, clap.CatalogVersion, clap.TrackID, clap.TrackKey, clap.Model); hit || err != nil {
		t.Fatalf("legacy CLAP: hit=%v err=%v", hit, err)
	}
	if _, hit, err := store.DSP().Find(ctx, dsp.CatalogVersion, dsp.TrackID, dsp.TrackKey, dsp.Version); hit || err != nil {
		t.Fatalf("legacy DSP: hit=%v err=%v", hit, err)
	}
	if _, hit, err := store.Representations().Find(ctx, mert.CatalogVersion, mert.TrackID, mert.TrackKey, mert.Model); hit || err != nil {
		t.Fatalf("legacy MERT: hit=%v err=%v", hit, err)
	}
	visits := 0
	if err := store.VisitAnalyses(ctx, clap.CatalogVersion, clap.Model, -1, func(core.AudioAnalysis) bool { visits++; return true }); err != nil || visits != 0 {
		t.Fatalf("legacy CLAP search: visits=%d err=%v", visits, err)
	}
	result, err := store.Representations().Search(ctx, searchQuery(10))
	if err != nil || result.SearchableTracks != 0 || len(result.Matches) != 0 {
		t.Fatalf("legacy MERT search: %+v err=%v", result, err)
	}
	for table, want := range map[string]any{"analysis": clap, "dsp_analysis": dsp, "audio_representation": mert} {
		var raw string
		if err := store.db.QueryRowContext(ctx, "SELECT data FROM "+table).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		expected, err := json.Marshal(want)
		if err != nil || raw != string(expected) {
			t.Fatalf("historical %s source bytes changed", table)
		}
	}
}

func TestPreviewPolicyRebuildsLegacyProjectionWithoutChangingSources(t *testing.T) {
	ctx, dir := context.Background(), t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	old := searchRepresentation("legacy", "legacy-key", 0)
	old.Identity.PolicyVersion, old.ID = "", ""
	old.ID = Fingerprint(old)
	current := searchRepresentation("current", "current-key", .2)
	for _, row := range []core.AudioRepresentation{old, current} {
		if err := store.Representations().Put(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate the previously persisted v1 projection, which considered the
	// old identity valid. Opening under v2 must invalidate that derived view.
	if _, err := store.db.Exec(`UPDATE audio_representation_vector SET valid=1;
UPDATE audio_representation_projection_policy SET version='pooled-exact/v1'`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	result, err := store.Representations().Search(ctx, searchQuery(10))
	if err != nil || result.SearchableTracks != 1 || len(result.Matches) != 1 || result.Matches[0].Representation.ID != current.ID {
		t.Fatalf("projection migration: %+v err=%v", result, err)
	}
	usage, err := store.Representations().Usage(ctx)
	if err != nil || usage.Records != 2 {
		t.Fatalf("source rows removed: %+v err=%v", usage, err)
	}
	var raw string
	if err := store.db.QueryRowContext(ctx, `SELECT data FROM audio_representation WHERE id=?`, old.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(old)
	if raw != string(want) {
		t.Fatal("projection rebuild changed legacy source payload")
	}
}
