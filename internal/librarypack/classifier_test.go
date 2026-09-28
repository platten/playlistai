package librarypack

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func classifierFixture() core.MusicClassifierEvidence {
	identity := core.MusicClassifierIdentity{Model: "discogs-effnet", Revision: "1", WeightsSHA256: strings.Repeat("a", 64), MetadataSHA256: strings.Repeat("b", 64)}
	return core.MusicClassifierEvidence{Version: 1, Encoder: identity, Preprocessing: "sample16k/v1", Runtime: "fixture", AudioSHA256: strings.Repeat("c", 64), Source: "licensed-fixture", SourceID: "recording-1", License: "CC-BY-4.0", Coverage: core.LibraryCLAPCoverage{CoveredSeconds: 10, Incomplete: true, PartialReason: "sample", Segments: []core.LibraryAudioInterval{{EndSeconds: 10}}}, Heads: []core.MusicClassifierHead{{Kind: "instrumentation", Model: identity, Classes: []string{"piano"}, Scores: []float32{.8}}}}
}

func TestClassifierRoundTripOwnedEvidenceAndStreaming(t *testing.T) {
	ctx := context.Background()
	pack := fixturePack("classifier")
	want := []core.MusicClassifierEvidence{classifierFixture()}
	pack.Tracks[1].ClassifierEvidence = want
	path, _ := writePackAt(t, "classifier.paipack", pack)
	manager, err := OpenManager(ctx, t.TempDir(), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	staged, err := manager.Stage(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Discard(staged) }()
	g := staged.Generation()
	if g.Manifest().Coverage.Classifier != 1 {
		t.Fatal("classifier manifest coverage not preserved")
	}
	g.manifest.Coverage.Classifier = 0
	if err := g.validateClassifierEvidenceRows(ctx, DefaultLimits()); err == nil {
		t.Fatal("forged zero classifier coverage accepted")
	}
	g.manifest.Coverage.Classifier = 1
	got, err := g.ClassifierEvidence(ctx, "local:main:a")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("lost evidence %+v, %v", got, err)
	}
	got[0].Heads[0].Scores[0] = 0
	again, err := g.ClassifierEvidence(ctx, "local:main:a")
	if err != nil || again[0].Heads[0].Scores[0] != .8 {
		t.Fatal("mutable classifier escaped", err)
	}
	source, err := g.OpenTrackSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	track, ok, err := source.Next(ctx)
	if err != nil || !ok || !reflect.DeepEqual(track.ClassifierEvidence, want) {
		t.Fatal("stream lost evidence", err)
	}
	if missing, err := g.ClassifierEvidence(ctx, "absent"); err != nil || len(missing) != 0 {
		t.Fatal(missing, err)
	}
}

func TestClassifierPackRejectsDurationDuplicatesAndAllocation(t *testing.T) {
	for name, mutate := range map[string]func(*Track){
		"duration":  func(t *Track) { t.DurationMilliseconds = 5000 },
		"duplicate": func(t *Track) { t.ClassifierEvidence = append(t.ClassifierEvidence, t.ClassifierEvidence[0]) },
		"score":     func(t *Track) { t.ClassifierEvidence[0].Heads[0].Scores[0] = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			p := fixturePack(name)
			p.Tracks[1].ClassifierEvidence = []core.MusicClassifierEvidence{classifierFixture()}
			mutate(&p.Tracks[1])
			if _, err := Write(context.Background(), filepath.Join(t.TempDir(), "bad.paipack"), p, Limits{}); err == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
	p := fixturePack("size")
	p.Tracks[1].ClassifierEvidence = []core.MusicClassifierEvidence{classifierFixture()}
	if _, err := Write(context.Background(), filepath.Join(t.TempDir(), "big.paipack"), p, Limits{MaxJSONBytes: 256}); err == nil {
		t.Fatal("oversized classifier evidence accepted")
	}
}

func TestOptionalClassifierTableAbsentIsUnknown(t *testing.T) {
	// Older generations have no table. The optional read is a deliberate no-op.
	ctx := context.Background()
	path, _ := writeFixture(t, "legacy.paipack", "legacy")
	path = rewriteCLAPFixture(t, path, FormatVersion, "DROP TABLE classifier_evidence")
	m, err := OpenManager(ctx, t.TempDir(), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s, err := m.Stage(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Discard(s) }()
	g := s.Generation()
	if g.hasClassifierEvidence {
		t.Fatal("absent classifier table declared present")
	}
	if got, err := g.ClassifierEvidence(ctx, "local:main:a"); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
}
