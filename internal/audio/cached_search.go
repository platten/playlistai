package audio

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

const cachedSearchPageSize = 256

// A negative limit scans all pages. A read transaction freezes the cache while
// keyset pagination bounds memory and lets cancellation interrupt every page.
func (s *Store) VisitAnalyses(ctx context.Context, catalog string, model core.AudioModelIdentity, limit int, visit func(core.AudioAnalysis) bool) error {
	if limit == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // read-only snapshot
	cursorTrack, cursorRow := "", int64(0)
	seenTrack := ""
	seenKeys := map[string]bool{}
	read := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		pageSize := cachedSearchPageSize
		if limit > 0 {
			pageSize = min(pageSize, limit-read)
			if pageSize == 0 {
				return nil
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT track, rowid, data FROM analysis WHERE catalog=? AND model=? AND (track>? OR (track=? AND rowid<?)) ORDER BY track, rowid DESC LIMIT ?`, catalog, Fingerprint(model), cursorTrack, cursorTrack, cursorRow, pageSize)
		if err != nil {
			return err
		}
		count := 0
		for rows.Next() {
			if err := ctx.Err(); err != nil {
				rows.Close()
				return err
			}
			var raw string
			if err := rows.Scan(&cursorTrack, &cursorRow, &raw); err != nil {
				rows.Close()
				return err
			}
			count++
			read++
			if seenTrack != cursorTrack {
				seenTrack = cursorTrack
				clear(seenKeys)
			}
			var record core.AudioAnalysis
			if json.Unmarshal([]byte(raw), &record) != nil || validateAnalysis(record) != nil || !record.Identity.CurrentPolicy() || record.CatalogVersion != catalog || record.Model != model {
				continue
			}
			id := record.ID
			record.ID = ""
			if id != Fingerprint(record) {
				continue
			}
			record.ID = id
			if seenKeys[record.TrackKey] {
				continue
			}
			seenKeys[record.TrackKey] = true
			if !visit(record) {
				rows.Close()
				return nil
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if count < pageSize {
			return nil
		}
	}
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
	strictInstrumental := requiredInstrumentalScreen(s.clauses)
	var instrumentalGroups [][][]float32
	s.queryMu.Lock()
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
	if strictInstrumental {
		var ok bool
		instrumentalGroups, ok = s.instrumentalQueryGroupsLocked()
		positive = positive || ok
	}
	s.queryMu.Unlock()
	if !positive {
		return nil, ctx.Err()
	}
	var candidates []core.Candidate
	incomplete := false
	err := scanner.VisitAnalyses(ctx, s.catalog, s.snapshot.Model, -1, func(record core.AudioAnalysis) bool {
		if ctx.Err() != nil || s.stopped() {
			incomplete = true
			return false
		}
		meta, exists := ports.CatalogMeta(ctx, cat, record.TrackID)
		if ctx.Err() != nil {
			incomplete = true
			return false
		}
		if !exists || exclude[record.TrackID] || core.ProvisionalRecordingKey(meta.Ref) != record.TrackKey {
			return true
		}
		a := core.AudioAssessment{PolicyVersion: s.snapshot.PolicyVersion}
		instrumentalScore := 0.0
		if strictInstrumental {
			state, _ := instrumentalEvidenceWithGroups(record, instrumentalGroups)
			if state != core.EvidenceMatch {
				return true
			}
			instrumentalScore = 1
		}
		for _, q := range questions {
			a.Clauses = append(a.Clauses, core.AudioClauseAssessment{Clause: q.clause, Score: segmentSimilarity(q.vector, record.Segments, q.clause.Negative), ScoreAvailable: true, State: core.EvidenceUnknown})
		}
		c := core.Candidate{Track: meta.Ref}
		ApplyScores(&c, a)
		// Positive and negative evidence only order a retrieval channel. They
		// cannot satisfy a hard criterion or exclude an otherwise eligible track.
		score := instrumentalScore + c.Scores.SemanticMatch - c.Scores.SemanticNegativeMatch
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
		err = ctx.Err()
	}
	if err != nil {
		err = fmt.Errorf("cached audio search incomplete: %w", err)
	} else if incomplete {
		err = errors.New("cached audio search incomplete: stopped before cache exhaustion")
	}
	for i := range candidates {
		candidates[i].Sources[0].Rank = i + 1
	}
	if len(candidates) > 0 {
		s.mu.Lock()
		s.retrievalFingerprint = Fingerprint(candidates)
		s.mu.Unlock()
	}
	return candidates, err
}
