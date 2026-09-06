package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type recordingIntentParser struct {
	inputs []ports.IntentInput
}

func (p *recordingIntentParser) Parse(_ context.Context, input ports.IntentInput) (core.MusicIntent, error) {
	p.inputs = append(p.inputs, input)
	if input.Prompt == "fail" {
		return core.MusicIntent{}, errors.New("parse failed")
	}
	return core.MusicIntent{
		Version: core.CurrentIntentVersion,
		Mode:    core.ModeSimilar,
		Count:   7,
		Controls: core.IntentControls{
			TotalTrackCount: 7,
		},
		References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Björk", Influence: core.InfluencePositive}},
	}, nil
}

func (*recordingIntentParser) Info() ports.ParserInfo {
	return ports.ParserInfo{Name: "test", Backend: "test", Version: "test/v1", Ready: true, ContractVersion: core.CurrentIntentVersion}
}

func (*recordingIntentParser) RuntimeMemoryBytes() int64 { return 1234 }

func TestEvaluateIntentModelCountsFailuresAndCarriesContext(t *testing.T) {
	t.Parallel()
	count := 7
	now := &core.TrackRef{Artist: "Kavinsky", Title: "Nightcall"}
	dataset := Dataset{Name: "intent-test", IntentCases: []IntentCase{
		{
			ID: "ok", Prompt: "continue", NowPlaying: now,
			RecentTracks: []core.TrackRef{{Artist: "Air", Title: "La femme d'argent"}}, Locale: "fr-FR",
			Expected: IntentLabels{Mode: core.ModeSimilar, TotalTrackCount: &count, TypedReferences: []string{"artist:positive:Björk"}},
		},
		{
			ID: "failed", Prompt: "fail",
			Expected: IntentLabels{Mode: core.ModeJourney, NegativeReferences: []string{"Coldplay"}},
		},
	}}
	parser := &recordingIntentParser{}
	report, err := EvaluateIntentModel(context.Background(), parser, dataset, IntentModelIdentity{ID: "test-model"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(parser.inputs) != 2 {
		t.Fatalf("parser inputs = %d, want 2", len(parser.inputs))
	}
	got := parser.inputs[0]
	if got.NowPlaying != now || got.Locale != "fr-FR" || len(got.RecentTracks) != 1 {
		t.Fatalf("context not preserved: %+v", got)
	}
	a := report.Aggregate
	if a.Runs != 2 || a.SchemaValidRuns != 1 || a.ExactRuns != 1 || a.LabeledFields != 5 || a.CorrectFields != 3 {
		t.Fatalf("aggregate = %+v", a)
	}
	if a.SchemaValidity != .5 || a.ExactCaseAccuracy != .5 || a.FieldAccuracy != .6 || a.PeakResidentBytes != 1234 {
		t.Fatalf("aggregate rates = %+v", a)
	}
	if report.Cases[1].Error == "" || report.Cases[1].SchemaValid {
		t.Fatalf("failure run = %+v", report.Cases[1])
	}
}

func TestPercentileMillisUsesNearestRank(t *testing.T) {
	t.Parallel()
	values := []int64{10, 20, 30, 40, 50}
	if got := percentileMillis(values, .5); got != 30 {
		t.Fatalf("median = %d, want 30", got)
	}
	if got := percentileMillis(values, .95); got != 50 {
		t.Fatalf("p95 = %d, want 50", got)
	}
}

func TestIntentModelReportPreservesRTX5090Environment(t *testing.T) {
	t.Parallel()
	report := IntentModelReport{
		Version: IntentModelReportVersion, DatasetName: "hardware-test",
		Model: IntentModelIdentity{
			ID: "test-model",
			Environment: IntentBenchmarkEnvironment{
				GOOS: "linux", GOARCH: "amd64", LogicalCPUs: 32,
				ExecutionMode: "gpu", SelectedDevice: "CUDA0",
				ContextSize: 4096, Threads: 16, GPULayers: 0,
				Devices: []IntentBenchmarkDevice{{
					ID: "CUDA0", Name: "NVIDIA GeForce RTX 5090",
					TotalBytes: 32768 << 20, FreeBytes: 31900 << 20,
				}},
			},
		},
	}
	path := filepath.Join(t.TempDir(), "report.md")
	if err := WriteIntentModelReportMarkdown(path, report); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"NVIDIA GeForce RTX 5090", "34359738368 bytes total", "device `CUDA0`", "context 4096"} {
		if !strings.Contains(text, want) {
			t.Fatalf("report missing %q:\n%s", want, text)
		}
	}
	jsonPath := filepath.Join(t.TempDir(), "report.json")
	if err := WriteIntentModelReportJSON(jsonPath, report); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	var decoded IntentModelReport
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	got := decoded.Model.Environment.Devices[0]
	if got.TotalBytes != 32768<<20 || got.FreeBytes != 31900<<20 {
		t.Fatalf("RTX 5090 memory did not round-trip: %+v", got)
	}
}

func TestIntentModelDatasetCoversContractCases(t *testing.T) {
	t.Parallel()
	dataset, err := LoadDataset(filepath.Join("testdata", "intent-model-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if dataset.Evidence != EvidenceJudged || len(dataset.IntentCases) != 12 {
		t.Fatalf("dataset evidence/cases = %q/%d", dataset.Evidence, len(dataset.IntentCases))
	}
	byID := map[string]IntentCase{}
	for _, item := range dataset.IntentCases {
		byID[item.ID] = item
		if intentLabelCount(item.Expected) == 0 || len(item.Tags) == 0 {
			t.Fatalf("case %q has no labels or tags", item.ID)
		}
	}
	if got := byID["semantic-nuance"].Prompt; got != "ambient electronic with microdetail, a deep groove, occasional sparkle, relaxing but not sleepy, no abstract drone" {
		t.Fatalf("semantic prompt = %q", got)
	}
	contextCase := byID["context-negative"]
	if contextCase.NowPlaying == nil || len(contextCase.RecentTracks) != 1 {
		t.Fatalf("context case is incomplete: %+v", contextCase)
	}
	for _, required := range []string{"multi-reference-negation", "required-versus-reference", "strict-unsupported-vocals", "non-latin-reference", "artist-title-collision", "evidence-spans"} {
		if _, ok := byID[required]; !ok {
			t.Fatalf("required case %q missing", required)
		}
	}
}

var _ ports.IntentParser = (*recordingIntentParser)(nil)
