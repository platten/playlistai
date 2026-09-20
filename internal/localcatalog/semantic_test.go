package localcatalog

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
)

func openSemanticCatalog(t *testing.T, globalPairing bool, modify ...func(*librarypack.Pack)) (*CompositeCatalog, core.AudioModelIdentity) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	space := librarypack.VectorSpace{Name: "library_clap", Dimension: 2, DType: "float32", ByteOrder: "little", Normalized: true, Model: "clap", ModelRevision: "paired", GraphSHA256: strings.Repeat("b", 64), Decoder: "decoder", Preprocessing: "clap/v1", Sampling: "excerpt/v1", Pooling: "duration-weighted-mean-l2/v1", Scope: "sampled_excerpts", Missingness: "absent-row"}
	model := core.AudioModelIdentity{Model: space.Model, Revision: space.ModelRevision, Weights: space.GraphSHA256, Preprocessing: space.Preprocessing, Dimension: 2, Runtime: "runtime/cpu"}
	pack := librarypack.Pack{CorpusGeneration: "semantic-corpus", MetadataGeneration: "semantic-metadata", MERTGeneration: "semantic-mert", CLAPGeneration: "semantic-clap", MERT: testSpace(), CLAP: space,
		Tracks: []librarypack.Track{
			{ID: "rich", Artist: "Music Artist", Title: "Rich", CLAP: []float32{.6, .8}, MERT: []float32{1, 0, 0}, CLAPEvidence: &librarypack.CLAPEvidence{Model: &model, Sampling: space.Sampling, Scope: librarypack.CLAPEvidenceScope, CoveredSeconds: 7, Incomplete: true, PartialReason: "short_decode", Segments: []librarypack.CLAPSegment{
				{Index: 0, StartSeconds: 10, EndSeconds: 13, ObservedSeconds: 3, InputSeconds: 10, Padding: "repeat", Validity: "valid", Vector: []float32{1, 0}},
				{Index: 1, StartSeconds: 60, EndSeconds: 64, ObservedSeconds: 4, InputSeconds: 10, Padding: "repeat", Validity: "valid", Vector: []float32{0, 1}},
			}}},
			{ID: "pooled", Artist: "Music Artist", Title: "Pooled", CLAP: []float32{1, 0}, MERT: []float32{.8, .6, 0}},
			{ID: "other", Artist: "Other Artist", Title: "Other", CLAP: []float32{0, 1}, MERT: []float32{0, 1, 0}},
		}}
	if globalPairing {
		pack.CLAPModel = &model
	}
	for _, fn := range modify {
		fn(&pack)
	}
	path := filepath.Join(root, "semantic.paipack")
	if _, err := librarypack.Write(ctx, path, pack, librarypack.Limits{}); err != nil {
		t.Fatal(err)
	}
	manager, err := librarypack.OpenManager(ctx, filepath.Join(root, "managed"), librarypack.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	activatePack(t, manager, path)
	lease, err := manager.Pin()
	if err != nil {
		t.Fatal(err)
	}
	local, err := Open(lease, Options{SourceID: "semantic"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	return &CompositeCatalog{base: testBase{}, local: local, mode: ModeLibraryOnly}, model
}

func TestLibraryAssessmentRetainsCoverageAndStrongestNegativeExcerpt(t *testing.T) {
	catalog, model := openSemanticCatalog(t, true)
	queries := []core.AudioClauseVector{
		{Clause: core.AudioClause{Kind: "instrumentation", Text: "piano", Scope: "journey_start", Group: "instrument-choice"}, Values: []float32{1, 0}},
		{Clause: core.AudioClause{Kind: "vocal", Text: "vocals", Negative: true, Strict: true}, Values: []float32{1, 0}},
		{Clause: core.AudioClause{Kind: "mood", Text: "calm"}, Values: []float32{.5, 0}},
	}
	catalog.BindLibraryQueries(model, queries)
	queries[0].Values[0] = 0 // Binding must own request values.
	assessment, ok, err := catalog.LibraryAssessment(context.Background(), catalog.local.NamespacedID("rich"))
	if err != nil || !ok || len(assessment.Clauses) != 3 || assessment.Eligible {
		t.Fatalf("assessment=%+v available=%v err=%v", assessment, ok, err)
	}
	if math.Abs(assessment.Clauses[0].Score-3.0/7) > 1e-6 || assessment.Clauses[1].Score != 1 || math.Abs(assessment.Clauses[2].Score-1.5/7) > 1e-6 {
		t.Fatalf("segment aggregation diluted negative or renormalized captions: %+v", assessment.Clauses)
	}
	for _, clause := range assessment.Clauses {
		if !clause.ScoreAvailable || clause.State != core.EvidenceUnknown {
			t.Fatalf("uncalibrated score claimed fit: %+v", clause)
		}
	}
	if assessment.Clauses[0].Clause.Scope != "journey_start" || assessment.Clauses[0].Clause.Group != "instrument-choice" || !assessment.Clauses[1].Clause.Strict {
		t.Fatal("structured clauses lost")
	}
	want := &core.LibraryCLAPCoverage{CoveredSeconds: 7, Incomplete: true, PartialReason: "short_decode", Segments: []core.LibraryAudioInterval{{StartSeconds: 10, EndSeconds: 13}, {StartSeconds: 60, EndSeconds: 64}}}
	if !reflect.DeepEqual(assessment.LibraryCoverage, want) {
		t.Fatalf("coverage=%+v", assessment.LibraryCoverage)
	}
	pooled, ok, err := catalog.LibraryAssessment(context.Background(), catalog.local.NamespacedID("pooled"))
	if err != nil || !ok || pooled.LibraryCoverage != nil || pooled.Clauses[0].Score != 1 {
		t.Fatalf("pooled-only data acquired coverage: %+v %v %v", pooled, ok, err)
	}
}

func TestMixedPackAllowsOnlyExplicitlyPairedRecordAssessment(t *testing.T) {
	catalog, model := openSemanticCatalog(t, false)
	query := []core.AudioClauseVector{{Clause: core.AudioClause{Kind: "mood", Text: "calm"}, Values: []float32{1, 0}}}
	catalog.BindLibraryQueries(model, query)
	if len(catalog.semanticQueries) != 1 || len(catalog.libraryQueries(catalog.local.manifest.PackID)) != 0 {
		t.Fatal("mixed pairing was dropped or exposed for whole-pack text retrieval")
	}
	if _, ok, err := catalog.LibraryAssessment(context.Background(), catalog.local.NamespacedID("rich")); err != nil || !ok {
		t.Fatalf("paired rich record unavailable: %v %v", ok, err)
	}
	if _, ok, err := catalog.LibraryAssessment(context.Background(), catalog.local.NamespacedID("pooled")); err != nil || ok {
		t.Fatalf("unknown runtime promoted to paired evidence: %v %v", ok, err)
	}
	model.Runtime = "runtime/cuda"
	catalog.BindLibraryQueries(model, query)
	if _, ok, err := catalog.LibraryAssessment(context.Background(), catalog.local.NamespacedID("rich")); err != nil || ok {
		t.Fatalf("different per-record runtime accepted: %v %v", ok, err)
	}
}

func TestLibraryQueriesRejectInvalidVectorsAndModelChanges(t *testing.T) {
	catalog, model := openSemanticCatalog(t, true)
	for _, values := range [][]float32{{1}, {1, 0, 0}, {0, 0}, {float32(math.NaN()), 0}, {float32(math.Inf(1)), 0}, {2, 0}} {
		catalog.BindLibraryQueries(model, []core.AudioClauseVector{{Values: values}})
		if len(catalog.semanticQueries) != 0 {
			t.Fatalf("invalid query accepted: %v", values)
		}
	}
	query := []core.AudioClauseVector{{Values: []float32{1, 0}}}
	catalog.BindLibraryQueries(model, query)
	if len(catalog.libraryQueries(catalog.local.manifest.PackID)) != 1 {
		t.Fatal("compatible query unavailable")
	}
	model.Weights = strings.Repeat("c", 64)
	catalog.BindLibraryQueries(model, query)
	if len(catalog.semanticQueries) != 0 {
		t.Fatal("rebound incompatible model reused old queries")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := catalog.LibraryAssessment(canceled, catalog.local.NamespacedID("rich")); err != context.Canceled {
		t.Fatalf("canceled query=%v", err)
	}
}

func TestLegacyCLAPPairingKeepsCoverageUnknown(t *testing.T) {
	catalog, model := openSemanticCatalog(t, false)
	// Generation v5/v6 archive reads are tested in librarypack. This isolates
	// the legacy paired-fingerprint adapter while using a pooled-only track.
	catalog.local.manifest.Version = librarypack.PooledCLAPVersion
	catalog.BindLibraryQueries(model, []core.AudioClauseVector{{Values: []float32{1, 0}}})
	if len(catalog.libraryQueries(catalog.local.manifest.PackID)) != 1 {
		t.Fatal("legacy exact paired fingerprint unavailable")
	}
	assessment, ok, err := catalog.LibraryAssessment(context.Background(), catalog.local.NamespacedID("pooled"))
	if err != nil || !ok || assessment.LibraryCoverage != nil || assessment.Clauses[0].State != core.EvidenceUnknown {
		t.Fatalf("legacy assessment=%+v %v %v", assessment, ok, err)
	}
}

func TestCLAPRuntimeIdentitySeparatesSourcesAndExternalQueries(t *testing.T) {
	cpu, cpuModel := openSemanticCatalog(t, true)
	gpu, _ := openSemanticCatalog(t, true, func(pack *librarypack.Pack) { pack.CLAPModel.Runtime = "runtime/cuda" })
	if cpu.local.CLAPEvidenceSource().SpaceID == gpu.local.CLAPEvidenceSource().SpaceID {
		t.Fatal("different explicit runtime identities share an audio space")
	}
	query := NeighborQuery{Vector: []float32{1, 0}, Space: &gpu.local.manifest.CLAP, Limit: 2}
	if _, err := gpu.local.CLAPNeighbors(context.Background(), query); !errors.Is(err, ErrIncompatibleSpace) {
		t.Fatalf("unknown external runtime accepted: %v", err)
	}
	query.CLAPModel = &cpuModel
	if _, err := gpu.local.CLAPNeighbors(context.Background(), query); !errors.Is(err, ErrIncompatibleSpace) {
		t.Fatalf("incompatible external runtime accepted: %v", err)
	}
	query.CLAPModel = gpu.local.manifest.CLAPModel
	if hits, err := gpu.local.CLAPNeighbors(context.Background(), query); err != nil || len(hits) != 2 {
		t.Fatalf("explicit compatible query unavailable: hits=%d err=%v", len(hits), err)
	}
}

func TestMixedCLAPNeighborsPartitionExplicitAndUnknownRuntime(t *testing.T) {
	catalog, _ := openSemanticCatalog(t, false, func(pack *librarypack.Pack) {
		for _, name := range []string{"peer", "cuda"} {
			track := pack.Tracks[0]
			track.ID = name
			evidence := *track.CLAPEvidence
			model := *evidence.Model
			if name == "cuda" {
				model.Runtime = "runtime/cuda"
			}
			evidence.Model = &model
			track.CLAPEvidence = &evidence
			pack.Tracks = append(pack.Tracks, track)
		}
	})
	ctx := context.Background()
	var sourceIDs []string
	for _, id := range []string{"rich", "cuda", "pooled"} {
		vector, ok, err := catalog.local.LibraryCLAPVector(ctx, catalog.local.NamespacedID(id))
		if err != nil || !ok {
			t.Fatalf("source %s unavailable: %v", id, err)
		}
		sourceIDs = append(sourceIDs, vector.Source.SpaceID)
	}
	if sourceIDs[0] == sourceIDs[1] || sourceIDs[0] == sourceIDs[2] || sourceIDs[1] == sourceIDs[2] {
		t.Fatal("mixed per-record identities share a source")
	}
	for seed, want := range map[string]string{"rich": "peer", "pooled": "other"} {
		hits, err := catalog.local.CLAPNeighbors(ctx, NeighborQuery{SeedID: catalog.local.NamespacedID(seed), Limit: 10})
		if err != nil || len(hits) != 1 || hits[0].Track.LocalID != want {
			t.Fatalf("seed %s crossed runtime boundary: hits=%+v err=%v", seed, hits, err)
		}
	}
	catalog.local.manifest.Coverage.Tracks = 50_001
	if _, err := catalog.local.CLAPNeighbors(ctx, NeighborQuery{SeedID: catalog.local.NamespacedID("rich"), Limit: 10}); !errors.Is(err, ErrIncompatibleSpace) {
		t.Fatalf("mixed identity scan exceeded bound: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := catalog.local.CLAPNeighbors(canceled, NeighborQuery{SeedID: catalog.local.NamespacedID("rich"), Limit: 10}); !errors.Is(err, context.Canceled) {
		t.Fatalf("mixed scan ignored cancellation: %v", err)
	}
	before := catalog.local.CLAPEvidenceSource().SpaceID
	catalog.local.manifest.PackID += "-different-pack"
	if before == catalog.local.CLAPEvidenceSource().SpaceID {
		t.Fatal("unknown runtimes compared across packs")
	}
}
