package multichannel

import (
	"context"
	"sort"
	"strings"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// A multi-stage category journey needs candidates from every retrieval branch
// to reach the bounded analysis queue. Provider fusion may otherwise place all
// exact hits for one well-covered stage ahead of broader candidates for sparse
// compound stages. This only balances retrieval supply; it does not establish
// stage membership or bypass later musical evidence checks.
func prioritizeJourneySupply(candidates []core.Candidate, intent core.MusicIntent) []core.Candidate {
	stages := journeyStageCriteria(intent)
	if intent.Mode != core.ModeJourney || len(stages) < 2 {
		return candidates
	}
	buckets := make([][]core.Candidate, len(stages))
	var rest []core.Candidate
	for _, candidate := range candidates {
		bucket := -1
		best := 0
		for stage, criterion := range stages {
			value := core.NormalizeIdentityPart(criterion.Value)
			for _, source := range candidate.Sources {
				query := core.NormalizeIdentityPart(source.QueryID)
				score := 0
				if value != "" && strings.Contains(query, value) {
					score = 2
				} else {
					for _, term := range strings.Fields(value) {
						if len([]rune(term)) >= 4 && strings.Contains(query, term) {
							score = 1
							break
						}
					}
				}
				if score > best {
					best, bucket = score, stage
				}
			}
		}
		if bucket < 0 {
			rest = append(rest, candidate)
		} else {
			buckets[bucket] = append(buckets[bucket], candidate)
		}
	}
	out := make([]core.Candidate, 0, len(candidates))
	for round := 0; ; round++ {
		added := false
		for stage := range buckets {
			if round < len(buckets[stage]) {
				out = append(out, buckets[stage][round])
				added = true
			}
		}
		if !added {
			break
		}
	}
	return append(out, rest...)
}

// Grounded recording annotations should reach the bounded analysis queue
// before broader compound-term discovery. Round-robin by stage so a dense end
// genre cannot hide a sparse start; remaining candidates retain the ordinary
// retrieval-supply balancing and still face every downstream fit check.
func (o *Orchestrator) prioritizeGroundedJourneySupply(ctx context.Context, candidates []core.Candidate, intent core.MusicIntent) []core.Candidate {
	stages := journeyStageCriteria(intent)
	if intent.Mode != core.ModeJourney || len(stages) < 2 {
		return candidates
	}
	buckets := make([][]core.Candidate, len(stages))
	var rest []core.Candidate
	for _, candidate := range candidates {
		stage := -1
		for index, criterion := range stages {
			if o.bestCriterion(ctx, candidate.Track.ID, criterion) == core.EvidenceMatch {
				stage = index
				break
			}
		}
		if stage < 0 {
			rest = append(rest, candidate)
		} else {
			buckets[stage] = append(buckets[stage], candidate)
		}
	}
	out := make([]core.Candidate, 0, len(candidates))
	for round := 0; ; round++ {
		added := false
		for stage := range buckets {
			if round < len(buckets[stage]) {
				out = append(out, buckets[stage][round])
				added = true
			}
		}
		if !added {
			break
		}
	}
	return append(out, prioritizeJourneySupply(rest, intent)...)
}

// Put candidates with affirmative recording-level evidence for any requested
// musical facet ahead of lexical discovery leads. This changes only which
// bounded candidates are checked first; it neither proves the remaining
// facets nor bypasses preview, eligibility, or final relevance checks.
func (o *Orchestrator) prioritizeGroundedRequestSupply(ctx context.Context, candidates []core.Candidate, intent core.MusicIntent) []core.Candidate {
	if !o.enhanced {
		return candidates
	}
	var criteria []core.MusicalCriterion
	for _, clause := range audio.Clauses(intent) {
		if !clause.Negative {
			criteria = append(criteria, core.MusicalCriterion{Kind: clause.Kind, Value: clause.Text, Scope: clause.Scope})
		}
	}
	if len(criteria) == 0 {
		return candidates
	}
	grounded := make([]core.Candidate, 0, len(candidates))
	unknown := make([]core.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		matched := false
		for _, criterion := range criteria {
			if o.bestCriterion(ctx, candidate.Track.ID, criterion) == core.EvidenceMatch {
				matched = true
				break
			}
		}
		if matched {
			grounded = append(grounded, candidate)
		} else {
			unknown = append(unknown, candidate)
		}
	}
	return append(grounded, unknown...)
}

func recommendationArtistCap(intent core.MusicIntent) int {
	if !genreArtistDiversity(intent) {
		return 0
	}
	return max(1, (intent.Count+2)/3)
}

func concentratedArtistPool(candidates []core.Candidate, intent core.MusicIntent) bool {
	cap := recommendationArtistCap(intent)
	if cap == 0 {
		return false
	}
	uses := map[string]int{}
	for _, candidate := range candidates {
		artist := core.NormalizeIdentityPart(candidate.Track.Artist)
		uses[artist]++
		if artist != "" && uses[artist] > cap {
			return true
		}
	}
	return false
}

// Bound by retrieval evidence, never source membership. Local and outside
// recordings compete on the same scale before final musical ranking.
func boundedMetadataCandidates(candidates []core.Candidate, limit int) []core.Candidate {
	ordered := append([]core.Candidate(nil), candidates...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Scores.RetrievalFusion != ordered[j].Scores.RetrievalFusion {
			return ordered[i].Scores.RetrievalFusion > ordered[j].Scores.RetrievalFusion
		}
		return ordered[i].Track.ID < ordered[j].Track.ID
	})
	return ordered[:min(len(ordered), max(0, limit))]
}

// Preparatory instrumental discovery exists specifically to supply recordings
// that can satisfy a strict vocal exclusion. Keep those grounded candidates in
// the bounded analysis page ahead of broader genre retrieval; every candidate
// still passes preview screening and the ordinary final relevance floor.
func prioritizeInstrumentalKnowledge(candidates []core.Candidate, intent core.MusicIntent) []core.Candidate {
	out, _ := prioritizeInstrumentalKnowledgePrefix(candidates, intent)
	return out
}

// Preparatory discovery already resolved these recordings into the request
// catalog. Put them in the first bounded pool as well as the continuation
// stream so a deep installed-pack page cannot delay strict vocal screening
// until after the request budget. Catalog membership and every downstream
// musical-fit check still apply.
func instrumentalKnowledgeCandidates(intent core.MusicIntent) []core.Candidate {
	if !core.WantsInstrumental(intent) || intent.Knowledge == nil {
		return nil
	}
	out := make([]core.Candidate, 0, len(intent.Knowledge.Candidates))
	for index, track := range intent.Knowledge.Candidates {
		out = append(out, core.Candidate{
			Track: track,
			Sources: []core.RetrievalEvidence{{
				Channel: "metadata_discovery", QueryID: "instrumental", Rank: index + 1, QueryWeight: 1,
			}},
		})
	}
	return out
}

// Instrumental discovery proposals are candidate leads, not retrieval anchors,
// until their previews have passed the strict vocal screen. Enhanced Hybrid
// puts those recordings directly through the iterative checker, so retaining
// them as anchors would analyze them twice and could let an unknown proposal
// influence retrieval before it was eligible.
func deferInstrumentalCandidateAnchors(intent core.MusicIntent) core.MusicIntent {
	if !core.WantsInstrumental(intent) || intent.Knowledge == nil || len(intent.Knowledge.Candidates) == 0 {
		return intent
	}
	candidates := make(map[string]bool, len(intent.Knowledge.Candidates))
	for _, track := range intent.Knowledge.Candidates {
		candidates[track.ID] = true
	}
	kept := intent.InferredAnchors[:0]
	for _, anchor := range intent.InferredAnchors {
		if anchor.Role == "online instrumental candidate; requires CLAP screening" && candidates[anchor.Reference.TrackID] {
			intent.AnchorAttempts = append(intent.AnchorAttempts, anchor)
			continue
		}
		kept = append(kept, anchor)
	}
	intent.InferredAnchors = kept
	return intent
}

func prioritizeInstrumentalKnowledgePrefix(candidates []core.Candidate, intent core.MusicIntent) ([]core.Candidate, int) {
	if !core.WantsInstrumental(intent) || intent.Knowledge == nil || len(intent.Knowledge.Candidates) == 0 {
		return candidates, 0
	}
	priority := make(map[string]bool, len(intent.Knowledge.Candidates))
	for _, track := range intent.Knowledge.Candidates {
		priority[track.ID] = true
	}
	out := append([]core.Candidate(nil), candidates...)
	sort.SliceStable(out, func(i, j int) bool {
		return priority[out[i].Track.ID] && !priority[out[j].Track.ID]
	})
	count := 0
	for count < len(out) && priority[out[count].Track.ID] {
		count++
	}
	// Provider candidates are valuable strict-vocal-screening leads, but a
	// failed preview batch must not consume the entire bounded window before
	// installed-pack candidates are tried. Give at most one requested playlist's
	// worth of grounded leads the first screening window, then place the ordinary
	// pool before any remaining provider alternatives.
	leadingKnowledge := max(3, intent.Count)
	if count > leadingKnowledge {
		reordered := make([]core.Candidate, 0, len(out))
		reordered = append(reordered, out[:leadingKnowledge]...)
		reordered = append(reordered, out[count:]...)
		reordered = append(reordered, out[leadingKnowledge:count]...)
		out, count = reordered, leadingKnowledge
	}
	return out, count
}

// Top up a shortlist before analysis when identity exclusions or duplicate
// recordings shrink retrieval pages. Provisional candidates are excluded only
// from preparation queries, not continuation: unselected alternatives remain
// available for a later refill. The request context and query bound cap work.
func (o *Orchestrator) prepareRecommendationPool(ctx context.Context, initial []core.Candidate, request ports.RetrievalRequest, eligible *eligibility, acceptedRecordings map[string]bool, size int) ([]core.Candidate, error) {
	size = min(size, o.cfg.MaxCandidates)
	attempted := request.AttemptedIDs
	request.AttemptedIDs = make(map[string]struct{}, len(attempted)+size)
	for id := range attempted {
		request.AttemptedIDs[id] = struct{}{}
	}
	seenRecordings := make(map[string]bool, size)
	artistUses := map[string]int{}
	artistCap := recommendationArtistCap(request.Intent)
	if artistCap > 0 {
		// The final contract needs at least three artists. Bound concentration
		// while preparing the pool so a deep run from one highly represented
		// artist cannot hide equally eligible artists on later retrieval pages.
		for _, track := range request.RecentSelections {
			artistUses[core.NormalizeIdentityPart(track.Artist)]++
		}
	}
	var pool []core.Candidate
	for queries := 0; queries < iterativeAttempts && len(pool) < size; queries++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw := initial
		initial = nil
		if len(raw) == 0 {
			var err error
			raw, err = o.retriever.Retrieve(ctx, request)
			if err != nil {
				// A later page failure must not erase already prepared candidates.
				// The caller can still assess them, unless cancellation forbids work.
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return pool, err
			}
		}
		before := len(request.AttemptedIDs)
		// A provider's concatenation order must not decide which source owns
		// the bounded pool. Compare common retrieval evidence before truncation.
		raw = boundedMetadataCandidates(raw, len(raw))
		raw = o.prioritizeGroundedRequestSupply(ctx, raw, request.Intent)
		raw = o.prioritizeGroundedJourneySupply(ctx, raw, request.Intent)
		raw = prioritizeInstrumentalKnowledge(raw, request.Intent)
		for _, candidate := range raw {
			id := candidate.Track.ID
			if _, seen := request.AttemptedIDs[id]; seen {
				continue
			}
			request.AttemptedIDs[id] = struct{}{}
			meta, exists := o.cat.Meta(id)
			if !exists {
				attempted[id] = struct{}{}
				continue
			}
			candidate.Track = meta.Ref
			key := core.ProvisionalRecordingKey(candidate.Track)
			artist := core.NormalizeIdentityPart(candidate.Track.Artist)
			if artistCap > 0 && artist != "" && artistUses[artist] >= artistCap {
				attempted[id] = struct{}{}
				continue
			}
			batch, err := eligible.filter(ctx, []core.Candidate{candidate}, request.Intent.Constraints.ExcludeSeedArtists)
			if err != nil {
				return nil, err
			}
			if len(batch) == 0 || acceptedRecordings[key] || seenRecordings[key] {
				attempted[id] = struct{}{}
				continue
			}
			seenRecordings[key] = true
			if len(pool) < o.cfg.MaxCandidates {
				pool = append(pool, candidate)
				artistUses[artist]++
			}
		}
		if len(request.AttemptedIDs) == before {
			break // exhausted or non-advancing retrieval must not spin
		}
	}
	return pool, nil
}
