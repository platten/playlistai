package evaluation

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/ports"
)

type recordingIntentParser struct {
	inputs []ports.IntentInput
	parse  func(ports.IntentInput) (core.MusicIntent, error)
}

func (p *recordingIntentParser) Parse(_ context.Context, input ports.IntentInput) (core.MusicIntent, error) {
	p.inputs = append(p.inputs, input)
	if p.parse != nil {
		return p.parse(input)
	}
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
	if dataset.Evidence != EvidenceJudged || len(dataset.IntentCases) != 16 {
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
	for _, required := range []string{"multi-reference-negation", "required-versus-reference", "strict-unsupported-vocals", "non-latin-reference", "artist-title-collision", "evidence-spans", "essential-electronic", "essential-electronic-no-rock", "electronic-rock-influence", "electronic-rock-journey"} {
		if _, ok := byID[required]; !ok {
			t.Fatalf("required case %q missing", required)
		}
	}
}

var _ ports.IntentParser = (*recordingIntentParser)(nil)

func TestIntentEvaluationPreparedSnapshotAndAttemptObservations(t *testing.T) {
	t.Parallel()
	prepared := 0
	parser := &recordingIntentParser{parse: func(input ports.IntentInput) (core.MusicIntent, error) {
		if !input.EnrichParsingContext || input.SourceFacts == nil || input.SourceFacts.ParsingContext == nil {
			t.Fatal("parser did not receive prepared enriched input")
		}
		tokens, indexes := 100, []int{0}
		input.ObserveParseAttempt(ports.ParseAttemptObservation{Attempt: 1, MandatoryTokens: &tokens, OutputAllowance: 800, OptionalByteBound: 120, UsedHintIndexes: indexes, Truncated: true})
		indexes[0], tokens = 7, 200
		input.ObserveParseAttempt(ports.ParseAttemptObservation{Attempt: 2, OutputAllowance: 800, OmittedHints: 1})
		input.SourceFacts.ParsingContext.Hints[0].Text = "parser mutation"
		return core.MusicIntent{Version: core.CurrentIntentVersion, OriginalDescription: input.Prompt, Translation: input.SourceFacts}, nil
	}}
	startup := int64(234)
	report, err := EvaluateIntentModelWithOptions(context.Background(), parser, Dataset{Name: "snapshot", IntentCases: []IntentCase{{ID: "warm", Prompt: "warm timbre"}}}, IntentModelIdentity{}, 2, IntentModelOptions{
		EnrichParsingContext: true, StartupMicros: &startup,
		PrepareInput: func(_ context.Context, input ports.IntentInput) ports.IntentInput {
			prepared++
			source := lexicon.Extract(input.Prompt)
			input.SourceFacts = &source
			input.RecognitionIdentity = "fixture/v1"
			return lexicon.PrepareParsingContext(input)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared != 2 || len(parser.inputs) != 2 || report.ContextPolicy != "enriched" || report.StartupMicros == nil || *report.StartupMicros != startup {
		t.Fatalf("preparation metadata lost: %+v", report)
	}
	if report.FirstPass == nil || report.FirstPass.Runs != 1 || report.WarmRepeat == nil || report.WarmRepeat.Runs != 1 {
		t.Fatalf("timing phases not separated: %+v", report)
	}
	for _, run := range report.Cases {
		if run.PreparationMicros == nil || run.ParsingMicros == nil || run.Retries == nil || *run.Retries != 1 || len(run.ParseAttempts) != 2 {
			t.Fatalf("missing diagnostics: %+v", run)
		}
		if run.PreparedSourceFacts.ParsingContext.Hints[0].Text == "parser mutation" || *run.ParseAttempts[0].MandatoryTokens != 100 || run.ParseAttempts[0].UsedHintIndexes[0] != 0 {
			t.Fatal("saved diagnostics changed after observation")
		}
		if run.ParseAttempts[1].MandatoryTokens != nil {
			t.Fatal("missing tokenizer measurement invented")
		}
	}
}

func TestParsingContextLabelsDetectScopeSpanAndAmbiguityLoss(t *testing.T) {
	t.Parallel()
	grounding := &core.IdentityGrounding{Candidates: []core.IdentityCandidate{{ID: "one"}, {ID: "two"}}}
	source := core.IntentTranslation{Atoms: []core.IntentAtom{{ID: "a", Value: "Muse", Scope: "journey_start", Group: "or-1", Grounding: grounding, Evidence: []core.SourceEvidence{{Text: "Muse", Start: 0, End: 4}}}, {ID: "b", Value: "Moby", Group: "or-1", Evidence: []core.SourceEvidence{{Text: "Moby", Start: 8, End: 12}}}}}
	for i := range source.Atoms {
		source.Atoms[i].Kind, source.Atoms[i].Polarity = "artist", "positive"
	}
	labels := &ParsingContextLabels{Atoms: []ParsingAtomLabel{{Text: "Muse", Scope: "journey_start"}}, ValidSourceSpans: true, PreserveAmbiguity: true, AlternativeValues: []string{"Muse", "Moby"}}
	intent := core.MusicIntent{OriginalDescription: "Muse or Moby", Translation: &source}
	for _, atom := range source.Atoms {
		intent.References = append(intent.References, core.IntentReference{Kind: core.ReferenceArtist, Query: atom.Value, Influence: core.InfluencePositive, Evidence: atom.Evidence, Grounding: atom.Grounding})
	}
	intent.Start = &intent.References[0]
	for _, check := range parsingContextChecks(intent, &source, labels) {
		if !check {
			t.Fatal("correct labels rejected")
		}
	}
	changed := source.Clone()
	changed.Atoms[0].Scope = "playlist"
	changed.Atoms[0].Evidence[0].Start = 1
	changed.Atoms[0].Grounding.Candidates = changed.Atoms[0].Grounding.Candidates[:1]
	changed.Atoms[1].Group = ""
	intent.Translation = &changed
	for _, check := range parsingContextChecks(intent, &source, labels) {
		if check {
			t.Fatal("protected-field regression concealed")
		}
	}
	empty := core.IntentTranslation{}
	intent.Translation = &empty
	if parsingContextChecks(intent, &empty, &ParsingContextLabels{PreserveAmbiguity: true})[0] {
		t.Fatal("ambiguity check passed without an ambiguous source")
	}
}

func TestParsingContextLabelsRequireOperationalFields(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, prompt string
		label        ParsingAtomLabel
		mutate       func(*core.MusicIntent)
	}{
		{"missing timbre", "warm timbre", ParsingAtomLabel{Text: "warm timbre", Kind: "texture"}, func(m *core.MusicIntent) { m.Preferences.TextureDescriptions = nil }},
		{"wrong facet", "warm timbre", ParsingAtomLabel{Text: "warm timbre", Kind: "texture"}, func(m *core.MusicIntent) {
			m.Preferences.Moods, m.Preferences.TextureDescriptions = m.Preferences.TextureDescriptions, nil
		}},
		{"wrong strength", "mostly instrumental", ParsingAtomLabel{Text: "instrumental", Strength: "preferred"}, func(m *core.MusicIntent) { m.Preferences.VocalPreferences[0].Strength = "required" }},
		{"missing essential criterion", "romantic classical", ParsingAtomLabel{Text: "romantic classical", ConceptID: "genre.romantic-classical"}, func(m *core.MusicIntent) { m.EssentialCriteria = nil }},
		{"missing start", "from Muse to Moby", ParsingAtomLabel{Text: "Muse", Scope: "journey_start"}, func(m *core.MusicIntent) { m.Start = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := lexicon.Extract(tc.prompt)
			intent := lexicon.Reconcile(core.MusicIntent{OriginalDescription: tc.prompt}, source)
			labels := &ParsingContextLabels{Atoms: []ParsingAtomLabel{tc.label}}
			if !parsingContextChecks(intent, &source, labels)[0] {
				t.Fatalf("valid operational mapping rejected: %+v", intent)
			}
			tc.mutate(&intent)
			if parsingContextChecks(intent, &source, labels)[0] {
				t.Fatal("retained source atom concealed broken operational field")
			}
		})
	}
	const prompt = "warm timbre"
	source := lexicon.Extract(prompt)
	intent := lexicon.Reconcile(core.MusicIntent{OriginalDescription: prompt}, source).Normalized()
	intent.Preferences.TextureDescriptions[0].Evidence[0].Start++
	if parsingContextChecks(intent, &source, &ParsingContextLabels{ValidSourceSpans: true})[0] {
		t.Fatal("invalid operational evidence span accepted")
	}
	intent = lexicon.Reconcile(core.MusicIntent{OriginalDescription: prompt}, source).Normalized()
	intent.Preferences.TextureDescriptions[0].Evidence = nil
	if parsingContextChecks(intent, &source, &ParsingContextLabels{ValidSourceSpans: true})[0] {
		t.Fatal("missing explicit preference evidence accepted")
	}
	artistSource := lexicon.Extract("like Air")
	artistIntent := lexicon.Reconcile(core.MusicIntent{OriginalDescription: "like Air"}, artistSource).Normalized()
	artistIntent.References[0].Evidence = nil
	if parsingContextChecks(artistIntent, &artistSource, &ParsingContextLabels{ValidSourceSpans: true})[0] {
		t.Fatal("missing explicit reference evidence accepted")
	}
	for _, mutate := range []func(*core.IntentReference){
		func(ref *core.IntentReference) { ref.Grounding.Candidates = ref.Grounding.Candidates[:1] },
		func(ref *core.IntentReference) { ref.TrackID = "silently-selected" },
		func(ref *core.IntentReference) {
			ref.Resolution = &core.ReferenceResolution{Status: core.ResolutionResolved, Selected: &core.ResolutionCandidate{EntityID: "chosen"}}
		},
	} {
		source := lexicon.Extract("like Phoenix")
		source.Atoms[0].Grounding = &core.IdentityGrounding{Candidates: []core.IdentityCandidate{{ID: "one"}, {ID: "two"}}}
		intent := lexicon.Reconcile(core.MusicIntent{OriginalDescription: "like Phoenix"}, source).Normalized()
		labels := &ParsingContextLabels{PreserveAmbiguity: true}
		if !parsingContextChecks(intent, &source, labels)[0] {
			t.Fatal("valid ambiguity rejected")
		}
		mutate(&intent.References[0])
		if parsingContextChecks(intent, &source, labels)[0] {
			t.Fatal("operational reference silently narrowed ambiguity")
		}
	}
}

func TestRescorePreservesObservedTimingAndPreparedEvidence(t *testing.T) {
	t.Parallel()
	const prompt = "warm timbre"
	source := lexicon.Extract(prompt)
	output := lexicon.Reconcile(core.MusicIntent{OriginalDescription: prompt}, source)
	output.Preferences.TextureDescriptions = nil
	dataset := Dataset{Name: "rescore", IntentCases: []IntentCase{{ID: "warm", Prompt: prompt, ParsingContext: &ParsingContextLabels{Atoms: []ParsingAtomLabel{{Text: prompt, Kind: "texture"}}}}}}
	report := IntentModelReport{Version: 3, DatasetName: dataset.Name, Aggregate: IntentModelAggregate{PeakResidentBytes: 1234}, Cases: []IntentModelRun{{CaseID: "warm", Phase: "first-pass", SchemaValid: true, Exact: true, CorrectFields: 1, LabeledFields: 1, LatencyMillis: 42, Output: output, PreparedSourceFacts: &source}}}
	rescored, err := RescoreIntentModelReport(report, dataset)
	if err != nil {
		t.Fatal(err)
	}
	if rescored.Cases[0].CorrectFields != 0 || rescored.Cases[0].Exact || rescored.ScoringVersion != IntentModelScoringVersion || rescored.Cases[0].LatencyMillis != 42 || rescored.Cases[0].PreparedSourceFacts != &source || rescored.Aggregate.PeakResidentBytes != 1234 || rescored.FirstPass.CorrectFields != 0 {
		t.Fatalf("incorrect rescore: %+v", rescored)
	}
	if report.Cases[0].CorrectFields != 1 {
		t.Fatal("rescore mutated the original report")
	}
}

func TestIntentModelLegacyReportMeasurementsRemainUnknown(t *testing.T) {
	t.Parallel()
	var report IntentModelReport
	if err := json.Unmarshal([]byte(`{"version":2,"datasetName":"legacy","cases":[{"caseId":"old","attempt":1,"latencyMillis":12}],"aggregate":{"runs":1}}`), &report); err != nil {
		t.Fatal(err)
	}
	if report.Version != 2 || report.StartupMicros != nil || report.FirstPass != nil || report.Cases[0].Retries != nil || report.Cases[0].PreparationMicros != nil || report.Cases[0].PreparedSourceFacts != nil {
		t.Fatal("legacy report synthesized new evidence")
	}
}

func TestDirectEvaluationFailureRetainsPreparedContext(t *testing.T) {
	t.Parallel()
	parser := &recordingIntentParser{parse: func(input ports.IntentInput) (core.MusicIntent, error) {
		if input.SourceFacts == nil || input.SourceFacts.ParsingContext == nil {
			t.Fatal("direct parsing skipped preparation")
		}
		return core.MusicIntent{}, errors.New("model unavailable")
	}}
	report, err := EvaluateIntentModelWithOptions(context.Background(), parser, Dataset{IntentCases: []IntentCase{{ID: "failure", Prompt: "warm timbre"}}}, IntentModelIdentity{}, 1, IntentModelOptions{EnrichParsingContext: true})
	if err != nil {
		t.Fatal(err)
	}
	run := report.Cases[0]
	if run.PreparedSourceFacts == nil || run.PreparedSourceFacts.ParsingContext == nil || run.PreparedSourceFacts.ParsingContext.Fingerprint == "" || run.PreparationMicros == nil || run.SchemaValid || run.Error == "" {
		t.Fatalf("direct failure lost prepared evidence: %+v", run)
	}
	if run.PreparedSourceFacts.Recognition.ReferenceLookup != "" {
		t.Fatal("direct text-only path invented source availability")
	}
}

func TestParsingContextDatasetFrozenSplitAndCoverage(t *testing.T) {
	t.Parallel()
	path := filepath.Join("testdata", "parsing-context-v1.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != "f1ac5d9c3460af53588d91804d41684449a69125ded4c3ea51160eb592bfe5f4" {
		t.Fatal("frozen labels changed; create a new version before evaluating another set")
	}
	dataset, err := LoadDataset(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(dataset.IntentCases) != 32 || dataset.Evidence != EvidenceSynthetic {
		t.Fatal("wrong evaluation scope/evidence")
	}
	counts, familySplits, tags := map[string]int{}, map[string]string{}, map[string]bool{}
	for _, item := range dataset.IntentCases {
		family, split := "", ""
		for _, tag := range item.Tags {
			tags[tag] = true
			if strings.HasPrefix(tag, "family:") {
				family = strings.TrimPrefix(tag, "family:")
			}
			if strings.HasPrefix(tag, "split:") {
				split = strings.TrimPrefix(tag, "split:")
			}
		}
		if family == "" || split != "development" && split != "heldout" || item.ParsingContext == nil || intentLabelCount(item.Expected) == 0 {
			t.Fatalf("unlabeled/unassigned case: %s", item.ID)
		}
		if prior, ok := familySplits[family]; ok && prior != split {
			t.Fatalf("family leaked across splits: %s", family)
		}
		familySplits[family] = split
		counts[split]++
	}
	if counts["development"] != 16 || counts["heldout"] != 16 || len(familySplits) != 16 {
		t.Fatalf("unbalanced split: %v", counts)
	}
	for _, tag := range []string{"artist_common_word", "reviewed_alias", "homonyms", "compound_names", "non_latin", "exclusions", "required_tracks", "or_alternatives", "journey_scope", "mood_timbre", "romantic_period", "vocal_strength", "unknown_wording", "unavailable_sources", "crowded_matches", "long_requests"} {
		if !tags[tag] {
			t.Fatalf("missing category %s", tag)
		}
	}
}
