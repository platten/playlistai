package audio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
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

func TestCachedSearchFindsBestAfterTwentyThousandRowsAndCancelsBetweenPages(t *testing.T) {
	service, _, resolver, _ := testService(t)
	store := service.Store.(*Store)
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(`INSERT INTO analysis(id,catalog,track,track_key,model,data) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	var rows []fakes.CatalogTrack
	for i := range 20001 {
		id := fmt.Sprintf("track-%05d", i)
		track := core.TrackRef{ID: id, Artist: "Synthetic", Title: id}
		rows = append(rows, fakes.CatalogTrack{ID: id, Display: track.Display()})
		record := validStoredAnalysis()
		record.TrackID, record.TrackKey = id, core.ProvisionalRecordingKey(track)
		if i == 20000 {
			record.Segments[0].Embedding = []float32{0, 1}
		}
		record.ID = ""
		record.ID = Fingerprint(record)
		raw, _ := json.Marshal(record)
		if _, err := stmt.Exec(record.ID, record.CatalogVersion, id, record.TrackKey, Fingerprint(record.Model), string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	cat := fakes.NewCatalog(2, rows...)
	intent := core.MusicIntent{VerificationPolicy: core.BestAvailable, Preferences: core.SemanticPreferences{Moods: []core.IntentPreference{{Value: "sleepy", Influence: core.InfluencePositive}}}}
	session, err := service.Begin(context.Background(), intent, "catalog", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	candidates, err := session.CachedCandidates(context.Background(), cat, 1, nil)
	if err != nil || len(candidates) != 1 || candidates[0].Track.ID != "track-20000" {
		t.Fatalf("later strongest cache row hidden: %+v %v", candidates, err)
	}
	if resolver.calls != 0 || len(session.Snapshot().Assessments) != 0 {
		t.Fatal("cache search spent acquisition or assessment admissions")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	visited := 0
	err = store.VisitAnalyses(ctx, "catalog", validStoredAnalysis().Model, -1, func(core.AudioAnalysis) bool {
		visited++
		if visited == cachedSearchPageSize+1 {
			cancel()
		}
		return true
	})
	if !errors.Is(err, context.Canceled) || visited != cachedSearchPageSize+1 {
		t.Fatalf("pagination ignored cancellation: visits=%d err=%v", visited, err)
	}
}
