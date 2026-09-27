package multichannel

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/logging"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/resolution"
)

const AutomaticAlgorithmVersion = "automatic/v7"
const automaticCandidateLimit = 512

// AutomaticEngine does one bounded retrieval/preparation pass, freezes its
// inputs and performs one assembly. Optional metadata and audio assistance share
// the preparation deadline; assembly reads only the frozen evidence.
type AutomaticEngine struct {
	cat                 ports.Catalog
	resolver            ports.ReferenceResolver
	retriever, prepared ports.CandidateRetriever
	cfg                 Config
	overlay             IntentOverlayProvider
	familiarity         func(context.Context, core.TrackRef) (float64, bool)
	featurePreparer     func(context.Context, core.MusicIntent, ports.Catalog) error
	intentPreparer      func(context.Context, core.MusicIntent, ports.Catalog, ports.ReferenceResolver) (core.MusicIntent, error)
	features            ports.FeatureStore
	audioProvider       func() *audio.Service
	calibrations        []core.AudioSimilarityCalibration
	enricher            ports.Enricher
}

func NewAutomatic(cat ports.Catalog, resolver ports.ReferenceResolver, retriever ports.CandidateRetriever, cfg Config) *AutomaticEngine {
	return &AutomaticEngine{cat: cat, resolver: resolver, retriever: retriever, cfg: cfg.normalized()}
}
func (a *AutomaticEngine) WithIntentOverlayProvider(v IntentOverlayProvider) *AutomaticEngine {
	a.overlay = v
	return a
}
func (a *AutomaticEngine) WithPreparedRetriever(v ports.CandidateRetriever) *AutomaticEngine {
	a.prepared = v
	return a
}
func (a *AutomaticEngine) WithFamiliarityReader(v func(context.Context, core.TrackRef) (float64, bool)) *AutomaticEngine {
	a.familiarity = v
	return a
}
func (a *AutomaticEngine) WithFeaturePreparer(v func(context.Context, core.MusicIntent, ports.Catalog) error) *AutomaticEngine {
	a.featurePreparer = v
	return a
}
func (a *AutomaticEngine) WithFeatures(v ports.FeatureStore) *AutomaticEngine {
	a.features = v
	return a
}
func (a *AutomaticEngine) WithIntentPreparer(v func(context.Context, core.MusicIntent, ports.Catalog, ports.ReferenceResolver) (core.MusicIntent, error)) *AutomaticEngine {
	a.intentPreparer = v
	return a
}

// Calibrations are trusted, independently evaluated policy artifacts; none are
// enabled by default. Snapshot copies isolate a running request from updates.
func (a *AutomaticEngine) WithAudioCalibrations(v []core.AudioSimilarityCalibration) *AutomaticEngine {
	a.calibrations = automaticCopy(v)
	return a
}
func (a *AutomaticEngine) WithAudioProvider(v func() *audio.Service) *AutomaticEngine {
	a.audioProvider = v
	return a
}
func (a *AutomaticEngine) WithEnricher(v ports.Enricher) *AutomaticEngine {
	a.enricher = v
	return a
}
func (*AutomaticEngine) AlgorithmVersion() string {
	return AutomaticAlgorithmVersion + "+" + core.AutomaticFitPolicyVersion
}
func (a *AutomaticEngine) Build(ctx context.Context, intent core.MusicIntent) (core.Playlist, error) {
	return a.BuildRecommendation(ctx, ports.RecommendationRequest{Intent: intent})
}
func (a *AutomaticEngine) BuildWithProfile(ctx context.Context, intent core.MusicIntent, profile core.TasteProfile) (core.Playlist, error) {
	return a.BuildRecommendation(ctx, ports.RecommendationRequest{Intent: intent, Profile: profile})
}

func (a *AutomaticEngine) BuildRecommendation(parent context.Context, request ports.RecommendationRequest) (result core.Playlist, buildErr error) {
	local := *a
	a = &local
	ctx, cancel := ports.WithAutomaticGenerationBudget(parent)
	defer cancel()
	work, finish := ports.GenerationWorkContext(ctx)
	defer finish()
	work, stop := context.WithCancel(work)
	defer stop()
	if request.StopChecking != nil {
		select {
		case <-request.StopChecking:
			stop()
		default:
		}
		go func() {
			select {
			case <-request.StopChecking:
				stop()
			case <-work.Done():
			}
		}()
	}
	intent := request.Intent.Normalized()
	intent.Controls.RecommendationMode = core.Automatic
	preparationStopped := false
	search := core.SearchSnapshot{PolicyVersion: core.AutomaticSearchPolicyVersion, QueryPolicyVersion: AutomaticAlgorithmVersion, EvidencePolicyVersion: core.AutomaticFitPolicyVersion, Profile: request.Profile, StopReason: "prepared_pool"}
	if deadline, ok := ctx.Deadline(); ok {
		search.GenerationLimitMilliseconds = deadline.Sub(ports.GenerationStarted(ctx)).Milliseconds()
	}
	defer func() {
		if buildErr != nil && ctx.Err() == nil && work.Err() != nil && (errors.Is(buildErr, context.Canceled) || errors.Is(buildErr, context.DeadlineExceeded)) {
			buildErr = nil
			preparationStopped = true
			result = outcomePlaylist(intent, intent.Seed, core.OutcomePartial, []core.OutcomeReason{{Code: "preparation_incomplete", Detail: "Preparation stopped before a complete candidate batch was available."}})
		}
		if buildErr == nil {
			if preparationStopped {
				search.StopReason = "preparation_stopped"
			}
			result.Search, buildErr = core.FreezeSearch(search, result)
		}
	}()
	if intent.Seed.IsZero() {
		intent.Seed = randomSeed()
	}
	seed, err := intent.Seed.Int64()
	if err != nil {
		return core.Playlist{}, err
	}
	cat, resolver, retriever := a.cat, a.resolver, a.retriever
	if a.overlay != nil {
		overlay, err := a.overlay(work, intent, cat, resolver, retriever)
		if err != nil {
			return core.Playlist{}, err
		}
		if overlay.Release != nil {
			defer overlay.Release()
		}
		if overlay.Catalog != nil {
			cat = overlay.Catalog
		}
		if overlay.Resolver != nil {
			resolver = overlay.Resolver
		}
		if overlay.Retriever != nil {
			retriever = overlay.Retriever
		}
		if overlay.PreparedRetriever != nil {
			a.prepared = overlay.PreparedRetriever
		}
		if overlay.FamiliarityReader != nil {
			a.familiarity = overlay.FamiliarityReader
		}
	}
	if err := ctx.Err(); err != nil {
		return core.Playlist{}, err
	}
	if cat == nil {
		return core.Playlist{}, fmt.Errorf("automatic: catalog unavailable")
	}
	if a.intentPreparer != nil {
		phase := automaticPhase(work, "intent_preparation")
		prepared, err := a.intentPreparer(work, intent, cat, resolver)
		phase(len(prepared.References), err)
		if err != nil {
			return core.Playlist{}, err
		}
		intent = prepared
	}
	if resolver != nil {
		phase := automaticPhase(work, "resolution")
		var issues []resolution.Issue
		intent, issues = resolution.ApplyContext(work, resolver, intent)
		phase(len(issues), work.Err())
		if reasons := explicitResolutionReasons(issues); len(reasons) > 0 && work.Err() == nil {
			state := core.OutcomePartial
			for _, issue := range issues {
				if issue.Inferred || issue.Influence == core.InfluenceNegative {
					continue
				}
				decided := false
				for _, ref := range intent.References {
					if ref.Query == issue.Query && ref.Grounding != nil {
						_, decided = ref.Grounding.DecidedArtist()
					}
				}
				if !decided || issue.Status == core.ResolutionAmbiguous {
					state = core.OutcomeNeedsClarification
				}
			}
			return outcomePlaylist(intent, intent.Seed, state, reasons), nil
		}
	}
	if err := intent.Validate(); err != nil {
		return core.Playlist{}, err
	}
	if reasons := intentCriterionConflictReasons(intent); len(reasons) > 0 {
		return outcomePlaylist(intent, intent.Seed, core.OutcomeNeedsClarification, reasons), nil
	}
	if reasons := artistRestrictionConflict(intent); len(reasons) > 0 {
		return outcomePlaylist(intent, intent.Seed, core.OutcomeNeedsClarification, reasons), nil
	}
	catalogVersion := ""
	if resolver != nil {
		catalogVersion = resolver.CatalogVersion()
	}
	retrievalIntent := intent
	retrievalIntent.InferredAnchors = nil
	retrievalIntent.GenreExpansions = nil
	references := resolvedReferenceTracksContext(work, cat, retrievalIntent)
	required := resolvedRequiredTracksContext(work, cat, intent.RequiredTracks)
	for _, ref := range intent.RequiredTracks {
		if len(resolvedRequiredTracksContext(work, cat, []core.IntentReference{ref})) == 0 && work.Err() == nil {
			return outcomePlaylist(intent, intent.Seed, core.OutcomeNeedsClarification, []core.OutcomeReason{{Code: "required_unresolved", Detail: "A required recording could not be resolved.", Criterion: ref.Query}}), nil
		}
	}
	var negative []core.TrackRef
	for _, group := range intentReferenceTracksContext(work, cat, intent, core.InfluenceNegative, false) {
		for _, ref := range group.reps {
			if meta, ok := ports.CatalogMeta(work, cat, ref.TrackID); ok {
				negative = append(negative, meta.Ref)
			}
		}
	}
	waypoints := journeyAnchors(work, cat, intent)
	for _, endpoint := range []struct {
		ref   *core.IntentReference
		first bool
	}{{intent.Start, true}, {intent.Destination, false}} {
		if endpoint.ref == nil {
			continue
		}
		tracks := resolvedRequiredTracksContext(work, cat, []core.IntentReference{*endpoint.ref})
		if len(tracks) == 0 && work.Err() == nil {
			return outcomePlaylist(intent, intent.Seed, core.OutcomeNeedsClarification, []core.OutcomeReason{{Code: "endpoint_unresolved", Detail: "A requested endpoint has no exact catalog recording."}}), nil
		}
		if len(tracks) > 0 && !endpoint.first && intent.Start != nil && len(required) > 0 && intent.Count > 1 && core.ProvisionalRecordingKey(required[0]) == core.ProvisionalRecordingKey(tracks[0]) {
			return outcomePlaylist(intent, intent.Seed, core.OutcomeNeedsClarification, []core.OutcomeReason{{Code: "endpoint_recording_conflict", Detail: "The requested first and final recordings are the same recording."}}), nil
		}
		if len(tracks) > 0 {
			required = moveEndpoint(required, tracks[0], endpoint.first)
			waypoints = moveEndpoint(waypoints, tracks[0], endpoint.first)
		}
	}
	if intent.Mode == core.ModeJourney {
		required = orderRequiredByWaypoints(required, waypoints)
	}
	if len(intent.RequiredTracks) > 0 && len(required) == 0 && work.Err() == nil {
		return outcomePlaylist(intent, intent.Seed, core.OutcomeNeedsClarification, []core.OutcomeReason{{Code: "required_unresolved", Detail: "A required recording could not be resolved."}}), nil
	}
	if len(required) > intent.Count && (intent.DurationSeconds <= 0 || intent.HasExplicitTrackCount()) {
		return core.Playlist{}, core.ErrCountBelowRequired
	}
	recent := resolvedContextTracksContext(work, cat, request.RecentSelections)
	// Private execution view enables already-tested packed-vector/MMR sequencing
	// helpers. The saved intent and result remain Automatic; no legacy gate runs.
	execution := retrievalIntent
	execution.Controls.RecommendationMode = core.EnhancedHybrid
	if request.Progress != nil {
		request.Progress.Report("generation", 0, automaticCandidateLimit, "Preparing candidate evidence")
	}
	var prepareErr error
	if a.featurePreparer != nil && work.Err() == nil {
		phase := automaticPhase(work, "feature_preparation")
		prepareErr = a.featurePreparer(work, execution, cat)
		phase(0, prepareErr)
	}
	req := ports.RetrievalRequest{Intent: retrievalIntent, Profile: request.Profile, RecentSelections: recent, Seed: seed}
	var pools [][]core.Candidate
	// Retrieval cannot spend the evidence-batch budget. Both sources return
	// completed candidates on their own timeout; the enclosing work context
	// stays live for preparation, with the outer reserve still for assembly.
	retrievalBudget := 8 * time.Second
	if deadline, ok := work.Deadline(); ok {
		retrievalBudget = min(retrievalBudget, max(time.Duration(0), time.Until(deadline)/2))
	}
	retrievalCtx, retrievalDone := context.WithTimeout(work, retrievalBudget)
	defer retrievalDone()
	var graphCandidates []core.Candidate
	for index, source := range []ports.CandidateRetriever{a.prepared, retriever} {
		if source == nil || retrievalCtx.Err() != nil {
			continue
		}
		sourceCtx := retrievalCtx
		sourceDone := func() {}
		if index == 0 {
			sourceCtx, sourceDone = context.WithTimeout(retrievalCtx, min(2*time.Second, retrievalBudget/4))
		}
		phase := automaticPhase(sourceCtx, []string{"graph_retrieval", "local_retrieval"}[index])
		candidates, err := source.Retrieve(sourceCtx, req)
		phase(len(candidates), err)
		sourceDone()
		pools = append(pools, candidates)
		if index == 0 {
			graphCandidates = automaticCopy(candidates)
		}
		if err != nil {
			prepareErr = err
		}
	}
	candidates := automaticBalancedPool(pools, min(automaticCandidateLimit, a.cfg.MaxCandidates), seed)
	tracks := append(append(append(append(append([]core.TrackRef(nil), required...), references...), waypoints...), recent...), negative...)
	for _, c := range candidates {
		tracks = append(tracks, c.Track)
	}
	phase := automaticPhase(work, "batch_prepare")
	batch, batchErr := a.prepareBatch(work, cat, tracks, intent)
	phase(len(batch.ids), batchErr)
	if batchErr == nil && work.Err() == nil {
		phase = automaticPhase(work, "online_evidence")
		metadataOrder := a.automaticMetadataOrder(batch, candidates, append(append([]core.TrackRef(nil), required...), waypoints...), seed)
		batchErr = a.acquireAutomaticEvidence(work, cat, batch, execution, catalogVersion, metadataOrder)
		phase(len(batch.ids), batchErr)
	}
	if batchErr != nil {
		prepareErr = batchErr
	}
	if err := ctx.Err(); err != nil {
		return core.Playlist{}, err
	}
	preparationStopped = prepareErr != nil || work.Err() != nil
	// No source reads below this boundary. Even catalog metadata/duration and
	// recording-credit diversity use the request-owned frozen batch.
	if request.Progress != nil {
		request.Progress.Report("generation", int64(len(batch.ids)), int64(len(batch.ids)), "Selecting and ordering prepared candidates")
	}
	phase = automaticPhase(ctx, "assembly")
	result, err = a.assembleAutomatic(ctx, batch, candidates, execution, request, references, required, waypoints, recent, seed, &search, graphCandidates)
	phase(len(result.Tracks), err)
	result.Intent = intent
	result.EvidenceCatalogVersion = catalogVersion
	if err != nil {
		return result, err
	}
	if preparationStopped {
		result.Outcome.State = core.OutcomePartial
		result.Outcome.Reasons = append(result.Outcome.Reasons, core.OutcomeReason{Code: "preparation_incomplete", Detail: "Candidate preparation stopped; only complete prepared recordings were considered."})
	}
	return result, nil
}

// Timing stays in the existing opt-in, bounded in-memory diagnostic sink.
// No prompt, recording identity, artist, provider response, or error text is
// included. Missing completion after a start is observable without inventing
// a blocked-stage diagnosis. These values never enter search/replay identity.
func automaticPhase(ctx context.Context, phase string) func(int, error) {
	started := time.Now()
	type timing struct {
		Phase          string `json:"phase"`
		State          string `json:"state"`
		Milliseconds   int64  `json:"milliseconds"`
		Elapsed        int64  `json:"elapsedMilliseconds"`
		Items          int    `json:"items"`
		Error          bool   `json:"error"`
		ContextStopped bool   `json:"contextStopped"`
	}
	emit := func(state string, items int, err error) {
		elapsed := int64(0)
		if generationStarted := ports.GenerationStarted(ctx); !generationStarted.IsZero() {
			elapsed = time.Since(generationStarted).Milliseconds()
		}
		logging.Diagnostic(ctx, "automatic.phase", timing{Phase: phase, State: state, Milliseconds: time.Since(started).Milliseconds(), Elapsed: elapsed, Items: items, Error: err != nil, ContextStopped: ctx.Err() != nil})
	}
	emit("started", 0, nil)
	return func(items int, err error) { emit("completed", items, err) }
}

// Round-robin source/query ranks before the global cap. Raw channel scores are
// never compared across spaces and input slice order cannot favor one artist.
func automaticBalancedPool(pools [][]core.Candidate, limit int, seed int64) []core.Candidate {
	byID := map[string]core.Candidate{}
	buckets := map[string][]string{}
	for pi, pool := range pools {
		for _, c := range pool {
			if c.Track.ID == "" {
				continue
			}
			old, exists := byID[c.Track.ID]
			if !exists {
				old = core.Candidate{Track: c.Track}
			}
			if len(c.Sources) == 0 {
				c.Sources = []core.RetrievalEvidence{{Channel: fmt.Sprint("prepared:", pi), Rank: 1}}
			}
			for _, source := range c.Sources {
				duplicate := false
				for _, s := range old.Sources {
					duplicate = duplicate || s.Channel == source.Channel && s.QueryID == source.QueryID
				}
				if !duplicate {
					old.Sources = append(old.Sources, source)
					key := source.Channel + "\x00" + source.QueryID
					buckets[key] = append(buckets[key], c.Track.ID)
				}
			}
			byID[c.Track.ID] = old
		}
	}
	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rank := func(c core.Candidate, key string) int {
		best := int(^uint(0) >> 1)
		for _, s := range c.Sources {
			if s.Channel+"\x00"+s.QueryID == key {
				best = min(best, max(1, s.Rank))
			}
		}
		return best
	}
	for _, key := range keys {
		sort.Slice(buckets[key], func(i, j int) bool {
			a, b := byID[buckets[key][i]], byID[buckets[key][j]]
			ra, rb := rank(a, key), rank(b, key)
			if ra != rb {
				return ra < rb
			}
			return stableHash(a.Track.ID, seed) < stableHash(b.Track.ID, seed)
		})
	}
	used := map[string]bool{}
	var out []core.Candidate
	for offset := 0; len(out) < limit; offset++ {
		advanced := false
		for _, key := range keys {
			if offset >= len(buckets[key]) {
				continue
			}
			advanced = true
			id := buckets[key][offset]
			if !used[id] {
				used[id] = true
				out = append(out, byID[id])
				if len(out) == limit {
					break
				}
			}
		}
		if !advanced {
			break
		}
	}
	return out
}

var _ ports.RecommendationEngine = (*AutomaticEngine)(nil)
var _ ports.ContextualRecommendationEngine = (*AutomaticEngine)(nil)
