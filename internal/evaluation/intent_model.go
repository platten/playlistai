package evaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/ports"
)

const IntentModelReportVersion = 3
const IntentModelScoringVersion = "context-operational/v1"

// IntentBenchmarkDevice records an accelerator reported by the exact
// llama.cpp runtime used for a benchmark. Byte counts are kept as integers so
// 24 GiB laptop and 32 GiB desktop RTX 5090 devices round-trip losslessly.
type IntentBenchmarkDevice struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	TotalBytes int64  `json:"totalBytes"`
	FreeBytes  int64  `json:"freeBytes"`
}

// IntentBenchmarkEnvironment captures the execution settings necessary to
// compare model runs across CPU and GPU hosts. Accelerator memory is a
// preflight snapshot, not a peak-VRAM measurement.
type IntentBenchmarkEnvironment struct {
	GOOS           string                  `json:"goos"`
	GOARCH         string                  `json:"goarch"`
	LogicalCPUs    int                     `json:"logicalCpus"`
	ExecutionMode  string                  `json:"executionMode"` // cpu | gpu | auto
	SelectedDevice string                  `json:"selectedDevice,omitempty"`
	ContextSize    int                     `json:"contextSize"`
	Threads        int                     `json:"threads"`
	GPULayers      int                     `json:"gpuLayers"`
	Devices        []IntentBenchmarkDevice `json:"devices,omitempty"`
	ProbeError     string                  `json:"probeError,omitempty"`
}

type IntentModelIdentity struct {
	ID            string                     `json:"id"`
	Path          string                     `json:"path"`
	ArtifactBytes int64                      `json:"artifactBytes"`
	SHA256        string                     `json:"sha256"`
	Runtime       string                     `json:"runtime"`
	Environment   IntentBenchmarkEnvironment `json:"environment"`
}

type IntentModelRun struct {
	CaseID              string                          `json:"caseId"`
	Tags                []string                        `json:"tags,omitempty"`
	Attempt             int                             `json:"attempt"`
	SchemaValid         bool                            `json:"schemaValid"`
	LabeledFields       int                             `json:"labeledFields"`
	CorrectFields       int                             `json:"correctFields"`
	Exact               bool                            `json:"exact"`
	LatencyMillis       int64                           `json:"latencyMillis"`
	Output              core.MusicIntent                `json:"output"`
	Error               string                          `json:"error,omitempty"`
	Phase               string                          `json:"phase,omitempty"`
	PreparationMicros   *int64                          `json:"preparationMicros,omitempty"`
	ParsingMicros       *int64                          `json:"parsingMicros,omitempty"`
	RecognitionIdentity string                          `json:"recognitionIdentity,omitempty"`
	PreparedSourceFacts *core.IntentTranslation         `json:"preparedSourceFacts,omitempty"`
	ParseAttempts       []ports.ParseAttemptObservation `json:"parseAttempts,omitempty"`
	Retries             *int                            `json:"retries,omitempty"`
}

type IntentModelAggregate struct {
	Runs                int     `json:"runs"`
	SchemaValidRuns     int     `json:"schemaValidRuns"`
	ExactRuns           int     `json:"exactRuns"`
	LabeledFields       int     `json:"labeledFields"`
	CorrectFields       int     `json:"correctFields"`
	SchemaValidity      float64 `json:"schemaValidity"`
	ExactCaseAccuracy   float64 `json:"exactCaseAccuracy"`
	FieldAccuracy       float64 `json:"fieldAccuracy"`
	MedianLatencyMillis int64   `json:"medianLatencyMillis"`
	P95LatencyMillis    int64   `json:"p95LatencyMillis"`
	PeakResidentBytes   int64   `json:"peakResidentBytes"`
}

type IntentModelReport struct {
	Version                  int                   `json:"version"`
	DatasetName              string                `json:"datasetName"`
	GeneratedAt              time.Time             `json:"generatedAt"`
	ParserVersion            string                `json:"parserVersion"`
	ContractVersion          int                   `json:"contractVersion"`
	Model                    IntentModelIdentity   `json:"model"`
	Cases                    []IntentModelRun      `json:"cases"`
	Aggregate                IntentModelAggregate  `json:"aggregate"`
	ContextPolicy            string                `json:"contextPolicy,omitempty"`
	StartupMicros            *int64                `json:"startupMicros,omitempty"`
	PreparationStartupMicros *int64                `json:"preparationStartupMicros,omitempty"`
	FirstPass                *IntentModelAggregate `json:"firstPass,omitempty"`
	WarmRepeat               *IntentModelAggregate `json:"warmRepeat,omitempty"`
	ScoringVersion           string                `json:"scoringVersion,omitempty"`
}

// IntentModelOptions leaves the parser and its process lifetime with the caller.
// Preparation uses the same recognition entrypoint as desktop parsing when set.
type IntentModelOptions struct {
	PrepareInput             func(context.Context, ports.IntentInput) ports.IntentInput
	EnrichParsingContext     bool
	StartupMicros            *int64
	PreparationStartupMicros *int64
}

// ParsingContextLabels score protected interpretation beyond the legacy lists.
// Empty atom attributes are unlabelled; no artist identity is assumed when the
// recognition snapshot is unavailable. Source-span validity and ambiguity are
// checked against the prepared snapshot, never against generated descriptions.
type ParsingContextLabels struct {
	Atoms             []ParsingAtomLabel `json:"atoms,omitempty"`
	ValidSourceSpans  bool               `json:"validSourceSpans,omitempty"`
	PreserveAmbiguity bool               `json:"preserveAmbiguity,omitempty"`
	AlternativeValues []string           `json:"alternativeValues,omitempty"`
}

type ParsingAtomLabel struct {
	Text      string `json:"text"`
	Kind      string `json:"kind,omitempty"`
	ConceptID string `json:"conceptId,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Polarity  string `json:"polarity,omitempty"`
	Strength  string `json:"strength,omitempty"`
	Degree    string `json:"degree,omitempty"`
}

type memoryReporter interface {
	RuntimeMemoryBytes() int64
}

// EvaluateIntentModel applies the real grammar-constrained parser to labeled
// intent cases. A parse failure counts every labeled field as incorrect.
func EvaluateIntentModel(ctx context.Context, parser ports.IntentParser, dataset Dataset, identity IntentModelIdentity, repeat int) (IntentModelReport, error) {
	return EvaluateIntentModelWithOptions(ctx, parser, dataset, identity, repeat, IntentModelOptions{})
}

func EvaluateIntentModelWithOptions(ctx context.Context, parser ports.IntentParser, dataset Dataset, identity IntentModelIdentity, repeat int, options IntentModelOptions) (IntentModelReport, error) {
	if parser == nil {
		return IntentModelReport{}, fmt.Errorf("intent evaluation: parser is required")
	}
	if repeat <= 0 {
		repeat = 1
	}
	prepare := options.PrepareInput
	if prepare == nil {
		// Match the direct llama client's text-only preparation so failed
		// attempts also retain the source facts and context they received.
		prepare = func(_ context.Context, input ports.IntentInput) ports.IntentInput {
			source := lexicon.Extract(input.Prompt)
			input.SourceFacts = &source
			return lexicon.PrepareParsingContext(input)
		}
	}
	info := parser.Info()
	report := IntentModelReport{Version: IntentModelReportVersion, DatasetName: dataset.Name, GeneratedAt: time.Now().UTC(), ParserVersion: info.Version, ContractVersion: info.ContractVersion, Model: identity, Cases: []IntentModelRun{}}
	report.ScoringVersion = IntentModelScoringVersion
	report.ContextPolicy = "baseline"
	if options.EnrichParsingContext {
		report.ContextPolicy = "enriched"
	}
	report.StartupMicros, report.PreparationStartupMicros = options.StartupMicros, options.PreparationStartupMicros
	var peakResidentBytes int64
	for attempt := 1; attempt <= repeat; attempt++ {
		for _, item := range dataset.IntentCases {
			if err := ctx.Err(); err != nil {
				return IntentModelReport{}, err
			}
			run := IntentModelRun{CaseID: item.ID, Tags: append([]string(nil), item.Tags...), Attempt: attempt, LabeledFields: intentLabelCount(item.Expected), Phase: "first-pass"}
			run.LabeledFields += len(parsingContextChecks(core.MusicIntent{}, nil, item.ParsingContext))
			if attempt > 1 {
				run.Phase = "warm-repeat"
			}
			input := intentInput(item)
			input.EnrichParsingContext = options.EnrichParsingContext
			preparedAt := time.Now()
			input = prepare(ctx, input)
			preparationMicros := time.Since(preparedAt).Microseconds()
			run.PreparationMicros = &preparationMicros
			run.RecognitionIdentity = input.RecognitionIdentity
			// Copy nested grounding/context slices before the parser runs.
			if input.SourceFacts != nil {
				cloned := input.SourceFacts.Clone()
				run.PreparedSourceFacts = &cloned
			}
			input.ObserveParseAttempt = func(observation ports.ParseAttemptObservation) {
				observation.UsedHintIndexes = append([]int(nil), observation.UsedHintIndexes...)
				if observation.MandatoryTokens != nil {
					tokens := *observation.MandatoryTokens
					observation.MandatoryTokens = &tokens
				}
				run.ParseAttempts = append(run.ParseAttempts, observation)
			}
			started := time.Now()
			intent, err := parser.Parse(ctx, input)
			elapsed := time.Since(started).Milliseconds()
			parsingMicros := time.Since(started).Microseconds()
			run.LatencyMillis, run.ParsingMicros = elapsed, &parsingMicros
			if len(run.ParseAttempts) > 0 {
				retries := len(run.ParseAttempts) - 1
				run.Retries = &retries
			}
			if err != nil {
				run.Error = err.Error()
			} else {
				run.SchemaValid = true
				run.Output = intent.Normalized()
			}
			scoreIntentModelRun(&run, item)
			report.Cases = append(report.Cases, run)
			if measured, ok := parser.(memoryReporter); ok {
				peakResidentBytes = max(peakResidentBytes, measured.RuntimeMemoryBytes())
			}
		}
	}
	for _, phase := range []string{"first-pass", "warm-repeat"} {
		var runs []IntentModelRun
		for _, run := range report.Cases {
			if run.Phase == phase {
				runs = append(runs, run)
			}
		}
		if len(runs) == 0 {
			continue
		}
		aggregate := aggregateIntentModelRuns(runs)
		if phase == "first-pass" {
			report.FirstPass = &aggregate
		} else {
			report.WarmRepeat = &aggregate
		}
	}
	report.Aggregate = aggregateIntentModelRuns(report.Cases)
	report.Aggregate.PeakResidentBytes = peakResidentBytes
	return report, nil
}

// RescoreIntentModelReport applies the current scorer to saved parser outputs.
// It never prepares new source facts, calls a model, or changes observed timing.
func RescoreIntentModelReport(report IntentModelReport, dataset Dataset) (IntentModelReport, error) {
	if report.DatasetName != dataset.Name {
		return IntentModelReport{}, fmt.Errorf("intent rescore: dataset name differs")
	}
	byID := make(map[string]IntentCase, len(dataset.IntentCases))
	for _, item := range dataset.IntentCases {
		byID[item.ID] = item
	}
	report.Cases = append([]IntentModelRun(nil), report.Cases...)
	for i := range report.Cases {
		item, ok := byID[report.Cases[i].CaseID]
		if !ok {
			return IntentModelReport{}, fmt.Errorf("intent rescore: case %q absent from labels", report.Cases[i].CaseID)
		}
		scoreIntentModelRun(&report.Cases[i], item)
	}
	report.Version, report.ScoringVersion = IntentModelReportVersion, IntentModelScoringVersion
	peak := report.Aggregate.PeakResidentBytes
	report.Aggregate = aggregateIntentModelRuns(report.Cases)
	report.Aggregate.PeakResidentBytes = peak
	for _, phase := range []struct {
		name      string
		aggregate **IntentModelAggregate
	}{{"first-pass", &report.FirstPass}, {"warm-repeat", &report.WarmRepeat}} {
		var runs []IntentModelRun
		for _, run := range report.Cases {
			if run.Phase == phase.name {
				runs = append(runs, run)
			}
		}
		if len(runs) > 0 {
			aggregate := aggregateIntentModelRuns(runs)
			*phase.aggregate = &aggregate
		}
	}
	return report, nil
}

func scoreIntentModelRun(run *IntentModelRun, item IntentCase) {
	run.LabeledFields = intentLabelCount(item.Expected) + len(parsingContextChecks(core.MusicIntent{}, nil, item.ParsingContext))
	run.CorrectFields, run.Exact = 0, false
	if !run.SchemaValid {
		return
	}
	checks := append(intentChecks(run.Output, item.Expected), parsingContextChecks(run.Output, run.PreparedSourceFacts, item.ParsingContext)...)
	for _, correct := range checks {
		if correct {
			run.CorrectFields++
		}
	}
	run.Exact = run.CorrectFields == run.LabeledFields
}

func parsingContextChecks(intent core.MusicIntent, source *core.IntentTranslation, labels *ParsingContextLabels) []bool {
	if labels == nil {
		return nil
	}
	var atoms []core.IntentAtom
	if intent.Translation != nil {
		atoms = intent.Translation.Atoms
	}
	var checks []bool
	for _, label := range labels.Atoms {
		found := false
		for _, atom := range atoms {
			matches := (label.Kind == "" || atom.Kind == label.Kind) && (label.ConceptID == "" || atom.ConceptID == label.ConceptID) && (label.Scope == "" || atom.Scope == label.Scope) && (label.Polarity == "" || atom.Polarity == label.Polarity) && (label.Strength == "" || atom.Strength == label.Strength) && (label.Degree == "" || atom.Degree == label.Degree)
			for _, evidence := range atom.Evidence {
				// Evidence may include a preference modifier ("mostly
				// instrumental") around the labeled occurrence.
				found = found || matches && strings.Contains(evidence.Text, label.Text) && operationalAtomMatches(intent, atom)
			}
		}
		checks = append(checks, found)
	}
	if labels.ValidSourceSpans {
		valid := len(atoms) > 0
		for _, ref := range operationalReferences(intent) {
			valid = valid && len(ref.Evidence) > 0
		}
		for _, group := range preferenceGroups(intent.Preferences) {
			for _, preference := range group {
				valid = valid && (!preference.Explicit || len(preference.Evidence) > 0)
			}
		}
		for _, atom := range atoms {
			valid = valid && len(atom.Evidence) > 0
			for _, evidence := range atom.Evidence {
				valid = valid && evidence.Start >= 0 && evidence.End > evidence.Start && evidence.End <= len(intent.OriginalDescription)
				if valid {
					valid = intent.OriginalDescription[evidence.Start:evidence.End] == evidence.Text
				}
			}
		}
		for _, evidence := range operationalEvidence(intent) {
			if !evidence.Explicit {
				continue
			}
			valid = valid && evidence.Start >= 0 && evidence.End > evidence.Start && evidence.End <= len(intent.OriginalDescription)
			if valid {
				valid = intent.OriginalDescription[evidence.Start:evidence.End] == evidence.Text
			}
		}
		checks = append(checks, valid)
	}
	if labels.PreserveAmbiguity {
		preserved := source != nil && intent.Translation != nil
		ambiguous := 0
		if source != nil {
			for _, before := range source.Atoms {
				if before.Grounding == nil || before.Grounding.Confirmed || len(before.Grounding.Candidates) < 2 && !before.Grounding.Truncated {
					continue
				}
				ambiguous++
				found := false
				for _, after := range atoms {
					if before.ID == after.ID && after.Grounding != nil {
						left, _ := json.Marshal(before.Grounding)
						right, _ := json.Marshal(after.Grounding)
						found = string(left) == string(right)
					}
				}
				operational := false
				for _, ref := range operationalReferences(intent) {
					if !referenceMatchesAtom(ref, before) {
						continue
					}
					left, _ := json.Marshal(before.Grounding)
					right, _ := json.Marshal(ref.Grounding)
					unchosen := ref.TrackID == "" && (ref.Resolution == nil || ref.Resolution.Status != core.ResolutionResolved && ref.Resolution.Selected == nil)
					operational = string(left) == string(right) && unchosen
					if !operational {
						break
					}
				}
				preserved = preserved && found && operational
			}
		}
		checks = append(checks, preserved && ambiguous > 0)
	}
	if labels.AlternativeValues != nil {
		group, valid := "", true
		for _, value := range labels.AlternativeValues {
			found := false
			for _, atom := range atoms {
				if atom.Value == value && atom.Group != "" {
					if group == "" {
						group = atom.Group
					}
					found = atom.Group == group && operationalAtomMatches(intent, atom)
				}
			}
			valid = valid && found
		}
		checks = append(checks, valid)
	}
	return checks
}

func operationalAtomMatches(intent core.MusicIntent, atom core.IntentAtom) bool {
	preferences := preferenceGroups(intent.Preferences)
	if group, musical := preferences[atom.Kind]; musical {
		found := false
		for _, preference := range group {
			if preference.Value == atom.Value && preference.ConceptID == atom.ConceptID && preference.Scope == atom.Scope && string(preference.Influence) == atom.Polarity && preference.Strength == atom.Strength && preference.Degree == atom.Degree && preference.Group == atom.Group && overlappingEvidence(preference.Evidence, atom.Evidence) {
				found = true
			}
		}
		if !found {
			return false
		}
		if atom.Polarity == "positive" && (atom.Strength == "required" || atom.Strength == "essential") {
			found = false
			for _, criterion := range intent.EssentialCriteria {
				// Repeated identical requirements are intentionally deduplicated.
				if criterion.Value == atom.Value && criterion.Kind == atom.Kind && criterion.ConceptID == atom.ConceptID && criterion.Scope == atom.Scope && criterion.Strength == atom.Strength && criterion.Group == atom.Group {
					found = true
				}
			}
		}
		return found
	}
	if atom.Scope == "journey_start" {
		return intent.Start != nil && referenceMatchesAtom(*intent.Start, atom)
	}
	if atom.Scope == "journey_end" {
		return intent.Destination != nil && referenceMatchesAtom(*intent.Destination, atom)
	}
	for _, ref := range operationalReferences(intent) {
		if referenceMatchesAtom(ref, atom) {
			return true
		}
	}
	return false
}

func preferenceGroups(preferences core.SemanticPreferences) map[string][]core.IntentPreference {
	vocals := preferences.VocalPreferences
	if len(vocals) == 0 && preferences.VocalPreference != nil {
		vocals = []core.IntentPreference{*preferences.VocalPreference}
	}
	return map[string][]core.IntentPreference{"genre": preferences.Genres, "style": preferences.Styles, "mood": preferences.Moods, "texture": preferences.TextureDescriptions, "instrumentation": preferences.Instrumentation, "vocal": vocals}
}

func overlappingEvidence(left, right []core.SourceEvidence) bool {
	for _, a := range left {
		for _, b := range right {
			if a.Start >= 0 && b.Start >= 0 && a.Start < b.End && b.Start < a.End {
				return true
			}
		}
	}
	return false
}

func referenceMatchesAtom(ref core.IntentReference, atom core.IntentAtom) bool {
	kind := core.ReferenceKind(atom.Kind)
	switch atom.Kind {
	case "exclude_artist", "require_artist", "start", "destination", "entity_mention":
		kind = core.ReferenceArtist
		if _, _, qualified := core.QualifiedReferenceParts(atom.Value); qualified {
			kind = core.ReferenceTrack
		}
	case "required_track":
		kind = core.ReferenceTrack
	}
	return ref.Kind == kind && ref.Query == atom.Value && string(ref.Influence) == atom.Polarity && overlappingEvidence(ref.Evidence, atom.Evidence)
}

func operationalReferences(intent core.MusicIntent) []core.IntentReference {
	var refs []core.IntentReference
	refs = append(refs, intent.References...)
	refs = append(refs, intent.RequiredTracks...)
	refs = append(refs, intent.Journey.Waypoints...)
	if intent.Start != nil {
		refs = append(refs, *intent.Start)
	}
	if intent.Destination != nil {
		refs = append(refs, *intent.Destination)
	}
	return refs
}

func operationalEvidence(intent core.MusicIntent) []core.SourceEvidence {
	var evidence []core.SourceEvidence
	for _, ref := range operationalReferences(intent) {
		evidence = append(evidence, ref.Evidence...)
	}
	for _, group := range preferenceGroups(intent.Preferences) {
		for _, preference := range group {
			evidence = append(evidence, preference.Evidence...)
		}
	}
	if intent.Preferences.VocalPreference != nil {
		evidence = append(evidence, intent.Preferences.VocalPreference.Evidence...)
	}
	for _, criterion := range intent.EssentialCriteria {
		evidence = append(evidence, criterion.Evidence...)
	}
	for _, constraint := range intent.HardConstraints {
		evidence = append(evidence, constraint.Evidence...)
	}
	for _, unsupported := range intent.Unsupported {
		evidence = append(evidence, unsupported.Evidence...)
	}
	for _, temporal := range intent.Temporal {
		evidence = append(evidence, temporal.Evidence...)
	}
	for _, anchor := range intent.InferredAnchors {
		evidence = append(evidence, anchor.Reference.Evidence...)
	}
	for _, anchor := range intent.AnchorAttempts {
		evidence = append(evidence, anchor.Reference.Evidence...)
	}
	return evidence
}

func aggregateIntentModelRuns(runs []IntentModelRun) IntentModelAggregate {
	a := IntentModelAggregate{Runs: len(runs)}
	latencies := make([]int64, 0, len(runs))
	for _, run := range runs {
		a.LabeledFields += run.LabeledFields
		a.CorrectFields += run.CorrectFields
		if run.SchemaValid {
			a.SchemaValidRuns++
		}
		if run.Exact {
			a.ExactRuns++
		}
		latencies = append(latencies, run.LatencyMillis)
	}
	if a.Runs > 0 {
		a.SchemaValidity = float64(a.SchemaValidRuns) / float64(a.Runs)
		a.ExactCaseAccuracy = float64(a.ExactRuns) / float64(a.Runs)
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		a.MedianLatencyMillis, a.P95LatencyMillis = percentileMillis(latencies, .5), percentileMillis(latencies, .95)
	}
	if a.LabeledFields > 0 {
		a.FieldAccuracy = float64(a.CorrectFields) / float64(a.LabeledFields)
	}
	return a
}

func percentileMillis(sorted []int64, percentile float64) int64 {
	index := int(float64(len(sorted)-1)*percentile + .5)
	return sorted[min(len(sorted)-1, max(0, index))]
}

func WriteIntentModelReportJSON(path string, report IntentModelReport) error {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

func WriteIntentModelReportMarkdown(path string, report IntentModelReport) error {
	a := report.Aggregate
	env := report.Model.Environment
	text := fmt.Sprintf("# Intent Model Evaluation: %s\n\nModel: `%s`  \nArtifact: `%d` bytes, SHA-256 `%s`  \nRuntime: `%s`  \nHost: `%s/%s`, %d logical CPUs  \nExecution: `%s`", report.DatasetName, report.Model.ID, report.Model.ArtifactBytes, report.Model.SHA256, report.Model.Runtime, env.GOOS, env.GOARCH, env.LogicalCPUs, env.ExecutionMode)
	if env.ContextSize > 0 {
		device := env.SelectedDevice
		if device == "" {
			device = "runtime default"
		}
		text += fmt.Sprintf(", device `%s`, context %d, threads %d, GPU layers %d", device, env.ContextSize, env.Threads, env.GPULayers)
	}
	text += fmt.Sprintf("  \nParser/schema: `%s` / `v%d`\n\n", report.ParserVersion, report.ContractVersion)
	if report.ContextPolicy != "" {
		text += fmt.Sprintf("Parsing context: `%s`. Token counts are measured only when reported; optional bounds are conservative UTF-8 byte estimates.\n\n", report.ContextPolicy)
	}
	if report.StartupMicros != nil {
		text += fmt.Sprintf("Parser startup: %d μs.\n\n", *report.StartupMicros)
	}
	if report.PreparationStartupMicros != nil {
		text += fmt.Sprintf("Recognition setup: %d μs.\n\n", *report.PreparationStartupMicros)
	}
	for _, phase := range []struct {
		name      string
		aggregate *IntentModelAggregate
	}{{"First pass", report.FirstPass}, {"Warm repeat", report.WarmRepeat}} {
		if phase.aggregate != nil {
			text += fmt.Sprintf("%s: %d runs, median %d ms, P95 %d ms, field accuracy %.3f.\n\n", phase.name, phase.aggregate.Runs, phase.aggregate.MedianLatencyMillis, phase.aggregate.P95LatencyMillis, phase.aggregate.FieldAccuracy)
		}
	}
	if len(env.Devices) > 0 {
		text += "Detected llama.cpp devices:\n\n"
		for _, device := range env.Devices {
			text += fmt.Sprintf("- `%s`: %s (%d bytes total, %d bytes free)\n", device.ID, device.Name, device.TotalBytes, device.FreeBytes)
		}
		text += "\n"
	}
	if env.ProbeError != "" {
		text += fmt.Sprintf("Device probe warning: `%s`\n\n", strings.NewReplacer("`", "'", "\n", " ").Replace(env.ProbeError))
	}
	text += fmt.Sprintf("| Runs | Schema-valid | Exact cases | Field accuracy | Median | P95 | Peak RSS |\n|---:|---:|---:|---:|---:|---:|---:|\n| %d | %.3f | %.3f | %.3f | %d ms | %d ms | %d bytes |\n\n", a.Runs, a.SchemaValidity, a.ExactCaseAccuracy, a.FieldAccuracy, a.MedianLatencyMillis, a.P95LatencyMillis, a.PeakResidentBytes)
	text += "| Case | Attempt | Schema | Correct fields | Exact | Latency | Error |\n|---|---:|---:|---:|---:|---:|---|\n"
	for _, run := range report.Cases {
		errText := strings.NewReplacer("|", "\\|", "\n", " ").Replace(run.Error)
		text += fmt.Sprintf("| %s | %d | %t | %d/%d | %t | %d ms | %s |\n", run.CaseID, run.Attempt, run.SchemaValid, run.CorrectFields, run.LabeledFields, run.Exact, run.LatencyMillis, errText)
	}
	text += "\n"
	return os.WriteFile(path, []byte(text), 0o644)
}
