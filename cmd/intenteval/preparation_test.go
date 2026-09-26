package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/evaluation"
	"github.com/platten/playlistai/internal/ports"
)

func TestIsolatedPreparationUsesDesktopRecognition(t *testing.T) {
	root := t.TempDir()
	container, err := prepareApplicationData(context.Background(), root, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = container.Close() })
	if container.IntentParser().Info().Backend != "rules" {
		t.Fatal("preparation started another LLM")
	}
	for _, tc := range []struct {
		prompt, name string
		candidates   int
		truncated    bool
	}{
		{"like Bjork", "Björk", 1, false},
		{"like Phoenix", "Phoenix", 2, false},
		{"like John Williams", "John Williams", 8, true},
		{"like 坂本龍一", "坂本龍一", 1, false},
	} {
		input := container.PrepareIntentInput(context.Background(), ports.IntentInput{Prompt: tc.prompt, EnrichParsingContext: true})
		if input.SourceFacts == nil || input.SourceFacts.ParsingContext == nil || input.RecognitionIdentity == "" || input.SourceFacts.Recognition.ReferenceLookup != "available" {
			t.Fatalf("source preparation unavailable: %+v", input)
		}
		found := false
		for _, atom := range input.SourceFacts.Atoms {
			if atom.Value == tc.name && atom.Grounding != nil && len(atom.Grounding.Candidates) == tc.candidates && atom.Grounding.Truncated == tc.truncated {
				found = true
			}
		}
		if !found {
			t.Fatalf("fixture %q not grounded: %+v", tc.prompt, input.SourceFacts.Atoms)
		}
	}
	// The fixture is reusable without changing its snapshot.
	if err := installRecognitionFixture(context.Background(), root); err != nil {
		t.Fatal(err)
	}
}

func TestPreparationUnavailableSourcesAndIsolation(t *testing.T) {
	t.Run("unavailable", func(t *testing.T) {
		container, err := prepareApplicationData(context.Background(), t.TempDir(), false)
		if err != nil {
			t.Fatal(err)
		}
		defer container.Close()
		input := container.PrepareIntentInput(context.Background(), ports.IntentInput{Prompt: "like Missing Artist", EnrichParsingContext: true})
		if input.SourceFacts.Recognition.ReferenceLookup != "unavailable" || input.SourceFacts.ParsingContext == nil {
			t.Fatal("missing sources concealed")
		}
	})
	t.Run("existing user data", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "prefs.json")
		raw := []byte(`{"modelPath":"private-model.gguf"}`)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := prepareApplicationData(context.Background(), root, false); err == nil {
			t.Fatal("accepted existing unmarked data")
		}
		after, err := os.ReadFile(path)
		if err != nil || string(after) != string(raw) {
			t.Fatal("user preferences changed")
		}
		entries, _ := os.ReadDir(root)
		if len(entries) != 1 {
			t.Fatal("wrote into existing user data")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Symlink(t.TempDir(), filepath.Join(root, "linked")); err != nil {
			t.Skip(err)
		}
		if _, err := isolatedIntentDataDirectory(root); err == nil {
			t.Fatal("accepted linked store")
		}
	})
	t.Run("configured parser", func(t *testing.T) {
		root, err := isolatedIntentDataDirectory(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := (config.Prefs{ModelPath: "do-not-start.gguf"}).Save(root); err != nil {
			t.Fatal(err)
		}
		if _, err := prepareApplicationData(context.Background(), root, false); err == nil {
			t.Fatal("accepted second parser")
		}
	})
}

func TestEnrichedEvaluationCLISelectsFrozenSplit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "report.json")
	args(t, "-dataset", "../../internal/evaluation/testdata/parsing-context-v1.json", "-backend", "rules", "-app-data", root, "-recognition-fixture", "-context-policy", "enriched", "-split", "heldout", "-repeat", "2", "-output", path, "-markdown", "")
	if err := run(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report evaluation.IntentModelReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if report.ContextPolicy != "enriched" || len(report.Cases) != 32 || report.FirstPass.Runs != 16 || report.WarmRepeat.Runs != 16 || report.PreparationStartupMicros == nil {
		t.Fatalf("wrong report: %+v", report)
	}
	for _, row := range report.Cases {
		if row.PreparedSourceFacts == nil || row.PreparedSourceFacts.ParsingContext == nil || !strings.Contains(strings.Join(row.Tags, ","), "split:heldout") {
			t.Fatalf("missing preparation/split: %+v", row)
		}
		if row.Retries != nil || row.ParseAttempts != nil {
			t.Fatal("invented model measurements for the rules backend")
		}
	}
}
