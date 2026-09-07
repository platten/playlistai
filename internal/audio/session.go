package audio

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/core"
)

const AnalysisBudget = 120 * time.Second

func CandidateAnalysisLimit(count int) int { return min(200, max(40, 4*count)) }

// Session is owned by one generation. The service and persistent records are
// shared; descriptions, clause embeddings, and eligibility are never shared.
type Session struct {
	service       *Service
	ctx           context.Context
	cancel        context.CancelFunc
	stop          <-chan struct{}
	catalog       string
	intent        core.MusicIntent
	clauses       []core.AudioClause
	fingerprint   string
	queries       map[string][]float32
	checked       map[string]core.AudioAssessment
	started       time.Time
	newCandidates int
	snapshot      core.AudioEvidenceSnapshot
}

func (s *Service) Begin(ctx context.Context, intent core.MusicIntent, catalog string, stop <-chan struct{}) (*Session, error) {
	if !s.Ready() {
		return nil, fmt.Errorf("audio: analysis requires an installed parity-validated model and calibrated policy")
	}
	budgetCtx, cancel := context.WithTimeout(ctx, AnalysisBudget)
	go func() {
		select {
		case <-stop:
			cancel()
		case <-budgetCtx.Done():
		}
	}()
	x := &Session{service: s, ctx: budgetCtx, cancel: cancel, stop: stop, catalog: catalog, intent: intent, clauses: Clauses(intent), queries: map[string][]float32{}, checked: map[string]core.AudioAssessment{}, started: time.Now(), snapshot: core.AudioEvidenceSnapshot{Model: s.Analyzer.Identity(), PolicyVersion: s.Policy.Version}}
	// Runtime capabilities, resolved anchor assessments and RNG controls do
	// not change the musical questions. Replay keeps the same assessment key.
	x.fingerprint = Fingerprint(struct {
		Description string
		Clauses     []core.AudioClause
	}{intent.OriginalDescription, x.clauses})
	return x, nil
}

func (s *Session) Close() {
	s.cancel()
	for _, v := range s.queries {
		clear(v)
	}
	clear(s.queries)
}

func (s *Session) Snapshot() core.AudioEvidenceSnapshot {
	if s.ctx != nil {
		s.stopped()
	}
	s.snapshot.ElapsedMilliseconds = time.Since(s.started).Milliseconds()
	out := s.snapshot
	// Execution timings and cache hits do not change the evidence's identity.
	assessments := append([]core.AudioAssessment(nil), out.Assessments...)
	sort.Slice(assessments, func(i, j int) bool { return assessments[i].TrackID < assessments[j].TrackID })
	out.ID = Fingerprint(struct {
		Model       core.AudioModelIdentity
		Policy      string
		Assessments []core.AudioAssessment
	}{out.Model, out.PolicyVersion, assessments})
	return out
}

func Clauses(intent core.MusicIntent) []core.AudioClause {
	var out []core.AudioClause
	for _, c := range intent.EssentialCriteria {
		out = append(out, core.AudioClause{Kind: c.Kind, Text: c.Value, Scope: c.Scope, Essential: true})
	}
	add := func(kind string, preferences []core.IntentPreference) {
		for _, p := range preferences {
			out = append(out, core.AudioClause{Kind: kind, Text: p.Value, Scope: "playlist", Negative: p.Influence == core.InfluenceNegative})
		}
	}
	add("genre", intent.Preferences.Genres)
	add("style", intent.Preferences.Styles)
	add("mood", intent.Preferences.Moods)
	add("instrumentation", intent.Preferences.Instrumentation)
	add("texture", intent.Preferences.TextureDescriptions)
	if p := intent.Preferences.VocalPreference; p != nil {
		add("vocal", []core.IntentPreference{*p})
	}
	for _, c := range intent.HardConstraints {
		if core.HardConstraintSupported(c.Kind) || c.Kind == "require_album" {
			continue
		}
		kind := c.Kind
		text := c.Value
		negative := strings.HasPrefix(kind, "exclude_")
		switch kind {
		case "exclude_style", "require_style":
			kind = "style"
		case "exclude_vocals", "require_instrumental":
			kind = "vocal"
			text = "vocals"
			negative = true
		case "require_vocals":
			kind = "vocal"
			text = "vocals"
		}
		out = append(out, core.AudioClause{Kind: kind, Text: text, Scope: "playlist", Strict: true, Negative: negative})
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
	out := core.AudioAssessment{TrackID: track.ID, IntentFingerprint: s.fingerprint, PolicyVersion: s.service.Policy.Version, Detail: "Preview evidence is unavailable; musical fit is unknown."}
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
		return out, err
	}
	if !hit {
		if !anchor && s.newCandidates >= CandidateAnalysisLimit(s.intent.Count) {
			s.snapshot.BudgetExhausted = true
			out.Detail = "The new-analysis count limit was reached before this track could be checked."
			return out, nil
		}
		if !anchor {
			s.newCandidates++
		}
		s.snapshot.NewAnalyses++
		var bytes int64
		record, bytes, err = s.service.AnalyzePreview(s.ctx, track, s.catalog)
		s.snapshot.BytesFetched += bytes
		if err != nil {
			out.Identity = record.Identity
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			return out, nil
		}
	} else {
		s.snapshot.CacheHits++
	}
	out.AnalysisID = record.ID
	out.Identity = record.Identity
	out.Detail = "Checked against the available preview only; the rest of the recording is unassessed."
	out.Eligible = true
	journeySeen, journeyMatched := false, false
	for _, clause := range s.clauses {
		assessment := core.AudioClauseAssessment{Clause: clause, State: core.EvidenceUnknown}
		// A contrastive similarity cannot prove strict absence, particularly
		// outside the preview. Unsupported hard clauses also remain unknown.
		supported := clause.Kind == "genre" || clause.Kind == "style" || clause.Kind == "mood" || clause.Kind == "instrumentation" || clause.Kind == "texture" || clause.Kind == "vocal"
		if supported && (!clause.Strict || clause.Kind != "vocal") {
			query, ok := s.queries[clause.Text]
			if !ok {
				query, err = s.service.Analyzer.EmbedText(s.ctx, clause.Text)
				if err == nil && validVector(query, record.Model.Dimension) {
					s.queries[clause.Text] = query
				} else {
					query = nil
				}
			}
			if len(query) > 0 {
				assessment.Score = segmentSimilarity(query, record.Segments, clause.Negative)
				assessment.State = core.EvidenceMismatch
				if !clause.Negative && assessment.Score >= s.service.Policy.MinimumPositive || clause.Negative && assessment.Score <= s.service.Policy.MaximumNegative {
					assessment.State = core.EvidenceMatch
				}
			}
		}
		out.Clauses = append(out.Clauses, assessment)
		if clause.Essential && strings.HasPrefix(clause.Scope, "journey_") {
			journeySeen = true
			journeyMatched = journeyMatched || assessment.State == core.EvidenceMatch
		} else if (clause.Essential || clause.Strict) && assessment.State != core.EvidenceMatch {
			out.Eligible = false
		}
	}
	if journeySeen && !journeyMatched {
		out.Eligible = false
	}
	if len(s.clauses) == 0 {
		out.Eligible = false
		out.Detail = "No musical clauses were available to assess against the description."
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := s.service.Store.PutAssessment(ctx, out); err != nil {
		return out, err
	}
	return out, nil
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

func (s *Session) Criterion(trackID string, criterion core.MusicalCriterion) core.EvidenceState {
	for _, a := range s.checked[trackID].Clauses {
		if a.Clause.Essential && a.Clause.Kind == criterion.Kind && a.Clause.Text == criterion.Value && a.Clause.Scope == criterion.Scope {
			return a.State
		}
	}
	return core.EvidenceUnknown
}

func ApplyScores(candidate *core.Candidate, a core.AudioAssessment) {
	var positives, negatives int
	var positive, negative float64
	for _, clause := range a.Clauses {
		if clause.State == core.EvidenceUnknown {
			continue
		}
		if clause.Clause.Negative {
			negatives++
			negative += clause.Score
		} else {
			positives++
			positive += clause.Score
		}
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
