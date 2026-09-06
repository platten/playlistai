package multichannel

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"fmt"
	"math/rand"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/resolution"
)

type Orchestrator struct {
	cat       ports.Catalog
	resolver  ports.ReferenceResolver
	retriever ports.CandidateRetriever
	ranker    ports.Ranker
	cfg       Config
}

func New(cat ports.Catalog, sim ports.SimilarityEngine, resolver ports.ReferenceResolver, cfg Config) *Orchestrator {
	cfg = cfg.normalized()
	return &Orchestrator{
		cat: cat, resolver: resolver, cfg: cfg,
		retriever: NewRetriever(cat, sim, cfg), ranker: NewRanker(cat, cfg),
	}
}

func NewWithComponents(cat ports.Catalog, resolver ports.ReferenceResolver, retriever ports.CandidateRetriever, ranker ports.Ranker, cfg Config) *Orchestrator {
	return &Orchestrator{cat: cat, resolver: resolver, retriever: retriever, ranker: ranker, cfg: cfg.normalized()}
}

func (*Orchestrator) AlgorithmVersion() string { return AlgorithmVersion }

func (o *Orchestrator) Build(ctx context.Context, intent core.MusicIntent) (core.Playlist, error) {
	return o.BuildWithProfile(ctx, intent, core.TasteProfile{})
}

func (o *Orchestrator) BuildWithProfile(ctx context.Context, intent core.MusicIntent, profile core.TasteProfile) (core.Playlist, error) {
	if err := ctx.Err(); err != nil {
		return core.Playlist{}, err
	}
	intent = intent.Normalized()
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
	if len(references) == 0 && len(required) == 0 {
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
	if err := eligible.validateRequired(required, intent.Constraints.ExcludeSeedArtists); err != nil {
		return core.Playlist{}, err
	}
	candidates, err := o.retriever.Retrieve(ctx, ports.RetrievalRequest{Intent: intent, Profile: profile, Seed: seedValue})
	if err != nil {
		return core.Playlist{}, err
	}
	candidates, err = eligible.filter(ctx, candidates, intent.Constraints.ExcludeSeedArtists)
	if err != nil {
		return core.Playlist{}, err
	}
	candidates, err = o.ranker.Rank(ctx, candidates, ports.RankRequest{Intent: intent, Profile: profile})
	if err != nil {
		return core.Playlist{}, err
	}

	sequencer := sequenceBuilder{cat: o.cat, cfg: o.cfg, intent: intent, eligibility: eligible, rng: rand.New(rand.NewSource(seedValue))} //nolint:gosec
	tracks, reasons, err := sequencer.sequence(ctx, candidates, references, required)
	if err != nil {
		return core.Playlist{}, err
	}
	playlist := core.Playlist{Tracks: tracks, Rationale: reasons, Mode: intent.Mode, Seed: seed, Intent: intent}
	if len(tracks) < intent.Count {
		playlist.Notices = append(playlist.Notices, core.PlaylistNotice{
			Code:      "eligible_tracks_exhausted",
			Detail:    "eligible ranked candidates were exhausted without relaxing exclusions or duplicate protection",
			Requested: intent.Count, Actual: len(tracks),
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

type sequenceBuilder struct {
	cat         ports.Catalog
	cfg         Config
	intent      core.MusicIntent
	eligibility *eligibility
	rng         *rand.Rand
}

func (s *sequenceBuilder) sequence(ctx context.Context, candidates []core.Candidate, references, required []core.TrackRef) ([]core.TrackRef, []core.StepReason, error) {
	if s.intent.Mode == core.ModeJourney {
		anchors := journeyAnchors(s.cat, s.intent)
		if len(required) >= 2 {
			anchors = required
		}
		if len(anchors) >= 2 {
			return s.journey(ctx, candidates, anchors, required, len(required) >= 2)
		}
	}
	return s.similar(ctx, candidates, required)
}

func (s *sequenceBuilder) similar(ctx context.Context, candidates []core.Candidate, required []core.TrackRef) ([]core.TrackRef, []core.StepReason, error) {
	tracks := make([]core.TrackRef, 0, s.intent.Count)
	reasons := make([]core.StepReason, 0, s.intent.Count)
	previous := ""
	for _, track := range required {
		if !s.eligibility.canFollow(track, previous) {
			return nil, nil, fmt.Errorf("%w: adjacent required tracks repeat artist %q", core.ErrRequiredTrackConflict, track.Artist)
		}
		tracks = append(tracks, track)
		reasons = append(reasons, requiredReason(track))
		previous = core.NormalizeIdentityPart(track.Artist)
	}
	remaining := append([]core.Candidate(nil), candidates...)
	for len(tracks) < s.intent.Count {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		candidate, kind, next, ok := s.pick(remaining, previous, "", nil)
		if !ok {
			break
		}
		remaining = next
		tracks = append(tracks, candidate.Track)
		reasons = append(reasons, s.candidateReason(candidate, kind, 0, false))
		previous = core.NormalizeIdentityPart(candidate.Track.Artist)
	}
	return tracks, reasons, nil
}

func (s *sequenceBuilder) journey(ctx context.Context, candidates []core.Candidate, anchors, required []core.TrackRef, emitAnchors bool) ([]core.TrackRef, []core.StepReason, error) {
	tracks := make([]core.TrackRef, 0, s.intent.Count)
	reasons := make([]core.StepReason, 0, s.intent.Count)
	remaining := append([]core.Candidate(nil), candidates...)
	previous := ""
	if !emitAnchors {
		for _, track := range required {
			if !s.eligibility.canFollow(track, previous) {
				return nil, nil, fmt.Errorf("%w: adjacent required tracks repeat artist %q", core.ErrRequiredTrackConflict, track.Artist)
			}
			tracks = append(tracks, track)
			reasons = append(reasons, requiredReason(track))
			previous = core.NormalizeIdentityPart(track.Artist)
		}
	}
	intermediates := s.intent.Count - len(required)
	perSegment := distribute(intermediates, len(anchors)-1)
	for segment := 0; segment < len(anchors)-1; segment++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		start, end := anchors[segment], anchors[segment+1]
		if emitAnchors && segment == 0 {
			tracks = append(tracks, start)
			reasons = append(reasons, requiredReason(start))
			previous = core.NormalizeIdentityPart(start.Artist)
		}
		for index := 0; index < perSegment[segment]; index++ {
			position := float64(index+1) / float64(perSegment[segment]+1)
			target := s.interpolateTarget(start, end, position)
			avoid := ""
			if emitAnchors && s.eligibility.noBackToBack && index == perSegment[segment]-1 {
				avoid = core.NormalizeIdentityPart(end.Artist)
			}
			candidate, kind, next, ok := s.pick(remaining, previous, avoid, &target)
			if !ok {
				break
			}
			remaining = next
			tracks = append(tracks, candidate.Track)
			reasons = append(reasons, s.candidateReason(candidate, kind, s.positionSimilarity(candidate, target), true))
			previous = core.NormalizeIdentityPart(candidate.Track.Artist)
		}
		if emitAnchors {
			if !s.eligibility.canFollow(end, previous) {
				return nil, nil, fmt.Errorf("%w: journey waypoint repeats artist %q", core.ErrRequiredTrackConflict, end.Artist)
			}
			tracks = append(tracks, end)
			reasons = append(reasons, requiredReason(end))
			previous = core.NormalizeIdentityPart(end.Artist)
		}
	}
	return tracks, reasons, nil
}

func (s *sequenceBuilder) pick(candidates []core.Candidate, previousArtist, avoidArtist string, target *ports.Vectors) (core.Candidate, string, []core.Candidate, bool) {
	eligible := make([]int, 0, len(candidates))
	for index, candidate := range candidates {
		artist := core.NormalizeIdentityPart(candidate.Track.Artist)
		if !s.eligibility.canFollow(candidate.Track, previousArtist) || (avoidArtist != "" && artist == avoidArtist) {
			continue
		}
		eligible = append(eligible, index)
	}
	if len(eligible) == 0 {
		return core.Candidate{}, "", candidates, false
	}
	chosen, kind := eligible[0], "ranked"
	if target != nil {
		best := -2.0
		for _, index := range eligible {
			score := candidates[index].Scores.Total + s.cfg.JourneyPositionWeight*s.positionSimilarity(candidates[index], *target)
			if score > best {
				best, chosen = score, index
			}
		}
	}
	chance := s.cfg.ExplorationChance * s.intent.Controls.Discovery
	if chance > 0 && s.rng.Float64() < chance {
		pool := make([]int, 0, s.cfg.ExplorationPickPool)
		for _, index := range eligible {
			if hasChannel(candidates[index], ChannelExploration) {
				pool = append(pool, index)
				if len(pool) == s.cfg.ExplorationPickPool {
					break
				}
			}
		}
		if len(pool) > 0 {
			chosen, kind = pool[s.rng.Intn(len(pool))], "exploration"
		}
	}
	candidate := candidates[chosen]
	remaining := make([]core.Candidate, 0, len(candidates)-1)
	remaining = append(remaining, candidates[:chosen]...)
	remaining = append(remaining, candidates[chosen+1:]...)
	return candidate, kind, remaining, true
}

func (s *sequenceBuilder) interpolateTarget(start, end core.TrackRef, position float64) ports.Vectors {
	a, _ := s.cat.Vectors(start.ID)
	b, _ := s.cat.Vectors(end.ID)
	return ports.Vectors{Audio: interpolate(a.Audio, b.Audio, position), Track: interpolate(a.Track, b.Track, position)}
}

func (s *sequenceBuilder) positionSimilarity(candidate core.Candidate, target ports.Vectors) float64 {
	vectors, ok := s.cat.Vectors(candidate.Track.ID)
	if !ok {
		return 0
	}
	weights := normalizedWeights(s.intent.Controls.AudioWeight, s.intent.Controls.CooccurrenceWeight)
	return float64(weights[0])*cosine(vectors.Audio, target.Audio) + float64(weights[1])*cosine(vectors.Track, target.Track)
}

func (s *sequenceBuilder) candidateReason(candidate core.Candidate, kind string, positionScore float64, hasPosition bool) core.StepReason {
	evidence := []core.ComponentEvidence{
		{Component: "audio_seed_affinity", Score: candidate.Scores.AudioSeedAffinity, Weight: s.intent.Controls.AudioWeight, Available: candidate.Available.AudioSeedAffinity},
		{Component: "cooccurrence_seed_affinity", Score: candidate.Scores.CooccurrenceAffinity, Weight: s.intent.Controls.CooccurrenceWeight, Available: candidate.Available.CooccurrenceAffinity},
		{Component: "listener_affinity", Score: candidate.Scores.ListenerAffinity, Weight: s.cfg.ListenerWeight, Available: candidate.Available.ListenerAffinity},
		{Component: "negative_preference_match", Score: candidate.Scores.NegativeMatch, Weight: -s.cfg.NegativePenalty, Available: candidate.Available.NegativeMatch},
		{Component: "recent_exposure", Score: candidate.Scores.RecentExposure, Weight: -s.cfg.ExposurePenalty, Available: candidate.Available.RecentExposure},
		{Component: "listener_novelty", Score: candidate.Scores.Novelty, Weight: s.cfg.NoveltyWeight * s.intent.Controls.Discovery, Available: candidate.Available.Novelty},
		{Component: "reciprocal_rank_fusion", Score: candidate.Scores.RetrievalFusion, Weight: s.cfg.RetrievalWeight, Available: candidate.Available.RetrievalFusion},
	}
	if hasPosition {
		evidence = append(evidence, core.ComponentEvidence{Component: "journey_position", Score: positionScore, Weight: s.cfg.JourneyPositionWeight, Available: true})
	}
	parts := []string{fmt.Sprintf("score %.3f", candidate.Scores.Total)}
	for _, item := range evidence {
		if item.Available {
			parts = append(parts, fmt.Sprintf("%s %.2f", strings.ReplaceAll(item.Component, "_", " "), item.Score))
		}
	}
	return core.StepReason{TrackID: candidate.Track.ID, Kind: kind, Detail: strings.Join(parts, " · "), Sources: candidate.Sources, Evidence: evidence}
}

func requiredReason(track core.TrackRef) core.StepReason {
	return core.StepReason{TrackID: track.ID, Kind: "required", Detail: "required track", Sources: []core.RetrievalEvidence{}, Evidence: []core.ComponentEvidence{{Component: "required", Score: 1, Weight: 1, Available: true}}}
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

func hasChannel(candidate core.Candidate, channel string) bool {
	for _, source := range candidate.Sources {
		if source.Channel == channel {
			return true
		}
	}
	return false
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
var _ ports.VersionedRecommendationEngine = (*Orchestrator)(nil)
