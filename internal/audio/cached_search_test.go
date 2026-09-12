package audio

import (
	"context"
	"errors"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestCachedScanRejectsIncompatibleAndCorruptRecords(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	base := validStoredAnalysis()
	put := func(a core.AudioAnalysis) {
		t.Helper()
		a.ID = ""
		a.ID = Fingerprint(a)
		if err := store.Put(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	put(base)
	other := validStoredAnalysis()
	other.Model.Revision = "2"
	put(other)
	other = validStoredAnalysis()
	other.CatalogVersion = "another"
	put(other)
	other = validStoredAnalysis()
	other.TrackID = "z"
	put(other)
	if _, err := store.db.Exec(`INSERT INTO analysis(id,catalog,track,track_key,model,data) VALUES(?,?,?,?,?,?)`, "corrupt", base.CatalogVersion, "a", base.TrackKey, Fingerprint(base.Model), `{}`); err != nil {
		t.Fatal(err)
	}
	var got []string
	err = store.VisitAnalyses(context.Background(), base.CatalogVersion, base.Model, 10, func(a core.AudioAnalysis) bool { got = append(got, a.TrackID); return true })
	if err != nil || len(got) != 2 || got[0] != "track" || got[1] != "z" {
		t.Fatalf("records=%v error=%v", got, err)
	}
	visits := 0
	err = store.VisitAnalyses(context.Background(), base.CatalogVersion, base.Model, 10, func(core.AudioAnalysis) bool { visits++; return false })
	if err != nil || visits != 1 {
		t.Fatalf("callback stop: %d %v", visits, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.VisitAnalyses(ctx, base.CatalogVersion, base.Model, 10, func(core.AudioAnalysis) bool { t.Fatal("visited after cancellation"); return true }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCachedScanKeepsLatestVersionPerRecordingAndHonorsLimit(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	base := validStoredAnalysis()
	for _, end := range []float64{10, 20} {
		a := validStoredAnalysis()
		a.Segments[0].EndSeconds = end
		a.ID = ""
		a.ID = Fingerprint(a)
		if err := store.Put(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	for _, limit := range []int{0, 1, 10} {
		var got []core.AudioAnalysis
		err := store.VisitAnalyses(context.Background(), base.CatalogVersion, base.Model, limit, func(a core.AudioAnalysis) bool { got = append(got, a); return true })
		if err != nil || len(got) != min(limit, 1) {
			t.Fatalf("limit=%d got=%v err=%v", limit, got, err)
		}
		if len(got) > 0 && got[0].Segments[0].EndSeconds != 20 {
			t.Fatal("older version shadowed newest")
		}
	}
}
