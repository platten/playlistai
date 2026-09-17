package localcatalog

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/reco/multichannel"
	"github.com/platten/playlistai/internal/similarity/brute"
)

type testBase struct{}

func (testBase) Len() int { return 1 }
func (testBase) Dim() int { return 2 }
func (testBase) ID(i int) string {
	if i == 0 {
		return "bundled"
	}
	return ""
}
func (testBase) RowOf(id string) (int, bool) { return 0, id == "bundled" }
func (testBase) Meta(id string) (core.TrackMeta, bool) {
	if id == "bundled" {
		return core.TrackMeta{Ref: core.TrackRef{ID: id, Artist: "Bundled", Title: "Only"}}, true
	}
	return core.TrackMeta{}, false
}
func (testBase) VectorsByRow(int) (ports.Vectors, bool) {
	return ports.Vectors{Audio: []float32{1, 0}, Track: []float32{1, 0}}, true
}
func (testBase) Vectors(id string) (ports.Vectors, bool) {
	if id == "bundled" {
		return testBase{}.VectorsByRow(0)
	}
	return ports.Vectors{}, false
}
func (testBase) RawRow(int) ([]int8, []int8, bool) { return []int8{127, 0}, []int8{127, 0}, true }
func (testBase) Resolve(string, int) []core.TrackRef {
	return []core.TrackRef{{ID: "bundled", Artist: "Bundled", Title: "Only"}}
}
func (testBase) ResolveReference(core.IntentReference) core.ReferenceResolution {
	return core.ReferenceResolution{Status: core.ResolutionUnresolved, CatalogVersion: "base"}
}
func (testBase) CatalogVersion() string { return "base" }

type baseRetriever struct{}

func (baseRetriever) Retrieve(context.Context, ports.RetrievalRequest) ([]core.Candidate, error) {
	return []core.Candidate{{Track: core.TrackRef{ID: "bundled", Artist: "Bundled", Title: "Only"}}}, nil
}

type packProvider struct{ manager *librarypack.Manager }

func (p packProvider) PinLocalCatalog() (*Catalog, error) {
	lease, err := p.manager.Pin()
	if err != nil {
		return nil, err
	}
	return Open(lease, Options{SourceID: "test"})
}

func TestRecommendationOverlayRetrievesOutOfCatalogTracksAndHonorsLibraryOnly(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	archive := filepath.Join(dir, "library.paipack")
	space := librarypack.VectorSpace{Name: "library_mert", Dimension: 2, DType: "float32", ByteOrder: "little", Normalized: true, Model: "mert", ModelRevision: "rev", GraphSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Decoder: "decoder", Preprocessing: "prep", Sampling: "sample", Pooling: "pool", Scope: "sampled_windows", Missingness: "absent"}
	_, err := librarypack.Write(ctx, archive, librarypack.Pack{CreatedAt: time.Unix(1, 0), CorpusGeneration: "corpus", MetadataGeneration: "metadata", MERTGeneration: "mert", MERT: space, Tracks: []librarypack.Track{
		{ID: "metadata-track", Artist: "Local Artist", Title: "Metadata Song"},
		{ID: "mert-track", Artist: "Local Artist", Title: "MERT Song", MERT: []float32{1, 0}},
		{ID: "neighbor", Artist: "Other Local", Title: "Neighbor", MERT: []float32{.8, .6}},
		{ID: "genre-track", Artist: "Tagged Local", Title: "Rock Song", RawTags: []byte(`{"genre":"rock"}`)},
	}}, librarypack.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := librarypack.OpenManager(ctx, filepath.Join(dir, "managed"), librarypack.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	staged, err := manager.Stage(ctx, archive)
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.Activate(ctx, staged); err != nil {
		t.Fatal(err)
	}
	provider := packProvider{manager}
	base := testBase{}
	overlay, err := PinRecommendationOverlay(ctx, provider, base, base, baseRetriever{}, ModeLibraryOnly, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer overlay.Close()
	metadata, err := overlay.Retriever.Retrieve(ctx, ports.RetrievalRequest{Intent: core.MusicIntent{References: []core.IntentReference{{Kind: core.ReferenceTrack, Query: "Metadata Song", Influence: core.InfluencePositive}}}, AttemptedIDs: map[string]struct{}{}})
	if err != nil {
		t.Fatal(err)
	}
	if !containsCandidate(metadata, "local:test:metadata-track") || containsCandidate(metadata, "bundled") {
		t.Fatalf("library-only metadata retrieval=%+v", metadata)
	}
	mert, err := overlay.Retriever.Retrieve(ctx, ports.RetrievalRequest{Intent: core.MusicIntent{References: []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "local:test:mert-track", Influence: core.InfluencePositive}}}, AttemptedIDs: map[string]struct{}{}})
	if err != nil {
		t.Fatal(err)
	}
	if !containsCandidate(mert, "local:test:neighbor") || containsCandidate(mert, "bundled") {
		t.Fatalf("library-only MERT retrieval=%+v", mert)
	}
	if _, ok := overlay.Catalog.Meta("local:test:metadata-track"); !ok {
		t.Fatal("local metadata absent from composite catalog")
	}

	engine := multichannel.New(base, brute.New(base), base, multichannel.DefaultConfig())
	engine.WithRequestOverlayProvider(func(ctx context.Context, catalog ports.Catalog, resolver ports.ReferenceResolver, retriever ports.CandidateRetriever) (multichannel.RequestOverlay, error) {
		pinned, err := PinRecommendationOverlay(ctx, provider, catalog, resolver, retriever, ModeLibraryOnly, 2)
		if err != nil {
			return multichannel.RequestOverlay{}, err
		}
		return multichannel.RequestOverlay{Catalog: pinned.Catalog, Resolver: pinned.Resolver, Retriever: pinned.Retriever, Release: pinned.Close}, nil
	})
	playlist, err := engine.Build(ctx, core.MusicIntent{Version: core.CurrentIntentVersion, Mode: core.ModeSimilar, Count: 1, TrackCountExplicit: true, References: []core.IntentReference{{Kind: core.ReferenceTrack, Query: "MERT Song", Influence: core.InfluencePositive}}, Controls: core.IntentControls{RecommendationMode: core.AcousticBrainzFirst, TotalTrackCount: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(playlist.Tracks) != 1 || playlist.Tracks[0].ID != "local:test:neighbor" || playlist.EvidenceCatalogVersion == "" {
		t.Fatalf("end-to-end local recommendation=%+v catalog=%q outcome=%+v intent=%+v", playlist.Tracks, playlist.EvidenceCatalogVersion, playlist.Outcome, playlist.Intent)
	}
	genrePlaylist, err := engine.Build(ctx, core.MusicIntent{Version: core.CurrentIntentVersion, Mode: core.ModeSimilar, Count: 1, TrackCountExplicit: true,
		VerificationPolicy: core.BestAvailable,
		EssentialCriteria:  []core.MusicalCriterion{{Kind: "genre", Value: "rock", Scope: "playlist"}},
		Preferences:        core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "rock", Influence: core.InfluencePositive}}},
		Controls:           core.IntentControls{RecommendationMode: core.AcousticBrainzFirst, TotalTrackCount: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(genrePlaylist.Tracks) != 1 || genrePlaylist.Tracks[0].ID != "local:test:genre-track" {
		t.Fatalf("genre-only local recommendation=%+v outcome=%+v", genrePlaylist.Tracks, genrePlaylist.Outcome)
	}
}

func containsCandidate(values []core.Candidate, id string) bool {
	for _, value := range values {
		if value.Track.ID == id {
			return true
		}
	}
	return false
}
