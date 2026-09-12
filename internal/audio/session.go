package audio

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/logging"
	"github.com/platten/playlistai/internal/musicconcepts"
)

const AnalysisBudget = 120 * time.Second
const SimilarityPolicyVersion = "clap-preview-similarity/v1"
const ScopedClausePolicyVersion = "scoped-clause-ranking/v1"

func CandidateAnalysisLimit(count int) int { return min(200, max(40, 4*count)) }

// Session is owned by one generation. The service and persistent records are
// shared; descriptions, clause embeddings, and eligibility are never shared.
type Session struct {
	service              *Service
	ctx                  context.Context
	cancel               context.CancelFunc
	stop                 <-chan struct{}
	catalog              string
	intent               core.MusicIntent
	clauses              []core.AudioClause
	fingerprint          string
	retrievalFingerprint string
	queries              map[string][]float32
	checked              map[string]core.AudioAssessment
	started              time.Time
	newCandidates        int
	limit                int
	snapshot             core.AudioEvidenceSnapshot
}

func (s *Service) Begin(ctx context.Context, intent core.MusicIntent, catalog string, stop <-chan struct{}) (*Session, error) {
	return s.BeginWithBudget(ctx, intent, catalog, stop, AnalysisBudget)
}

// BeginWithBudget lets iterative generation share one bounded session across
// candidate refills; it never resets the analysis or request cancellation state.
func (s *Service) BeginWithBudget(ctx context.Context, intent core.MusicIntent, catalog string, stop <-chan struct{}, budget time.Duration) (*Session, error) {
	if !s.ReadyFor(intent) {
		return nil, fmt.Errorf("audio: analysis requires an authorized, parity-validated model supporting the requested checks")
	}
	budgetCtx, cancel := context.WithTimeout(ctx, budget)
	go func() {
		select {
		case <-stop:
			cancel()
		case <-budgetCtx.Done():
		}
	}()
	x := &Session{service: s, ctx: budgetCtx, cancel: cancel, stop: stop, catalog: catalog, intent: intent, clauses: Clauses(intent), queries: map[string][]float32{}, checked: map[string]core.AudioAssessment{}, started: time.Now(), snapshot: core.AudioEvidenceSnapshot{Model: s.Analyzer.Identity(), PolicyVersion: s.Policy.Version}}
	x.limit = CandidateAnalysisLimit(intent.Count)
	if budget > AnalysisBudget {
		x.limit = min(1000, max(100, 20*intent.Count))
	}
	if !s.Policy.Valid() {
		x.snapshot.PolicyVersion = SimilarityPolicyVersion
	}
	if x.typedQueries() {
		x.snapshot.PolicyVersion += "+" + QueryPolicyVersion
	}
	if structuredClauses(x.clauses) {
		x.snapshot.PolicyVersion += "+" + ScopedClausePolicyVersion
	}
	if core.WantsInstrumental(intent) || requiredInstrumentalScreen(x.clauses) {
		if x.snapshot.PolicyVersion != "" {
			x.snapshot.PolicyVersion += "+"
		}
		x.snapshot.PolicyVersion += VocalPolicyVersion
	}
	// Runtime capabilities, resolved anchor assessments and RNG controls do
	// not change the musical questions. Replay keeps the same assessment key.
	x.fingerprint = Fingerprint(struct {
		Description string
		Clauses     []core.AudioClause
		QueryPolicy string
	}{intent.OriginalDescription, x.clauses, x.snapshot.PolicyVersion})
	return x, nil
}

func (s *Session) Close() {
	s.cancel()
	for _, v := range s.queries {
		clear(v)
	}
	clear(s.queries)
}

func (s *Session) Calibrated() bool { return s.service.Policy.Valid() }

// ShouldStop checks cancellation and the existing budget flags without copying,
// sorting or hashing every assessment. Poll this in candidate loops; materialize
// a full Snapshot only when returning evidence to the caller/history.
func (s *Session) ShouldStop() bool {
	if s.ctx != nil {
		s.stopped()
	}
	return s.snapshot.Stopped || s.snapshot.BudgetExhausted
}

func (s *Session) Snapshot() core.AudioEvidenceSnapshot {
	s.ShouldStop()
	s.snapshot.ElapsedMilliseconds = time.Since(s.started).Milliseconds()
	out := s.snapshot
	// Execution timings and cache hits do not change the evidence's identity.
	assessments := append([]core.AudioAssessment(nil), out.Assessments...)
	sort.Slice(assessments, func(i, j int) bool { return assessments[i].TrackID < assessments[j].TrackID })
	out.ID = Fingerprint(struct {
		Model       core.AudioModelIdentity
		Policy      string
		Assessments []core.AudioAssessment
		Retrieval   string `json:",omitempty"`
	}{out.Model, out.PolicyVersion, assessments, s.retrievalFingerprint})
	return out
}

func Clauses(intent core.MusicIntent) []core.AudioClause {
	var out []core.AudioClause
	for _, c := range intent.EssentialCriteria {
		out = append(out, core.AudioClause{Kind: c.Kind, Text: c.Value, Scope: c.Scope, Essential: c.Strength != "preferred", Strict: c.Strength == "required", Strength: c.Strength, Group: c.Group, ConceptID: c.ConceptID})
	}
	add := func(kind string, preferences []core.IntentPreference) {
		for _, p := range preferences {
			// A stage genre is already represented by its scoped essential
			// clause. Repeating it at playlist scope would flatten a journey.
			duplicate := false
			for _, c := range intent.EssentialCriteria {
				sameKind := c.Kind == kind || (c.Kind == "genre" || c.Kind == "style") && (kind == "genre" || kind == "style")
				sameScope := p.Scope == "" || c.Scope == p.Scope
				sameValue := strings.EqualFold(musicconcepts.Canonical(c.Kind, c.Value), musicconcepts.Canonical(kind, p.Value))
				coversStrength := (p.Strength != "required" || c.Strength == "required") && (p.Strength != "essential" || c.Strength != "preferred")
				duplicate = duplicate || sameKind && sameScope && sameValue && coversStrength && c.Group == p.Group && p.Influence != core.InfluenceNegative && p.Degree != "mostly" && p.Degree != "reduced"
			}
			if duplicate {
				continue
			}
			scope := p.Scope
			if scope == "" {
				scope = "playlist"
			}
			out = append(out, core.AudioClause{Kind: kind, Text: p.Value, Scope: scope, Negative: p.Influence == core.InfluenceNegative,
				Essential: p.Strength == "essential" || p.Strength == "required", Strict: p.Strength == "required", Strength: p.Strength, ConceptID: p.ConceptID, Degree: p.Degree, Group: p.Group})
		}
	}
	add("genre", intent.Preferences.Genres)
	add("style", intent.Preferences.Styles)
	add("mood", intent.Preferences.Moods)
	add("instrumentation", intent.Preferences.Instrumentation)
	add("texture", intent.Preferences.TextureDescriptions)
	add("vocal", intent.Preferences.VocalRequests())
	for _, c := range intent.HardConstraints {
		if core.HardConstraintSupported(c.Kind) || c.Kind == "require_album" || c.Kind == "require_artist" {
			continue
		}
		kind := c.Kind
		text := c.Value
		negative := strings.HasPrefix(kind, "exclude_")
		switch kind {
		case "exclude_style", "require_style":
			kind = "style"
		case "exclude_vocal":
			kind = "vocal"
			text = musicconcepts.Canonical("vocal", text)
		case "exclude_vocals", "require_instrumental":
			kind = "vocal"
			text = "vocals"
			negative = true
		case "require_vocals":
			kind = "vocal"
			text = "vocals"
		}
		clause := core.AudioClause{Kind: kind, Text: text, Scope: "playlist", Strict: true, Negative: negative}
		if c.Kind == "exclude_vocal" {
			clause.Strength = "required"
			if concept, ok := musicconcepts.Find("vocal", text); ok {
				clause.ConceptID = concept.ID
			}
		}
		out = append(out, clause)
	}
	if core.WantsInstrumental(intent) {
		// Vocal preferences must not become optional ranking hints.
		strict := false
		for _, clause := range out {
			strict = strict || clause.Strict && instrumentalClause(clause)
		}
		if !strict {
			out = append(out, core.AudioClause{Kind: "vocal", Text: "vocals", Scope: "playlist", Strict: true, Negative: true})
		}
	}
	if len(out) == 0 && len(intent.References) == 0 && len(intent.RequiredTracks) == 0 && intent.Start == nil && intent.Destination == nil && len(intent.Seeds.TrackIDs) == 0 && len(intent.Seeds.Queries) == 0 && strings.TrimSpace(intent.OriginalDescription) != "" {
		out = append(out, core.AudioClause{Kind: "description", Text: intent.OriginalDescription, Scope: "playlist"})
	}
	return out
}

func (s *Session) stopped() bool {
	select {
	case <-s.stop:
		s.snapshot.Stopped = true
		return true
	default:
	}
	if s.ctx.Err() != nil {
		s.snapshot.BudgetExhausted = true
		return true
	}
	return false
}

// Check returns unknown for unavailable evidence. All channels call this same
// method before ranking. anchor=true is bounded separately by six proposals.
func (s *Session) Check(ctx context.Context, track core.TrackRef, anchor bool) (core.AudioAssessment, error) {
	if prior, ok := s.checked[track.ID]; ok {
		return prior, nil
	}
	out := core.AudioAssessment{TrackID: track.ID, IntentFingerprint: s.fingerprint, PolicyVersion: s.snapshot.PolicyVersion, Detail: "Preview evidence is unavailable; musical fit is unknown."}
	defer func() {
		// Unknown and missing evidence stays visible in the history snapshot.
		// Only reusable analyses have an analysis ID and enter the feature DB.
		s.checked[track.ID] = out
		s.snapshot.Assessments = append(s.snapshot.Assessments, out)
	}()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if s.stopped() {
		out.Detail = "Analysis stopped before this track could be checked."
		return out, nil
	}
	record, hit, err := s.service.Store.Find(s.ctx, s.catalog, track.ID, core.ProvisionalRecordingKey(track), s.service.Analyzer.Identity())
	if err != nil {
		if parentErr := ctx.Err(); parentErr != nil {
			return out, parentErr
		}
		if s.stopped() {
			out.Detail = "Analysis stopped before this track could be checked."
			return out, nil
		}
		return out, err
	}
	completed := false
	if !hit {
		if !anchor && s.newCandidates >= s.limit {
			s.snapshot.BudgetExhausted = true
			out.Detail = "The new-analysis count limit was reached before this track could be checked."
			return out, nil
		}
		if !anchor {
			s.newCandidates++
		}
		s.snapshot.NewAnalyses++
		var bytes int64
		var comparisonErr error
		record, bytes, err = s.service.analyzePreview(s.ctx, track, s.catalog, func(record core.AudioAnalysis) error {
			out = s.assess(record, out)
			if comparisonErr = ctx.Err(); comparisonErr == nil {
				comparisonErr = s.service.Store.PutAssessment(ctx, out)
			}
			completed = comparisonErr == nil
			return comparisonErr
		})
		s.snapshot.BytesFetched += bytes
		if comparisonErr != nil {
			return out, comparisonErr
		}
		if err != nil {
			out.Identity = record.Identity
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			// Optional work may exhaust the session after every CLAP clause
			// was assessed and saved. Keep that completed current-generation
			// result while stopping further acquisition on the expired budget.
			if completed {
				s.stopped()
			}
			return out, nil
		}
	} else {
		s.snapshot.CacheHits++
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if completed {
		return out, nil
	}
	out = s.assess(record, out)
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := s.service.Store.PutAssessment(ctx, out); err != nil {
		return out, err
	}
	return out, nil
}

func (s *Session) assess(record core.AudioAnalysis, out core.AudioAssessment) core.AudioAssessment {
	// The complete derived CLAP record includes every preview-segment audio
	// embedding. Diagnostic writes remain opt-in and memory-only; logging at this
	// point avoids an extra database read for both cached and new analyses.
	logging.Diagnostic(s.ctx, "analysis.clap_audio_embedding", record)
	out.AnalysisID = record.ID
	out.Identity = record.Identity
	out.Detail = "Checked against the available preview only; the rest of the recording is unassessed."
	out.Eligible = true
	vocalState, vocalScore := core.EvidenceUnknown, 0.0
	needsVocalScreen := core.WantsInstrumental(s.intent) || requiredInstrumentalScreen(s.clauses)
	if needsVocalScreen {
		vocalState, vocalScore = s.instrumentalEvidence(record)
		out.Detail = "CLAP vocal screening compared instrumental, singing, speech and non-musical descriptions for every sampled preview segment. This is a preview-only zero-shot assessment, not a guarantee about the complete recording."
	}
	scored := false
	for _, clause := range s.clauses {
		assessment := core.AudioClauseAssessment{Clause: clause, State: core.EvidenceUnknown}
		// A contrastive similarity cannot prove strict absence, particularly
		// outside the preview. Unsupported hard clauses also remain unknown.
		supported := clause.Kind == "genre" || clause.Kind == "style" || clause.Kind == "mood" || clause.Kind == "instrumentation" || clause.Kind == "texture" || clause.Kind == "vocal" || clause.Kind == "description"
		if needsVocalScreen && instrumentalClause(clause) {
			assessment.State, assessment.Score = vocalState, vocalScore
			assessment.ScoreAvailable = vocalState != core.EvidenceUnknown
			if !clause.Negative && len(s.queries[instrumentalPrompts[0]]) > 0 {
				assessment.Score = segmentSimilarity(s.queries[instrumentalPrompts[0]], record.Segments, false)
			}
		} else if supported && (!clause.Strict || clause.Kind != "vocal") {
			query := s.clauseQuery(clause)
			if len(query) > 0 {
				assessment.Score = segmentSimilarity(query, record.Segments, clause.Negative)
				assessment.ScoreAvailable = true
				if s.service.Policy.Valid() {
					assessment.State = core.EvidenceMismatch
					if !clause.Negative && assessment.Score >= s.service.Policy.MinimumPositive || clause.Negative && assessment.Score <= s.service.Policy.MaximumNegative {
						assessment.State = core.EvidenceMatch
					}
				}
			}
		}
		out.Clauses = append(out.Clauses, assessment)
		scored = scored || assessment.ScoreAvailable
	}
	out.Eligible = clausesEligible(out.Clauses, s.service.Policy.Valid())
	if len(s.clauses) == 0 {
		out.Eligible = false
		out.Detail = "No musical clauses were available to assess against the description."
	}
	if !scored {
		out.Eligible = false
		out.Detail = "The preview was analyzed, but no usable description comparison was available."
	}
	return out
}

// A recording may fit any journey stage before sequencing, but must meet every
// independent requirement of that stage. Explicit OR alternatives share a group.
// Playlist requirements always apply; unknown required evidence never passes.
func clausesEligible(clauses []core.AudioClauseAssessment, calibrated bool) bool {
	type groupState struct {
		scope   string
		matched bool
	}
	groups := map[string]groupState{}
	stages := map[string]bool{}
	for i, a := range clauses {
		c := a.Clause
		scope := c.Scope
		if scope == "" {
			scope = "playlist"
		}
		if strings.HasPrefix(scope, "journey_") {
			stages[scope] = true
		}
		if !c.Strict && (!c.Essential || !calibrated && !instrumentalClause(c)) {
			continue
		}
		group := c.Group
		if group == "" {
			group = fmt.Sprintf("\x00%d", i)
		}
		key := scope + "\x00" + group
		g := groups[key]
		g.scope, g.matched = scope, g.matched || a.State == core.EvidenceMatch
		groups[key] = g
	}
	for _, group := range groups {
		if strings.HasPrefix(group.scope, "journey_") {
			stages[group.scope] = stages[group.scope] && group.matched
		} else if !group.matched {
			return false
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

func segmentSimilarity(query []float32, segments []core.AudioSegment, negative bool) float64 {
	total, best := 0.0, -1.0
	for _, segment := range segments {
		score := 0.0
		for i, x := range query {
			score += float64(x) * float64(segment.Embedding[i])
		}
		score = math.Max(-1, math.Min(1, score))
		total += score
		best = max(best, score)
	}
	if negative {
		return best
	}
	return total / float64(len(segments))
}

// Assessment returns request-local evidence for read-only scoring. Callers must
// not mutate its slices; the session owns the evidence until generation ends.
func (s *Session) Assessment(trackID string) (core.AudioAssessment, bool) {
	a, ok := s.checked[trackID]
	return a, ok
}

func (s *Session) Criterion(trackID string, criterion core.MusicalCriterion) core.EvidenceState {
	for _, a := range s.checked[trackID].Clauses {
		if a.Clause.Essential && a.Clause.Kind == criterion.Kind && a.Clause.Text == criterion.Value && a.Clause.Scope == criterion.Scope {
			return a.State
		}
	}
	return core.EvidenceUnknown
}

// StageSimilarity guides placement within a journey without claiming that an
// uncalibrated similarity verifies stage membership.
func (s *Session) StageSimilarity(trackID string, criterion core.MusicalCriterion) (float64, bool) {
	for _, a := range s.checked[trackID].Clauses {
		if a.Clause.Essential && a.Clause.Kind == criterion.Kind && a.Clause.Text == criterion.Value && a.Clause.Scope == criterion.Scope && a.ScoreAvailable {
			return a.Score, true
		}
	}
	return 0, false
}

func ApplyScores(candidate *core.Candidate, a core.AudioAssessment) {
	if strings.Contains(a.PolicyVersion, QueryPolicyVersion) || strings.Contains(a.PolicyVersion, ScopedClausePolicyVersion) {
		applyTypedScores(candidate, a)
		return
	}
	var positives, negatives int
	var positive, negative float64
	stageScores := map[string]float64{}
	stageCounts := map[string]int{}
	type facet struct {
		sum   float64
		count int
		scope string
	}
	facets := map[string]facet{}
	for _, clause := range a.Clauses {
		if clause.State == core.EvidenceUnknown && !clause.ScoreAvailable {
			continue
		}
		if clause.Clause.Negative {
			negatives++
			negative += clause.Score
		} else if strings.HasPrefix(clause.Clause.Scope, "journey_") {
			stageScores[clause.Clause.Scope] += clause.Score
			stageCounts[clause.Clause.Scope]++
		} else {
			positives++
			positive += clause.Score
		}
		if !clause.Clause.Negative {
			kind := clause.Clause.Kind
			if kind == "style" {
				kind = "genre"
			}
			key := clause.Clause.Scope + "\x00" + kind
			f := facets[key]
			f.sum += clause.Score
			f.count++
			f.scope = clause.Clause.Scope
			facets[key] = f
		}
	}
	if strings.Contains(a.PolicyVersion, "typed-clause-ensemble/v1") {
		// Multiple genre aliases must not outvote a single requested mood.
		// Equal facet weighting is a ranking policy, not calibrated confidence.
		positive, positives = 0, 0
		clear(stageScores)
		clear(stageCounts)
		keys := make([]string, 0, len(facets))
		for key := range facets {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			f := facets[key]
			mean := f.sum / float64(f.count)
			if strings.HasPrefix(f.scope, "journey_") {
				stageScores[f.scope] += mean
				stageCounts[f.scope]++
			} else {
				positive += mean
				positives++
			}
		}
	}
	if len(stageScores) > 0 {
		best := -1.0
		for scope, total := range stageScores {
			best = max(best, total/float64(stageCounts[scope]))
		}
		positive += best
		positives++
	}
	if positives > 0 {
		candidate.Scores.SemanticMatch = positive / float64(positives)
		candidate.Available.SemanticMatch = true
	}
	if negatives > 0 {
		candidate.Scores.SemanticNegativeMatch = negative / float64(negatives)
		candidate.Available.SemanticNegativeMatch = true
	}
}

func structuredClauses(clauses []core.AudioClause) bool {
	for _, c := range clauses {
		if c.Strength != "" || c.Degree != "" || c.Group != "" {
			return true
		}
	}
	return false
}
