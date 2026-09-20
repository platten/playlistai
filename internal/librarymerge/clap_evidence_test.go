package librarymerge

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
)

func TestCombinePreservesCLAPSegmentsAndPairing(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	space := librarypack.VectorSpace{Name: "library_clap", Dimension: 2, DType: "float32", ByteOrder: "little", Normalized: true, Model: "clap", ModelRevision: "paired", GraphSHA256: strings.Repeat("b", 64), Decoder: "decoder", Preprocessing: "clap/v1", Sampling: "excerpt/v1", Pooling: "duration-weighted-mean-l2/v1", Scope: "sampled_excerpts", Missingness: "absent-row"}
	model := core.AudioModelIdentity{Model: space.Model, Revision: space.ModelRevision, Weights: space.GraphSHA256, Preprocessing: space.Preprocessing, Dimension: space.Dimension, Runtime: "runtime/cpu"}
	evidence := &librarypack.CLAPEvidence{Model: &model, Sampling: space.Sampling, Scope: librarypack.CLAPEvidenceScope, CoveredSeconds: 5, Incomplete: true, PartialReason: "short_decode", Segments: []librarypack.CLAPSegment{{Index: 0, StartSeconds: 10, EndSeconds: 15, ObservedSeconds: 5, InputSeconds: 10, Padding: "repeat", Validity: "valid", Vector: []float32{1, 0}}}}
	var inputs []string
	for i, name := range []string{"pooled", "rich"} {
		track := librarypack.Track{ID: name, Artist: "Artist", Title: "Song", ISRC: "USAAA2600001", CLAP: []float32{1, 0}}
		if i == 1 {
			track.CLAPEvidence = evidence
		}
		path := filepath.Join(dir, name+".paipack")
		_, err := librarypack.Write(ctx, path, librarypack.Pack{CorpusGeneration: "corpus-" + name, MetadataGeneration: "metadata-" + name, CLAPGeneration: "clap-" + name, CLAP: space, CLAPModel: &model, Tracks: []librarypack.Track{track}}, librarypack.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, path)
	}
	output := filepath.Join(dir, "combined.paipack")
	report, err := Combine(ctx, inputs, output, Options{MaxRAM: 64 << 20, Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if report.Manifest.Version != librarypack.FormatVersion || report.OutputTracks != 1 || report.Manifest.CLAPModel == nil || *report.Manifest.CLAPModel != model {
		t.Fatalf("merged pairing=%+v", report)
	}
	manager, err := librarypack.OpenManager(ctx, t.TempDir(), librarypack.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	staged, err := manager.Stage(ctx, output)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Discard(staged) }()
	source, err := staged.Generation().OpenTrackSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	track, ok, err := source.Next(ctx)
	if err != nil || !ok || !reflect.DeepEqual(track.CLAPEvidence, evidence) {
		t.Fatalf("merged segments=%+v ok=%v error=%v", track.CLAPEvidence, ok, err)
	}
}

func TestMergeDoesNotAttachSegmentsFromConflictingPool(t *testing.T) {
	first := librarypack.Track{CLAP: []float32{1, 0}}
	second := librarypack.Track{CLAP: []float32{0, 1}, CLAPEvidence: &librarypack.CLAPEvidence{CoveredSeconds: 10}}
	var vector []float32
	conflicts := map[string]int{}
	mergeTrackEvidence(&first, &vector, second, nil, conflicts)
	if first.CLAPEvidence != nil || conflicts["clap"] != 1 {
		t.Fatalf("conflicting segments attached to wrong pooled vector: %+v %v", first, conflicts)
	}
}

func TestPairedCLAPMergeDoesNotInventLegacyRuntime(t *testing.T) {
	model := core.AudioModelIdentity{Model: "clap", Runtime: "cpu"}
	inputs := []*stagedInput{{manifest: librarypack.Manifest{Coverage: librarypack.Coverage{CLAP: 1}, CLAPModel: &model}}, {manifest: librarypack.Manifest{Coverage: librarypack.Coverage{CLAP: 1}}}}
	if got, err := compatibleCLAPModel(inputs); err != nil || got != nil {
		t.Fatalf("legacy runtime invented: %+v %v", got, err)
	}
	other := model
	other.Runtime = "cuda"
	inputs[1].manifest.CLAPModel = &other
	if _, err := compatibleCLAPModel(inputs); err == nil {
		t.Fatal("conflicting runtime identities accepted")
	}
}
