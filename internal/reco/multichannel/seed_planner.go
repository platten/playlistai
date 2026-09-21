package multichannel

import (
	"context"
	"sort"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

const groundedSeedReason = "Selected from catalog recordings against the current request"
const seedCandidateLimit = 128

// planGroundedSeeds searches recordings before asking a model for artist
// suggestions. It does not choose a required opening or change explicit intent.
func (o *Orchestrator) planGroundedSeeds(ctx context.Context, intent core.MusicIntent, request ports.RecommendationRequest, seed int64) (core.MusicIntent, []core.Candidate, error) {
	if !o.enhanced || o.retriever == nil || len(intent.EssentialCriteria) == 0 || hasExplicitRetrievalReference(o.cat, intent) || len(intent.RequiredTracks) > 0 || intent.Mode == core.ModeJourney {
		return intent, nil, nil
	}
	if err := ctx.Err(); err != nil {
		return intent, nil, err
	}
	if len(intent.AnchorAttempts) > 0 {
		var replay []core.Candidate
		for _, anchor := range intent.InferredAnchors {
			if anchor.Reason == groundedSeedReason {
				if meta, ok := o.cat.Meta(anchor.Reference.TrackID); ok {
					replay = append(replay, core.Candidate{Track: meta.Ref})
				}
			}
		}
		if len(replay) == 0 {
			return intent, nil, nil
		}
		ranked, err := o.rankGroundedSeeds(ctx, replay, intent, request.RecentSelections)
		return intent, ranked, err
	}
	query := intent
	query.InferredAnchors = nil
	searchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	go func() {
		select {
		case <-request.StopChecking:
			cancel()
		case <-searchCtx.Done():
		}
	}()
	candidates, err := o.retriever.Retrieve(searchCtx, ports.RetrievalRequest{Intent: query, RecentSelections: request.RecentSelections, Seed: seed})
	if err != nil {
		if ctx.Err() != nil {
			return intent, nil, ctx.Err()
		}
		return intent, nil, nil
	}
	pool := append([]core.Candidate{}, candidates...)
	// Metadata channels and semantic channels contribute to the same bounded
	// pool. Source namespace is never a preference or an eligibility condition.
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Scores.RetrievalFusion != candidates[j].Scores.RetrievalFusion {
			return candidates[i].Scores.RetrievalFusion > candidates[j].Scores.RetrievalFusion
		}
		return core.NormalizeIdentityPart(candidates[i].Track.Display()) < core.NormalizeIdentityPart(candidates[j].Track.Display())
	})
	if len(candidates) > seedCandidateLimit {
		candidates = candidates[:seedCandidateLimit]
	}
	ranked, err := o.rankGroundedSeeds(ctx, candidates, query, request.RecentSelections)
	if err != nil {
		return intent, nil, err
	}
	if err := o.preferViableSeedNeighborhoods(ctx, ranked); err != nil {
		return intent, nil, err
	}
	var selected []core.Candidate
	artists, recordings := map[string]bool{}, map[string]bool{}
	for _, candidate := range ranked {
		artist, key := core.NormalizeIdentityPart(candidate.Track.Artist), core.ProvisionalRecordingKey(candidate.Track)
		if artists[artist] || recordings[key] {
			continue
		}
		artists[artist], recordings[key] = true, true
		selected = append(selected, candidate)
		if len(selected) == 3 {
			break
		}
	}
	if len(selected) == 0 {
		return intent, nil, nil
	}
	o.groundedSeedPool = pool
	intent.InferredAnchors = nil
	version := ""
	if o.resolver != nil {
		version = o.resolver.CatalogVersion()
	}
	for _, candidate := range selected {
		track := candidate.Track
		intent.InferredAnchors = append(intent.InferredAnchors, core.InferredAnchor{
			Reference: core.IntentReference{Kind: core.ReferenceTrack, Query: track.Display(), TrackID: track.ID, Influence: core.InfluencePositive, Resolution: &core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: version, Selected: &core.ResolutionCandidate{Kind: core.ReferenceTrack, EntityID: track.ID, Artist: track.Artist, Title: track.Title, Confidence: 1, Representatives: []core.WeightedTrack{{TrackID: track.ID, Weight: 1}}}}},
			Role:      "retrieval", Reason: groundedSeedReason, Suitability: core.AnchorSuitability{State: candidate.MusicalFit, Score: candidate.Scores.Total, Detail: "Selected using available recording evidence and request relevance. Unknown attributes and uncalibrated audio similarities remain unverified."},
		})
	}
	intent.AnchorAttempts = append([]core.InferredAnchor(nil), intent.InferredAnchors...)
	return intent, selected, nil
}

func (o *Orchestrator) rankGroundedSeeds(ctx context.Context, candidates []core.Candidate, intent core.MusicIntent, recent []core.TrackRef) ([]core.Candidate, error) {
	valid := make([]core.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, exists := o.cat.Meta(candidate.Track.ID); exists && o.metadataEligible(candidate.Track, intent) && o.seedPeriodKnown(candidate.Track, intent) {
			valid = append(valid, candidate)
		}
	}
	eligible := newEligibility(intent, nil, nil)
	eligible.excludeRecent(recent)
	var err error
	valid, err = eligible.filter(ctx, valid, false)
	if err != nil {
		return nil, err
	}
	if o.features != nil {
		valid, _, err = filterSemanticConstraints(ctx, o.features, valid, intent.HardConstraints)
		if err != nil {
			return nil, err
		}
	}
	// Uncalibrated similarity cannot manufacture a verified starting point.
	// Use existing strict criterion evidence, including typed pack annotations.
	strict := *o
	strict.bestAvailable = false
	strict.audioSession = nil
	grounded := valid[:0]
	for _, candidate := range valid {
		groups := map[string]bool{}
		contradiction := false
		for _, criterion := range intent.EssentialCriteria {
			key := criterion.Group
			if key == "" {
				key = criterion.Kind + ":" + criterion.Value + ":" + criterion.Scope
			}
			state := strict.bestCriterion(ctx, candidate.Track.ID, criterion)
			contradiction = contradiction || state == core.EvidenceMismatch && criterion.Group == ""
			groups[key] = groups[key] || state == core.EvidenceMatch
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		match := true
		for _, state := range groups {
			match = match && state
		}
		candidate.MusicalFit = core.EvidenceMatch
		prominence := false
		for _, preference := range intent.Preferences.Instrumentation {
			prominence = prominence || preference.Influence != core.InfluenceNegative && preference.Degree == "mostly"
		}
		if prominence {
			candidate.MusicalFit = core.EvidenceUnknown
		}
		if !match && !contradiction && o.bestAvailable {
			if _, ok := o.packedOnly(ctx, candidate.Track.ID, intent); ok && o.packedSupport(ctx, candidate.Track.ID) {
				match = true
				candidate.MusicalFit = core.EvidenceUnknown
			}
		}
		if prominence && !o.bestAvailable {
			match = false
		}
		if match {
			grounded = append(grounded, candidate)
		}
	}
	valid = grounded
	if len(valid) == 0 {
		return nil, nil
	}
	valid, err = o.rankCandidates(ctx, valid, ports.RankRequest{Intent: intent, EnhancedAudio: o.enhancedSnapshot})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(valid, func(i, j int) bool {
		if valid[i].Scores.Total != valid[j].Scores.Total {
			return valid[i].Scores.Total > valid[j].Scores.Total
		}
		left, right := core.NormalizeIdentityPart(valid[i].Track.Display()), core.NormalizeIdentityPart(valid[j].Track.Display())
		if left != right {
			return left < right
		}
		return valid[i].Track.ID < valid[j].Track.ID
	})
	return valid, nil
}

func (o *Orchestrator) seedPeriodKnown(track core.TrackRef, intent core.MusicIntent) bool {
	for _, period := range intent.Temporal {
		if period.Scope != "" && period.Scope != "playlist" {
			continue
		}
		metadata, ok := o.knowledgeTrack(track.ID)
		if !ok {
			return false
		}
		first, last := metadata.CompositionStartYear, metadata.CompositionEndYear
		if period.Basis == "original_release" {
			first = yearFromDate(metadata.OriginalReleaseDate)
			last = first
		}
		if first <= 0 || last < period.StartYear || first > period.EndYear {
			return false
		}
	}
	return true
}

// refineArtistRepresentatives retains the resolved entity and reference role;
// only its recording representatives change. Explicit recording IDs and
// required tracks are never substituted.
func (o *Orchestrator) refineArtistRepresentatives(ctx context.Context, intent core.MusicIntent) (core.MusicIntent, error) {
	if !o.enhanced {
		return intent, nil
	}
	indexed, ok := o.cat.(ports.ArtistRecordingCatalog)
	if !ok {
		return intent, nil
	}
	type refinement struct {
		ref core.IntentReference
		err error
	}
	refined := map[string]refinement{}
	refine := func(ref core.IntentReference, scope string) (core.IntentReference, error) {
		if ref.Kind != core.ReferenceArtist || ref.Influence == core.InfluenceNegative || ref.Resolution == nil || ref.Resolution.Selected == nil || ref.Resolution.Status != core.ResolutionResolved {
			return ref, nil
		}
		key := referenceKey(ref) + "\x00" + scope
		if prior, exists := refined[key]; exists {
			return prior.ref, prior.err
		}
		remember := func(result core.IntentReference, err error) (core.IntentReference, error) {
			refined[key] = refinement{ref: result, err: err}
			return result, err
		}
		scoped := seedStageIntent(intent, scope)
		// Ordinary reference representatives already carry resolver weights and
		// seed retrieval directly. Discography refinement is useful only when an
		// endpoint must become output or a scoped musical criterion can choose a
		// demonstrably better recording; otherwise it spends bounded generation
		// time without changing the request contract.
		if scope != "journey_start" && scope != "journey_end" && len(scoped.EssentialCriteria) == 0 {
			return remember(ref, nil)
		}
		tracks, err := indexed.ArtistRecordings(ctx, ref.Resolution.Selected.Artist)
		if err != nil {
			return remember(ref, err)
		}
		// With no stage description and a large discography, the resolver's
		// representative is a better endpoint contract than an arbitrary bounded
		// medoid. Small artist pools can still use the established cohesion rule.
		if (scope == "journey_start" || scope == "journey_end") && len(scoped.EssentialCriteria) == 0 && len(tracks) > 16 {
			return remember(ref, nil)
		}
		// Composite catalogs sort stable IDs, which can put hundreds of base
		// recordings before the installed pack. Collect affirmative recording
		// matches before applying the bound; stop after a small useful set rather
		// than probing every recording for optional preview/embedding evidence.
		if len(scoped.EssentialCriteria) > 0 {
			grounded := make([]core.TrackRef, 0, 12)
			for _, track := range tracks {
				matched := true
				for _, criterion := range scoped.EssentialCriteria {
					matched = matched && o.bestCriterion(ctx, track.ID, criterion) == core.EvidenceMatch
				}
				if matched {
					grounded = append(grounded, track)
					if len(grounded) == 12 {
						break
					}
				}
			}
			if len(grounded) > 0 {
				tracks = grounded
			}
		}
		if len(tracks) > seedCandidateLimit {
			tracks = tracks[:seedCandidateLimit]
		}
		ranked, err := o.rankGroundedSeeds(ctx, candidatesForTracks(tracks), scoped, nil)
		if err != nil {
			return remember(ref, err)
		}
		if len(ranked) == 0 {
			return remember(ref, nil)
		}
		if len(scoped.EssentialCriteria) == 0 {
			// No requested substyle: use same-artist neighborhood cohesion as
			// the representative heuristic, not resolver row order.
			for i := range ranked {
				ranked[i].Scores.Total = 0
			}
		}
		if err := o.preferViableSeedNeighborhoods(ctx, ranked); err != nil {
			return remember(ref, err)
		}
		resolution := *ref.Resolution
		selected := *resolution.Selected
		selected.Representatives = nil
		for _, candidate := range ranked[:min(3, len(ranked))] {
			selected.Representatives = append(selected.Representatives, core.WeightedTrack{TrackID: candidate.Track.ID, Weight: 1 / float64(min(3, len(ranked)))})
		}
		resolution.Selected = &selected
		ref.Resolution = &resolution
		ref.TrackID = selected.Representatives[0].TrackID
		return remember(ref, nil)
	}
	scopeFor := func(ref core.IntentReference) string {
		if intent.Start != nil && referenceKey(ref) == referenceKey(*intent.Start) {
			return "journey_start"
		}
		if intent.Destination != nil && referenceKey(ref) == referenceKey(*intent.Destination) {
			return "journey_end"
		}
		if intent.Mode == core.ModeJourney {
			return "journey_via"
		}
		return "playlist"
	}
	intent.References = append([]core.IntentReference(nil), intent.References...)
	for i, ref := range intent.References {
		updated, err := refine(ref, scopeFor(ref))
		if err != nil {
			return intent, err
		}
		intent.References[i] = updated
	}
	if intent.Start != nil {
		updated, err := refine(*intent.Start, "journey_start")
		if err != nil {
			return intent, err
		}
		intent.Start = &updated
	}
	if intent.Destination != nil {
		updated, err := refine(*intent.Destination, "journey_end")
		if err != nil {
			return intent, err
		}
		intent.Destination = &updated
	}
	intent.Journey.Waypoints = append([]core.IntentReference(nil), intent.Journey.Waypoints...)
	for i, ref := range intent.Journey.Waypoints {
		updated, err := refine(ref, scopeFor(ref))
		if err != nil {
			return intent, err
		}
		intent.Journey.Waypoints[i] = updated
	}
	return intent, nil
}

func seedStageIntent(intent core.MusicIntent, scope string) core.MusicIntent {
	result := intent
	result.EssentialCriteria = nil
	result.Temporal = nil
	include := func(s string) bool { return s == "" || s == "playlist" || s == scope }
	for _, c := range intent.EssentialCriteria {
		if include(c.Scope) {
			c.Scope = "playlist"
			result.EssentialCriteria = append(result.EssentialCriteria, c)
		}
	}
	for _, p := range intent.Temporal {
		if include(p.Scope) {
			p.Scope = "playlist"
			result.Temporal = append(result.Temporal, p)
		}
	}
	filter := func(preferences []core.IntentPreference) []core.IntentPreference {
		var out []core.IntentPreference
		for _, p := range preferences {
			if include(p.Scope) {
				p.Scope = "playlist"
				out = append(out, p)
			}
		}
		return out
	}
	result.Preferences.Genres = filter(intent.Preferences.Genres)
	result.Preferences.Styles = filter(intent.Preferences.Styles)
	result.Preferences.Moods = filter(intent.Preferences.Moods)
	result.Preferences.Instrumentation = filter(intent.Preferences.Instrumentation)
	result.Preferences.VocalPreferences = filter(intent.Preferences.VocalPreferences)
	result.Preferences.TextureDescriptions = filter(intent.Preferences.TextureDescriptions)
	return result
}

// Compare the top twelve request-fit candidates with at most sixteen viable
// neighbors from this request's already-filtered union. Neighborhood support
// only breaks equal relevance scores; it cannot rescue a mismatching seed.
func (o *Orchestrator) preferViableSeedNeighborhoods(ctx context.Context, ranked []core.Candidate) error {
	type vector struct {
		dense, library, clap []float32
		space, clapSpace     string
	}
	vectors := make([]vector, len(ranked))
	for i, c := range ranked {
		if err := ctx.Err(); err != nil {
			return err
		}
		if v, ok := o.cat.Vectors(c.Track.ID); ok {
			vectors[i].dense = v.Audio
		}
		if library, ok := o.cat.(ports.LibraryAudioCatalog); ok {
			v, found, err := library.LibraryVector(ctx, c.Track.ID)
			if err != nil {
				return err
			}
			if found {
				vectors[i].library = v.Values
				vectors[i].space = v.Source.SpaceID
			}
		}
		if library, ok := o.cat.(ports.LibrarySemanticCatalog); ok {
			v, found, err := library.LibraryCLAPVector(ctx, c.Track.ID)
			if err != nil {
				return err
			}
			if found {
				vectors[i].clap = v.Values
				vectors[i].clapSpace = v.Source.SpaceID
			}
		}
	}
	support := map[string]float64{}
	for i := range ranked[:min(12, len(ranked))] {
		var neighbors []float64
		for j := range ranked {
			if i == j {
				continue
			}
			a, b := vectors[i], vectors[j]
			similarity := 0.0
			if len(a.library) > 0 && a.space != "" && a.space == b.space && len(a.library) == len(b.library) {
				similarity = cosine(a.library, b.library)
			} else if len(a.clap) > 0 && a.clapSpace != "" && a.clapSpace == b.clapSpace && len(a.clap) == len(b.clap) {
				similarity = cosine(a.clap, b.clap)
			} else if len(a.dense) > 0 && len(a.dense) == len(b.dense) {
				similarity = cosine(a.dense, b.dense)
			}
			if similarity > 0 {
				neighbors = append(neighbors, similarity)
			}
		}
		sort.Sort(sort.Reverse(sort.Float64Slice(neighbors)))
		for _, v := range neighbors[:min(16, len(neighbors))] {
			support[ranked[i].Track.ID] += v
		}
	}
	sort.SliceStable(ranked[:min(12, len(ranked))], func(i, j int) bool {
		if ranked[i].Scores.Total != ranked[j].Scores.Total {
			return ranked[i].Scores.Total > ranked[j].Scores.Total
		}
		return support[ranked[i].Track.ID] > support[ranked[j].Track.ID]
	})
	return nil
}

// permitGroundedSeeds keeps automatically selected retrieval anchors eligible
// as ordinary playlist content. Required/user-named seeds retain their rules.
func permitGroundedSeeds(e *eligibility, seeds []core.Candidate) {
	for _, candidate := range seeds {
		delete(e.excludedIDs, candidate.Track.ID)
		delete(e.excludedRecordings, core.ProvisionalRecordingKey(candidate.Track))
	}
}
