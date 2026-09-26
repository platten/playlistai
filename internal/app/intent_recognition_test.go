package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/ports"
)

func TestIntentRecognitionPinsPackAndInvalidatesOnReplacement(t *testing.T) {
	c := &Container{cfg: testConfig(t)}
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	input := ports.IntentInput{Prompt: "like Local Artist"}
	before := c.PrepareIntentInput(ctx, input)
	pack := filepath.Join(t.TempDir(), "first.paipack")
	writeAppLibraryPack(t, pack, "first", "one", "")
	if _, err := c.ImportLocalLibrary(ctx, pack); err != nil {
		t.Fatal(err)
	}
	first := c.PrepareIntentInput(ctx, input)
	if first.RecognitionIdentity == before.RecognitionIdentity || !strings.Contains(first.RecognitionIdentity, "paipack-recognition") {
		t.Fatal("pack missing from parse identity")
	}
	found := false
	for _, atom := range first.SourceFacts.Atoms {
		found = found || atom.Grounding != nil && atom.Grounding.Provider == "paipack" && atom.Value == "Local Artist"
	}
	if !found {
		t.Fatalf("pack artist unrecognized: %+v", first.SourceFacts)
	}
	secondPack := filepath.Join(t.TempDir(), "second.paipack")
	writeAppLibraryPack(t, secondPack, "second", "two", "")
	if _, err := c.ImportLocalLibrary(ctx, secondPack); err != nil {
		t.Fatal(err)
	}
	second := c.PrepareIntentInput(ctx, input)
	if second.RecognitionIdentity == first.RecognitionIdentity {
		t.Fatal("reimport reused stale parse cache")
	}
	if again := c.PrepareIntentInput(ctx, first); again.RecognitionIdentity != first.RecognitionIdentity {
		t.Fatal("prepared immutable source changed")
	}
}

func TestIntentRecognitionSharedPacksRespectLibraryOnly(t *testing.T) {
	c := &Container{cfg: testConfig(t)}
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	pack := filepath.Join(t.TempDir(), "shared.paipack")
	writeAppLibraryPack(t, pack, "shared", "one", "")
	if _, err := c.ImportDiscoveryAsset(ctx, pack, nil); err != nil {
		t.Fatal(err)
	}
	input := ports.IntentInput{Prompt: "like Local Artist"}
	shared := c.PrepareIntentInput(ctx, input)
	if !strings.Contains(shared.RecognitionIdentity, "shared-discovery") {
		t.Fatal("shared pack absent from recognition identity")
	}
	if _, err := c.ImportLocalLibrary(ctx, pack); err != nil {
		t.Fatal(err)
	}
	combined := c.PrepareIntentInput(ctx, input)
	for _, atom := range combined.SourceFacts.Atoms {
		if atom.Grounding != nil && len(atom.Grounding.Candidates) != 1 {
			t.Fatalf("duplicate sources created ambiguity: %+v", atom)
		}
	}
	if _, err := c.SetLocalLibraryMode(LocalLibraryOnly); err != nil {
		t.Fatal(err)
	}
	local := c.PrepareIntentInput(ctx, input)
	if strings.Contains(local.RecognitionIdentity, "shared-discovery") || local.RecognitionIdentity == combined.RecognitionIdentity {
		t.Fatal("library-only retained shared recognition")
	}
}

func TestParsingContextPreparedBeforeCacheIdentity(t *testing.T) {
	c := &Container{cfg: testConfig(t)}
	t.Cleanup(func() { _ = c.Close() })
	input := ports.IntentInput{Prompt: "warm timbre"}
	baseline := c.PrepareIntentInput(context.Background(), input)
	input.EnrichParsingContext = true
	enriched := c.PrepareIntentInput(context.Background(), input)
	if baseline.SourceFacts.ParsingContext != nil || enriched.SourceFacts.ParsingContext == nil || baseline.RecognitionIdentity == enriched.RecognitionIdentity {
		t.Fatal("policy did not separate parse caches")
	}
	if again := c.PrepareIntentInput(context.Background(), enriched); again.SourceFacts != enriched.SourceFacts || again.RecognitionIdentity != enriched.RecognitionIdentity {
		t.Fatal("context not prepared exactly once")
	}
	changed := enriched.SourceFacts.Clone()
	changed.ParsingContext.PolicyVersion = "future-policy"
	enriched.SourceFacts = &changed
	if future := c.PrepareIntentInput(context.Background(), enriched); future.RecognitionIdentity == enriched.RecognitionIdentity {
		t.Fatal("policy version did not invalidate cache")
	}
	enriched.EnrichParsingContext = false
	if restored := c.PrepareIntentInput(context.Background(), enriched); restored.RecognitionIdentity != baseline.RecognitionIdentity || restored.SourceFacts.ParsingContext != nil {
		t.Fatal("switching policy retained enriched context")
	}
}
