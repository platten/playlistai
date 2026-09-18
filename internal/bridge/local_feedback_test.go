package bridge

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/ports"
)

func TestImportedLibraryFeedbackAndAcceptanceUsePinnedEvidence(t *testing.T) {
	ctx := context.Background()
	c := newLoadedContainer(t)
	api := New(c, nil)
	archive := filepath.Join(t.TempDir(), "feedback.paipack")
	space := librarypack.VectorSpace{Name: "library_mert", Dimension: 2, DType: "float32", ByteOrder: "little", Normalized: true, Model: "mert", ModelRevision: "rev", GraphSHA256: strings.Repeat("a", 64), Decoder: "fixture", Preprocessing: "prep", Sampling: "sample", Pooling: "pool", Scope: "sampled_windows", Missingness: "absent"}
	manifest, err := librarypack.Write(ctx, archive, librarypack.Pack{CorpusGeneration: "corpus", MetadataGeneration: "metadata", MERTGeneration: "mert", MERT: space, Tracks: []librarypack.Track{{ID: "liked", Artist: "Local Artist", Title: "Liked Track", MERT: []float32{1, 0}}, {ID: "accepted", Artist: "Local Artist", Title: "Accepted Track", MERT: []float32{0, 1}}}}, librarypack.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.ImportLocalLibraryPack(ctx, archive); err != nil {
		t.Fatal(err)
	}
	if _, err := api.SetLocalLibraryMode("library_only"); err != nil {
		t.Fatal(err)
	}
	like, err := api.RecordFeedback(ctx, RecordFeedbackRequest{Type: core.FeedbackLike, TrackID: "local:primary:liked"})
	if err != nil || like.Profile.ColdStart || like.Profile.PositiveEvidence != 1 || like.Profile.ClusterCount != 1 {
		t.Fatalf("local like: %+v %v", like, err)
	}
	if !strings.Contains(like.Profile.CatalogVersion, manifest.PackID) {
		t.Fatalf("pinned pack absent from profile identity: %+v", like)
	}
	profile, err := api.tasteProfile(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Mode: core.ModeSimilar, Count: 1, Seed: core.RNGSeed("123"), Controls: core.IntentControls{RecommendationMode: core.EnhancedHybrid, TotalTrackCount: 1}}.Normalized()
	identity, err := generationIdentity(intent, api.catalogVersion(), api.recommendationVersionFor(intent), profile.AlgorithmVersion, profile.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := api.profileForBuild(ctx, BuildPlaylistRequest{Reproducibility: identity}, intent)
	if err != nil || replayed.SnapshotID != profile.SnapshotID {
		t.Fatalf("library-only replay rejected combined feedback identity: %+v %v", replayed, err)
	}
	changed := identity
	changed.AlgorithmVersion += "+library-evidence/v1"
	if _, err := api.profileForBuild(ctx, BuildPlaylistRequest{Reproducibility: changed}, intent); err == nil {
		t.Fatal("changed experiment version accepted for exact replay")
	}
	accept, err := api.RecordTrackAcceptance(ctx, RecordAcceptanceRequest{TrackIDs: []string{"local:primary:accepted", "local:primary:accepted"}, RequestID: "export"})
	if err != nil || accept.EventCount != 1 || accept.Profile.RequestEvidence != 1 {
		t.Fatalf("local acceptance: %+v %v", accept, err)
	}
	// Output source mode does not prevent durable feedback for bundled music.
	if _, err := api.RecordFeedback(ctx, RecordFeedbackRequest{Type: core.FeedbackDislike, TrackID: "seed0001"}); err != nil {
		t.Fatal(err)
	}
	events, err := c.Feedback.ListFeedback(ctx, ports.FeedbackQuery{RequestID: "export"})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if !strings.Contains(event.Versions.Catalog, manifest.PackID) {
			t.Fatalf("feedback lacks pinned identity: %+v", event)
		}
	}
	if _, err := api.RecordTrackAcceptance(ctx, RecordAcceptanceRequest{TrackIDs: []string{"local:primary:liked", "local:primary:missing"}, RequestID: "invalid"}); err == nil {
		t.Fatal("unknown local track accepted")
	}
	invalid, err := c.Feedback.ListFeedback(ctx, ports.FeedbackQuery{RequestID: "invalid"})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range invalid {
		if event.RequestID == "invalid" {
			t.Fatal("invalid batch partially persisted")
		}
	}
}
