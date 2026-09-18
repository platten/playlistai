package evaluation

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/localcatalog"
)

type testLibraryProvider struct{ manager *librarypack.Manager }

func (p testLibraryProvider) PinLocalCatalog() (*localcatalog.Catalog, error) {
	lease, err := p.manager.Pin()
	if err != nil {
		return nil, err
	}
	return localcatalog.Open(lease, localcatalog.Options{SourceID: "library"})
}

func TestLibraryEvaluationUsesProductionOverlayAndAbstainsOnUnjudgedTracks(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fixture.paipack")
	_, err := librarypack.Write(ctx, path, librarypack.Pack{
		CorpusGeneration: "corpus", MetadataGeneration: "metadata", MERTGeneration: "mert",
		MERT:   librarypack.VectorSpace{Name: "library_mert", Dimension: 2, DType: "float32", ByteOrder: "little", Normalized: true, Model: "fixture", ModelRevision: "one", GraphSHA256: strings.Repeat("a", 64), Decoder: "fixture", Preprocessing: "fixture", Sampling: "fixture", Pooling: "fixture", Scope: "sampled_windows", Missingness: "absent"},
		Tracks: []librarypack.Track{{ID: "seed", Artist: "Seed", Title: "Origin", MERT: []float32{1, 0}}, {ID: "a", Artist: "Alpha", Title: "One", RawTags: []byte(`{"genre":"rock"}`), MERT: []float32{1, 0}}, {ID: "b", Artist: "Beta", Title: "Two", MERT: []float32{0.8, 0.6}}},
	}, librarypack.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := librarypack.OpenManager(ctx, t.TempDir(), librarypack.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	staged, err := manager.Stage(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := localcatalog.BuildIndexes(ctx, staged.Generation(), localcatalog.IndexBuildOptions{Workers: 1}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Activate(ctx, staged); err != nil {
		t.Fatal(err)
	}
	base := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "external", Display: "External - Track", Audio: []float32{1, 0}, Track: []float32{1, 0}})
	runner := Runner{Catalog: base, Resolver: base, Similarity: fakes.NewSimilarityEngine(base), Parser: rules.New(), K: 2}
	runner, release, err := runner.WithLibrary(ctx, testLibraryProvider{manager}, localcatalog.ModeLibraryOnly)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, ok := runner.library.feedback.Meta("external"); !ok {
		t.Fatal("library-only evaluation dropped base feedback view")
	}
	if _, ok := runner.Catalog.Meta("external"); ok {
		t.Fatal("feedback view changed output source policy")
	}
	localPlaylist := core.Playlist{Tracks: []core.TrackRef{{ID: "local:library:a"}}, Intent: core.MusicIntent{EssentialCriteria: []core.MusicalCriterion{{Kind: "genre", Value: "rock", Scope: "playlist"}}}}
	if violations := EssentialCriterionViolations(ctx, localPlaylist, nil, runner.Catalog); violations != 0 {
		t.Fatalf("sourced local genre falsely violated: %d", violations)
	}
	localPlaylist.Intent.EssentialCriteria[0].Value = "jazz"
	if violations := EssentialCriterionViolations(ctx, localPlaylist, nil, runner.Catalog); violations != 1 {
		t.Fatal("unverified genre escaped metrics")
	}
	item := RecommendationCase{ID: "case", ListenerID: "private", OccurredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Intent: core.MusicIntent{Version: core.CurrentIntentVersion, Mode: core.ModeSimilar, Seed: "18446744073709551615", References: []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "local:library:seed", Influence: core.InfluencePositive}}, Controls: core.IntentControls{TotalTrackCount: 2}}, Relevance: map[string]float64{"local:library:a": 3}}
	item.Intent.VerificationPolicy = core.BestAvailable
	item.Intent.Count = 2
	item.Intent.TrackCountExplicit = true
	item.Intent.Controls.AudioWeight = 1
	dataset := Dataset{Version: ContractVersion, Name: "synthetic", Evidence: EvidenceSynthetic, RecommendationCases: []RecommendationCase{item}}
	for i := 1; i < 5; i++ {
		next := item
		next.ID = fmt.Sprintf("case-%d", i)
		next.OccurredAt = item.OccurredAt.Add(time.Duration(i) * 24 * time.Hour)
		dataset.RecommendationCases = append(dataset.RecommendationCases, next)
	}
	report, err := runner.Run(ctx, dataset)
	if err != nil {
		t.Fatal(err)
	}
	variants := append(append([]VariantResult(nil), report.Development...), report.HeldOutTest...)
	seen := 0
	for _, v := range variants {
		for _, c := range v.Cases {
			seen++
			if c.Error != "" {
				t.Fatalf("%s: %s", v.Name, c.Error)
			}
			if c.ReturnedAtK != 2 || c.JudgedAtK != 1 || c.NDCGAtK != nil {
				t.Fatalf("missing judgment treated as negative: %+v", c)
			}
			if c.Generation.CatalogVersion != runner.Resolver.CatalogVersion() || c.Generation.RNGSeed != item.Intent.Seed {
				t.Fatalf("lost reproducibility: %+v", c.Generation)
			}
			for _, id := range c.Generation.TrackIDs {
				if !strings.HasPrefix(id, "local:library:") {
					t.Fatalf("escaped library source policy: %s", id)
				}
			}
		}
	}
	if seen != 4 {
		t.Fatalf("expected off/on outputs, got %d", seen)
	}
}
