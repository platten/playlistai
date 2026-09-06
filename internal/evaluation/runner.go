package evaluation

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/reco/deejai"
	"github.com/platten/playlistai/internal/reco/multichannel"
	"github.com/platten/playlistai/internal/resolution"
	"github.com/platten/playlistai/internal/taste"
)

type Runner struct {
	Catalog    ports.Catalog
	Resolver   ports.ReferenceResolver
	Similarity ports.SimilarityEngine
	Parser     ports.IntentParser
	Features   ports.FeatureStore
	Semantic   ports.SemanticSearcher
	K          int
}

type variant struct {
	name       string
	engine     ports.RecommendationEngine
	retriever  ports.CandidateRetriever
	ranker     ports.Ranker
	selector   ports.CandidateSelector
	sequencer  ports.PlaylistSequencer
	parameters ParameterSet
	profile    bool
	control    func(core.MusicIntent) core.MusicIntent
}

func LoadDataset(path string) (Dataset, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Dataset{}, err
	}
	var dataset Dataset
	if err := json.Unmarshal(raw, &dataset); err != nil {
		return Dataset{}, fmt.Errorf("evaluation dataset: %w", err)
	}
	if dataset.Version != ContractVersion {
		return Dataset{}, fmt.Errorf("evaluation: unsupported dataset version %d", dataset.Version)
	}
	if dataset.Name == "" {
		return Dataset{}, errors.New("evaluation: dataset name is required")
	}
	switch dataset.Evidence {
	case EvidenceSynthetic, EvidenceUnlabeled, EvidenceJudged, EvidenceObserved:
	default:
		return Dataset{}, fmt.Errorf("evaluation: invalid evidence level %q", dataset.Evidence)
	}
	for _, item := range dataset.RecommendationCases {
		for id, grade := range item.Relevance {
			if id == "" || grade < 0 || grade > 3 {
				return Dataset{}, fmt.Errorf("evaluation: case %q has invalid relevance judgment %q=%g", item.ID, id, grade)
			}
		}
	}
	for _, item := range dataset.Interactions {
		if item.ListenerID == "" {
			return Dataset{}, errors.New("evaluation: interaction listenerId is required")
		}
		if err := item.Event.Validate(); err != nil {
			return Dataset{}, fmt.Errorf("evaluation: interaction %q: %w", item.Event.ID, err)
		}
	}
	return dataset, nil
}

func (r Runner) Run(ctx context.Context, dataset Dataset) (Report, error) {
	if r.K <= 0 {
		r.K = 20
	}
	if r.Catalog == nil || r.Resolver == nil || r.Similarity == nil || r.Parser == nil {
		return Report{}, errors.New("evaluation: catalog, resolver, similarity, and parser are required")
	}
	version := r.Resolver.CatalogVersion()
	if dataset.CatalogVersion != "" && dataset.CatalogVersion != version {
		return Report{}, fmt.Errorf("evaluation: dataset catalog %q does not match %q", dataset.CatalogVersion, version)
	}
	report := Report{Version: ContractVersion, DatasetName: dataset.Name, Evidence: dataset.Evidence, CatalogVersion: version, ParserVersion: r.Parser.Info().Version, GeneratedAt: time.Now().UTC(), K: r.K}
	if r.Semantic != nil {
		info := r.Semantic.Info()
		report.SemanticInfo = &info
	}
	report.Cohorts = cohortCounts(dataset)
	report.Intent = r.evaluateIntent(ctx, dataset.IntentCases)
	report.Resolution = r.evaluateResolution(dataset.ResolutionCases)
	if len(dataset.RecommendationCases) == 0 {
		report.Limitations = append(report.Limitations, "no recommendation cases: retrieval, ranking, playlist, and latency metrics are unavailable")
		return report, nil
	}
	split, err := TemporalSplitCases(dataset.RecommendationCases)
	if err != nil {
		return Report{}, err
	}
	report.TemporalSplit = split
	defaults := parametersFromConfig("current-defaults", multichannel.DefaultConfig())
	devCases := casesInSplit(dataset.RecommendationCases, split, SplitDevelopment)
	selected := defaults
	if dataset.Evidence != EvidenceSynthetic && len(dataset.TuningGrid) > 0 && hasRelevance(devCases, dataset.Interactions) {
		selected, report.TuningCandidates = r.tune(ctx, dataset, split)
		report.SelectedParameters = &selected
	} else {
		report.Limitations = append(report.Limitations, "defaults were not tuned: real development relevance judgments and a tuning grid are required")
	}
	report.Development = r.evaluateVariants(ctx, dataset, split, SplitDevelopment, selected)
	// Parameters are frozen above. Every variant is now evaluated exactly once
	// on the held-out temporal test cases.
	report.HeldOutTest = r.evaluateVariants(ctx, dataset, split, SplitTest, selected)
	if dataset.Evidence == EvidenceSynthetic {
		report.Limitations = append(report.Limitations, "synthetic measurements validate the harness only and are not evidence of musical recommendation quality")
	}
	if dataset.Evidence == EvidenceUnlabeled {
		report.Limitations = append(report.Limitations, "unlabeled workflow output supports blind collection only; it is not recommendation-quality evidence")
	}
	if !hasRelevance(casesInSplit(dataset.RecommendationCases, split, SplitTest), dataset.Interactions) {
		report.Limitations = append(report.Limitations, "held-out Recall@K and NDCG@K unavailable because no relevance judgments were supplied")
	}
	if r.Semantic == nil {
		report.Limitations = append(report.Limitations, "semantic ablation unavailable because no compatible feature sidecar and local encoder were configured")
	}
	return report, nil
}

func (r Runner) evaluateIntent(ctx context.Context, cases []IntentCase) ExtractionResult {
	result := ExtractionResult{Cases: len(cases)}
	for _, item := range cases {
		intent, err := r.Parser.Parse(ctx, intentInput(item))
		if err != nil {
			result.LabeledFields += intentLabelCount(item.Expected)
			continue
		}
		checks := intentChecks(intent.Normalized(), item.Expected)
		for _, ok := range checks {
			result.LabeledFields++
			if ok {
				result.CorrectFields++
			}
		}
	}
	if result.LabeledFields > 0 {
		value := float64(result.CorrectFields) / float64(result.LabeledFields)
		result.Accuracy = &value
	}
	return result
}

func intentLabelCount(labels IntentLabels) int {
	count := 0
	if labels.Mode != "" {
		count++
	}
	if labels.TotalTrackCount != nil {
		count++
	}
	for _, labeled := range []bool{
		labels.PositiveReferences != nil,
		labels.NegativeReferences != nil,
		labels.PositivePreferences != nil,
		labels.NegativePreferences != nil,
		labels.HardConstraints != nil,
		labels.TypedReferences != nil,
		labels.RequiredTracks != nil,
		labels.JourneyWaypoints != nil,
		labels.Unsupported != nil,
		labels.EvidenceSpans != nil,
	} {
		if labeled {
			count++
		}
	}
	return count
}

func intentChecks(intent core.MusicIntent, want IntentLabels) []bool {
	checks := []bool{}
	if want.Mode != "" {
		checks = append(checks, intent.Mode == want.Mode)
	}
	if want.TotalTrackCount != nil {
		checks = append(checks, intent.Count == *want.TotalTrackCount)
	}
	positiveRefs, negativeRefs := []string{}, []string{}
	for _, ref := range intent.References {
		if ref.Influence == core.InfluenceNegative {
			negativeRefs = append(negativeRefs, ref.Query)
		} else {
			positiveRefs = append(positiveRefs, ref.Query)
		}
	}
	positivePrefs, negativePrefs := preferenceLabels(intent.Preferences)
	constraints := []string{}
	for _, c := range intent.HardConstraints {
		constraints = append(constraints, c.Kind+":"+c.Value)
	}
	if want.PositiveReferences != nil {
		checks = append(checks, equalLabels(positiveRefs, want.PositiveReferences))
	}
	if want.NegativeReferences != nil {
		checks = append(checks, equalLabels(negativeRefs, want.NegativeReferences))
	}
	if want.PositivePreferences != nil {
		checks = append(checks, equalLabels(positivePrefs, want.PositivePreferences))
	}
	if want.NegativePreferences != nil {
		checks = append(checks, equalLabels(negativePrefs, want.NegativePreferences))
	}
	if want.HardConstraints != nil {
		checks = append(checks, equalLabels(constraints, want.HardConstraints))
	}
	if want.TypedReferences != nil {
		checks = append(checks, equalLabels(referenceLabels(intent.References), want.TypedReferences))
	}
	if want.RequiredTracks != nil {
		checks = append(checks, equalLabels(referenceLabels(intent.RequiredTracks), want.RequiredTracks))
	}
	if want.JourneyWaypoints != nil {
		checks = append(checks, equalLabels(referenceLabels(intent.Journey.Waypoints), want.JourneyWaypoints))
	}
	if want.Unsupported != nil {
		unsupported := make([]string, 0, len(intent.Unsupported))
		for _, item := range intent.Unsupported {
			unsupported = append(unsupported, item.Text)
		}
		checks = append(checks, equalLabels(unsupported, want.Unsupported))
	}
	if want.EvidenceSpans != nil {
		checks = append(checks, equalLabels(evidenceSpans(intent), want.EvidenceSpans))
	}
	return checks
}

func intentInput(item IntentCase) ports.IntentInput {
	return ports.IntentInput{Prompt: item.Prompt, NowPlaying: item.NowPlaying, RecentTracks: item.RecentTracks, Locale: item.Locale}
}

func referenceLabels(references []core.IntentReference) []string {
	out := make([]string, 0, len(references))
	for _, reference := range references {
		out = append(out, string(reference.Kind)+":"+string(reference.Influence)+":"+reference.Query)
	}
	return out
}

func evidenceSpans(intent core.MusicIntent) []string {
	spans := map[string]struct{}{}
	add := func(evidence []core.SourceEvidence) {
		for _, item := range evidence {
			if value := normalizeLabel(item.Text); value != "" {
				spans[value] = struct{}{}
			}
		}
	}
	for _, group := range [][]core.IntentReference{intent.References, intent.RequiredTracks, intent.Journey.Waypoints} {
		for _, item := range group {
			add(item.Evidence)
		}
	}
	for _, group := range [][]core.IntentPreference{intent.Preferences.Styles, intent.Preferences.Moods, intent.Preferences.Instrumentation, intent.Preferences.TextureDescriptions} {
		for _, item := range group {
			add(item.Evidence)
		}
	}
	if intent.Preferences.VocalPreference != nil {
		add(intent.Preferences.VocalPreference.Evidence)
	}
	for _, item := range intent.HardConstraints {
		add(item.Evidence)
	}
	for _, item := range intent.Unsupported {
		add(item.Evidence)
	}
	out := make([]string, 0, len(spans))
	for span := range spans {
		out = append(out, span)
	}
	return out
}

func preferenceLabels(p core.SemanticPreferences) ([]string, []string) {
	positive, negative := []string{}, []string{}
	groups := [][]core.IntentPreference{p.Styles, p.Moods, p.Instrumentation, p.TextureDescriptions}
	if p.VocalPreference != nil {
		groups = append(groups, []core.IntentPreference{*p.VocalPreference})
	}
	for _, group := range groups {
		for _, item := range group {
			if item.Influence == core.InfluenceNegative {
				negative = append(negative, item.Value)
			} else {
				positive = append(positive, item.Value)
			}
		}
	}
	return positive, negative
}

func equalLabels(got, want []string) bool {
	a, b := normalizeLabels(got), normalizeLabels(want)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func normalizeLabels(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = normalizeLabel(v)
	}
	sort.Strings(out)
	return out
}

func (r Runner) evaluateResolution(cases []ResolutionCase) ResolutionResult {
	result := ResolutionResult{Cases: len(cases)}
	for _, item := range cases {
		got := r.Resolver.ResolveReference(item.Reference)
		if got.Status != item.ExpectedStatus {
			continue
		}
		if len(item.AcceptableEntityIDs) > 0 {
			matched := got.Status == core.ResolutionResolved && got.Selected != nil && contains(item.AcceptableEntityIDs, got.Selected.EntityID)
			if got.Status == core.ResolutionAmbiguous {
				for _, alternative := range got.Alternatives {
					matched = matched || contains(item.AcceptableEntityIDs, alternative.EntityID)
				}
			}
			if !matched {
				continue
			}
		}
		result.Correct++
	}
	if result.Cases > 0 {
		value := float64(result.Correct) / float64(result.Cases)
		result.Accuracy = &value
	}
	return result
}

func (r Runner) evaluateVariants(ctx context.Context, dataset Dataset, split TemporalSplit, which Split, parameters ParameterSet) []VariantResult {
	result := []VariantResult{}
	for _, v := range r.variants(parameters) {
		result = append(result, r.evaluateVariant(ctx, dataset, split, which, v))
	}
	return result
}

func (r Runner) evaluateVariant(ctx context.Context, dataset Dataset, split TemporalSplit, which Split, v variant) VariantResult {
	out := VariantResult{Name: v.name, AlgorithmVersion: algorithmVersion(v.engine), Split: which, Parameters: v.parameters, Cases: []CaseMetrics{}}
	for _, item := range casesInSplit(dataset.RecommendationCases, split, which) {
		out.Cases = append(out.Cases, r.evaluateCase(ctx, dataset, split, which, item, v))
	}
	out.Aggregate = aggregate(out.Cases)
	out.Uncertainty = uncertainty(out.Cases)
	return out
}

func (r Runner) evaluateCase(ctx context.Context, dataset Dataset, split TemporalSplit, which Split, item RecommendationCase, v variant) CaseMetrics {
	metrics := CaseMetrics{CaseID: item.ID}
	parseStarted := time.Now()
	intent := item.Intent
	if intent.Version == 0 {
		parsed, err := r.Parser.Parse(ctx, ports.IntentInput{Prompt: item.Prompt, SessionID: item.ListenerID})
		if err != nil {
			metrics.Error = err.Error()
			return metrics
		}
		intent = parsed
	}
	metrics.Latency.ParseMicros = time.Since(parseStarted).Microseconds()
	intent = intent.Normalized()
	if v.control != nil {
		intent = v.control(intent).Normalized()
	}
	resolved, issues := resolution.Apply(r.Resolver, intent)
	if err := resolution.BlockingError(issues); err != nil {
		metrics.Error = err.Error()
		return metrics
	}
	intent = resolved.Normalized()
	profile := core.TasteProfile{ColdStart: true, Clusters: []core.TasteCluster{}, RecentExposures: map[string]float64{}}
	if v.profile {
		records := ProfileEvents(dataset.Interactions, item.ListenerID, item.OccurredAt, which, split)
		events := make([]core.FeedbackEvent, len(records))
		for i, record := range records {
			events[i] = record.Event
		}
		built, err := taste.BuildProfile(ctx, r.Catalog, events, taste.ProfileOptions{IncludeAllExposures: true})
		if err != nil {
			metrics.Error = err.Error()
			return metrics
		}
		profile = built
	}
	recent := refsForIDs(r.Catalog, item.RecentExposures)
	if v.retriever != nil {
		retrievalStarted := time.Now()
		candidates, err := v.retriever.Retrieve(ctx, ports.RetrievalRequest{Intent: intent, Profile: profile, RecentSelections: recent, Seed: seedInt64(intent.Seed)})
		metrics.Latency.RetrievalMicros = time.Since(retrievalStarted).Microseconds()
		if err == nil {
			ids := make([]string, len(candidates))
			for i, c := range candidates {
				ids[i] = c.Track.ID
			}
			relevance := caseRelevance(item, dataset.Interactions)
			if value, ok := RecallAtK(ids, relevance, r.K); ok {
				metrics.RecallAtK = &value
			}
			if v.ranker != nil {
				rankingStarted := time.Now()
				ranked, rankErr := v.ranker.Rank(ctx, candidates, ports.RankRequest{Intent: intent, Profile: profile})
				metrics.Latency.RankingMicros = time.Since(rankingStarted).Microseconds()
				if rankErr == nil && v.selector != nil && v.sequencer != nil {
					required := intentTracks(r.Catalog, intent.RequiredTracks)
					waypoints := intentTracks(r.Catalog, intent.Journey.Waypoints)
					selectionStarted := time.Now()
					selected, selectErr := v.selector.Select(ctx, ranked, ports.SelectionRequest{Intent: intent, Required: required, Waypoints: waypoints, RecentSelections: recent, Count: max(0, intent.Count-len(required))})
					if selectErr == nil {
						_, _ = v.sequencer.Sequence(ctx, ports.SequenceRequest{Intent: intent, Candidates: selected.Candidates, Required: required, Waypoints: waypoints, ReferenceAnchors: intentTracks(r.Catalog, intent.References), RecentSelections: recent, Seed: seedInt64(intent.Seed)})
					}
					metrics.Latency.SequencingMicros = time.Since(selectionStarted).Microseconds()
				}
			}
		}
	}
	started := time.Now()
	var playlist core.Playlist
	var err error
	if contextual, ok := v.engine.(ports.ContextualRecommendationEngine); ok {
		playlist, err = contextual.BuildRecommendation(ctx, ports.RecommendationRequest{Intent: intent, Profile: profile, RecentSelections: recent})
	} else if personalized, ok := v.engine.(ports.PersonalizedRecommendationEngine); ok && v.profile {
		playlist, err = personalized.BuildWithProfile(ctx, intent, profile)
	} else {
		playlist, err = v.engine.Build(ctx, intent)
	}
	metrics.Latency.TotalMicros = time.Since(started).Microseconds()
	if err != nil {
		metrics.Error = err.Error()
		return metrics
	}
	ids := playlist.IDs()
	if value, ok := NDCGAtK(ids, caseRelevance(item, dataset.Interactions), r.K); ok {
		metrics.NDCGAtK = &value
	}
	metrics.HardConstraintViolations = HardConstraintViolations(ctx, playlist, r.Features)
	metrics.RecordingDuplicates, metrics.ArtistDiversity, metrics.MaxArtistShare, metrics.CatalogCoverage, metrics.RecentExposureRepetition, metrics.TransitionQuality = PlaylistDiagnostics(r.Catalog, playlist, item.RecentExposures)
	metrics.Generation = GenerationRecord{TrackIDs: ids, CatalogVersion: r.Resolver.CatalogVersion(), AlgorithmVersion: algorithmVersion(v.engine), IntentFingerprint: fingerprintJSON(intent.Normalized()), ContextFingerprint: fingerprintJSON(item.RecentExposures), IntentVersion: playlist.Intent.Version, ProfileVersion: profile.AlgorithmVersion, ProfileSnapshot: profile.SnapshotID, RNGSeed: playlist.Seed}
	return metrics
}

func (r Runner) variants(parameters ParameterSet) []variant {
	baseCfg := configFromParameters(parameters)
	noDiversity := baseCfg
	noDiversity.MMRMinimumLambda = 1
	noDiversity.EmbeddingRedundancyWeight = 0
	noDiversity.ArtistConcentrationWeight = 0
	noDiversity.AlbumConcentrationWeight = 0
	noDiversity.SoftArtistSpacingMax = 0
	noDiversity.LocalImprovementPasses = 0
	withoutDiversityAndSequencing := func(i core.MusicIntent) core.MusicIntent {
		i.Controls.ArtistDiversity = 0
		i.Controls.TransitionSmoothness = 0
		return i
	}
	baseline := deejai.New(r.Catalog, r.Similarity, r.Resolver)
	plainRetriever := multichannel.NewRetriever(r.Catalog, r.Similarity, noDiversity)
	plainRanker := multichannel.NewRanker(r.Catalog, noDiversity)
	plainSelector := multichannel.NewSelector(r.Catalog, noDiversity)
	plainSequencer := multichannel.NewSequencer(r.Catalog, noDiversity)
	plain := multichannel.New(r.Catalog, r.Similarity, r.Resolver, noDiversity)
	variants := []variant{
		{name: "audio_only", engine: baseline, parameters: parameters, control: func(i core.MusicIntent) core.MusicIntent {
			i.Controls.AudioWeight = 1
			i.Controls.CooccurrenceWeight = 0
			return i
		}},
		{name: "cooccurrence_only", engine: baseline, parameters: parameters, control: func(i core.MusicIntent) core.MusicIntent {
			i.Controls.AudioWeight = 0
			i.Controls.CooccurrenceWeight = 1
			return i
		}},
		{name: "blended_walk", engine: baseline, parameters: parameters},
		{name: "multi_channel", engine: plain, retriever: plainRetriever, ranker: plainRanker, selector: plainSelector, sequencer: plainSequencer, parameters: parameters, control: withoutDiversityAndSequencing},
		{name: "personalization", engine: plain, retriever: plainRetriever, ranker: plainRanker, selector: plainSelector, sequencer: plainSequencer, parameters: parameters, profile: true, control: withoutDiversityAndSequencing},
	}
	if r.Semantic != nil {
		semRetriever := multichannel.NewSemanticRetriever(r.Catalog, r.Similarity, r.Semantic, noDiversity)
		variants = append(variants, variant{name: "semantic", engine: multichannel.NewWithSemantic(r.Catalog, r.Similarity, r.Resolver, r.Features, r.Semantic, noDiversity), retriever: semRetriever, ranker: plainRanker, selector: plainSelector, sequencer: plainSequencer, parameters: parameters, control: withoutDiversityAndSequencing})
	}
	full := multichannel.New(r.Catalog, r.Similarity, r.Resolver, baseCfg)
	fullRetriever := multichannel.NewRetriever(r.Catalog, r.Similarity, baseCfg)
	if r.Semantic != nil {
		full = multichannel.NewWithSemantic(r.Catalog, r.Similarity, r.Resolver, r.Features, r.Semantic, baseCfg)
		fullRetriever = multichannel.NewSemanticRetriever(r.Catalog, r.Similarity, r.Semantic, baseCfg)
	}
	variants = append(variants, variant{name: "diversity_sequencing", engine: full, retriever: fullRetriever, ranker: multichannel.NewRanker(r.Catalog, baseCfg), selector: multichannel.NewSelector(r.Catalog, baseCfg), sequencer: multichannel.NewSequencer(r.Catalog, baseCfg), profile: true, parameters: parameters})
	return variants
}

func (r Runner) tune(ctx context.Context, dataset Dataset, split TemporalSplit) (ParameterSet, []VariantResult) {
	best := dataset.TuningGrid[0]
	bestViolations := int(^uint(0) >> 1)
	bestNDCG := -1.0
	results := make([]VariantResult, 0, len(dataset.TuningGrid))
	for _, parameters := range dataset.TuningGrid {
		v := r.variants(parameters)
		candidate := r.evaluateVariant(ctx, dataset, split, SplitDevelopment, v[len(v)-1])
		results = append(results, candidate)
		score := -1.0
		if candidate.Aggregate.NDCGAtK != nil {
			score = *candidate.Aggregate.NDCGAtK
		}
		violations := candidate.Aggregate.HardConstraintViolations
		if violations < bestViolations || (violations == bestViolations && (score > bestNDCG || (score == bestNDCG && parameters.Name < best.Name))) {
			best, bestViolations, bestNDCG = parameters, violations, score
		}
	}
	return best, results
}

func parametersFromConfig(name string, c multichannel.Config) ParameterSet {
	return ParameterSet{Name: name, ListenerWeight: c.ListenerWeight, NegativePenalty: c.NegativePenalty, SemanticWeight: c.SemanticWeight, SemanticNegativePenalty: c.SemanticNegativePenalty, MMRMinimumLambda: c.MMRMinimumLambda, SelectionRelevanceWindow: c.SelectionRelevanceWindow, TransitionRelevanceWeight: c.TransitionRelevanceWeight}
}
func configFromParameters(p ParameterSet) multichannel.Config {
	c := multichannel.DefaultConfig()
	if p.ListenerWeight >= 0 {
		c.ListenerWeight = p.ListenerWeight
	}
	if p.NegativePenalty >= 0 {
		c.NegativePenalty = p.NegativePenalty
	}
	if p.SemanticWeight >= 0 {
		c.SemanticWeight = p.SemanticWeight
	}
	if p.SemanticNegativePenalty >= 0 {
		c.SemanticNegativePenalty = p.SemanticNegativePenalty
	}
	if p.MMRMinimumLambda > 0 {
		c.MMRMinimumLambda = p.MMRMinimumLambda
	}
	if p.SelectionRelevanceWindow > 0 {
		c.SelectionRelevanceWindow = p.SelectionRelevanceWindow
	}
	if p.TransitionRelevanceWeight >= 0 {
		c.TransitionRelevanceWeight = p.TransitionRelevanceWeight
	}
	return c
}
func casesInSplit(cases []RecommendationCase, split TemporalSplit, want Split) []RecommendationCase {
	out := []RecommendationCase{}
	for _, item := range cases {
		if split.Assignments[item.ID] == want {
			out = append(out, item)
		}
	}
	return out
}
func hasRelevance(cases []RecommendationCase, interactions []InteractionRecord) bool {
	for _, item := range cases {
		for _, grade := range caseRelevance(item, interactions) {
			if grade > 0 {
				return true
			}
		}
	}
	return false
}

// caseRelevance combines explicit judgments with explicit post-generation
// request outcomes. Exposures and unrelated or pre-generation events are not
// relevance evidence. The latest matching outcome for a track wins.
func caseRelevance(item RecommendationCase, interactions []InteractionRecord) map[string]float64 {
	result := make(map[string]float64, len(item.Relevance))
	for id, grade := range item.Relevance {
		result[id] = grade
	}
	requestID := item.RequestID
	if requestID == "" {
		requestID = item.ID
	}
	type outcome struct {
		at    time.Time
		id    string
		grade float64
	}
	outcomes := map[string]outcome{}
	for _, record := range interactions {
		event := record.Event
		if record.ListenerID != item.ListenerID || event.RequestID != requestID || event.OccurredAt.Before(item.OccurredAt) {
			continue
		}
		grade, judged := interactionGrade(event.Type)
		if !judged {
			continue
		}
		current, exists := outcomes[event.TrackID]
		if !exists || event.OccurredAt.After(current.at) || (event.OccurredAt.Equal(current.at) && event.ID > current.id) {
			outcomes[event.TrackID] = outcome{at: event.OccurredAt, id: event.ID, grade: grade}
		}
	}
	for id, value := range outcomes {
		result[id] = value.grade
	}
	return result
}

func interactionGrade(kind core.FeedbackType) (float64, bool) {
	switch kind {
	case core.FeedbackLike, core.FeedbackMoreLike:
		return 3, true
	case core.FeedbackAccepted:
		return 2, true
	case core.FeedbackDislike, core.FeedbackLessLike, core.FeedbackRemoved:
		return 0, true
	default:
		return 0, false
	}
}

func cohortCounts(dataset Dataset) map[string]int {
	result := map[string]int{}
	add := func(tags []string) {
		seen := map[string]struct{}{}
		for _, tag := range tags {
			tag = normalizeLabel(tag)
			if _, exists := seen[tag]; !exists {
				result[tag]++
				seen[tag] = struct{}{}
			}
		}
	}
	for _, item := range dataset.IntentCases {
		add(item.Tags)
	}
	for _, item := range dataset.ResolutionCases {
		add(item.Tags)
	}
	for _, item := range dataset.RecommendationCases {
		add(item.Tags)
	}
	return result
}
func contains(values []string, want string) bool {
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
func refsForIDs(cat ports.Catalog, ids []string) []core.TrackRef {
	out := []core.TrackRef{}
	for _, id := range ids {
		if meta, ok := cat.Meta(id); ok {
			out = append(out, meta.Ref)
		}
	}
	return out
}

func intentTracks(cat ports.Catalog, references []core.IntentReference) []core.TrackRef {
	ids := make([]string, 0, len(references))
	for _, reference := range references {
		id := reference.TrackID
		if reference.Resolution != nil && reference.Resolution.Selected != nil && len(reference.Resolution.Selected.Representatives) > 0 {
			id = reference.Resolution.Selected.Representatives[0].TrackID
		}
		if id != "" {
			ids = append(ids, id)
		}
	}
	return refsForIDs(cat, ids)
}
func algorithmVersion(engine ports.RecommendationEngine) string {
	if v, ok := engine.(ports.VersionedRecommendationEngine); ok {
		return v.AlgorithmVersion()
	}
	return "unknown"
}
func seedInt64(seed core.RNGSeed) int64 {
	value, err := seed.Int64()
	if err != nil {
		return 1
	}
	return value
}

func fingerprintJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
