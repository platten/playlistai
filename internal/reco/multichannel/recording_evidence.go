package multichannel

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// Verification is optional acquisition, outside scoring. One generation can
// investigate a bounded shortlist; outages must leave time for audio/assembly.
const recordingVerificationLimit = 32
const recordingVerificationTimeout = 10 * time.Second

func (o *Orchestrator) WithRecordingVerifier(verifier ports.RecordingVerifier) *Orchestrator {
	o.recordingVerifier = verifier
	return o
}

func (o *Orchestrator) verifyRecording(ctx context.Context, ref core.TrackRef, intent core.MusicIntent) error {
	return o.acquireRecording(ctx, ref, intent, false)
}

func (o *Orchestrator) verifyRequiredRecording(ctx context.Context, ref core.TrackRef, intent core.MusicIntent) error {
	return o.acquireRecording(ctx, ref, intent, true)
}

func (o *Orchestrator) acquireRecording(ctx context.Context, ref core.TrackRef, intent core.MusicIntent, required bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !o.enhanced || o.recordingVerifier == nil || len(audio.Clauses(intent)) == 0 || o.replayingEvidence {
		return ctx.Err()
	}
	if o.verificationAttempted[ref.ID] || len(o.verificationAttempted) >= recordingVerificationLimit {
		return ctx.Err()
	}
	if tier, _ := o.enhancedTier(ctx, core.Candidate{Track: ref}, intent); tier == fitStrong {
		return ctx.Err()
	}
	track, _ := o.knowledgeTrackContext(ctx, ref.ID)
	track.Ref = ref
	if meta, ok := ports.CatalogMeta(ctx, o.cat, ref.ID); ok {
		if recordingISRCConflict(track, meta.ISRC) {
			return nil
		}
		if canonical := canonicalRecordingISRC(meta.ISRC); canonical != "" {
			track.ISRC = canonical
		}
	}
	if meta, ok := ports.CatalogMeta(ctx, o.cat, ref.ID); ok && meta.MusicBrainzRecording != "" {
		if track.RecordingID != "" && !strings.EqualFold(track.RecordingID, meta.MusicBrainzRecording) {
			return nil // disagreement with the catalog identity is not a new fact
		}
		track.RecordingID = meta.MusicBrainzRecording
		track.IdentityStatus, track.Matched = core.ResolutionResolved, true
	}
	needsIdentity := track.IdentityStatus != core.ResolutionResolved || track.RecordingID == ""
	enricher, canEnrich := o.recordingVerifier.(ports.Enricher)
	// Failed identity searches share the total acquisition ceiling. Reserve
	// half for identity-ready candidates; required output tracks only bypass
	// this optional subquota, never the total ceiling or identity checks.
	if needsIdentity && (!canEnrich || !required && o.optionalIdentityAttempts >= recordingVerificationLimit/2) {
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-o.verificationStop:
		return nil
	default:
	}
	if o.verificationAttempted == nil {
		o.verificationAttempted = map[string]bool{}
		o.verifiedRecordings = map[string]core.EnrichedTrack{}
	}
	o.verificationAttempted[ref.ID] = true
	if needsIdentity && !required {
		o.optionalIdentityAttempts++
	}
	var criteria []core.MusicalCriterion
	for _, clause := range audio.Clauses(intent) {
		criteria = append(criteria, core.MusicalCriterion{Kind: clause.Kind, Value: clause.Text, Scope: clause.Scope, Strength: clause.Strength, Group: clause.Group, CoverageGroup: clause.CoverageGroup})
	}
	child, cancel := context.WithTimeout(ctx, recordingVerificationTimeout)
	defer cancel()
	go func() {
		select {
		case <-o.verificationStop:
			cancel()
		case <-child.Done():
		}
	}()
	if needsIdentity {
		rows, err := enricher.Enrich(child, []core.TrackRef{ref}, ports.NopProgress{})
		if err == nil && len(rows) == 1 && rows[0].Ref.ID == ref.ID && rows[0].IdentityStatus == core.ResolutionResolved && rows[0].RecordingID != "" && (track.RecordingID == "" || strings.EqualFold(track.RecordingID, rows[0].RecordingID)) {
			track = mergeRecordingMetadata(rows[0], track)
		} else {
			return ctx.Err() // unresolved identity cannot acquire recording facts
		}
	}
	updated, err := o.recordingVerifier.VerifyRecording(child, track, criteria)
	// A confirmed contradiction must survive the optional acquisition boundary.
	// Only the exact existing catalog identity can be quarantined by this response.
	if err == nil && updated.Ref == track.Ref && track.RecordingID != "" && strings.EqualFold(updated.RecordingID, track.RecordingID) && updated.IdentityStatus == core.ResolutionAmbiguous {
		updated.Matched, updated.Claims = false, nil
		o.verifiedRecordings[ref.ID] = updated
		return ctx.Err()
	}
	// Provider failure is missing evidence. Never accept a different recording
	// merely because an optional verifier found its title easier to resolve.
	if err != nil && len(updated.Claims) == 0 || updated.Ref.ID != ref.ID || updated.IdentityStatus != core.ResolutionResolved || updated.RecordingID == "" || track.RecordingID != "" && !strings.EqualFold(track.RecordingID, updated.RecordingID) {
		return ctx.Err()
	}
	o.verifiedRecordings[ref.ID] = updated
	return ctx.Err()
}

func canonicalRecordingISRC(value string) string {
	value = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(value)))
	if len(value) != 12 {
		return ""
	}
	for i, c := range value {
		letter, digit := c >= 'A' && c <= 'Z', c >= '0' && c <= '9'
		if i < 2 && !letter || i >= 2 && i < 5 && !letter && !digit || i >= 5 && !digit {
			return ""
		}
	}
	return value
}

func recordingISRCConflict(track core.EnrichedTrack, local string) bool {
	local = canonicalRecordingISRC(local)
	if local == "" {
		return false
	}
	known := false
	for _, value := range append([]string{track.ISRC}, track.AllISRCs...) {
		if canonical := canonicalRecordingISRC(value); canonical != "" {
			known = true
			if canonical == local {
				return false
			}
		}
	}
	return known
}

func (o *Orchestrator) recordingKnowledge(base *core.KnowledgeSnapshot) *core.KnowledgeSnapshot {
	if len(o.verifiedRecordings) == 0 {
		return base
	}
	snapshot := core.KnowledgeSnapshot{}
	if base != nil {
		snapshot = *base
		snapshot.Tracks = append([]core.EnrichedTrack(nil), snapshot.Tracks...)
	}
	ids := make([]string, 0, len(o.verifiedRecordings))
	for id := range o.verifiedRecordings {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		track := o.verifiedRecordings[id]
		found := false
		for i := range snapshot.Tracks {
			if snapshot.Tracks[i].Ref.ID == id {
				snapshot.Tracks[i] = track
				found = true
				break
			}
		}
		if !found {
			snapshot.Tracks = append(snapshot.Tracks, track)
		}
	}
	return &snapshot
}

func (o *Orchestrator) freezeRecordingEvidence(result *core.Playlist) {
	if len(o.verifiedRecordings) == 0 {
		return
	}
	snapshot := o.recordingKnowledge(result.Intent.Knowledge)
	snapshot.ID = ""
	snapshot.ID = audio.Fingerprint(snapshot)
	result.Intent.Knowledge = snapshot
}

func reverseEvidence(state core.EvidenceState) core.EvidenceState {
	if state == core.EvidenceMatch {
		return core.EvidenceMismatch
	}
	if state == core.EvidenceMismatch {
		return core.EvidenceMatch
	}
	return state
}

func claimValueState(claim core.RecordingClaim, clause core.AudioClause, graph core.GenreGraph) core.EvidenceState {
	compatibleGenre := claim.Kind == "genre" && clause.Kind == "style" || claim.Kind == "style" && clause.Kind == "genre"
	if claim.Kind != clause.Kind && !compatibleGenre {
		return core.EvidenceUnknown
	}
	matches := core.NormalizeIdentityPart(claim.Value) == core.NormalizeIdentityPart(clause.Text)
	if clause.Kind == "genre" || clause.Kind == "style" {
		matches = enhancedCategoryMatches(clause.Text, claim.Value, graph)
	}
	if clause.Kind == "vocal" {
		normalize := func(value string) string {
			switch strings.ToLower(value) {
			case "vocal", "vocals", "voice", "singing", "mixed":
				return "vocals"
			case "instrumental", "no vocals":
				return "instrumental"
			}
			return value
		}
		actual, wanted := normalize(claim.Value), normalize(clause.Text)
		matches = actual == wanted
		if actual != wanted && (actual == "vocals" || actual == "instrumental") && (wanted == "vocals" || wanted == "instrumental") {
			return reverseEvidence(claim.State)
		}
	}
	if matches {
		return claim.State
	}
	return core.EvidenceUnknown
}

func attributedRecordingClaim(claim core.RecordingClaim, track core.EnrichedTrack) bool {
	scope := claim.Scope == "recording" && strings.EqualFold(claim.EntityID, track.RecordingID) || claim.Scope == "work" && claim.Kind == "composer"
	return track.IdentityStatus == core.ResolutionResolved && track.RecordingID != "" && scope &&
		strings.EqualFold(claim.RecordingID, track.RecordingID) && claim.EntityID != "" && claim.Source.Provider != "" && claim.Source.URL != "" && claim.Source.Revision != "" && claim.Locator != ""
}

func vocalAbsenceRequired(clause core.AudioClause) bool {
	if clause.Kind != "vocal" || clause.Degree == "mostly" || clause.Degree == "reduced" {
		return false
	}
	value := core.NormalizeIdentityPart(clause.Text)
	return clause.Negative && (value == "vocals" || value == "vocal" || value == "singing" || value == "voice") ||
		!clause.Negative && (value == "instrumental" || value == "no vocals")
}

// A source locator alone does not establish that a freeform quote entails a
// fact. Both tiering and reconciliation use the same decisive-method policy.
func decisiveClaimMethod(track core.EnrichedTrack, claim core.RecordingClaim, clause core.AudioClause) bool {
	if claim.Coverage != nil || clause.Kind == "instrumentation" && (clause.Degree == "mostly" || clause.Degree == "reduced") {
		return false
	}
	if claim.Method == "recording_credit" {
		return clause.Kind == "instrumentation" || clause.Kind == "composer" || clause.Kind == "vocal"
	}
	if claim.Method == "publisher_field" {
		return (clause.Kind == "genre" || clause.Kind == "style") && linkedPublisherGenre(track, claim)
	}
	return claim.Method == "linked_statement"
}

// assessClause reconciles completed observations only. Retrieval hints and weak
// community tags remain visible, but cannot buy a verified criterion.
func (o *Orchestrator) assessClause(ctx context.Context, id string, clause core.AudioClause, preview core.AudioAssessment) core.CriterionAssessment {
	out := core.CriterionAssessment{Clause: clause, State: core.EvidenceUnknown}
	track, _ := o.knowledgeTrackContext(ctx, id)
	criterion := core.MusicalCriterion{Kind: clause.Kind, Value: clause.Text, Scope: clause.Scope, Strength: clause.Strength, Group: clause.Group, CoverageGroup: clause.CoverageGroup}
	positive, negative, vocalContradiction := false, false, false
	add := func(state core.EvidenceState, claim core.RecordingClaim, decisive bool) {
		out.Claims = append(out.Claims, claim)
		if !decisive {
			return
		}
		if clause.Negative {
			state = reverseEvidence(state)
		}
		positive = positive || state == core.EvidenceMatch
		negative = negative || state == core.EvidenceMismatch
	}
	addNative := func(state core.EvidenceState, source string) {
		if state != core.EvidenceMatch && state != core.EvidenceMismatch {
			return
		}
		claim := core.RecordingClaim{Kind: clause.Kind, Value: clause.Text, State: state, Scope: "recording", EntityID: id, RecordingID: track.RecordingID, Source: core.ContextSource{Provider: source, Revision: o.sourceCatalogVersion}, Locator: id, Method: "catalog_evidence"}
		// Ordinary credits never establish how prominent an instrument sounds.
		decisive := clause.Kind != "instrumentation" || clause.Degree != "mostly" && clause.Degree != "reduced"
		if clause.Kind == "genre" || clause.Kind == "style" {
			// This aggregate interface returns only a state, not independent
			// recording provenance. Missing or differently normalized annotations
			// cannot promote imported tags into corroboration.
			claim.Method, decisive = "catalog_genre_hint", false
		}
		requestedState := state
		if clause.Negative {
			requestedState = reverseEvidence(state)
		}
		if vocalAbsenceRequired(clause) && requestedState == core.EvidenceMatch {
			// Native labels and classifier predictions do not establish absence
			// over a whole recording, irrespective of their confidence score.
			claim.Method, decisive = "native_vocal_hint", false
		}
		add(state, claim, decisive)
	}
	genreClause := clause.Kind == "genre" || clause.Kind == "style"
	matchedEmbeddedGenre := false
	if meta, ok := ports.CatalogMeta(ctx, o.cat, id); ok {
		for _, annotation := range meta.Annotations {
			genreAnnotation := annotation.Kind == "genre" || annotation.Kind == "style"
			if annotation.Origin != "classifier_output" && (!genreClause || !genreAnnotation) {
				continue
			}
			method, provider := "embedded_tag", "embedded_metadata"
			if annotation.Origin == "classifier_output" {
				method, provider = "classifier_label", "embedded_classifier"
			}
			claim := core.RecordingClaim{Kind: annotation.Kind, Value: annotation.Value, State: core.EvidenceMatch,
				Scope: "recording", EntityID: id, RecordingID: track.RecordingID,
				Source:  core.ContextSource{Provider: provider, Revision: o.sourceCatalogVersion},
				Locator: annotation.SourceKey, Method: method}
			if state := claimValueState(claim, clause, core.GenreGraph{}); state != core.EvidenceUnknown {
				add(state, claim, false)
				matchedEmbeddedGenre = matchedEmbeddedGenre || genreClause
			}
		}
	}
	if catalog, ok := o.cat.(interface {
		CriterionEvidence(context.Context, string, core.MusicalCriterion) core.EvidenceState
	}); ok {
		state := catalog.CriterionEvidence(ctx, id, criterion)
		// An aggregate lookup of these same tags is not independent support.
		if state != core.EvidenceMatch || !matchedEmbeddedGenre {
			addNative(state, "catalog")
		}
	}
	if clause.Kind != "composer" && o.features != nil {
		if features, found, err := o.features.Features(ctx, id); err == nil && found {
			featureCriterion := criterion
			if clause.Kind == "vocal" {
				switch strings.ToLower(clause.Text) {
				case "vocal", "vocals", "voice", "singing":
					featureCriterion.Value = "vocal"
				case "instrumental", "no vocals":
					featureCriterion.Value = "instrumental"
				default:
					features.VocalEvidence = core.FeatureValue{}
				}
			}
			state := core.CriterionEvidence(features, featureCriterion)
			if genreClause {
				for _, value := range append(append([]core.FeatureValue(nil), features.Styles...), features.Tags...) {
					if !core.ReliableFeature(value) || !enhancedCategoryMatches(clause.Text, value.Value, core.GenreGraph{}) {
						continue
					}
					for _, provenance := range value.Provenance {
						claim := core.RecordingClaim{Kind: clause.Kind, Value: value.Value, State: core.EvidenceMatch,
							Scope: "recording", EntityID: id, RecordingID: track.RecordingID,
							Source:  core.ContextSource{Provider: provenance.Source, Revision: provenance.SourceVersion},
							Locator: provenance.SourceID, Method: "sidecar_tag", ExtractorVersion: provenance.ModelVersion}
						add(core.EvidenceMatch, claim, false)
					}
				}
			} else {
				addNative(state, "semantic_sidecar")
			}
		}
	}
	graph := core.GenreGraph{}
	if o.knowledge != nil {
		graph = o.knowledge.Graph
	}
	for _, claim := range track.Claims {
		state := claimValueState(claim, clause, graph)
		if state == core.EvidenceUnknown && publisherGenreLabelMatches(track, claim, clause) {
			state = claim.State
		}
		if state == core.EvidenceUnknown {
			continue
		}
		decisive := attributedRecordingClaim(claim, track) && decisiveClaimMethod(track, claim, clause)
		add(state, claim, decisive)
	}
	for _, tag := range track.GenreTags {
		if tag.Votes <= 0 || tag.Source == "" {
			continue
		}
		claim := core.RecordingClaim{Kind: "genre", Value: tag.Name, State: core.EvidenceMatch, Scope: "recording", EntityID: tag.EntityID, RecordingID: track.RecordingID, Source: core.ContextSource{Provider: tag.Source}, Locator: tag.Facet, Method: "community_tag"}
		if strings.EqualFold(tag.Name, "instrumental") || strings.EqualFold(tag.Name, "no vocals") {
			claim.Kind = "vocal"
		}
		if state := claimValueState(claim, clause, graph); state != core.EvidenceUnknown {
			add(state, claim, false)
		}
	}
	if packed, ok := o.packedAssessments[id]; ok {
		for _, observed := range packed.Clauses {
			if observed.Clause != clause {
				continue
			}
			claim := core.RecordingClaim{Kind: clause.Kind, Value: clause.Text, State: core.EvidenceUnknown,
				Scope: "recording", EntityID: id, RecordingID: track.RecordingID,
				Source: core.ContextSource{Provider: "library_clap", Revision: packed.AnalysisID}, Locator: packed.AnalysisID,
				Method: "audio_similarity", ExtractorVersion: packed.PolicyVersion}
			if packed.LibraryCoverage != nil {
				claim.Coverage = &core.PreviewCoverage{Available: true, CoveredSeconds: packed.LibraryCoverage.CoveredSeconds, Source: "library_clap"}
			}
			add(core.EvidenceUnknown, claim, false)
		}
	}
	if preview.TrackID == "" && o.audioSession != nil {
		preview, _ = o.audioSession.Assessment(id)
	}
	for _, observed := range preview.Clauses {
		if observed.Clause.Kind != clause.Kind || observed.Clause.Text != clause.Text || observed.Clause.Scope != clause.Scope || observed.Clause.Negative != clause.Negative || clause.Degree != "" && observed.Clause.Degree != clause.Degree {
			continue
		}
		state := observed.State
		claim := core.RecordingClaim{Kind: clause.Kind, Value: clause.Text, State: state, Scope: "recording", EntityID: id, RecordingID: preview.Identity.RecordingID, Source: core.ContextSource{Provider: preview.Identity.Provider, Revision: preview.AnalysisID}, Locator: preview.AnalysisID, Method: "audio_observation", Coverage: preview.Coverage}
		// Audio assessments already apply polarity. Undo it before the common
		// reconciliation path, which applies it exactly once.
		if clause.Negative {
			state = reverseEvidence(state)
		}
		claim.State = state // claims describe Value; criteria carry request polarity
		absenceRequested := vocalAbsenceRequired(clause)
		// Detecting vocals can refute a no-vocals request. An instrumental
		// excerpt cannot refute a statement that vocals occur elsewhere.
		decisive := clause.Kind != "vocal" || observed.State == core.EvidenceMatch && !absenceRequested || observed.State == core.EvidenceMismatch && absenceRequested
		add(state, claim, decisive)
		vocalContradiction = vocalContradiction || absenceRequested && observed.State == core.EvidenceMismatch
	}
	if clause.Kind != "composer" && o.scorer != nil && (clause.Kind == "genre" || clause.Kind == "style") {
		coverage, scores, err := o.scorer.Score(ctx, clause.Text, []string{id})
		if err == nil && coverage.Complete {
			for _, score := range scores {
				if score.TrackID == id && score.State == core.EvidenceMatch && score.Score >= o.cfg.SemanticMinimumScore {
					add(core.EvidenceMatch, core.RecordingClaim{Kind: clause.Kind, Value: clause.Text, State: core.EvidenceMatch,
						Scope: "recording", EntityID: id, RecordingID: track.RecordingID,
						Source:  core.ContextSource{Provider: "grounded_semantic_index", Revision: o.sourceCatalogVersion},
						Locator: id, Method: "text_index"}, false)
				}
			}
		}
	}
	out.Conflict = positive && negative
	switch {
	case vocalContradiction || negative && !positive:
		out.State, out.Detail = core.EvidenceMismatch, "Recording evidence contradicts this characteristic."
	case out.Conflict:
		out.Detail = "Recording evidence disagrees; this characteristic remains unverified."
	case positive:
		out.State, out.Detail = core.EvidenceMatch, "Applicable recording evidence supports this characteristic."
	default:
		out.Detail = "Applicable recording evidence is missing or insufficient."
	}
	return out
}

func (o *Orchestrator) candidateCriteria(ctx context.Context, id string, intent core.MusicIntent) []core.CriterionAssessment {
	var out []core.CriterionAssessment
	for _, clause := range audio.Clauses(intent) {
		out = append(out, o.assessClause(ctx, id, clause, core.AudioAssessment{}))
	}
	return out
}

func (o *Orchestrator) weakRecordingSupport(ctx context.Context, id string, clause core.AudioClause) bool {
	if clause.Negative || clause.Strict {
		return false
	}
	track, _ := o.knowledgeTrackContext(ctx, id)
	for _, claim := range o.assessClause(ctx, id, clause, core.AudioAssessment{}).Claims {
		if (claim.Method == "embedded_tag" || claim.Method == "sidecar_tag" || claim.Method == "catalog_genre_hint") && claim.EntityID == id && claimValueState(claim, clause, core.GenreGraph{}) == core.EvidenceMatch {
			return true // useful retrieval metadata, never independent fulfillment
		}
		if track.IdentityStatus == core.ResolutionResolved && track.RecordingID != "" && claim.Method == "community_tag" && claim.Scope == "recording" && strings.EqualFold(claim.EntityID, track.RecordingID) && strings.EqualFold(claim.RecordingID, track.RecordingID) && claim.Source.Provider != "" && claimValueState(claim, clause, core.GenreGraph{}) == core.EvidenceMatch {
			return true
		}
	}
	return false
}

func (o *Orchestrator) strongClause(ctx context.Context, id string, clause core.AudioClause, preview core.AudioAssessment) bool {
	assessment := o.assessClause(ctx, id, clause, preview)
	if assessment.State != core.EvidenceMatch || assessment.Conflict {
		return false
	}
	if clause.Kind != "vocal" {
		return true
	}
	track, _ := o.knowledgeTrackContext(ctx, id)
	for _, claim := range assessment.Claims {
		value := core.NormalizeIdentityPart(clause.Text)
		if claim.Method == "audio_observation" && claim.State == core.EvidenceMatch && !clause.Negative && clause.Degree == "" && (value == "vocals" || value == "vocal" || value == "singing" || value == "voice") {
			return true // observed presence does not require observing every second
		}
		if claim.Method == "audio_observation" || claim.Method == "community_tag" || claim.Coverage != nil {
			continue
		}
		attributed := attributedRecordingClaim(claim, track) && decisiveClaimMethod(track, claim, clause)
		if claim.Method != "catalog_evidence" && !attributed {
			continue
		}
		state := claimValueState(claim, clause, core.GenreGraph{})
		if clause.Negative {
			state = reverseEvidence(state)
		}
		if state == core.EvidenceMatch {
			return true
		}
	}
	return false // preview screening remains useful but is not a whole-song claim
}

func (o *Orchestrator) recordCandidateDecision(ctx context.Context, candidate core.Candidate, intent core.MusicIntent, decision, reason string) core.Candidate {
	if !o.enhanced {
		return candidate
	}
	candidate.Criteria = o.candidateCriteria(ctx, candidate.Track.ID, intent)
	candidate.Decision = decision
	if reason != "" {
		candidate.DecisionReasons = appendUniqueString(candidate.DecisionReasons, reason)
	}
	o.rememberCandidateAssessment(candidate)
	return candidate
}

func (o *Orchestrator) qualityTargetMet(ctx context.Context, assembly candidateAssembly, intent core.MusicIntent) bool {
	if !assembly.complete(intent.Count) || len(audio.Clauses(intent)) == 0 || !includesOtherArtistsContext(ctx, o.cat, intent, assembly.sequence.Tracks) {
		return false
	}
	// Per-track tiering retains unsupported requests unless an exact static
	// capability annotation has been superseded by decisive recording proof.
	for _, track := range assembly.sequence.Tracks {
		if tier, _ := o.enhancedTier(ctx, core.Candidate{Track: track}, intent); tier != fitStrong {
			return false
		}
	}
	if len(playlistCoverageUnits(intent.EssentialCriteria)) > 0 {
		_, report, err := o.filterEnhancedEssential(ctx, candidatesForTracks(assembly.sequence.Tracks), intent.EssentialCriteria)
		if err != nil || len(playlistCoverageReasons(intent.EssentialCriteria, report.Matched)) > 0 {
			return false
		}
	}
	return ctx.Err() == nil && o.strongJourneyOrder(ctx, assembly.sequence.Tracks, intent)
}

// A track fitting some stage does not prove that it fits its position. Require
// a monotone assignment of the actual output to every fully supported stage.
func (o *Orchestrator) strongJourneyOrder(ctx context.Context, tracks []core.TrackRef, intent core.MusicIntent) bool {
	stages := journeyStageCriteria(intent)
	if intent.Mode != core.ModeJourney || len(stages) < 2 {
		return true
	}
	clauses := audio.Clauses(intent)
	reachable := make([]bool, len(stages))
	for position, track := range tracks {
		metadata, _ := o.knowledgeTrackContext(ctx, track.ID)
		comparisons := acousticComparisons(metadata, clauses)
		next := make([]bool, len(stages))
		for stage, criterion := range stages {
			canReach := position == 0 && stage == 0 || position > 0 && (reachable[stage] || stage > 0 && reachable[stage-1])
			if !canReach || !o.stageDateEligibleContext(ctx, track.ID, criterion.Scope, intent) {
				continue
			}
			groups := map[string]bool{}
			for i, comparison := range comparisons {
				clause := comparison.Clause
				if clause.Scope != criterion.Scope {
					continue
				}
				key := "group:" + clause.Group
				if clause.Group == "" {
					key = fmt.Sprint("clause:", i)
				}
				groups[key] = groups[key] || comparison.AcousticState != "opposing" && comparison.AcousticState != "conflicting" && o.strongClause(ctx, track.ID, clause, core.AudioAssessment{})
			}
			next[stage] = true
			for _, supported := range groups {
				next[stage] = next[stage] && supported
			}
		}
		reachable = next
	}
	return len(tracks) > 0 && reachable[len(stages)-1] && ctx.Err() == nil
}
