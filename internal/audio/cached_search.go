package audio

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

const CachedSearchLimit = 20000

func (s *Store) VisitAnalyses(ctx context.Context, catalog string, model core.AudioModelIdentity, limit int, visit func(core.AudioAnalysis) bool) error {
	if limit <= 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM analysis WHERE catalog=? AND model=? ORDER BY track, rowid DESC LIMIT ?`, catalog, Fingerprint(model), min(limit, CachedSearchLimit))
	if err != nil {
		return err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		var record core.AudioAnalysis
		if json.Unmarshal([]byte(raw), &record) != nil || validateAnalysis(record) != nil || record.CatalogVersion != catalog || record.Model != model {
			continue
		}
		id := record.ID
		record.ID = ""
		if id != Fingerprint(record) {
			continue
		}
		record.ID = id
		key := record.TrackID + "\x00" + record.TrackKey
		if seen[key] {
			continue
		}
		seen[key] = true
		if !visit(record) {
			break
		}
	}
	return rows.Err()
}

// CachedCandidates expands recall without preview downloads, admission or
// assessment writes. Candidates must still pass every normal eligibility and
// preview check. Scan order, limits and tie-breaking are deterministic.
func (s *Session) CachedCandidates(ctx context.Context, cat ports.Catalog, limit int, exclude map[string]bool) ([]core.Candidate, error) {
	scanner, ok := s.service.Store.(ports.CachedAnalysisScanner)
	if !ok || limit <= 0 || s.intent.VerificationPolicy != core.BestAvailable {
		return nil, nil
	}
	limit = min(limit, 512)
	type question struct {
		clause core.AudioClause
		vector []float32
	}
	var questions []question
	positive := false
	for _, clause := range s.clauses {
		if clause.Strict {
			continue
		}
		q := s.clauseQuery(clause)
		if len(q) == 0 {
			continue
		}
		positive = positive || !clause.Negative
		questions = append(questions, question{clause, q})
	}
	if !positive {
		return nil, ctx.Err()
	}
	var candidates []core.Candidate
	err := scanner.VisitAnalyses(s.ctx, s.catalog, s.snapshot.Model, CachedSearchLimit, func(record core.AudioAnalysis) bool {
		if ctx.Err() != nil || s.stopped() {
			return false
		}
		meta, exists := cat.Meta(record.TrackID)
		if !exists || exclude[record.TrackID] || core.ProvisionalRecordingKey(meta.Ref) != record.TrackKey {
			return true
		}
		a := core.AudioAssessment{PolicyVersion: s.snapshot.PolicyVersion}
		for _, q := range questions {
			a.Clauses = append(a.Clauses, core.AudioClauseAssessment{Clause: q.clause, Score: segmentSimilarity(q.vector, record.Segments, q.clause.Negative), ScoreAvailable: true, State: core.EvidenceUnknown})
		}
		c := core.Candidate{Track: meta.Ref}
		ApplyScores(&c, a)
		// Positive and negative evidence only order a retrieval channel. They
		// cannot satisfy a hard criterion or exclude an otherwise eligible track.
		score := c.Scores.SemanticMatch - c.Scores.SemanticNegativeMatch
		c.Sources = []core.RetrievalEvidence{{Channel: "cached_audio", QueryID: s.fingerprint, Score: score, QueryWeight: 1}}
		at := sort.Search(len(candidates), func(i int) bool {
			other := candidates[i].Sources[0].Score
			return score > other || score == other && c.Track.ID < candidates[i].Track.ID
		})
		if at < limit {
			candidates = append(candidates, core.Candidate{})
			copy(candidates[at+1:], candidates[at:])
			candidates[at] = c
			if len(candidates) > limit {
				candidates = candidates[:limit]
			}
		}
		return true
	})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	for i := range candidates {
		candidates[i].Sources[0].Rank = i + 1
	}
	if len(candidates) > 0 {
		s.retrievalFingerprint = Fingerprint(candidates)
	}
	return candidates, err
}
