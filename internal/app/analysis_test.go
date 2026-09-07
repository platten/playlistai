package app

import (
	"context"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/history"
	"github.com/platten/playlistai/internal/ports"
)

func TestOptionalAnalysisAndSeparateRetentionControls(t *testing.T) {
	ctx := context.Background()
	c, err := New(ctx, testConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	status, err := c.GetAnalysisStatus(ctx)
	if err != nil || status.Enabled || status.Available || c.AudioService() != nil {
		t.Fatalf("uninstalled analysis active: %+v %v", status, err)
	}
	if c.SetAnalysisEnabled(true) == nil {
		t.Fatal("unvalidated model enabled")
	}
	if _, err := c.History.Save(ctx, history.Record{Name: "Saved", Prompt: "keep me", IntentJSON: []byte("{}"), RequestJSON: []byte("{}"), TracksJSON: []byte("[]"), ResultJSON: []byte("{}")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Feedback.RecordFeedback(ctx, core.FeedbackEvent{Type: core.FeedbackLike, Scope: core.FeedbackScopeDurable, TrackID: "one"}); err != nil {
		t.Fatal(err)
	}
	ref := core.TrackRef{ID: "one", Artist: "Synthetic", Title: "Fixture"}
	record := core.AudioAnalysis{TrackID: ref.ID, TrackKey: core.ProvisionalRecordingKey(ref), CatalogVersion: "fixture", Model: core.AudioModelIdentity{Model: "synthetic", Revision: "fixture", Preprocessing: audio.PreprocessingVersion, Runtime: "fixture", Dimension: 2}, Identity: core.PreviewIdentity{Provider: "deezer", ProviderID: "fixture", Status: core.ResolutionResolved}, AudioSHA256: strings.Repeat("0", 64), Segments: []core.AudioSegment{{EndSeconds: 10, Embedding: []float32{1, 0}}}}
	record.ID = audio.Fingerprint(record)
	if err := c.analysis.store.Put(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveAnalysisModel(); err != nil {
		t.Fatal(err)
	}
	status, _ = c.GetAnalysisStatus(ctx)
	if status.Storage.Records != 1 {
		t.Fatal("model removal discarded reusable features")
	}
	if err := c.ClearAnalysis(ctx); err != nil {
		t.Fatal(err)
	}
	status, _ = c.GetAnalysisStatus(ctx)
	if status.Storage.Records != 0 {
		t.Fatal("explicit clear retained analysis")
	}
	saved, err := c.History.List(ctx, 10)
	if err != nil || len(saved) != 1 {
		t.Fatal("analysis clear removed playlist history")
	}
	if err := c.History.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	feedback, err := c.Feedback.ListFeedback(ctx, ports.FeedbackQuery{})
	if err != nil || len(feedback) != 1 {
		t.Fatal("analysis/history clear removed taste evidence")
	}
}
