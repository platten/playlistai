package multichannel

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/resolution"
)

// Orchestrator owns the versioned retrieve -> eligibility -> rank -> select ->
// sequence pipeline while preserving the complete resolved intent.
type Orchestrator struct {
	bestAvailable   bool
	candidateSource ports.MusicCandidateSource
	knowledge       *core.KnowledgeSnapshot
	anchorProposer  func(context.Context, core.MusicIntent, []string) ([]core.InferredAnchor, error)
	audioProvider   func() *audio.Service
	audioSession    *audio.Session // set only on the request-local orchestrator copy
	cat             ports.Catalog
	resolver        ports.ReferenceResolver
	retriever       ports.CandidateRetriever
	ranker          ports.Ranker
	selector        ports.CandidateSelector
	sequencer       ports.PlaylistSequencer
	features        ports.FeatureStore
	semantic        ports.SemanticSearcher
	scorer          ports.SemanticScorer
	cfg             Config
}

func (o *Orchestrator) WithCandidateSource(source ports.MusicCandidateSource) *Orchestrator {
	o.candidateSource = source
	return o
}

func (o *Orchestrator) WithAnchorProposer(propose func(context.Context, core.MusicIntent, []string) ([]core.InferredAnchor, error)) *Orchestrator {
	o.anchorProposer = propose
	return o
}

// WithAudioProvider is startup wiring; the provider snapshots optional model
// state under the container lock when each generation begins.
func (o *Orchestrator) WithAudioProvider(provider func() *audio.Service) *Orchestrator {
	o.audioProvider = provider
	return o
}

func NewWithSemantic(cat ports.Catalog, sim ports.SimilarityEngine, resolver ports.ReferenceResolver, features ports.FeatureStore, semantic ports.SemanticSearcher, cfg Config) *Orchestrator {
	cfg = cfg.normalized()
	scorer, _ := semantic.(ports.SemanticScorer)
	return &Orchestrator{
		cat: cat, resolver: resolver, features: features, semantic: semantic, scorer: scorer, cfg: cfg,
		retriever: NewSemanticRetriever(cat, sim, semantic, cfg), ranker: NewRanker(cat, cfg),
		selector: NewSelector(cat, cfg), sequencer: NewSequencer(cat, cfg),
	}
}

func New(cat ports.Catalog, sim ports.SimilarityEngine, resolver ports.ReferenceResolver, cfg Config) *Orchestrator {
	cfg = cfg.normalized()
	return &Orchestrator{
		cat: cat, resolver: resolver, cfg: cfg,
		retriever: NewRetriever(cat, sim, cfg), ranker: NewRanker(cat, cfg),
		selector: NewSelector(cat, cfg), sequencer: NewSequencer(cat, cfg),
	}
}

func (o *Orchestrator) AlgorithmVersion() string {
	version := AlgorithmVersion
	if o.candidateSource != nil {
		version += "+iterative/v1"
	}
	if o.semantic == nil && o.features == nil {
		return version
	}
	var info core.FeatureStoreInfo
	if o.features != nil {
		info = o.features.Info()
	}
	if o.semantic != nil {
		info = o.semantic.Info()
	}
	return fmt.Sprintf("%s+semantic:s%d:%s@%s:%s", version, info.SchemaVersion, info.FeatureVersion, info.ModelRevision, info.QueryEncoder)
}

type essentialEvidenceReport struct {
	Eligible    map[string]bool
	Matched     map[string]int
	Scores      map[string]float64
	Sources     map[string][]core.FeatureProvenance
	Unsupported []core.OutcomeReason
}

func explicitResolutionReasons(issues []resolution.Issue) []core.OutcomeReason {
	var reasons []core.OutcomeReason
	for _, issue := range issues {
		if issue.Inferred || issue.Influence == core.InfluenceNegative {
			continue
		}
		switch issue.Status {
		case core.ResolutionAmbiguous:
			reasons = append(reasons, core.OutcomeReason{Code: "ambiguous_reference", Detail: fmt.Sprintf("%q matches more than one catalog entity", issue.Query), Criterion: issue.Query, Action: "choose the intended artist or track from the alternatives"})
		case core.ResolutionUnresolved:
			reasons = append(reasons, core.OutcomeReason{Code: "unresolved_reference", Detail: fmt.Sprintf("%q was not found in the catalog", issue.Query), Criterion: issue.Query, Action: "check the spelling or choose a catalog result"})
		}
	}
	return reasons
}

func outcomePlaylist(intent core.MusicIntent, seed core.RNGSeed, state core.GenerationOutcomeState, reasons []core.OutcomeReason) core.Playlist {
	return core.Playlist{Mode: intent.Mode, Seed: seed, Intent: intent, Outcome: core.GenerationOutcome{State: state, Reasons: reasons}}
}

func essentialSummary(criteria []core.MusicalCriterion) string {
	values := make([]string, 0, len(criteria))
	for _, criterion := range criteria {
		values = appendUniqueString(values, criterion.Value)
	}
	return strings.Join(values, ", ")
}

func (o *Orchestrator) unsupportedStrictReasons(intent core.MusicIntent) []core.OutcomeReason {
	var active []core.HardConstraint
	if o.features != nil {
		active = activeSemanticConstraints(o.features.Info(), intent.HardConstraints)
	}
	activeKey := map[string]bool{}
	for _, constraint := range active {
		activeKey[constraint.Kind+"\x00"+constraint.Value] = true
	}
	var reasons []core.OutcomeReason
	for _, constraint := range intent.HardConstraints {
		if constraint.Kind == "require_album" {
			resolved := false
			for _, ref := range intent.References {
				resolved = resolved || ref.Kind == core.ReferenceAlbum && strings.EqualFold(ref.Query, constraint.Value) && ref.Resolution != nil && ref.Resolution.Status == core.ResolutionResolved
			}
			if resolved {
				continue
			}
		}
		if o.audioSession != nil && o.audioSession.SupportsConstraint(constraint.Kind) {
			continue
		}
		if core.HardConstraintSupported(constraint.Kind) || activeKey[constraint.Kind+"\x00"+constraint.Value] {
			continue
		}
		reasons = append(reasons, core.OutcomeReason{
			Code: "unsupported_hard_constraint", Detail: "the loaded catalog evidence cannot enforce this strict requirement",
			Criterion: constraint.Kind + ": " + constraint.Value, Action: "install a compatible feature sidecar or relax the requirement",
		})
	}
	return reasons
}

func (o *Orchestrator) unsupportedEssentialReasons(intent core.MusicIntent) []core.OutcomeReason {
	if o.audioSession != nil {
		return nil
	}
	var reasons []core.OutcomeReason
	for _, criterion := range intent.EssentialCriteria {
		supported := o.scorer != nil
		if o.features != nil && criterionSupported(o.features.Info(), criterion) {
			supported = true
		}
		if !supported {
			reasons = append(reasons, core.OutcomeReason{
				Code: "unsupported_essential_criterion", Detail: "no loaded musical evidence can verify the defining request category",
				Criterion: criterion.Value, Action: "choose a fitting reference track or install a compatible semantic sidecar",
			})
		}
	}
	return reasons
}

func (o *Orchestrator) uncoveredEssentialReasons(ctx context.Context, intent core.MusicIntent) ([]core.OutcomeReason, error) {
	if o.audioSession != nil {
		return nil, nil
	}
	var reasons []core.OutcomeReason
	for _, criterion := range intent.EssentialCriteria {
		if o.features != nil && criterionSupported(o.features.Info(), criterion) {
			continue
		}
		if o.scorer == nil {
			continue
		}
		coverage, _, err := o.scorer.Score(ctx, criterion.Value, nil)
		if err != nil {
			if !errors.Is(err, core.ErrUnavailable) {
				return nil, err
			}
			reasons = append(reasons, core.OutcomeReason{Code: "essential_query_uncovered", Detail: "the query encoder does not support the defining concept", Criterion: criterion.Value, Action: "choose a fitting reference track or extend the semantic sidecar vocabulary"})
			continue
		}
		if !coverage.Complete {
			reasons = append(reasons, core.OutcomeReason{Code: "essential_query_uncovered", Detail: "the query encoder dropped part of the defining concept", Criterion: criterion.Value, Action: "choose a fitting reference track or extend the semantic sidecar vocabulary"})
		}
	}
	return reasons, nil
}

func intentCriterionConflictReasons(intent core.MusicIntent) []core.OutcomeReason {
	var reasons []core.OutcomeReason
	for _, criterion := range intent.EssentialCriteria {
		if criterion.Kind != "style" {
			continue
		}
		for _, constraint := range intent.HardConstraints {
			if constraint.Kind == "exclude_style" && styleMatches(canonicalStyle(constraint.Value), canonicalStyle(criterion.Value)) {
				reasons = append(reasons, core.OutcomeReason{
					Code: "essential_exclusion_conflict", Detail: "the same defining musical category is also excluded",
					Criterion: criterion.Value, Action: "remove the exclusion or make the intended cross-genre exception explicit",
				})
			}
		}
	}
	return reasons
}

func (o *Orchestrator) assessInferredAnchors(ctx context.Context, intent core.MusicIntent) (core.MusicIntent, []core.PlaylistNotice, error) {
	local := *o
	local.bestAvailable = false
	o = &local
	var notices []core.PlaylistNotice
	for index := range intent.InferredAnchors {
		anchor := &intent.InferredAnchors[index]
		resolution := anchor.Reference.Resolution
		if resolution == nil || resolution.Status != core.ResolutionResolved || resolution.Selected == nil {
			anchor.Suitability = core.AnchorSuitability{State: core.EvidenceUnknown, Detail: "the proposed anchor did not resolve unambiguously in the catalog"}
			notices = append(notices, core.PlaylistNotice{Code: "inferred_anchor_rejected", Detail: fmt.Sprintf("Inferred anchor %q was not used because it did not resolve unambiguously", anchor.Reference.Query)})
			continue
		}
		if len(intent.EssentialCriteria) == 0 && o.audioSession == nil {
			anchor.Suitability = core.AnchorSuitability{State: core.EvidenceMatch, Detail: "resolved retrieval proposal; no essential musical criterion required validation"}
			continue
		}
		tracks := make([]core.TrackRef, 0, len(resolution.Selected.Representatives))
		for _, representative := range resolution.Selected.Representatives {
			if meta, ok := o.cat.Meta(representative.TrackID); ok {
				tracks = append(tracks, meta.Ref)
			}
		}
		if o.audioSession != nil {
			// One actual recording per proposal keeps the six-proposal bound
			// independent of how many medoids an artist resolver supplies.
			if len(tracks) > 1 {
				tracks = tracks[:1]
			}
			var kept []core.WeightedTrack
			for _, track := range tracks {
				assessment, err := o.audioSession.Check(ctx, track, true)
				if err != nil {
					return intent, nil, err
				}
				if assessment.Eligible {
					kept = append(kept, core.WeightedTrack{TrackID: track.ID, Weight: 1})
				}
			}
			anchor.Suitability = core.AnchorSuitability{State: core.EvidenceUnknown, Detail: "No verified preview established musical fit for this proposed recording."}
			if len(kept) > 0 {
				anchor.Reference.Resolution.Selected.Representatives = kept
				anchor.Reference.TrackID = kept[0].TrackID
				anchor.Suitability = core.AnchorSuitability{State: core.EvidenceMatch, Detail: "This representative recording passed the description clauses using preview-scoped audio evidence."}
				if !o.audioSession.Calibrated() {
					anchor.Suitability = core.AnchorSuitability{State: core.EvidenceUnknown, Detail: "Preview similarity is available for ranking; uncalibrated scores do not establish categorical musical fit."}
				}
			}
			continue
		}
		_, report, err := o.filterEssential(ctx, candidatesForTracks(tracks), intent.EssentialCriteria)
		if err != nil {
			return intent, nil, err
		}
		if len(report.Unsupported) > 0 {
			anchor.Suitability = core.AnchorSuitability{State: core.EvidenceUnsupported, Detail: "the loaded evidence cannot validate this proposal against the essential criterion"}
			continue
		}
		var kept []core.WeightedTrack
		for _, representative := range resolution.Selected.Representatives {
			if report.Eligible[representative.TrackID] {
				kept = append(kept, representative)
			}
		}
		if len(kept) == 0 {
			anchor.Suitability = core.AnchorSuitability{State: core.EvidenceMismatch, Detail: "none of the representative catalog tracks had affirmative evidence for the essential criterion"}
			notices = append(notices, core.PlaylistNotice{Code: "inferred_anchor_rejected", Detail: fmt.Sprintf("Inferred anchor %q was not used because its representative tracks did not fit %s", anchor.Reference.Query, essentialSummary(intent.EssentialCriteria))})
			continue
		}
		var total, evidenceScore float64
		var sources []core.FeatureProvenance
		for _, representative := range kept {
			total += representative.Weight
			evidenceScore += report.Scores[representative.TrackID]
			sources = appendUniqueProvenance(sources, report.Sources[representative.TrackID]...)
		}
		for i := range kept {
			if total > 0 {
				kept[i].Weight /= total
			} else {
				kept[i].Weight = 1 / float64(len(kept))
			}
		}
		anchor.Reference.Resolution.Selected.Representatives = kept
		anchor.Reference.TrackID = kept[0].TrackID
		anchor.Suitability = core.AnchorSuitability{
			State: core.EvidenceMatch, Score: evidenceScore / float64(len(kept)), Source: sources,
			Detail: fmt.Sprintf("%d request-specific representative track(s) have affirmative evidence for %s", len(kept), essentialSummary(intent.EssentialCriteria)),
		}
	}
	return intent.Normalized(), notices, nil
}

func (o *Orchestrator) scoreSemanticUnion(ctx context.Context, candidates []core.Candidate, intent core.MusicIntent) ([]core.Candidate, core.QueryCoverage, []core.PlaylistNotice, error) {
	if o.audioSession != nil {
		return candidates, core.QueryCoverage{Complete: true}, nil, nil
	}
	positive, negative := semanticQueryText(intent)
	if o.scorer == nil || len(candidates) == 0 {
		var notices []core.PlaylistNotice
		if positive != "" {
			notices = append(notices, core.PlaylistNotice{Code: "semantic_scoring_unavailable", Detail: "the request was preserved, but the loaded sidecar cannot score the complete candidate union"})
		}
		return candidates, core.QueryCoverage{}, notices, nil
	}
	ids := candidateTrackIDs(candidates)
	result := append([]core.Candidate(nil), candidates...)
	byID := make(map[string]int, len(result))
	for index := range result {
		byID[result[index].Track.ID] = index
	}
	var coverage core.QueryCoverage
	var notices []core.PlaylistNotice
	apply := func(text string, negative bool) error {
		if text == "" {
			return nil
		}
		queryCoverage, scores, err := o.scorer.Score(ctx, text, ids)
		if !negative {
			coverage = queryCoverage
		}
		if err != nil {
			if errors.Is(err, core.ErrUnavailable) {
				notices = append(notices, core.PlaylistNotice{Code: "semantic_query_unsupported", Detail: err.Error()})
				return nil
			}
			return err
		}
		if !queryCoverage.Complete {
			notices = append(notices, core.PlaylistNotice{Code: "semantic_query_partial", Detail: "semantic scoring preserved matched concepts and reported unmatched vocabulary"})
		}
		for _, score := range scores {
			index, ok := byID[score.TrackID]
			if !ok || score.State == core.EvidenceUnknown || score.State == core.EvidenceUnsupported {
				continue
			}
			if negative {
				result[index].Scores.SemanticNegativeMatch = score.Score
				result[index].Available.SemanticNegativeMatch = true
			} else {
				result[index].Scores.SemanticMatch = score.Score
				result[index].Available.SemanticMatch = true
			}
		}
		return nil
	}
	if err := apply(positive, false); err != nil {
		return nil, coverage, nil, err
	}
	if err := apply(negative, true); err != nil {
		return nil, coverage, nil, err
	}
	return result, coverage, notices, nil
}

func (o *Orchestrator) filterEssential(ctx context.Context, candidates []core.Candidate, criteria []core.MusicalCriterion) ([]core.Candidate, essentialEvidenceReport, error) {
	if o.bestAvailable {
		return o.bestEssential(ctx, candidates, criteria)
	}
	report := essentialEvidenceReport{Eligible: map[string]bool{}, Matched: map[string]int{}, Scores: map[string]float64{}, Sources: map[string][]core.FeatureProvenance{}}
	if len(criteria) == 0 {
		for _, candidate := range candidates {
			report.Eligible[candidate.Track.ID] = true
		}
		return candidates, report, nil
	}
	ids := candidateTrackIDs(candidates)
	states := make([]map[string]core.EvidenceState, len(criteria))
	for criterionIndex, criterion := range criteria {
		states[criterionIndex] = map[string]core.EvidenceState{}
		if o.audioSession != nil {
			for _, id := range ids {
				states[criterionIndex][id] = o.audioSession.Criterion(id, criterion)
			}
		} else if o.features != nil && criterionSupported(o.features.Info(), criterion) {
			for _, id := range ids {
				features, ok, err := o.features.Features(ctx, id)
				if err != nil {
					return nil, report, err
				}
				if ok {
					state := criterionState(features, criterion)
					states[criterionIndex][id] = state
					if state == core.EvidenceMatch {
						report.Scores[id] = maxFloat(report.Scores[id], criterionConfidence(features, criterion))
						report.Sources[id] = appendUniqueProvenance(report.Sources[id], criterionProvenance(features, criterion)...)
					}
				} else {
					states[criterionIndex][id] = core.EvidenceUnknown
				}
			}
		} else if o.scorer != nil {
			coverage, scores, err := o.scorer.Score(ctx, criterion.Value, ids)
			if err != nil || !coverage.Complete {
				report.Unsupported = append(report.Unsupported, core.OutcomeReason{Code: "essential_query_uncovered", Detail: "the query encoder could not preserve the defining concept", Criterion: criterion.Value, Action: "choose a fitting reference track or extend the semantic sidecar vocabulary"})
				continue
			}
			for _, score := range scores {
				state := score.State
				if state != core.EvidenceUnknown && score.Score >= o.cfg.SemanticMinimumScore {
					state = core.EvidenceMatch
				} else if state != core.EvidenceUnknown {
					state = core.EvidenceMismatch
				}
				states[criterionIndex][score.TrackID] = state
				if state == core.EvidenceMatch {
					report.Scores[score.TrackID] = maxFloat(report.Scores[score.TrackID], score.Score)
					info := o.semantic.Info()
					report.Sources[score.TrackID] = appendUniqueProvenance(report.Sources[score.TrackID], core.FeatureProvenance{
						Source: "semantic-sidecar", SourceID: score.TrackID, SourceVersion: info.FeatureVersion,
						ModelVersion: strings.Trim(info.TextModel+"@"+info.ModelRevision, "@"),
					})
				}
			}
		} else {
			report.Unsupported = append(report.Unsupported, core.OutcomeReason{Code: "unsupported_essential_criterion", Detail: "no loaded evidence source supports this criterion", Criterion: criterion.Value, Action: "install a compatible sidecar or choose a fitting reference"})
		}
	}
	if len(report.Unsupported) > 0 {
		return nil, report, nil
	}
	result := make([]core.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		playlistOK := true
		journeyCount, journeyMatches := 0, 0
		for index, criterion := range criteria {
			state := states[index][candidate.Track.ID]
			if state == core.EvidenceMatch {
				report.Matched[criterionKey(criterion)]++
			}
			if strings.HasPrefix(criterion.Scope, "journey_") {
				journeyCount++
				if state == core.EvidenceMatch {
					journeyMatches++
				}
			} else if state != core.EvidenceMatch {
				playlistOK = false
			}
		}
		if playlistOK && (journeyCount == 0 || journeyMatches > 0) {
			result = append(result, candidate)
			report.Eligible[candidate.Track.ID] = true
		}
	}
	return result, report, nil
}

func appendUniqueProvenance(out []core.FeatureProvenance, values ...core.FeatureProvenance) []core.FeatureProvenance {
	for _, value := range values {
		duplicate := false
		for _, existing := range out {
			if existing.Source == value.Source && existing.SourceID == value.SourceID && existing.SourceVersion == value.SourceVersion && existing.ModelVersion == value.ModelVersion {
				duplicate = true
				break
			}
		}
		if !duplicate {
			out = append(out, value)
		}
	}
	return out
}

func candidateTrackIDs(candidates []core.Candidate) []string {
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.Track.ID)
	}
	return ids
}

func candidatesForTracks(tracks []core.TrackRef) []core.Candidate {
	result := make([]core.Candidate, 0, len(tracks))
	for _, track := range tracks {
		result = append(result, core.Candidate{Track: track})
	}
	return result
}

func requiredTracksEligible(tracks []core.TrackRef, eligible map[string]bool) bool {
	for _, track := range tracks {
		if !eligible[track.ID] {
			return false
		}
	}
	return true
}

func (o *Orchestrator) Build(ctx context.Context, intent core.MusicIntent) (core.Playlist, error) {
	return o.BuildRecommendation(ctx, ports.RecommendationRequest{Intent: intent})
}

func (o *Orchestrator) BuildWithProfile(ctx context.Context, intent core.MusicIntent, profile core.TasteProfile) (core.Playlist, error) {
	return o.BuildRecommendation(ctx, ports.RecommendationRequest{Intent: intent, Profile: profile})
}

func (o *Orchestrator) BuildRecommendation(ctx context.Context, request ports.RecommendationRequest) (result core.Playlist, buildErr error) {
	local := *o
	o = &local
	if err := ctx.Err(); err != nil {
		return core.Playlist{}, err
	}
	intent := request.Intent.Normalized()
	o.bestAvailable = intent.VerificationPolicy == core.BestAvailable
	o.knowledge = intent.Knowledge
	var resolutionIssues []resolution.Issue
	if o.resolver != nil {
		intent, resolutionIssues = resolution.Apply(o.resolver, intent)
	}
	if err := intent.Validate(); err != nil {
		return core.Playlist{}, err
	}
	intent = intent.Normalized()
	seed := intent.Seed
	if seed.IsZero() {
		seed = randomSeed()
	}
	seedValue, err := seed.Int64()
	if err != nil {
		return core.Playlist{}, fmt.Errorf("seed: %w", err)
	}
	intent.Seed = seed
	var discovery ports.MusicCandidateStream
	if o.candidateSource != nil {
		discovery = o.candidateSource.OpenCandidates(intent, o.cat, o.resolver)
		if discovery != nil {
			o.knowledge = discovery.Snapshot()
			intent.Knowledge = o.knowledge
			defer func() { result.Intent.Knowledge = discovery.Snapshot() }()
		}
	}

	if reasons := explicitResolutionReasons(resolutionIssues); len(reasons) > 0 {
		return outcomePlaylist(intent, seed, core.OutcomeNeedsClarification, reasons), nil
	}
	if reasons := intentCriterionConflictReasons(intent); len(reasons) > 0 {
		return outcomePlaylist(intent, seed, core.OutcomeNeedsClarification, reasons), nil
	}
	if o.audioProvider != nil && len(audio.Clauses(intent)) > 0 {
		if service := o.audioProvider(); service.ReadyFor(intent) {
			catalogVersion := "unknown"
			if o.resolver != nil {
				catalogVersion = o.resolver.CatalogVersion()
			}
			budget := audio.AnalysisBudget
			if o.candidateSource != nil {
				budget = iterativeBudget
			}
			o.audioSession, err = service.BeginWithBudget(ctx, intent, catalogVersion, request.StopChecking, budget)
			if err != nil {
				return core.Playlist{}, err
			}
			defer o.audioSession.Close()
			defer func() {
				snapshot := o.audioSession.Snapshot()
				result.AudioEvidence = &snapshot
				if !service.Policy.Valid() && len(result.Tracks) > 0 {
					result.Notices = append(result.Notices, core.PlaylistNotice{Code: "audio_similarity_ranking", Detail: "CLAP compared verified previews with your musical descriptions to help rank tracks. Similarities are not calibrated musical-fit judgments; unheard parts of each recording remain unassessed."})
				}
				if core.WantsInstrumental(intent) && len(result.Tracks) > 0 {
					result.Notices = append(result.Notices, core.PlaylistNotice{Code: "vocal_preview_screening", Detail: "CLAP screened every selected preview for vocals. Vocal or uncertain previews were excluded; unheard parts of each recording remain unassessed."})
				}
				if snapshot.Stopped || snapshot.BudgetExhausted {
					if result.Outcome.State == core.OutcomeFulfilled {
						result.Outcome.State = core.OutcomePartial
					}
					result.Outcome.Reasons = append(result.Outcome.Reasons, core.OutcomeReason{Code: "analysis_stopped", Detail: "Analysis stopped; only checked tracks were retained.", Action: "continue with these tracks or refine the description"})
				}
			}()
		}
	}
	if core.WantsInstrumental(intent) && o.audioSession == nil {
		return outcomePlaylist(intent, seed, core.OutcomeUnsupported, []core.OutcomeReason{{Code: "vocal_analysis_unavailable", Detail: "This request needs CLAP preview screening to exclude vocals, but the local analysis model is not ready.", Action: "download and validate the CLAP model in Settings, then generate again"}}), nil
	}
	if request.Progress != nil {
		request.Progress.Report("generation", 0, 0, "Finding starting points")
	}
	intent, anchorNotices, err := o.assessInferredAnchors(ctx, intent)
	if err != nil {
		return core.Playlist{}, err
	}
	if (o.audioSession != nil || o.bestAvailable) && o.anchorProposer != nil && len(intent.AnchorAttempts) == 0 {
		var kept []core.InferredAnchor
		var rejected []string
		for _, anchor := range intent.InferredAnchors {
			resolved := anchor.Reference.Resolution != nil && anchor.Reference.Resolution.Status == core.ResolutionResolved
			if anchor.Suitability.State == core.EvidenceMatch || o.bestAvailable && resolved && anchor.Suitability.State != core.EvidenceMismatch {
				kept = append(kept, anchor)
			} else {
				rejected = append(rejected, anchor.Reference.Query)
			}
		}
		intent.AnchorAttempts = append([]core.InferredAnchor(nil), intent.InferredAnchors...)
		if len(rejected) > 0 || len(intent.InferredAnchors) == 0 {
			proposals, proposalErr := o.anchorProposer(ctx, intent, rejected)
			if proposalErr == nil {
				if len(proposals) > 3-len(kept) {
					proposals = proposals[:3-len(kept)]
				}
				replacement := intent
				replacement.InferredAnchors = proposals
				if o.resolver != nil {
					replacement, _ = resolution.Apply(o.resolver, replacement)
				}
				replacement, _, err = o.assessInferredAnchors(ctx, replacement)
				if err != nil {
					return core.Playlist{}, err
				}
				intent.AnchorAttempts = append(intent.AnchorAttempts, replacement.InferredAnchors...)
				intent.InferredAnchors = append(append([]core.InferredAnchor(nil), kept...), replacement.InferredAnchors...)
				intent = intent.Normalized()
			}
		}
	}
	if reasons := o.unsupportedStrictReasons(intent); len(reasons) > 0 {
		playlist := outcomePlaylist(intent, seed, core.OutcomeUnsupported, reasons)
		playlist.Notices = append(playlist.Notices, anchorNotices...)
		return playlist, nil
	}
	if reasons := o.unsupportedEssentialReasons(intent); len(reasons) > 0 && !o.bestAvailable {
		playlist := outcomePlaylist(intent, seed, core.OutcomeUnsupported, reasons)
		playlist.Notices = append(playlist.Notices, anchorNotices...)
		return playlist, nil
	}
	if reasons, coverageErr := o.uncoveredEssentialReasons(ctx, intent); coverageErr != nil {
		return core.Playlist{}, coverageErr
	} else if len(reasons) > 0 && !o.bestAvailable {
		playlist := outcomePlaylist(intent, seed, core.OutcomeUnsupported, reasons)
		playlist.Notices = append(playlist.Notices, anchorNotices...)
		return playlist, nil
	}

	references := resolvedReferenceTracks(o.cat, intent)
	required := resolvedRequiredTracks(o.cat, intent.RequiredTracks)
	waypoints := journeyAnchors(o.cat, intent)
	if intent.Destination != nil {
		d := *intent.Destination
		if d.Resolution == nil && o.resolver != nil {
			r := o.resolver.ResolveReference(d)
			d.Resolution = &r
		}
		destinations := resolvedRequiredTracks(o.cat, []core.IntentReference{d})
		if len(destinations) == 0 {
			return outcomePlaylist(intent, seed, core.OutcomeNeedsClarification, []core.OutcomeReason{{Code: "destination_unresolved", Detail: "The final artist, album or track could not be resolved.", Action: "choose a catalog destination"}}), nil
		}
		end := destinations[0]
		if len(required) == 0 {
			for _, ref := range references {
				if ref.ID != end.ID && ref.Artist != end.Artist {
					required = append(required, ref)
					break
				}
			}
		}
		if len(required) == 0 && o.knowledge != nil {
			for _, ref := range o.knowledge.Candidates {
				if ref.ID != end.ID && ref.Artist != end.Artist {
					required = append(required, ref)
					break
				}
			}
		}
		found := false
		for _, ref := range required {
			if ref.ID == end.ID {
				found = true
			}
		}
		if !found {
			required = append(required, end)
		}
		waypoints = append([]core.TrackRef(nil), required...)
	}
	if intent.Mode == core.ModeJourney {
		required = orderRequiredByWaypoints(required, waypoints)
	}
	recentSelections := resolvedContextTracks(o.cat, request.RecentSelections)
	positiveSemantic, _ := semanticQueryText(intent)
	semanticSeeded := discovery != nil || positiveSemantic != "" && o.semantic != nil || o.knowledge != nil && len(o.knowledge.Candidates) > 0
	if len(references) == 0 && len(required) == 0 && !semanticSeeded {
		if core.WantsInstrumental(intent) {
			return outcomePlaylist(intent, seed, core.OutcomeNeedsClarification, []core.OutcomeReason{{Code: "instrumental_seed_unavailable", Detail: "No suitable instrumental starting point could be found in the catalog or online lookup.", Action: "retry the search or name an instrumental artist or recording"}}), nil
		}
		if len(intent.EssentialCriteria) > 0 || len(intent.InferredAnchors) > 0 {
			reasons := []core.OutcomeReason{{
				Code: "no_suitable_anchor", Detail: "no inferred anchor had affirmative musical evidence and semantic retrieval is unavailable",
				Criterion: essentialSummary(intent.EssentialCriteria), Action: "choose a known artist or track that fits the request, or install a compatible semantic sidecar",
			}}
			playlist := outcomePlaylist(intent, seed, core.OutcomeUnsupported, reasons)
			playlist.Notices = append(playlist.Notices, anchorNotices...)
			return playlist, nil
		}
		if positiveSemantic != "" {
			return core.Playlist{}, fmt.Errorf("%w: semantic sidecar or compatible local encoder unavailable", core.ErrNoSeeds)
		}
		return core.Playlist{}, core.ErrNoSeeds
	}
	if len(intent.RequiredTracks) > 0 && len(required) == 0 {
		return core.Playlist{}, fmt.Errorf("%w: none of the required tracks resolved", core.ErrRequiredTrackConflict)
	}
	if intent.Count < len(required) {
		return core.Playlist{}, fmt.Errorf("%w: requested %d tracks but %d are required", core.ErrCountBelowRequired, intent.Count, len(required))
	}
	if stages := journeyCriteria(intent.EssentialCriteria); intent.Mode == core.ModeJourney && intent.Count < len(stages) {
		return outcomePlaylist(intent, seed, core.OutcomeNeedsClarification, []core.OutcomeReason{{Code: "journey_count_too_short", Detail: "The requested count cannot represent every journey stage.", Action: "increase the track count or remove a stage"}}), nil
	}

	eligible := newEligibility(intent, references, required)
	eligible.excludeRecent(recentSelections)
	if err := eligible.validateRequired(required, intent.Constraints.ExcludeSeedArtists); err != nil {
		return core.Playlist{}, err
	}
	for _, track := range required {
		if !o.metadataEligible(track, intent) {
			return outcomePlaylist(intent, seed, core.OutcomeNeedsClarification, []core.OutcomeReason{{Code: "required_metadata_conflict", Detail: "A required recording conflicts with metadata constraints."}}), nil
		}
	}
	if o.audioSession != nil {
		for _, track := range required {
			assessment, err := o.audioSession.Check(ctx, track, false)
			if err != nil {
				return core.Playlist{}, err
			}
			if !assessment.Eligible {
				return outcomePlaylist(intent, seed, core.OutcomeNeedsClarification, []core.OutcomeReason{{Code: "required_track_audio_conflict", Detail: "A required track lacks preview evidence for the description and exclusions.", Criterion: track.Display(), Action: "remove the required track or refine the conflicting requirement"}}), nil
			}
		}
	}
	var candidates []core.Candidate
	if o.candidateSource == nil {
		candidates, err = o.retriever.Retrieve(ctx, ports.RetrievalRequest{
			Intent: intent, Profile: request.Profile, RecentSelections: recentSelections, Seed: seedValue,
		})
		if err != nil {
			return core.Playlist{}, err
		}
	}
	semanticNotices := append([]core.PlaylistNotice(nil), anchorNotices...)
	var positiveCoverage core.QueryCoverage
	candidates, positiveCoverage, scoreNotices, err := o.scoreSemanticUnion(ctx, candidates, intent)
	if err != nil {
		return core.Playlist{}, err
	}
	semanticNotices = append(semanticNotices, scoreNotices...)
	semanticMatched := hasSemanticCandidates(candidates)
	if o.candidateSource == nil && discovery == nil && len(references) == 0 && len(required) == 0 && len(candidates) == 0 {
		return core.Playlist{}, fmt.Errorf("%w: semantic index produced no grounded candidates", core.ErrNoSeeds)
	}
	metadataCandidates := candidates[:0]
	for _, candidate := range candidates {
		if o.metadataEligible(candidate.Track, intent) {
			metadataCandidates = append(metadataCandidates, candidate)
		}
	}
	candidates = metadataCandidates
	candidates, err = eligible.filter(ctx, candidates, intent.Constraints.ExcludeSeedArtists)
	if err != nil {
		return core.Playlist{}, err
	}
	// Apply all independent strict metadata checks before progressively showing
	// audio-eligible candidates. Ranking and ordering may still select a subset.
	if o.features != nil {
		var enforced int
		candidates, enforced, err = filterSemanticConstraints(ctx, o.features, candidates, intent.HardConstraints)
		if err != nil {
			return core.Playlist{}, err
		}
		if enforced > 0 {
			markSemanticConstraintsEnforced(&intent, o.features.Info())
			semanticNotices = append(semanticNotices, core.PlaylistNotice{Code: "semantic_constraints_enforced", Detail: fmt.Sprintf("%d grounded semantic hard constraint(s) were enforced; unknown evidence was ineligible", enforced), Requested: intent.Count, Actual: len(candidates)})
		}
		if err := validateRequiredSemanticConstraints(ctx, o.features, required, intent.HardConstraints, nil); err != nil {
			return outcomePlaylist(intent, seed, core.OutcomeNeedsClarification, []core.OutcomeReason{{Code: "required_track_constraint_conflict", Detail: err.Error(), Action: "remove the required track or relax the conflicting exclusion"}}), nil
		}
	}
	if o.candidateSource != nil {
		var notices []core.PlaylistNotice
		candidates, notices, err = o.collectIteratively(ctx, candidates, discovery, intent, request, eligible, references, required, waypoints, seedValue)
		if err != nil {
			return core.Playlist{}, err
		}
		semanticNotices = append(semanticNotices, notices...)
		semanticMatched = hasSemanticCandidates(candidates)
	} else if o.audioSession != nil {
		checked := make([]core.Candidate, 0, len(candidates))
		stages := journeyStageCriteria(intent)
		for i, candidate := range candidates {
			if request.Progress != nil {
				request.Progress.Report("generation", int64(i), int64(len(candidates)), "Checking musical fit")
			}
			assessment, err := o.audioSession.Check(ctx, candidate.Track, false)
			if err != nil {
				return core.Playlist{}, err
			}
			if !assessment.Eligible {
				continue
			}
			if len(intent.EssentialCriteria) > 0 {
				fit, _, err := o.filterEssential(ctx, []core.Candidate{candidate}, intent.EssentialCriteria)
				if err != nil {
					return core.Playlist{}, err
				}
				if len(fit) == 0 {
					continue
				}
			}
			if intent.Mode == core.ModeJourney && len(stages) > 0 {
				placeable := false
				for _, stage := range stages {
					fit, _, err := o.filterJourneyStage(ctx, []core.Candidate{candidate}, stage, intent)
					if err != nil {
						return core.Playlist{}, err
					}
					placeable = placeable || len(fit) > 0
				}
				if !placeable {
					continue
				}
			}
			audio.ApplyScores(&candidate, assessment)
			checked = append(checked, candidate)
			if request.OnChecked != nil {
				request.OnChecked(candidate.Track)
			}
		}
		candidates = checked
		semanticMatched = hasSemanticCandidates(candidates)
	}
	if len(intent.EssentialCriteria) > 0 {
		var report essentialEvidenceReport
		candidates, report, err = o.filterEssential(ctx, candidates, intent.EssentialCriteria)
		if err != nil {
			return core.Playlist{}, err
		}
		if !o.bestAvailable && (len(report.Unsupported) > 0 || (!positiveCoverage.Complete && o.features == nil && o.audioSession == nil)) {
			reasons := report.Unsupported
			if len(reasons) == 0 {
				reasons = []core.OutcomeReason{{Code: "essential_query_uncovered", Detail: "the semantic query encoder did not preserve every defining concept", Criterion: essentialSummary(intent.EssentialCriteria), Action: "choose a fitting reference track or install a sidecar whose vocabulary covers the requested category"}}
			}
			playlist := outcomePlaylist(intent, seed, core.OutcomeUnsupported, reasons)
			playlist.Notices = append(playlist.Notices, semanticNotices...)
			return playlist, nil
		}
		_, requiredReport, requiredErr := o.filterEssential(ctx, candidatesForTracks(required), intent.EssentialCriteria)
		if requiredErr != nil {
			return core.Playlist{}, requiredErr
		}
		if len(requiredReport.Unsupported) > 0 {
			playlist := outcomePlaylist(intent, seed, core.OutcomeUnsupported, requiredReport.Unsupported)
			playlist.Notices = append(playlist.Notices, semanticNotices...)
			return playlist, nil
		}
		if !requiredTracksEligible(required, requiredReport.Eligible) {
			return outcomePlaylist(intent, seed, core.OutcomeNeedsClarification, []core.OutcomeReason{{Code: "required_track_essential_conflict", Detail: "a required track lacks affirmative evidence for the essential musical criterion", Criterion: essentialSummary(intent.EssentialCriteria), Action: "remove the required track or make the cross-genre exception explicit"}}), nil
		}
	}
	if o.bestAvailable {
		if request.OnSuggested != nil && o.audioSession == nil {
			for _, candidate := range candidates {
				request.OnSuggested(candidate.Track)
			}
		}
	}
	candidates, err = o.ranker.Rank(ctx, candidates, ports.RankRequest{Intent: intent, Profile: request.Profile})
	if err != nil {
		return core.Playlist{}, err
	}
	if request.Progress != nil {
		request.Progress.Report("generation", 0, 0, "Ordering your playlist")
	}
	setSemanticCapability(&intent, semanticMatched, len(semanticNotices) > 0)
	for index := range intent.HardConstraints {
		if intent.HardConstraints[index].Kind == "require_album" {
			intent.HardConstraints[index].RuntimeEnforced = true
		}
	}
	if o.audioSession != nil {
		intent.Capabilities = append(intent.Capabilities, core.CapabilityStatus{Name: "audio_analysis", Status: "limited", Detail: "Description clauses checked with aligned audio/text embeddings; assessments cover verified previews only."})
		for index := range intent.Capabilities {
			if intent.Capabilities[index].Name == "semantic_preferences" {
				intent.Capabilities[index].Status = "limited"
				intent.Capabilities[index].Detail = "Positive and negative clauses scored against verified preview segments using the installed aligned model."
			}
		}
		for index := range intent.HardConstraints {
			if intent.HardConstraints[index].Kind == "exclude_style" || intent.HardConstraints[index].Kind == "require_style" {
				intent.HardConstraints[index].RuntimeEnforced = true
			}
		}
	}
	if stages := journeyCriteria(intent.EssentialCriteria); intent.Mode == core.ModeJourney && len(stages) > 0 && intent.Count < len(stages) {
		return outcomePlaylist(intent, seed, core.OutcomeNeedsClarification, []core.OutcomeReason{{
			Code: "journey_count_too_short", Detail: fmt.Sprintf("%d tracks cannot represent %d requested journey stages", intent.Count, len(stages)),
			Criterion: essentialSummary(stages), Action: fmt.Sprintf("request at least %d tracks or remove a journey stage", len(stages)),
		}}), nil
	}
	reserved, remaining, reserveReasons, err := o.reserveJourneyStages(ctx, candidates, required, intent)
	if err != nil {
		return core.Playlist{}, err
	}
	if len(reserveReasons) > 0 && len(required)+len(reserved) >= intent.Count {
		return outcomePlaylist(intent, seed, core.OutcomeNeedsClarification, []core.OutcomeReason{{
			Code: "journey_count_too_short", Detail: "the requested count cannot represent every evidence-backed journey stage",
			Criterion: essentialSummary(intent.EssentialCriteria), Action: "increase the track count or remove a journey stage",
		}}), nil
	}

	selectionContext := append([]core.TrackRef(nil), required...)
	for _, candidate := range reserved {
		selectionContext = append(selectionContext, candidate.Track)
	}
	selection, err := o.selector.Select(ctx, remaining, ports.SelectionRequest{
		Intent: intent, Required: selectionContext, Waypoints: waypoints,
		RecentSelections: recentSelections, Count: intent.Count - len(required) - len(reserved),
	})
	if err != nil {
		return core.Playlist{}, err
	}
	selection.Candidates = append(reserved, selection.Candidates...)
	trajectoryWaypoints := waypoints
	if len(trajectoryWaypoints) < 2 && len(required) >= 2 {
		trajectoryWaypoints = required
	}
	var trajectory ports.Trajectory
	if intent.Mode == core.ModeJourney && len(trajectoryWaypoints) >= 2 {
		trajectory = NewWaypointTrajectory(o.cat, trajectoryWaypoints)
	}
	stageMembership, err := o.categoryMembership(ctx, append(candidatesForTracks(required), selection.Candidates...), intent)
	if err != nil {
		return core.Playlist{}, err
	}
	sequence, err := o.sequencer.Sequence(ctx, ports.SequenceRequest{
		Intent: intent, Candidates: selection.Candidates, Required: required, Waypoints: waypoints,
		ReferenceAnchors: references, RecentSelections: recentSelections,
		Trajectory: trajectory, Seed: seedValue, CategoryStages: stageMembership,
	})
	if err != nil {
		if errors.Is(err, core.ErrRequiredTrackConflict) {
			return outcomePlaylist(intent, seed, core.OutcomeNeedsClarification, []core.OutcomeReason{{Code: "required_track_order_conflict", Detail: err.Error(), Action: "change the required-track order, choose a fitting waypoint, or relax hard artist spacing"}}), nil
		}
		return core.Playlist{}, err
	}
	playlist := core.Playlist{
		Tracks: sequence.Tracks, Rationale: sequence.Rationale, Mode: intent.Mode, Seed: seed, Intent: intent,
		Notices: append(append(append([]core.PlaylistNotice{}, semanticNotices...), selection.Notices...), sequence.Notices...),
		Outcome: core.GenerationOutcome{State: core.OutcomeFulfilled},
	}
	if positiveSemantic != "" && !semanticMatched {
		playlist.Notices = append(playlist.Notices, core.PlaylistNotice{Code: "semantic_fallback", Detail: "semantic intent was preserved but no compatible grounded semantic matches were available; seeded embedding retrieval remained active", Requested: intent.Count, Actual: len(playlist.Tracks)})
	}
	if len(playlist.Tracks) < intent.Count {
		playlist.Notices = append(playlist.Notices, core.PlaylistNotice{
			Code:      "eligible_tracks_exhausted",
			Detail:    "eligible sufficiently relevant candidates were exhausted without relaxing hard exclusions or recording deduplication",
			Requested: intent.Count, Actual: len(playlist.Tracks),
		})
		playlist.Outcome.State = core.OutcomePartial
		playlist.Outcome.Reasons = append(playlist.Outcome.Reasons, core.OutcomeReason{Code: "eligible_tracks_exhausted", Detail: "eligible sufficiently relevant tracks were exhausted", Action: "reduce the requested count or add another fitting reference"})
	}
	if len(intent.EssentialCriteria) > 0 {
		_, finalReport, finalErr := o.filterEssential(ctx, candidatesForTracks(playlist.Tracks), intent.EssentialCriteria)
		if finalErr != nil {
			return core.Playlist{}, finalErr
		}
		for _, criterion := range intent.EssentialCriteria {
			if strings.HasPrefix(criterion.Scope, "journey_") && finalReport.Matched[criterionKey(criterion)] == 0 {
				playlist.Outcome.State = core.OutcomePartial
				playlist.Outcome.Reasons = append(playlist.Outcome.Reasons, core.OutcomeReason{Code: "journey_criterion_missing", Detail: "the selected playlist did not contain grounded evidence for one journey stage", Criterion: criterion.Value, Action: "choose a reference track for this journey stage"})
			}
		}
	}
	if len(stageMembership) > 0 {
		states := make([][]core.EvidenceState, len(playlist.Tracks))
		for index, track := range playlist.Tracks {
			states[index] = make([]core.EvidenceState, len(stageMembership))
			for stage, membership := range stageMembership {
				if membership[track.ID] {
					states[index][stage] = core.EvidenceMatch
				}
			}
		}
		if core.JourneySequenceViolations(states, len(stageMembership)) > 0 {
			playlist.Outcome.State = core.OutcomePartial
			playlist.Outcome.Reasons = append(playlist.Outcome.Reasons, core.OutcomeReason{Code: "journey_order_incomplete", Detail: "not every category stage could be represented in the requested direction", Action: "choose fitting references for the missing stages or simplify the journey"})
		}
	}
	playlist.Outcome.Reasons = append(playlist.Outcome.Reasons, reserveReasons...)
	if len(reserveReasons) > 0 && playlist.Outcome.State == core.OutcomeFulfilled {
		playlist.Outcome.State = core.OutcomePartial
	}
	o.annotateFit(ctx, &playlist)
	return playlist, nil
}

func journeyCriteria(criteria []core.MusicalCriterion) []core.MusicalCriterion {
	return core.JourneyCriteria(criteria)
}

// reserveJourneyStages protects one sufficiently relevant, evidence-backed
// candidate per semantic journey stage before diversity selection. It never
// creates identities or relaxes eligibility; the remaining MMR pool is still
// selected normally.
func (o *Orchestrator) reserveJourneyStages(ctx context.Context, ranked []core.Candidate, fixed []core.TrackRef, intent core.MusicIntent) ([]core.Candidate, []core.Candidate, []core.OutcomeReason, error) {
	criteria := journeyStageCriteria(intent)
	if (o.bestAvailable && !hasStagePeriods(intent) && len(criteria) < 2) || intent.Mode != core.ModeJourney || len(criteria) == 0 {
		return nil, ranked, nil, nil
	}
	used := map[string]bool{}
	for _, track := range fixed {
		used[track.ID] = true
	}
	best := 0.0
	if len(ranked) > 0 {
		best = ranked[0].Scores.Total
	}
	floor := maxFloat(o.cfg.SelectionMinimumRelevance, best-o.cfg.SelectionRelevanceWindow)
	var reserved []core.Candidate
	var reasons []core.OutcomeReason
	assignedFixed := map[string]bool{}
	for _, criterion := range criteria {
		fixedCandidates := candidatesForTracks(fixed)
		_, fixedReport, err := o.filterJourneyStage(ctx, fixedCandidates, criterion, intent)
		if err != nil {
			return nil, nil, nil, err
		}
		satisfied := false
		for _, track := range fixed {
			if !assignedFixed[track.ID] && fixedReport.Eligible[track.ID] {
				satisfied = true
				assignedFixed[track.ID] = true
				break
			}
		}
		if satisfied {
			continue
		}
		_, report, err := o.filterJourneyStage(ctx, ranked, criterion, intent)
		if err != nil {
			return nil, nil, nil, err
		}
		chosen := -1
		for index, candidate := range ranked {
			if !used[candidate.Track.ID] && report.Eligible[candidate.Track.ID] && candidate.Scores.Total >= floor {
				if chosen < 0 {
					chosen = index
				}
				if !o.bestAvailable || criterion.Kind == "" || o.bestCriterion(ctx, candidate.Track.ID, criterion) == core.EvidenceMatch {
					chosen = index
					break
				}
			}
		}
		if chosen < 0 {
			reasons = append(reasons, core.OutcomeReason{Code: "journey_stage_unavailable", Detail: "no sufficiently relevant candidate had affirmative evidence for a journey stage", Criterion: criterion.Value, Action: "choose a reference track for this stage or simplify the journey"})
			continue
		}
		used[ranked[chosen].Track.ID] = true
		reserved = append(reserved, ranked[chosen])
	}
	remaining := make([]core.Candidate, 0, len(ranked)-len(reserved))
	for _, candidate := range ranked {
		if !used[candidate.Track.ID] {
			remaining = append(remaining, candidate)
		}
	}
	return reserved, remaining, reasons, nil
}

func maxFloat(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}

func (o *Orchestrator) categoryMembership(ctx context.Context, candidates []core.Candidate, intent core.MusicIntent) ([]map[string]bool, error) {
	if o.bestAvailable && !hasStagePeriods(intent) && len(journeyCriteria(intent.EssentialCriteria)) < 2 {
		return nil, nil
	}
	criteria := journeyStageCriteria(intent)
	if intent.Mode != core.ModeJourney {
		return nil, nil
	}
	stages := make([]map[string]bool, len(criteria))
	for index, criterion := range criteria {
		_, report, err := o.filterJourneyStage(ctx, candidates, criterion, intent)
		if err != nil {
			return nil, err
		}
		stages[index] = report.Eligible
	}
	return stages, nil
}

func resolvedReferenceTracks(cat ports.Catalog, intent core.MusicIntent) []core.TrackRef {
	refs := positiveReferenceVectors(cat, intent)
	seen := map[string]struct{}{}
	result := make([]core.TrackRef, 0)
	for _, reference := range refs {
		for _, representative := range reference.reps {
			meta, ok := cat.Meta(representative.id)
			if !ok {
				continue
			}
			if _, duplicate := seen[meta.Ref.ID]; duplicate {
				continue
			}
			seen[meta.Ref.ID] = struct{}{}
			result = append(result, meta.Ref)
		}
	}
	return result
}

func resolvedRequiredTracks(cat ports.Catalog, references []core.IntentReference) []core.TrackRef {
	seen := map[string]struct{}{}
	result := make([]core.TrackRef, 0, len(references))
	for _, reference := range references {
		id := reference.TrackID
		if reference.Resolution != nil && reference.Resolution.Selected != nil && len(reference.Resolution.Selected.Representatives) > 0 {
			id = reference.Resolution.Selected.Representatives[0].TrackID
		}
		meta, ok := cat.Meta(id)
		if !ok {
			continue
		}
		key := core.ProvisionalRecordingKey(meta.Ref)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, meta.Ref)
	}
	return result
}

func resolvedContextTracks(cat ports.Catalog, tracks []core.TrackRef) []core.TrackRef {
	result := make([]core.TrackRef, 0, len(tracks))
	seen := map[string]struct{}{}
	for _, track := range tracks {
		meta, ok := cat.Meta(track.ID)
		if !ok {
			continue
		}
		key := core.ProvisionalRecordingKey(meta.Ref)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, meta.Ref)
	}
	return result
}

func orderRequiredByWaypoints(required, waypoints []core.TrackRef) []core.TrackRef {
	byRecording := make(map[string]core.TrackRef, len(required))
	for _, track := range required {
		byRecording[core.ProvisionalRecordingKey(track)] = track
	}
	result := make([]core.TrackRef, 0, len(required))
	used := map[string]struct{}{}
	for _, waypoint := range waypoints {
		key := core.ProvisionalRecordingKey(waypoint)
		if track, ok := byRecording[key]; ok {
			result = append(result, track)
			used[key] = struct{}{}
		}
	}
	for _, track := range required {
		key := core.ProvisionalRecordingKey(track)
		if _, already := used[key]; !already {
			result = append(result, track)
		}
	}
	return result
}

func journeyAnchors(cat ports.Catalog, intent core.MusicIntent) []core.TrackRef {
	references := intent.Journey.Waypoints
	if len(references) < 2 {
		references = intent.References
	}
	result := make([]core.TrackRef, 0, len(references))
	seen := map[string]struct{}{}
	for _, reference := range references {
		if reference.Influence != core.InfluencePositive {
			continue
		}
		id := reference.TrackID
		if reference.Resolution != nil && reference.Resolution.Selected != nil && len(reference.Resolution.Selected.Representatives) > 0 {
			id = reference.Resolution.Selected.Representatives[0].TrackID
		}
		meta, ok := cat.Meta(id)
		if !ok {
			continue
		}
		if _, duplicate := seen[meta.Ref.ID]; duplicate {
			continue
		}
		seen[meta.Ref.ID] = struct{}{}
		result = append(result, meta.Ref)
	}
	return result
}

func randomSeed() core.RNGSeed {
	var raw [8]byte
	if _, err := cryptorand.Read(raw[:]); err != nil {
		return core.NewRNGSeed(1)
	}
	value := binary.LittleEndian.Uint64(raw[:])
	if value == 0 {
		value = 1
	}
	return core.NewRNGSeed(value)
}

func interpolate(a, b []float32, position float64) []float32 {
	length := minInt(len(a), len(b))
	result := make([]float32, length)
	for index := range result {
		result[index] = float32((1-position)*float64(a[index]) + position*float64(b[index]))
	}
	return normalizeVector(result)
}

func distribute(total, buckets int) []int {
	result := make([]int, buckets)
	if total <= 0 || buckets <= 0 {
		return result
	}
	for index := range result {
		result[index] = total / buckets
		if index < total%buckets {
			result[index]++
		}
	}
	return result
}

var _ ports.RecommendationEngine = (*Orchestrator)(nil)
var _ ports.PersonalizedRecommendationEngine = (*Orchestrator)(nil)
var _ ports.ContextualRecommendationEngine = (*Orchestrator)(nil)
var _ ports.VersionedRecommendationEngine = (*Orchestrator)(nil)
