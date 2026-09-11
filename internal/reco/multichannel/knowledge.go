package multichannel

import (
	"context"
	"strconv"
	"strings"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
)

func (o *Orchestrator) knowledgeTrack(id string) (core.EnrichedTrack, bool) {
	if o.knowledge != nil {
		for _, track := range o.knowledge.Tracks {
			if track.Ref.ID == id {
				return track, true
			}
		}
	}
	return core.EnrichedTrack{}, false
}

func (o *Orchestrator) bestCriterion(ctx context.Context, id string, c core.MusicalCriterion) core.EvidenceState {
	if o.audioSession != nil {
		if state := o.audioSession.Criterion(id, c); state != core.EvidenceUnknown && state != "" {
			return state
		}
	}
	if o.features != nil {
		if features, ok, err := o.features.Features(ctx, id); err == nil && ok {
			if c.Kind == "genre" {
				graph := core.GenreGraph{}
				if o.knowledge != nil {
					graph = o.knowledge.Graph
				}
				for _, v := range append(append([]core.FeatureValue(nil), features.Styles...), features.Tags...) {
					if core.ReliableFeature(v) && (core.StyleMatches(c.Value, v.Value) || graph.Matches(c.Value, v.Value)) {
						return core.EvidenceMatch
					}
				}
				if core.FacetComplete(features, "styles") || core.FacetComplete(features, "tags") {
					return core.EvidenceMismatch
				}
			} else {
				if state := core.CriterionEvidence(features, c); state != core.EvidenceUnknown && state != core.EvidenceUnsupported {
					return state
				}
			}
		}
	}
	if track, ok := o.knowledgeTrack(id); ok && track.IdentityStatus == core.ResolutionResolved && (c.Kind == "genre" || c.Kind == "style") {
		for _, tag := range track.GenreTags {
			if tag.Votes > 0 && tag.Source != "" && (core.StyleMatches(c.Value, tag.Name) || o.knowledge.Graph.Matches(c.Value, tag.Name)) {
				return core.EvidenceMatch
			}
		}
	}
	// A compatible grounded-description index is also musical evidence. Score
	// this ID explicitly: absence from retrieval top-K does not imply mismatch.
	if o.scorer != nil && (c.Kind == "genre" || c.Kind == "style") {
		coverage, scores, err := o.scorer.Score(ctx, c.Value, []string{id})
		if err == nil && coverage.Complete {
			for _, score := range scores {
				if score.TrackID == id && score.State == core.EvidenceMatch && score.Score >= o.cfg.SemanticMinimumScore {
					return core.EvidenceMatch
				}
			}
		}
	}
	return core.EvidenceUnknown
}

func (o *Orchestrator) bestEssential(ctx context.Context, candidates []core.Candidate, criteria []core.MusicalCriterion) ([]core.Candidate, essentialEvidenceReport, error) {
	report := essentialEvidenceReport{Eligible: map[string]bool{}, Matched: map[string]int{}, Scores: map[string]float64{}, Sources: map[string][]core.FeatureProvenance{}}
	var out []core.Candidate
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return nil, report, ctx.Err()
		}
		eligible := true
		allMatched := len(criteria) > 0
		journeys, journeyMismatches := 0, 0
		for _, c := range criteria {
			state := o.bestCriterion(ctx, candidate.Track.ID, c)
			allMatched = allMatched && state == core.EvidenceMatch
			if state == core.EvidenceMatch {
				report.Matched[criterionKey(c)]++
				report.Scores[candidate.Track.ID] = 1
			}
			if strings.HasPrefix(c.Scope, "journey_") {
				journeys++
				if state == core.EvidenceMismatch {
					journeyMismatches++
				}
			} else if state == core.EvidenceMismatch {
				eligible = false
			}
		}
		if journeys > 0 && journeys == journeyMismatches {
			eligible = false
		}
		if eligible {
			candidate.MusicalFit = core.EvidenceUnknown
			if allMatched {
				candidate.MusicalFit = core.EvidenceMatch
			}
			report.Eligible[candidate.Track.ID] = true
			out = append(out, candidate)
		}
	}
	return out, report, nil
}

func (o *Orchestrator) metadataEligible(track core.TrackRef, intent core.MusicIntent) bool {
	if !artistOnlyEligible(intent, track) {
		return false
	}
	metadata, known := o.knowledgeTrack(track.ID)
	if !acousticCompatible(acousticComparisons(metadata, audio.Clauses(intent))) {
		return false
	}
	for _, constraint := range intent.HardConstraints {
		if constraint.Kind == "require_album" {
			member := false
			for _, ref := range intent.References {
				if ref.Kind != core.ReferenceAlbum || !strings.EqualFold(ref.Query, constraint.Value) || ref.Resolution == nil || ref.Resolution.Status != core.ResolutionResolved || ref.Resolution.Selected == nil {
					continue
				}
				for _, representative := range ref.Resolution.Selected.Representatives {
					member = member || representative.TrackID == track.ID
				}
			}
			if !member {
				return false
			}
		}
	}
	for _, excluded := range intent.Constraints.ArtistsExclude {
		if identityMentions(track.Artist, excluded) || identityMentions(track.Title, excluded) {
			return false
		}
		if known {
			for _, artist := range metadata.AllArtists {
				if identityMentions(artist, excluded) {
					return false
				}
			}
		}
	}
	for _, period := range intent.Temporal {
		if period.Scope != "playlist" && period.Scope != "" {
			continue
		}
		first, last := metadata.CompositionStartYear, metadata.CompositionEndYear
		if period.Basis == "original_release" {
			first = yearFromDate(metadata.OriginalReleaseDate)
			last = first
		}
		if first > 0 && (last < period.StartYear || first > period.EndYear) {
			return false
		}
	}
	return true
}

func identityMentions(value, name string) bool {
	value = core.NormalizeIdentityPart(value)
	name = core.NormalizeIdentityPart(name)
	if value == name {
		return true
	}
	// Punctuation and feature-credit separators are boundaries, not substrings.
	normalize := func(s string) string {
		return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
			switch r {
			case ',', ';', '(', ')', '[', ']', '&', '/':
				return ' '
			}
			return r
		}, s)), " ")
	}
	return strings.Contains(" "+normalize(value)+" ", " "+normalize(name)+" ")
}
func yearFromDate(date string) int {
	if len(date) < 4 {
		return 0
	}
	year, _ := strconv.Atoi(date[:4])
	return year
}

func (o *Orchestrator) annotateFit(ctx context.Context, playlist *core.Playlist) {
	if !o.bestAvailable {
		return
	}
	unknown := false
	for i, track := range playlist.Tracks {
		assessment := core.TrackAssessment{TrackID: track.ID, State: core.EvidenceMatch}
		journeySeen, journeyMatch := false, false
		for _, c := range playlist.Intent.EssentialCriteria {
			if strings.HasPrefix(c.Scope, "journey_") {
				journeySeen = true
				journeyMatch = journeyMatch || o.bestCriterion(ctx, track.ID, c) == core.EvidenceMatch
				continue
			}
			if o.bestCriterion(ctx, track.ID, c) != core.EvidenceMatch {
				assessment.State = core.EvidenceUnknown
				assessment.Reasons = append(assessment.Reasons, "Suggested fit for "+c.Value+"; supporting evidence is incomplete.")
			}
		}
		if journeySeen && !journeyMatch {
			assessment.State = core.EvidenceUnknown
			assessment.Reasons = append(assessment.Reasons, "Suggested journey placement; genre evidence is incomplete.")
		}
		if len(playlist.Intent.Temporal) > 0 || len(playlist.Intent.Preferences.Moods) > 0 || len(playlist.Intent.Preferences.TextureDescriptions) > 0 || playlist.Intent.Preferences.VocalPreference != nil || len(playlist.Intent.Preferences.Instrumentation) > 0 {
			assessment.State = core.EvidenceUnknown
			assessment.Reasons = append(assessment.Reasons, "Era and descriptive qualities may be approximate; no full-recording guarantee.")
		}
		if assessment.State == core.EvidenceUnknown {
			unknown = true
			if i < len(playlist.Rationale) {
				playlist.Rationale[i].Detail += " · Suggested musical fit; evidence is incomplete"
			}
		}
		playlist.Assessments = append(playlist.Assessments, assessment)
	}
	if unknown && len(playlist.Tracks) > 0 {
		playlist.Outcome.State = core.OutcomePartial
		playlist.Outcome.Reasons = append(playlist.Outcome.Reasons, core.OutcomeReason{Code: "suggested_musical_fit", Detail: "Best available suggestions include tracks whose fit could not be fully verified.", Action: "review the suggestions or add a more specific reference"})
	}
}

// Stage dates constrain placement, not the entire candidate pool. Unknown dates
// retain the existing best-available behavior; known contradictions never qualify.
func (o *Orchestrator) stageDateEligible(id, scope string, intent core.MusicIntent) bool {
	metadata, _ := o.knowledgeTrack(id)
	for _, period := range intent.Temporal {
		if period.Scope != scope {
			continue
		}
		first, last := metadata.CompositionStartYear, metadata.CompositionEndYear
		if period.Basis == "original_release" {
			first = yearFromDate(metadata.OriginalReleaseDate)
			last = first
		}
		if last == 0 {
			last = first
		}
		if first > 0 && (last < period.StartYear || first > period.EndYear) {
			return false
		}
	}
	return true
}

func hasStagePeriods(intent core.MusicIntent) bool {
	for _, period := range intent.Temporal {
		if strings.HasPrefix(period.Scope, "journey_") {
			return true
		}
	}
	return false
}

func journeyStageCriteria(intent core.MusicIntent) []core.MusicalCriterion {
	criteria := journeyCriteria(intent.EssentialCriteria)
	if hasStagePeriods(intent) {
		// Even a date-only start needs a following stage, and conversely an end
		// restriction must not accidentally apply to the entire playlist.
		scopes := []string{"journey_start", "journey_end"}
		for _, period := range intent.Temporal {
			if period.Scope == "journey_via" {
				scopes = append(scopes, period.Scope)
			}
		}
		for _, scope := range scopes {
			found := false
			for _, c := range criteria {
				found = found || c.Scope == scope
			}
			if !found {
				criteria = append(criteria, core.MusicalCriterion{Scope: scope, Value: scope})
			}
		}
	}
	return journeyCriteria(criteria)
}

func (o *Orchestrator) filterJourneyStage(ctx context.Context, candidates []core.Candidate, criterion core.MusicalCriterion, intent core.MusicIntent) ([]core.Candidate, essentialEvidenceReport, error) {
	var criteria []core.MusicalCriterion
	if criterion.Kind != "" {
		criteria = []core.MusicalCriterion{criterion}
	}
	eligible, report, err := o.filterEssential(ctx, candidates, criteria)
	if err != nil {
		return nil, report, err
	}
	result := make([]core.Candidate, 0, len(eligible))
	for _, candidate := range eligible {
		fits := true
		// Incomplete tags do not mean a known destination track also belongs
		// at the start. Prefer its affirmative stage evidence to an unknown fit.
		if o.bestAvailable && criterion.Kind != "" && o.bestCriterion(ctx, candidate.Track.ID, criterion) == core.EvidenceUnknown {
			for _, other := range journeyCriteria(intent.EssentialCriteria) {
				if other.Scope != criterion.Scope && o.bestCriterion(ctx, candidate.Track.ID, other) == core.EvidenceMatch {
					fits = false
					break
				}
			}
			// When metadata cannot place a preview, prefer the stage whose
			// description is closer in CLAP space. This is approximate placement;
			// bestCriterion stays unknown and final coverage still reports it.
			if fits && o.audioSession != nil {
				if score, ok := o.audioSession.StageSimilarity(candidate.Track.ID, criterion); ok {
					for _, other := range journeyCriteria(intent.EssentialCriteria) {
						if other.Scope == criterion.Scope || !o.stageDateEligible(candidate.Track.ID, other.Scope, intent) || o.bestCriterion(ctx, candidate.Track.ID, other) == core.EvidenceMismatch {
							continue
						}
						if otherScore, available := o.audioSession.StageSimilarity(candidate.Track.ID, other); available && otherScore > score+1e-6 {
							fits = false
							break
						}
					}
				}
			}
		}
		if fits && o.stageDateEligible(candidate.Track.ID, criterion.Scope, intent) {
			result = append(result, candidate)
		} else {
			delete(report.Eligible, candidate.Track.ID)
		}
	}
	return result, report, nil
}
