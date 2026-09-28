package multichannel

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func stableHash(id string, seed int64) uint64 {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s", seed, id)))
	return binary.LittleEndian.Uint64(sum[:8])
}

func automaticScopes(intent core.MusicIntent) []string {
	var scopes []string
	for _, c := range journeyStageCriteria(intent) {
		if !strings.HasPrefix(c.Scope, "journey_") {
			continue
		}
		found := false
		for _, s := range scopes {
			found = found || s == c.Scope
		}
		if !found {
			scopes = append(scopes, c.Scope)
		}
	}
	return scopes
}

// Factual constraints use positive supplied identity/date evidence. Unknown
// dates cannot satisfy an explicit date range. Musical requirements are handled
// separately as estimated fit; they never alter these factual checks.
func automaticFacts(b *automaticBatch, track core.TrackRef, intent core.MusicIntent, scope string) bool {
	meta, exists := b.meta[track.ID]
	if !exists {
		return false
	}
	recording := b.recordings[track.ID]
	if recording.IdentityStatus == core.ResolutionAmbiguous {
		return false
	}
	if !automaticArtistFacts(meta, recording, intent) {
		return false
	}
	for _, name := range intent.Constraints.ArtistsExclude {
		if identityMentions(track.Artist, name) {
			return false
		}
		for _, artist := range recording.AllArtists {
			if identityMentions(artist, name) {
				return false
			}
		}
		for _, a := range meta.Annotations {
			if (a.Kind == "performer" || a.Kind == "artist_credit") && identityMentions(a.Value, name) {
				return false
			}
		}
	}
	for _, constraint := range intent.HardConstraints {
		if constraint.Kind == "require_album" {
			if _, ok := requiredAlbumMembers(intent)[track.ID]; !ok {
				return false
			}
		}
	}
	for _, period := range intent.Temporal {
		s := period.Scope
		if s == "" {
			s = "playlist"
		}
		if s != "playlist" && s != scope {
			continue
		}
		first, last := recording.CompositionStartYear, recording.CompositionEndYear
		if period.Basis == "original_release" {
			first = yearFromDate(recording.OriginalReleaseDate)
			last = first
		}
		if first <= 0 || last <= 0 || first < period.StartYear || last > period.EndYear {
			return false
		}
	}
	return true
}

func (a *AutomaticEngine) assembleAutomatic(ctx context.Context, b *automaticBatch, candidates []core.Candidate, intent core.MusicIntent, request ports.RecommendationRequest, references, required, waypoints, recent []core.TrackRef, seed int64, search *core.SearchSnapshot, graphCandidates []core.Candidate) (core.Playlist, error) {
	result := outcomePlaylist(intent, intent.Seed, core.OutcomeFulfilled, nil)
	clauses := audio.Clauses(intent)
	scopes := automaticScopes(intent)
	eligible := newEligibility(intent, references, required)
	eligible.excludeRecent(recent)
	if err := eligible.validateRequired(required, intent.Constraints.ExcludeSeedArtists); err != nil {
		return result, err
	}
	fits := map[string]core.AutomaticFitAssessment{}
	referenceGate := newAutomaticReferenceGate(ctx, b, intent, graphCandidates)
	referenceMatches, referenceDetails := map[string]bool{}, map[string]string{}
	byCandidateID := map[string]core.Candidate{}
	for _, c := range candidates {
		byCandidateID[c.Track.ID] = c
	}
	// Build every assessment once. Repeated admission/coverage/outcome reads are
	// pure map lookups of this exact policy result.
	for _, id := range b.ids {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		fits[id] = automaticAssessment(b, id, clauses, a.cfg.SemanticMinimumScore)
		c := byCandidateID[id]
		c.Track = b.meta[id].Ref
		referenceMatches[id], referenceDetails[id] = referenceGate.match(c)
	}
	automaticRankEstimates(fits)
	membership := make([]map[string]bool, len(scopes))
	for i := range membership {
		membership[i] = map[string]bool{}
	}
	allowed := func(track core.TrackRef) bool {
		if !automaticFacts(b, track, intent, "playlist") || !automaticScopeFits(fits[track.ID], "playlist") || !referenceMatches[track.ID] {
			return false
		}
		if len(scopes) == 0 {
			return true
		}
		any := false
		for i, s := range scopes {
			fitsStage := automaticStageFits(fits[track.ID], s, scopes) && automaticFacts(b, track, intent, s)
			membership[i][track.ID] = fitsStage
			any = any || fitsStage
		}
		return any
	}
	for _, track := range required {
		if !allowed(track) {
			result.Outcome = core.GenerationOutcome{State: core.OutcomeNeedsClarification, Reasons: []core.OutcomeReason{{Code: "required_track_conflict", Detail: "A required recording lacks the requested factual constraints or sufficiently supported musical fit.", Criterion: track.Display()}}}
			return result, nil
		}
	}
	filtered, err := eligible.filter(ctx, candidates, intent.Constraints.ExcludeSeedArtists)
	if err != nil {
		return result, err
	}
	eligibleIDs := map[string]bool{}
	for _, c := range filtered {
		eligibleIDs[c.Track.ID] = true
	}
	for _, c := range candidates {
		fit := fits[c.Track.ID]
		c.FitAssessment = &fit
		c.Decision = "rejected"
		c.DecisionReasons = []string{"recording_identity_or_explicit_exclusion"}
		search.Candidates = append(search.Candidates, c)
	}
	search.Considered = len(candidates)
	candidates = filtered
	var ranked []core.Candidate
	maxRRF := 0.0
	for _, c := range candidates {
		sum := 0.0
		for _, s := range c.Sources {
			sum += 1 / (a.cfg.ReciprocalRankConstant + float64(max(1, s.Rank)))
		}
		maxRRF = math.Max(maxRRF, sum)
	}
	positive := positiveReferenceVectorsContext(ctx, b, intent)
	negative := negativeReferenceVectorsContext(ctx, b, intent)
	for _, c := range candidates {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		meta, ok := b.meta[c.Track.ID]
		if !ok {
			continue
		}
		c.Track = meta.Ref
		if !allowed(c.Track) {
			continue
		}
		fit := fits[c.Track.ID]
		// Overall fit refers to a stage the recording can occupy. Other stage
		// comparisons remain in the clause ledger, without lowering this stage.
		if len(scopes) > 0 {
			fit.State = core.AutomaticUnknown
			for i, scope := range scopes {
				if membership[i][c.Track.ID] {
					groups := append(automaticGroups(fit, "playlist", false), automaticGroups(fit, scope, false)...)
					state := automaticOverall(groups)
					if automaticStateValue(state) > automaticStateValue(fit.State) {
						fit.State = state
					}
				}
			}
		}
		descriptive, descriptionKnown := automaticScopeScore(fit, "playlist")
		for _, scope := range scopes {
			if v, known := automaticScopeScore(fit, scope); known && v > descriptive {
				descriptive, descriptionKnown = v, true
			}
		}
		similarity, similarityKnown := 0.0, false
		if v, ok := b.mert[c.Track.ID]; ok {
			for _, ref := range references {
				if anchor, exists := b.mert[ref.ID]; exists {
					if score, compatible := libraryCosine(v, anchor); compatible {
						similarity = math.Max(similarity, score)
						similarityKnown = true
					}
				}
			}
		}
		c.Scores.LibraryMERT, c.Available.LibraryMERT = similarity, similarityKnown
		if v, ok := b.Vectors(c.Track.ID); ok {
			audioScore, audioOK := weightedSpaceSimilarity(v.Audio, positive, true)
			trackScore, trackOK := weightedSpaceSimilarity(v.Track, positive, false)
			if audioOK {
				similarity = math.Max(similarity, audioScore)
				similarityKnown = true
			}
			if trackOK {
				similarity = math.Max(similarity, trackScore)
				similarityKnown = true
			}
			c.Scores.AudioSeedAffinity, c.Available.AudioSeedAffinity = audioScore, audioOK
			c.Scores.CooccurrenceAffinity, c.Available.CooccurrenceAffinity = trackScore, trackOK
		}
		if !descriptionKnown && (!similarityKnown || similarity <= 0) && (!referenceGate.active() || !referenceMatches[c.Track.ID]) {
			continue
		} // never count padding from unknowns
		negativeScore, negativeKnown := 0.0, false
		if v, ok := b.Vectors(c.Track.ID); ok {
			negativeScore, negativeKnown = negativeSeedAffinity(v, negative, intent.Controls.AudioWeight, intent.Controls.CooccurrenceWeight)
		}
		c.Scores.RequestNegativeMatch, c.Available.RequestNegativeMatch = negativeScore, negativeKnown
		score := descriptive
		if len(clauses) == 0 {
			score = 0 // compatible reference channels are fused by rank below
			fit.State = core.AutomaticPlausible
			fit.Detail = "Estimated reference similarity in compatible prepared spaces; no verified sonic claim."
			if referenceGate.active() {
				fit.Detail = referenceDetails[c.Track.ID]
			}
		} else if similarityKnown {
			score = .85 * descriptive
		}
		if fit.State == core.AutomaticConflicting { // optional opposition is a rank penalty, strict groups were screened above
			score -= .1
		}
		rrf := 0.0
		for _, s := range c.Sources {
			rrf += 1 / (a.cfg.ReciprocalRankConstant + float64(max(1, s.Rank)))
		}
		if maxRRF > 0 {
			rrf /= maxRRF
		}
		c.Scores.RetrievalFusion, c.Available.RetrievalFusion = rrf, len(c.Sources) > 0
		// Familiarity is subordinate to fit and bounded. Missing popularity remains
		// unavailable, and is never equated with poor musical quality.
		familiarity := b.familiarity[c.Track.ID]
		c.Scores.RequestFit = clamp(.9*score+.07*rrf+.03*familiarity-.25*math.Max(0, negativeScore), 0, 1)
		c.Available.RequestFit = true
		c.Scores.Total = c.Scores.RequestFit
		c.FitTier = fitClose
		if fit.State == core.AutomaticStrong {
			c.FitTier = fitStrong
		}
		c.FitAssessment = &fit
		c.Decision = "accepted"
		c.MusicalFit = core.EvidenceUnknown
		fits[c.Track.ID] = fit
		ranked = append(ranked, c)
	}
	referenceRanks := automaticReferenceRanks(ranked, b)
	for i := range ranked {
		weight := .15
		if len(clauses) == 0 {
			weight = 1
		}
		ranked[i].Scores.RequestFit = clamp(ranked[i].Scores.RequestFit+.9*weight*referenceRanks[ranked[i].Track.ID], 0, 1)
		ranked[i].Scores.Total = ranked[i].Scores.RequestFit
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		left, right := ranked[i], ranked[j]
		if left.FitTier != right.FitTier {
			return left.FitTier == fitStrong
		}
		if left.Scores.Total != right.Scores.Total {
			return left.Scores.Total > right.Scores.Total
		}
		return stableHash(left.Track.ID, seed) < stableHash(right.Track.ID, seed)
	})
	byID := map[string]core.Candidate{}
	for _, c := range ranked {
		byID[c.Track.ID] = c
	}
	referenceRejected := false
	for i, c := range search.Candidates {
		if accepted, ok := byID[c.Track.ID]; ok {
			search.Candidates[i] = accepted
		} else if eligibleIDs[c.Track.ID] {
			search.Candidates[i].DecisionReasons = []string{"insufficient_or_conflicting_prepared_fit"}
			if !referenceMatches[c.Track.ID] {
				search.Candidates[i].DecisionReasons = []string{"reference_similarity_uncorroborated"}
				referenceRejected = true
			}
		}
	}
	search.EligibleCandidates = append([]core.Candidate(nil), ranked...)
	search.Eligible = len(ranked)
	cfg := a.cfg
	cfg.LibraryEvidenceEnabled = true
	if referenceGate.active() {
		// The reference gate supplies categorical eligibility, including exact
		// requested identities without vectors. A legacy numeric score floor
		// must not demand a fabricated cosine from those admitted recordings.
		cfg.SelectionMinimumRelevance = 0
	}
	reserved, pool := automaticReserve(ranked, required, intent, fits, membership)
	fixed := append([]core.TrackRef(nil), required...)
	for _, c := range reserved {
		fixed = append(fixed, c.Track)
	}
	count := intent.Count
	if intent.DurationSeconds > 0 && !intent.HasExplicitTrackCount() {
		count = core.MaxCount
	}
	selected, err := NewSelector(b, cfg).Select(ctx, pool, ports.SelectionRequest{Intent: intent, Required: fixed, Waypoints: waypoints, RecentSelections: recent, Count: max(0, count-len(fixed))})
	if err != nil {
		return result, err
	}
	chosen := append(append([]core.Candidate(nil), reserved...), selected.Candidates...)
	helper := &Orchestrator{cat: b, requestContext: ctx}
	if intent.DurationSeconds > 0 {
		chosen, err = helper.fitDuration(ctx, intent, chosen, ranked, required, len(reserved))
		if err != nil {
			return result, err
		}
	}
	sequence, err := NewSequencer(b, cfg).Sequence(ctx, ports.SequenceRequest{Intent: intent, Candidates: chosen, Required: required, Waypoints: waypoints, ReferenceAnchors: references, RecentSelections: recent, Trajectory: NewWaypointTrajectory(b, waypoints), Seed: seed, CategoryStages: membership})
	if err != nil {
		return result, err
	}
	result.Tracks, result.Rationale = sequence.Tracks, sequence.Rationale
	for i := range result.Rationale {
		reason := &result.Rationale[i]
		reason.Detail = "Selected and ordered from prepared evidence; musical fit is an estimate."
		reason.Evidence = nil
		if c, ok := byID[reason.TrackID]; ok {
			reason.Evidence = []core.ComponentEvidence{{Component: "automatic_fit", Score: c.Scores.RequestFit, Weight: 1, Available: true, Detail: core.AutomaticFitPolicyVersion + "; categorical estimated agreement, not a probability"}, {Component: "retrieval_fusion", Score: c.Scores.RetrievalFusion, Available: c.Available.RetrievalFusion, Detail: "Reciprocal rank across prepared retrieval channels"}}
		}
	}

	result.Notices = append(append([]core.PlaylistNotice(nil), selected.Notices...), sequence.Notices...)
	result.Duration = helper.assessDurationContext(ctx, result.Tracks, intent)
	for _, track := range result.Tracks {
		fit := fits[track.ID]
		result.FitAssessments = append(result.FitAssessments, core.AutomaticTrackFit{TrackID: track.ID, Assessment: fit})
		if request.OnSuggested != nil {
			request.OnSuggested(track)
		}
	}
	result.Outcome = automaticOutcome(result, intent, fits, required, membership)
	if referenceRejected && len(result.Tracks) < intent.Count {
		result.Outcome.State = core.OutcomePartial
		result.Outcome.Reasons = append(result.Outcome.Reasons, core.OutcomeReason{Code: "reference_similarity_unconfirmed", Detail: "Fewer recordings had the requested identity or prepared artist relationships and calibrated seed-audio support."})
	}
	if core.RequiresOtherArtists(intent) && !includesOtherArtistsContext(ctx, b, intent, result.Tracks) {
		result.Outcome.State = core.OutcomePartial
		result.Outcome.Reasons = append(result.Outcome.Reasons, core.OutcomeReason{Code: "other_artists_missing", Detail: "No suitable recording by another artist was retained."})
	}
	return result, ctx.Err()
}

func automaticReserve(ranked []core.Candidate, fixed []core.TrackRef, intent core.MusicIntent, fits map[string]core.AutomaticFitAssessment, stages []map[string]bool) ([]core.Candidate, []core.Candidate) {
	units := playlistCoverageUnits(intent.EssentialCriteria)
	matches := func(id string, u int) bool {
		if u >= len(units) {
			return stages[u-len(units)][id]
		}
		for _, criterion := range units[u] {
			for _, c := range fits[id].Clauses {
				if c.Clause.Kind == criterion.Kind && c.Clause.Text == criterion.Value && c.Clause.Scope == criterion.Scope && c.State == core.AutomaticStrong {
					return true
				}
			}
		}
		return false
	}
	covered := append(make([]bool, len(units)), make([]bool, len(stages))...)
	used := map[string]bool{}
	for _, track := range fixed {
		used[core.ProvisionalRecordingKey(track)] = true
		for i := range covered {
			covered[i] = covered[i] || matches(track.ID, i)
		}
	}
	var reserved []core.Candidate
	limit := intent.Count
	if intent.DurationSeconds > 0 && !intent.HasExplicitTrackCount() {
		limit = core.MaxCount
	}
	for len(fixed)+len(reserved) < limit {
		best, gain := -1, 0
		for i, c := range ranked {
			if used[core.ProvisionalRecordingKey(c.Track)] {
				continue
			}
			g := 0
			for j, done := range covered {
				if !done && matches(c.Track.ID, j) {
					g++
				}
			}
			if g > gain {
				best, gain = i, g
			}
		}
		if best < 0 {
			break
		}
		c := ranked[best]
		used[core.ProvisionalRecordingKey(c.Track)] = true
		reserved = append(reserved, c)
		for j := range covered {
			covered[j] = covered[j] || matches(c.Track.ID, j)
		}
	}
	var remaining []core.Candidate
	for _, c := range ranked {
		if !used[core.ProvisionalRecordingKey(c.Track)] {
			remaining = append(remaining, c)
		}
	}
	return reserved, remaining
}

func automaticOutcome(result core.Playlist, intent core.MusicIntent, fits map[string]core.AutomaticFitAssessment, required []core.TrackRef, stages []map[string]bool) core.GenerationOutcome {
	out := core.GenerationOutcome{State: core.OutcomeFulfilled}
	reason := func(code, detail string) {
		out.State = core.OutcomePartial
		out.Reasons = append(out.Reasons, core.OutcomeReason{Code: code, Detail: detail})
	}
	if len(result.Tracks) < intent.Count && (intent.DurationSeconds <= 0 || intent.HasExplicitTrackCount()) {
		reason("insufficient_supported_candidates", "Prepared evidence supplied fewer suitable recordings than requested.")
	}
	ids := map[string]bool{}
	matched := map[string]int{}
	softUnknown := false
	for _, t := range result.Tracks {
		ids[t.ID] = true
		for _, c := range fits[t.ID].Clauses {
			if c.State == core.AutomaticStrong {
				matched[criterionKey(core.MusicalCriterion{Kind: c.Clause.Kind, Value: c.Clause.Text, Scope: c.Clause.Scope})]++
			}
			if !automaticStrictClause(c.Clause) && c.State != core.AutomaticStrong {
				softUnknown = true
			}
		}
	}
	for _, track := range required {
		if !ids[track.ID] {
			reason("required_track_missing", "A required recording could not be retained with the other constraints.")
			break
		}
	}
	for _, r := range playlistCoverageReasons(intent.EssentialCriteria, matched) {
		r.Detail = "No selected recording has strong estimated fit for this requested genre coverage."
		out.State = core.OutcomePartial
		out.Reasons = append(out.Reasons, r)
	}
	if len(stages) > 0 {
		// An ordered subsequence must represent every stage, and every intervening
		// recording must fit its assigned stage. Sequencer output is rechecked.
		stage := 0
		for _, track := range result.Tracks {
			if stage >= len(stages) {
				break
			}
			if !stages[stage][track.ID] {
				if stage+1 < len(stages) && stages[stage+1][track.ID] {
					stage++
				} else {
					stage = -1
					break
				}
			}
		}
		represented := 0
		for _, track := range result.Tracks {
			if represented < len(stages) && stages[represented][track.ID] {
				represented++
			}
		}
		if stage != len(stages)-1 || represented < len(stages) {
			reason("journey_incomplete", "The output does not completely represent the requested ordered musical stages.")
		}
	}
	if result.Duration != nil && result.Duration.State != core.EvidenceMatch {
		reason("duration_unconfirmed", "Full-recording duration does not confirm the requested playlist duration.")
	}
	if softUnknown {
		reason("descriptive_fit_estimated", "Best estimates — some qualities are unconfirmed")
	}
	if len(result.Tracks) > 0 {
		for _, ref := range intent.References {
			if ref.Influence == core.InfluencePositive && ref.Strength == "preferred" {
				reason("reference_fit_estimated", "Artist relationships and audio similarity guide discovery; musical resemblance is an estimate.")
				break
			}
		}
	}
	for _, unsupported := range intent.Unsupported {
		if !automaticRequirementSupported(result.Tracks, intent, fits, unsupported) {
			reason("unsupported_request", "Additional requested wording is retained but unsupported by prepared evidence.")
			break
		}
	}
	return out
}

// Supersede only the exact static registry annotation, leaving source intent
// unchanged. A same-text parser/model unsupported request remains unsupported.
func automaticRequirementSupported(tracks []core.TrackRef, intent core.MusicIntent, fits map[string]core.AutomaticFitAssessment, u core.UnsupportedRequirement) bool {
	if len(tracks) == 0 {
		return false
	}
	found := false
	for _, constraint := range intent.HardConstraints {
		switch constraint.Kind {
		case "exclude_style", "require_style", "exclude_vocals", "exclude_vocal", "require_instrumental", "require_vocals", "require_artist", "require_album":
		default:
			continue
		}
		text := constraint.Value
		if len(constraint.Evidence) > 0 && constraint.Evidence[0].Text != "" {
			text = constraint.Evidence[0].Text
		}
		if u.Reason != "the current catalog cannot enforce "+constraint.Kind || u.Text != strings.TrimSpace(text) || !slices.Equal(u.Evidence, constraint.Evidence) {
			continue
		}
		found = true
		if constraint.Kind == "require_artist" || constraint.Kind == "require_album" {
			continue
		} // output already passed factual membership checks
		wanted := audio.Clauses(core.MusicIntent{HardConstraints: []core.HardConstraint{constraint}})
		if len(wanted) == 0 {
			return false
		}
		for _, track := range tracks {
			for _, clause := range wanted {
				supported := false
				for _, c := range fits[track.ID].Clauses {
					if c.Clause.Kind == clause.Kind && c.Clause.Text == clause.Text && c.Clause.Negative == clause.Negative && c.State == core.AutomaticStrong {
						supported = true
					}
				}
				if !supported {
					return false
				}
			}
		}
	}
	return found
}

func automaticArtistFacts(meta core.TrackMeta, recording core.EnrichedTrack, intent core.MusicIntent) bool {
	ids := map[string]bool{}
	for _, id := range recording.ArtistIDs {
		if validArtistMBID(id) {
			ids[strings.ToLower(id)] = true
		}
	}
	for _, credit := range catalogArtistIdentities(meta.Annotations) {
		if validArtistMBID(credit.id) {
			ids[strings.ToLower(credit.id)] = true
		}
	}
	artistID := func(ref core.IntentReference) string {
		if ref.Grounding != nil {
			if selected, ok := ref.Grounding.DecidedArtist(); ok && validArtistMBID(selected.ID) {
				return strings.ToLower(selected.ID)
			}
		}
		if ref.Resolution != nil && ref.Resolution.Selected != nil && validArtistMBID(ref.Resolution.Selected.EntityID) {
			return strings.ToLower(ref.Resolution.Selected.EntityID)
		}
		return ""
	}
	for _, ref := range explicitArtistReferences(intent) {
		if ref.Kind == core.ReferenceArtist && ref.Influence == core.InfluenceNegative {
			if id := artistID(ref); id != "" && ids[id] {
				return false
			}
		}
	}
	for _, constraint := range intent.HardConstraints {
		if constraint.Kind != "require_artist" {
			continue
		}
		matched := false
		for _, ref := range intent.References {
			if ref.Kind != core.ReferenceArtist || ref.Influence == core.InfluenceNegative || !strings.EqualFold(ref.Query, constraint.Value) {
				continue
			}
			if id := artistID(ref); id != "" {
				matched = ids[id]
			} else {
				artist, ok := requiredArtist(intent, constraint.Value)
				matched = ok && core.NormalizeIdentityPart(meta.Ref.Artist) == core.NormalizeIdentityPart(artist)
			}
			break
		}
		if !matched {
			return false
		}
	}
	return true
}
