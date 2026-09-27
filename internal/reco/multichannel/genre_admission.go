package multichannel

import (
	"context"
	"fmt"
	"strings"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
)

// Defining positive genres require corroboration at output admission. Keep
// explicitly preferred hints soft, and keep every alternative of an OR group.
// This is separate from retrieval so unknown candidates can acquire evidence.
func genreAdmissionGroups(intent core.MusicIntent) [][]core.AudioClause {
	// Preferences inferred from retrieval anchors remain hints. Typed defining
	// criteria and explicit required preferences are the request contract.
	prepare := func(preferences []core.IntentPreference) []core.IntentPreference {
		out := append([]core.IntentPreference(nil), preferences...)
		for i := range out {
			if !out[i].Explicit && out[i].Influence != core.InfluenceNegative {
				out[i].Strength = "preferred"
			} else if out[i].Explicit && out[i].Strength == "" && len(intent.EssentialCriteria) == 0 {
				out[i].Strength = "essential" // compatibility for explicit untyped preferences
			}
		}
		return out
	}
	intent.Preferences.Genres = prepare(intent.Preferences.Genres)
	intent.Preferences.Styles = prepare(intent.Preferences.Styles)
	var out [][]core.AudioClause
	for _, group := range admissionClauseGroups(audio.Clauses(intent)) {
		for _, clause := range group {
			if !clause.Negative && (clause.Kind == "genre" || clause.Kind == "style") && (clause.Essential || clause.Strict) {
				out = append(out, group)
				break
			}
		}
	}
	return out
}

func admissionClauseGroups(clauses []core.AudioClause) [][]core.AudioClause {
	var groups [][]core.AudioClause
	positions := map[string]int{}
	for i, clause := range clauses {
		scope := clause.Scope
		if scope == "" {
			scope = "playlist"
		}
		key := scope + "\x00" + clause.Group
		if clause.CoverageGroup != "" && !clause.Negative {
			key = scope + "\x00coverage:" + clause.CoverageGroup
		} else if clause.Group == "" {
			key += fmt.Sprint("\x00", i)
		}
		index, exists := positions[key]
		if !exists {
			index = len(groups)
			positions[key] = index
			groups = append(groups, nil)
		}
		groups[index] = append(groups[index], clause)
	}
	return groups
}

// Empty scope admits any possible journey stage; a concrete scope checks the
// stage being assigned. Playlist obligations always apply.
func (o *Orchestrator) confirmedGenres(ctx context.Context, id string, intent core.MusicIntent, scope string) bool {
	if !o.enhanced {
		return true
	}
	stages := map[string]bool{}
	if scope == "" && intent.Mode == core.ModeJourney {
		for _, stage := range journeyStageCriteria(intent) {
			stages[stage.Scope] = true
		}
	}
	for _, group := range genreAdmissionGroups(intent) {
		groupScope := group[0].Scope
		journey := strings.HasPrefix(groupScope, "journey_")
		if journey && scope != "" && groupScope != scope {
			continue
		}
		matched := false
		for _, clause := range group {
			matched = matched || o.assessClause(ctx, id, clause, core.AudioAssessment{}).State == core.EvidenceMatch
		}
		if !journey || scope != "" {
			if !matched {
				return false
			}
		} else {
			previous, exists := stages[groupScope]
			stages[groupScope] = (!exists || previous) && matched
		}
	}
	if len(stages) == 0 {
		return true
	}
	for _, matched := range stages {
		if matched {
			return true
		}
	}
	return false
}

// Once recording verification has run, an uncalibrated preview cannot rescue
// a missing ordinary genre. Keep searching other candidates, which may already
// have cited evidence, rather than acquiring audio for an inadmissible track.
func (o *Orchestrator) previewCannotConfirmGenre(ctx context.Context, id string, intent core.MusicIntent) bool {
	if !o.enhanced || o.audioSession == nil || o.audioSession.Calibrated() || intent.Mode == core.ModeJourney || core.WantsInstrumental(intent) {
		return false
	}
	groups := genreAdmissionGroups(intent)
	for _, group := range groups {
		for _, clause := range group {
			// Mixed alternatives, scoped stages and special vocal screening keep
			// their existing paths. Only ordinary positive playlist genres qualify.
			value := core.NormalizeIdentityPart(clause.Text)
			if clause.Kind != "genre" && clause.Kind != "style" || clause.Negative || clause.Scope != "" && clause.Scope != "playlist" || clause.Degree != "" || value == "instrumental" || value == "no vocals" {
				return false
			}
		}
	}
	return len(groups) > 0 && !o.confirmedGenres(ctx, id, intent, "") && ctx.Err() == nil
}

func (o *Orchestrator) filterConfirmedOutput(ctx context.Context, candidates []core.Candidate, intent core.MusicIntent) ([]core.Candidate, error) {
	if !o.enhanced {
		return candidates, ctx.Err()
	}
	out := make([]core.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		genreConfirmed := o.confirmedGenres(ctx, candidate.Track.ID, intent, "")
		if genreConfirmed && o.enhancedStageConstraints(ctx, candidate.Track.ID, "playlist", intent) {
			out = append(out, candidate)
		} else {
			reason := "strict_criterion_unconfirmed"
			if !genreConfirmed {
				reason = "requested_genre_unconfirmed"
			}
			o.recordCandidateDecision(ctx, candidate, intent, "rejected", reason)
		}
	}
	return out, ctx.Err()
}

func (o *Orchestrator) requiredOutputReasons(ctx context.Context, required []core.TrackRef, intent core.MusicIntent) []core.OutcomeReason {
	if !o.enhanced {
		return nil
	}
	var reasons []core.OutcomeReason
	for _, track := range required {
		var scopes []string
		add := func(ref *core.IntentReference, scope string) {
			if ref == nil {
				return
			}
			for _, representative := range referenceTrackIdentitiesContext(ctx, o.cat, *ref, false) {
				if representative.TrackID == track.ID {
					scopes = appendUniqueString(scopes, scope)
				}
			}
		}
		add(intent.Start, "journey_start")
		add(intent.Destination, "journey_end")
		for i := range intent.Journey.Waypoints {
			scope := "journey_via"
			if i == 0 {
				scope = "journey_start"
			} else if i == len(intent.Journey.Waypoints)-1 {
				scope = "journey_end"
			}
			add(&intent.Journey.Waypoints[i], scope)
		}
		if len(scopes) == 0 {
			scopes = []string{""}
		}
		for _, scope := range scopes {
			genreConfirmed := o.confirmedGenres(ctx, track.ID, intent, scope)
			strictConfirmed := o.enhancedStageConstraints(ctx, track.ID, "playlist", intent) && (scope == "" || o.enhancedStageConstraints(ctx, track.ID, scope, intent))
			if !genreConfirmed || !strictConfirmed {
				reason := core.OutcomeReason{Code: "required_track_evidence_unconfirmed", Criterion: track.Display(), Detail: "A required track or waypoint lacks sufficient evidence for a strict musical requirement; it was not included.", Action: "Choose a confirmed recording or remove the required track."}
				if !genreConfirmed {
					reason.Code = "required_track_genre_unconfirmed"
					reason.Detail = "A required track or waypoint lacks corroborated support for its requested genre; it was not included."
				}
				o.recordCandidateDecision(ctx, core.Candidate{Track: track}, intent, "rejected", reason.Code)
				reasons = append(reasons, reason)
				break
			}
		}
	}
	return reasons
}
