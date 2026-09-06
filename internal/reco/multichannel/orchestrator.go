package multichannel

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"fmt"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/resolution"
)

// Orchestrator owns the versioned retrieve -> eligibility -> rank -> select ->
// sequence pipeline while preserving the complete resolved intent.
type Orchestrator struct {
	cat       ports.Catalog
	resolver  ports.ReferenceResolver
	retriever ports.CandidateRetriever
	ranker    ports.Ranker
	selector  ports.CandidateSelector
	sequencer ports.PlaylistSequencer
	features  ports.FeatureStore
	semantic  ports.SemanticSearcher
}

func NewWithSemantic(cat ports.Catalog, sim ports.SimilarityEngine, resolver ports.ReferenceResolver, features ports.FeatureStore, semantic ports.SemanticSearcher, cfg Config) *Orchestrator {
	cfg = cfg.normalized()
	return &Orchestrator{
		cat: cat, resolver: resolver, features: features, semantic: semantic,
		retriever: NewSemanticRetriever(cat, sim, semantic, cfg), ranker: NewRanker(cat, cfg),
		selector: NewSelector(cat, cfg), sequencer: NewSequencer(cat, cfg),
	}
}

func New(cat ports.Catalog, sim ports.SimilarityEngine, resolver ports.ReferenceResolver, cfg Config) *Orchestrator {
	cfg = cfg.normalized()
	return &Orchestrator{
		cat: cat, resolver: resolver,
		retriever: NewRetriever(cat, sim, cfg), ranker: NewRanker(cat, cfg),
		selector: NewSelector(cat, cfg), sequencer: NewSequencer(cat, cfg),
	}
}

// NewWithComponents replaces retrieval and ranking while retaining the default
// selector and sequencer. NewPipeline is available when every boundary needs
// to be supplied by an evaluation fixture.
func NewWithComponents(cat ports.Catalog, resolver ports.ReferenceResolver, retriever ports.CandidateRetriever, ranker ports.Ranker, cfg Config) *Orchestrator {
	cfg = cfg.normalized()
	return &Orchestrator{
		cat: cat, resolver: resolver, retriever: retriever, ranker: ranker,
		selector: NewSelector(cat, cfg), sequencer: NewSequencer(cat, cfg),
	}
}

func NewPipeline(cat ports.Catalog, resolver ports.ReferenceResolver, retriever ports.CandidateRetriever, ranker ports.Ranker, selector ports.CandidateSelector, sequencer ports.PlaylistSequencer) *Orchestrator {
	return &Orchestrator{
		cat: cat, resolver: resolver, retriever: retriever, ranker: ranker,
		selector: selector, sequencer: sequencer,
	}
}

func (o *Orchestrator) AlgorithmVersion() string {
	if o.semantic == nil {
		return AlgorithmVersion
	}
	info := o.semantic.Info()
	return AlgorithmVersion + "+semantic:" + info.FeatureVersion + "@" + info.ModelRevision
}

func (o *Orchestrator) Build(ctx context.Context, intent core.MusicIntent) (core.Playlist, error) {
	return o.BuildRecommendation(ctx, ports.RecommendationRequest{Intent: intent})
}

func (o *Orchestrator) BuildWithProfile(ctx context.Context, intent core.MusicIntent, profile core.TasteProfile) (core.Playlist, error) {
	return o.BuildRecommendation(ctx, ports.RecommendationRequest{Intent: intent, Profile: profile})
}

func (o *Orchestrator) BuildRecommendation(ctx context.Context, request ports.RecommendationRequest) (core.Playlist, error) {
	if err := ctx.Err(); err != nil {
		return core.Playlist{}, err
	}
	intent := request.Intent.Normalized()
	if o.resolver != nil {
		var issues []resolution.Issue
		intent, issues = resolution.Apply(o.resolver, intent)
		if err := resolution.BlockingError(issues); err != nil {
			return core.Playlist{}, err
		}
	}
	if err := intent.Validate(); err != nil {
		return core.Playlist{}, err
	}
	intent = intent.Normalized()

	references := resolvedReferenceTracks(o.cat, intent)
	required := resolvedRequiredTracks(o.cat, intent.RequiredTracks)
	waypoints := journeyAnchors(o.cat, intent)
	if intent.Mode == core.ModeJourney {
		required = orderRequiredByWaypoints(required, waypoints)
	}
	recentSelections := resolvedContextTracks(o.cat, request.RecentSelections)
	positiveSemantic, _ := semanticQueryText(intent)
	semanticSeeded := positiveSemantic != "" && o.semantic != nil
	if len(references) == 0 && len(required) == 0 && !semanticSeeded {
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

	seed := intent.Seed
	if seed.IsZero() {
		seed = randomSeed()
	}
	seedValue, err := seed.Int64()
	if err != nil {
		return core.Playlist{}, fmt.Errorf("seed: %w", err)
	}
	intent.Seed = seed

	eligible := newEligibility(intent, references, required)
	eligible.excludeRecent(recentSelections)
	if err := eligible.validateRequired(required, intent.Constraints.ExcludeSeedArtists); err != nil {
		return core.Playlist{}, err
	}
	candidates, err := o.retriever.Retrieve(ctx, ports.RetrievalRequest{
		Intent: intent, Profile: request.Profile, RecentSelections: recentSelections, Seed: seedValue,
	})
	if err != nil {
		return core.Playlist{}, err
	}
	semanticMatched := hasSemanticCandidates(candidates)
	if len(references) == 0 && len(required) == 0 && len(candidates) == 0 {
		return core.Playlist{}, fmt.Errorf("%w: semantic index produced no grounded candidates", core.ErrNoSeeds)
	}
	candidates, err = eligible.filter(ctx, candidates, intent.Constraints.ExcludeSeedArtists)
	if err != nil {
		return core.Playlist{}, err
	}
	semanticNotices := []core.PlaylistNotice{}
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
		if err := validateRequiredSemanticConstraints(ctx, o.features, required, intent.HardConstraints); err != nil {
			return core.Playlist{}, err
		}
	}
	candidates, err = o.ranker.Rank(ctx, candidates, ports.RankRequest{Intent: intent, Profile: request.Profile})
	if err != nil {
		return core.Playlist{}, err
	}
	setSemanticCapability(&intent, semanticMatched, len(semanticNotices) > 0)

	selection, err := o.selector.Select(ctx, candidates, ports.SelectionRequest{
		Intent: intent, Required: required, Waypoints: waypoints,
		RecentSelections: recentSelections, Count: intent.Count - len(required),
	})
	if err != nil {
		return core.Playlist{}, err
	}
	trajectoryWaypoints := waypoints
	if len(trajectoryWaypoints) < 2 && len(required) >= 2 {
		trajectoryWaypoints = required
	}
	var trajectory ports.Trajectory
	if intent.Mode == core.ModeJourney && len(trajectoryWaypoints) >= 2 {
		trajectory = NewWaypointTrajectory(o.cat, trajectoryWaypoints)
	}
	sequence, err := o.sequencer.Sequence(ctx, ports.SequenceRequest{
		Intent: intent, Candidates: selection.Candidates, Required: required, Waypoints: waypoints,
		ReferenceAnchors: references, RecentSelections: recentSelections,
		Trajectory: trajectory, Seed: seedValue,
	})
	if err != nil {
		return core.Playlist{}, err
	}
	playlist := core.Playlist{
		Tracks: sequence.Tracks, Rationale: sequence.Rationale, Mode: intent.Mode, Seed: seed, Intent: intent,
		Notices: append(append(append([]core.PlaylistNotice{}, semanticNotices...), selection.Notices...), sequence.Notices...),
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
	}
	return playlist, nil
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
