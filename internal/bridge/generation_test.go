package bridge

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/history"
)

func TestGenerationProgressAndStopAreScoped(t *testing.T) {
	a := New(newLoadedContainer(t), nil)
	ctx1, finish1 := a.beginGeneration(context.Background(), "old")
	defer finish1()
	ctx2, finish2 := a.beginGeneration(context.Background(), "new")
	defer finish2()
	a.StopAndKeepCheckedTracks("old")
	select {
	case <-generationFromContext(ctx1).stop:
	default:
		t.Fatal("old generation not stopped")
	}
	select {
	case <-generationFromContext(ctx2).stop:
		t.Fatal("stale stop affected newer generation")
	default:
	}
	if generationProgress(ctx1).generationID != "old" || generationProgress(ctx2).generationID != "new" {
		t.Fatal("progress ID lost")
	}
	if ctx1.Err() != nil {
		t.Fatal("keep-checked canceled final sequencing")
	}
}

func TestHistoryRetainsAudioSnapshotAndOriginalDescription(t *testing.T) {
	c := newLoadedContainer(t)
	a := New(c, nil)
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, OriginalDescription: "relaxing but not sleepy", Seed: "18446744073709551615"}.Normalized()
	identity, err := generationIdentity(intent, "catalog", "audio/v1", "profile", "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	before := identity.ID
	result := PlaylistResult{Intent: intent, Seed: intent.Seed, AudioEvidence: &core.AudioEvidenceSnapshot{ID: "evidence-one", PolicyVersion: "fixture"}, Reproducibility: identity}
	withEvidenceIdentity(&result.Reproducibility, result.AudioEvidence)
	if result.Reproducibility.ID == before || result.Reproducibility.EvidenceSnapshot != "evidence-one" {
		t.Fatal("evidence absent from generation identity")
	}
	raw, _ := json.Marshal(result)
	request, _ := json.Marshal(BuildPlaylistRequest{Version: core.CurrentIntentVersion, Intent: intent, Reproducibility: result.Reproducibility})
	intentRaw, _ := json.Marshal(intent)
	record, err := c.History.Save(context.Background(), history.Record{Name: "Snapshot", Prompt: intent.OriginalDescription, IntentJSON: intentRaw, RequestJSON: request, TracksJSON: []byte("[]"), ResultJSON: raw})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := a.LoadSavedPlaylist(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Result.AudioEvidence == nil || loaded.Result.AudioEvidence.ID != "evidence-one" || loaded.Request.Intent.OriginalDescription != intent.OriginalDescription || loaded.Result.Seed != intent.Seed {
		t.Fatalf("history lost evidence/description/seed: %+v", loaded)
	}
}

func TestHistoryRetainsEnhancedReplayInput(t *testing.T) {
	c := newLoadedContainer(t)
	a := New(c, nil)
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, OriginalDescription: "deep bass", Seed: "7"}.Normalized()
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	input := core.EnhancedAudioInput{PolicyVersion: core.EnhancedAudioPolicyVersion, CatalogVersion: "catalog", PositiveCentroid: []float32{1, 0}}
	result := PlaylistResult{Intent: intent, Seed: intent.Seed, EnhancedAudio: &input}
	a.saveGenerated(context.Background(), "Enhanced", intent.OriginalDescription, intent, BuildPlaylistRequest{Intent: intent}, result)
	rows, err := c.History.List(context.Background(), 1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("save: %v", err)
	}
	loaded, err := a.LoadSavedPlaylist(rows[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Request.EnhancedAudio == nil || loaded.Result.EnhancedAudio == nil || loaded.Request.EnhancedAudio.PositiveCentroid[0] != 1 || loaded.Request.EnhancedAudio.PolicyVersion != core.EnhancedAudioPolicyVersion {
		t.Fatal("enhanced replay snapshot lost")
	}
}
