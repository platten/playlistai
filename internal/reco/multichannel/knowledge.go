package multichannel

import (
	"context"
	"strconv"
	"strings"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func (o *Orchestrator) knowledgeTrack(id string) (core.EnrichedTrack, bool) {
	var result core.EnrichedTrack
	found := false
	if o.knowledge != nil {
		for _, track := range o.knowledge.Tracks {
			if track.Ref.ID == id {
				result, found = track, true
				break
			}
		}
	}
	if provider, ok := o.cat.(ports.LibraryMetadataCatalog); ok && o.requestContext != nil {
		if local, available, err := provider.LibraryRecordingMetadata(o.requestContext, id); err == nil && available {
			if !found {
				return local, true
			}
			result = mergeRecordingMetadata(result, local)
			if features, available := provider.LibraryTrackFeatures(o.requestContext, id); available {
				if features.Conflicts["original_release_date"] {
					result.OriginalReleaseDate = ""
				}
				if features.Conflicts["edition_date"] {
					result.ReleaseEditionDate, result.Year = "", 0
				}
				if features.Conflicts["composition_date"] {
					result.CompositionStartYear, result.CompositionEndYear = 0, 0
				}
			}
		}
	}
	return result, found
}

// Contradictory dates remain unknown; edition dates never stand in for original
// recording or composition dates. Identity was established by the catalog join.
func mergeRecordingMetadata(base, local core.EnrichedTrack) core.EnrichedTrack {
	mergeDate := func(a, b string) string {
		if a == "" {
			return b
		}
		if b == "" {
			return a
		}
		if len(a) >= 4 && len(b) >= 4 && a[:4] == b[:4] {
			return a
		}
		return ""
	}
	base.OriginalReleaseDate = mergeDate(base.OriginalReleaseDate, local.OriginalReleaseDate)
	base.ReleaseEditionDate = mergeDate(base.ReleaseEditionDate, local.ReleaseEditionDate)
	if base.CompositionStartYear == 0 {
		base.CompositionStartYear, base.CompositionEndYear = local.CompositionStartYear, local.CompositionEndYear
	} else if local.CompositionStartYear != 0 && (base.CompositionStartYear != local.CompositionStartYear || base.CompositionEndYear != local.CompositionEndYear) {
		base.CompositionStartYear, base.CompositionEndYear = 0, 0
	}
	base.GenreTags = append(append([]core.AttributedGenreTag(nil), base.GenreTags...), local.GenreTags...)
	base.AllArtists = append(append([]string(nil), base.AllArtists...), local.AllArtists...)
	return base
}

func (o *Orchestrator) bestCriterion(ctx context.Context, id string, c core.MusicalCriterion) core.EvidenceState {
	if o.audioSession != nil {
		if state := o.audioSession.Criterion(id, c); state != core.EvidenceUnknown && state != "" {
			return state
		}
	}
	if catalog, ok := o.cat.(interface {
		CriterionEvidence(context.Context, string, core.MusicalCriterion) core.EvidenceState
	}); ok {
		if state := catalog.CriterionEvidence(ctx, id, c); state != core.EvidenceUnknown && state != "" {
			return state
		}
	}
	if o.features != nil {
		if features, ok, err := o.features.Features(ctx, id); err == nil && ok {
			if c.Kind == "genre" || o.enhanced && c.Kind == "style" {
				graph := core.GenreGraph{}
				if o.knowledge != nil {
					graph = o.knowledge.Graph
				}
				for _, v := range append(append([]core.FeatureValue(nil), features.Styles...), features.Tags...) {
					matches := core.StyleMatches(c.Value, v.Value) || graph.Matches(c.Value, v.Value)
					if o.enhanced {
						matches = enhancedCategoryMatches(c.Value, v.Value, graph)
					}
					if core.ReliableFeature(v) && matches {
						return core.EvidenceMatch
					}
				}
				if core.FacetComplete(features, "styles") || core.FacetComplete(features, "tags") {
					return core.EvidenceMismatch
				}
			} else {
				criterion := c
				if o.enhanced && c.Kind == "vocal" {
					// The sidecar vocal facet has only vocal/mixed/instrumental.
					// Its absence of "harsh vocals" cannot prove no harsh singing.
					switch strings.ToLower(strings.TrimSpace(c.Value)) {
					case "vocal", "vocals", "voice", "singing":
						criterion.Value = "vocal"
					case "instrumental", "no vocals":
						criterion.Value = "instrumental"
					default:
						return core.EvidenceUnknown
					}
				}
				if state := core.CriterionEvidence(features, criterion); state != core.EvidenceUnknown && state != core.EvidenceUnsupported {
					return state
				}
			}
		}
	}
	if track, ok := o.knowledgeTrack(id); ok && track.IdentityStatus == core.ResolutionResolved {
		if state := o.recordingTagCriterion(track, c); state != core.EvidenceUnknown {
			return state
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

func (o *Orchestrator) recordingTagCriterion(track core.EnrichedTrack, c core.MusicalCriterion) core.EvidenceState {
	if track.IdentityStatus != core.ResolutionResolved {
		return core.EvidenceUnknown
	}
	if c.Kind == "vocal" {
		// Positive recording-level tags are direct evidence that a recording is
		// instrumental. A conclusive preview assessment is checked above and
		// therefore still wins when sampled audio contains vocals.
		for _, tag := range track.GenreTags {
			value := strings.ToLower(strings.TrimSpace(tag.Name))
			if tag.Votes > 0 && tag.Source != "" && (value == "instrumental" || value == "no vocals") {
				switch strings.ToLower(strings.TrimSpace(c.Value)) {
				case "instrumental", "no vocals":
					return core.EvidenceMatch
				case "vocal", "vocals", "voice", "singing":
					return core.EvidenceMismatch
				}
			}
		}
		return core.EvidenceUnknown
	}
	if c.Kind != "genre" && c.Kind != "style" {
		return core.EvidenceUnknown
	}
	graph := core.GenreGraph{}
	if o.knowledge != nil {
		graph = o.knowledge.Graph
	}
	for _, tag := range track.GenreTags {
		matches := core.StyleMatches(c.Value, tag.Name) || graph.Matches(c.Value, tag.Name)
		if o.enhanced {
			matches = enhancedCategoryMatches(c.Value, tag.Name, graph)
		}
		if tag.Votes > 0 && tag.Source != "" && matches {
			return core.EvidenceMatch
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
	if !acousticCompatibleFor(intent, acousticComparisons(metadata, audio.Clauses(intent))) {
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
		if len(playlist.Intent.Temporal) > 0 || len(playlist.Intent.Preferences.Moods) > 0 || len(playlist.Intent.Preferences.TextureDescriptions) > 0 || len(playlist.Intent.Preferences.VocalRequests()) > 0 || len(playlist.Intent.Preferences.Instrumentation) > 0 {
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
	if intent.Controls.RecommendationMode == core.EnhancedHybrid {
		var grouped []core.MusicalCriterion
		seen := map[string]bool{}
		for _, c := range criteria {
			if c.Group != "" {
				key := c.Scope + "\x00" + c.Group
				if seen[key] {
					continue
				}
				seen[key] = true
				c.Kind = "" // this stage is assessed against its whole OR group
			}
			grouped = append(grouped, c)
		}
		criteria = grouped
		for _, clause := range audio.Clauses(intent) {
			if !strings.HasPrefix(clause.Scope, "journey_") {
				continue
			}
			found := false
			for _, c := range criteria {
				found = found || c.Scope == clause.Scope
			}
			if !found {
				criteria = append(criteria, core.MusicalCriterion{Scope: clause.Scope, Value: clause.Scope})
			}
		}
	}
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
	if o.enhanced && criterion.Group != "" {
		for _, c := range intent.EssentialCriteria {
			if c.Scope == criterion.Scope && c.Group == criterion.Group {
				criteria = append(criteria, c)
			}
		}
	} else if criterion.Kind != "" {
		criteria = []core.MusicalCriterion{criterion}
	}
	eligible, report, err := o.filterEssential(ctx, candidates, criteria)
	if err != nil {
		return nil, report, err
	}
	result := make([]core.Candidate, 0, len(eligible))
	for _, candidate := range eligible {
		fits := true
		// Incomplete tags do not prove that a recording belongs to every stage.
		// A positive direct preview comparison can nevertheless support an
		// overlapping adjacent stage; known categories are not mutually exclusive.
		if o.bestAvailable && (criterion.Kind == "" || o.bestCriterion(ctx, candidate.Track.ID, criterion) == core.EvidenceUnknown) {
			stageScore, stageCompared := 0.0, false
			if o.audioSession != nil {
				stageScore, stageCompared = o.stageSimilarity(candidate.Track.ID, criterion)
			}
			if !stageCompared || stageScore <= 0 {
				for _, other := range journeyStageCriteria(intent) {
					if other.Scope != criterion.Scope && o.bestCriterion(ctx, candidate.Track.ID, other) == core.EvidenceMatch {
						fits = false
						break
					}
				}
			}
			// A positive preview comparison is direct evidence for this stage, but
			// it is not categorical proof. Do not force an uncalibrated CLAP vector
			// into only its numerically closest stage: adjacent journey descriptions
			// commonly overlap, and reservation below still assigns distinct tracks.
			// Non-positive comparisons cannot displace a positive eligible stage;
			// bestCriterion stays unknown so final coverage reports this as a close
			// rather than strong fit.
			if fits && stageCompared {
				if stageScore <= 0 {
					for _, other := range journeyStageCriteria(intent) {
						if other.Scope == criterion.Scope || !o.stageDateEligible(candidate.Track.ID, other.Scope, intent) || o.bestCriterion(ctx, candidate.Track.ID, other) == core.EvidenceMismatch {
							continue
						}
						if otherScore, available := o.stageSimilarity(candidate.Track.ID, other); available && otherScore > stageScore+1e-6 {
							fits = false
							break
						}
					}
				}
			}
		}
		if fits && o.stageDateEligible(candidate.Track.ID, criterion.Scope, intent) && o.enhancedStageConstraints(ctx, candidate.Track.ID, criterion.Scope, intent) {
			result = append(result, candidate)
		} else {
			delete(report.Eligible, candidate.Track.ID)
		}
	}
	return result, report, nil
}

func (o *Orchestrator) stageSimilarity(id string, criterion core.MusicalCriterion) (float64, bool) {
	if o.audioSession == nil {
		return 0, false
	}
	if !o.enhanced {
		return o.audioSession.StageSimilarity(id, criterion)
	}
	assessment, ok := o.audioSession.Assessment(id)
	if !ok {
		return 0, false
	}
	stage := core.AudioAssessment{PolicyVersion: assessment.PolicyVersion}
	for _, clause := range assessment.Clauses {
		if clause.Clause.Scope == criterion.Scope {
			stage.Clauses = append(stage.Clauses, clause)
		}
	}
	var candidate core.Candidate
	audio.ApplyScores(&candidate, stage)
	return candidate.Scores.SemanticMatch - candidate.Scores.SemanticNegativeMatch, candidate.Available.SemanticMatch || candidate.Available.SemanticNegativeMatch
}
