package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/core"
)

func TestEnhancedEvidenceCLIReplayIsFrozenAndDeterministic(t *testing.T) {
	dir := t.TempDir()
	cat, err := catalog.Open("../../internal/catalog/testdata")
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	input := core.EnhancedAudioInput{CatalogVersion: cat.CatalogVersion(), Model: core.AudioRepresentationIdentity{Model: "fixture", Revision: "v1", Preprocessing: "fixture", Runtime: "fixture", Pooling: "mean", Dimension: 2, WeightsSHA256: "fixture"}, Representations: map[string]core.AudioRepresentation{}}
	for row := 0; row < cat.Len(); row++ {
		id := cat.ID(row)
		meta, _ := cat.Meta(id)
		input.Representations[id] = core.AudioRepresentation{TrackID: id, TrackKey: core.ProvisionalRecordingKey(meta.Ref), CatalogVersion: input.CatalogVersion, Model: input.Model, Pooled: []float32{1, float32(row % 3)}}
	}
	write := func(name string, v any) string {
		t.Helper()
		p := filepath.Join(dir, name)
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
		return p
	}
	evidence := write("evidence.json", input)
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, OriginalDescription: "Justice", References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Justice", Influence: core.InfluencePositive}}, Controls: core.IntentControls{TotalTrackCount: 3}}.Normalized()
	prompts := write("prompts.json", []promptCase{{Prompt: "Justice", Artist: "Justice", Count: 3}})
	replay := write("replay.json", []result{{Prompt: "Justice", Intent: intent, ParsedIntent: intent}})
	var first []result
	for i := range 2 {
		output := filepath.Join(dir, "out.json")
		commandArgs(t, "-catalog", "../../internal/catalog/testdata", "-prompts", prompts, "-replay", replay, "-mode", "enhanced_hybrid", "-enhanced-evidence", evidence, "-output", output)
		if err := run(); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		var got []result
		if err = json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Playlist.EnhancedAudio == nil || len(got[0].EnhancedEvidenceSHA256) != 64 || len(got[0].EnhancedSnapshotFingerprint) != 64 {
			t.Fatalf("snapshot not consumed: %+v", got)
		}
		available := false
		for _, reason := range got[0].Playlist.Rationale {
			for _, e := range reason.Evidence {
				available = available || e.Component == "mert_audio_affinity" && e.Available
			}
		}
		if !available {
			t.Fatal("CLI merely accepts mode; MERT did not reach ranking")
		}
		got[0].Milliseconds = 0
		if i == 0 {
			first = got
		} else if !reflect.DeepEqual(first, got) {
			t.Fatal("frozen replay changed")
		}
	}
}

func TestEnhancedEvidenceRejectsInvalidInputAndOnlineFlags(t *testing.T) {
	dir := t.TempDir()
	for _, raw := range []string{"null", "{} {}", `{"catalogVersion":"other"}`, `{"policyVersion":"unknown"}`, `{"pcm":[]}`} {
		path := filepath.Join(dir, "bad.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readEnhancedEvidence(path, "current"); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, args := range [][]string{{"-enhanced-evidence", "x"}, {"-mode", "enhanced_hybrid", "-enhanced-evidence", "x", "-online"}, {"-mode", "enhanced_hybrid", "-enhanced-evidence", "x", "-bundle", "x"}} {
		commandArgs(t, args...)
		if run() == nil {
			t.Fatal("unsafe flag combination accepted")
		}
	}
}

func TestExplicitRulesParserDoesNotLoadLanguageModel(t *testing.T) {
	dir := t.TempDir()
	prompts := filepath.Join(dir, "prompts.json")
	if err := os.WriteFile(prompts, []byte(`[{"prompt":"Justice","artist":"Justice"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "parsed.json")
	commandArgs(t, "-rules-parser", "-parse-only", "-catalog", "../../internal/catalog/testdata", "-prompts", prompts, "-output", output)
	if err := run(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var reports []result
	if err = json.Unmarshal(b, &reports); err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || reports[0].Parser.Backend != "rules" || reports[0].Replayed || !reports[0].ParseOnly {
		t.Fatal("rules parser provenance lost")
	}
	commandArgs(t, "-rules-parser", "-replay", "prior.json")
	if run() == nil {
		t.Fatal("ambiguous parser options accepted")
	}
}
