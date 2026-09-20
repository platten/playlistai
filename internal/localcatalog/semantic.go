package localcatalog

import (
	"context"
	"math"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/ports"
)

// v6 GraphSHA256 is the indexer's paired-encoder/tokenizer fingerprint, not
// merely an audio graph digest. Equal dimensions or model names do not suffice.
func (c *Catalog) compatibleCLAP(model core.AudioModelIdentity) bool {
	return c.manifest.CLAPCompatible(model)
}

func (c *CompositeCatalog) BindLibraryQueries(model core.AudioModelIdentity, queries []core.AudioClauseVector) {
	c.semanticModel = model
	c.semanticQueries = nil
	// Keep potentially compatible queries for per-record v7 evidence even when
	// a mixed legacy merge has no manifest-wide runtime identity. Such queries
	// are deliberately withheld from unrestricted pooled-vector retrieval.
	manifest := c.local.manifest
	if manifest.CLAPModel == nil {
		manifest.CLAPModel = &model
	}
	if manifest.CLAPCompatible(model) {
		for _, query := range queries {
			if len(query.Values) == model.Dimension && validClauseVector(query.Values) {
				query.Values = append([]float32(nil), query.Values...)
				c.semanticQueries = append(c.semanticQueries, query)
			}
		}
	}
	if base, ok := c.base.(ports.LibrarySemanticCatalog); ok && c.mode != ModeLibraryOnly {
		base.BindLibraryQueries(model, queries)
	}
}

func (c *CompositeCatalog) libraryQueries(packID string) []core.AudioClauseVector {
	if c.local.manifest.PackID == packID {
		if c.local.compatibleCLAP(c.semanticModel) {
			return c.semanticQueries
		}
		return nil
	}
	if base, ok := c.base.(interface {
		libraryQueries(string) []core.AudioClauseVector
	}); ok && c.mode != ModeLibraryOnly {
		return base.libraryQueries(packID)
	}
	return nil
}

func (c *Catalog) LibraryCLAPVector(ctx context.Context, id string) (core.LibraryVector, bool, error) {
	localID, err := c.localID(id)
	if err != nil {
		return core.LibraryVector{}, false, nil
	}
	generation, done, err := c.withGeneration()
	if err != nil {
		return core.LibraryVector{}, false, err
	}
	defer done()
	vector, ok, err := generation.CLAPVector(ctx, localID)
	if err != nil || !ok {
		return core.LibraryVector{}, ok, err
	}
	model := c.manifest.CLAPModel
	if model == nil {
		evidence, _, err := generation.CLAPEvidence(ctx, localID)
		if err != nil {
			return core.LibraryVector{}, false, err
		}
		if evidence != nil {
			model = evidence.Model
		}
	}
	return core.LibraryVector{Source: c.clapEvidenceSource(model), Values: vector}, true, nil
}

func (c *CompositeCatalog) LibraryCLAPVector(ctx context.Context, id string) (core.LibraryVector, bool, error) {
	if c.local.owns(id) {
		return c.local.LibraryCLAPVector(ctx, id)
	}
	if c.mode == ModeLibraryOnly {
		return core.LibraryVector{}, false, nil
	}
	if meta, ok := c.baseMetadata(id); ok {
		if match, found := c.matchBase(ctx, meta); found {
			if vector, exists, err := c.local.LibraryCLAPVector(ctx, match.ID); exists || err != nil {
				return vector, exists, err
			}
		}
	}
	if base, ok := c.base.(ports.LibrarySemanticCatalog); ok {
		return base.LibraryCLAPVector(ctx, id)
	}
	return core.LibraryVector{}, false, nil
}

func (c *CompositeCatalog) LibraryAssessment(ctx context.Context, id string) (core.AudioAssessment, bool, error) {
	if err := ctx.Err(); err != nil {
		return core.AudioAssessment{}, false, err
	}
	localID := id
	owned := c.local.owns(id)
	if !owned && c.mode != ModeLibraryOnly {
		if meta, ok := c.baseMetadata(id); ok {
			if match, found := c.matchBase(ctx, meta); found {
				localID, owned = match.ID, true
			}
		}
	}
	if owned && len(c.semanticQueries) > 0 {
		vector, exists, err := c.local.LibraryCLAPVector(ctx, localID)
		if err != nil {
			return core.AudioAssessment{}, false, err
		}
		if exists {
			evidence, err := c.local.clapSegmentEvidence(ctx, localID)
			if err != nil {
				return core.AudioAssessment{}, false, err
			}
			paired := c.local.compatibleCLAP(c.semanticModel)
			if evidence != nil && evidence.Model != nil {
				paired = *evidence.Model == c.semanticModel
			}
			if !paired {
				if base, ok := c.base.(ports.LibrarySemanticCatalog); ok && c.mode != ModeLibraryOnly {
					return base.LibraryAssessment(ctx, id)
				}
				return core.AudioAssessment{}, false, nil
			}
			out := core.AudioAssessment{TrackID: id, AnalysisID: c.local.manifest.PackID + ":clap:" + localID,
				PolicyVersion: "packed-clap/segments-v2+" + audio.QueryPolicyVersion,
				IntentFingerprint: audio.Fingerprint(struct {
					Model   core.AudioModelIdentity
					Queries []core.AudioClauseVector
				}{c.semanticModel, c.semanticQueries}),
				Detail: "CLAP similarity against sampled library audio; no calibrated or whole-recording fit claim."}
			if evidence != nil {
				out.LibraryCoverage = &core.LibraryCLAPCoverage{CoveredSeconds: evidence.CoveredSeconds, Incomplete: evidence.Incomplete, PartialReason: evidence.PartialReason}
				for _, segment := range evidence.Segments {
					if segment.Validity == "valid" {
						out.LibraryCoverage.Segments = append(out.LibraryCoverage.Segments, core.LibraryAudioInterval{StartSeconds: segment.StartSeconds, EndSeconds: segment.EndSeconds})
					}
				}
			}
			for _, query := range c.semanticQueries {
				if err := ctx.Err(); err != nil {
					return core.AudioAssessment{}, false, err
				}
				score, available := packedClauseScore(query, vector.Values, evidence)
				out.Clauses = append(out.Clauses, core.AudioClauseAssessment{Clause: query.Clause,
					Score: score, ScoreAvailable: available, State: core.EvidenceUnknown})
			}
			// Unknown strict requirements still require their own evidence checks.
			return out, true, nil
		}
	}
	if base, ok := c.base.(ports.LibrarySemanticCatalog); ok && c.mode != ModeLibraryOnly {
		return base.LibraryAssessment(ctx, id)
	}
	return core.AudioAssessment{}, false, nil
}

func (c *Catalog) clapSegmentEvidence(ctx context.Context, id string) (*librarypack.CLAPEvidence, error) {
	localID, err := c.localID(id)
	if err != nil {
		return nil, err
	}
	generation, done, err := c.withGeneration()
	if err != nil {
		return nil, err
	}
	defer done()
	evidence, _, err := generation.CLAPEvidence(ctx, localID)
	return evidence, err
}

func validClauseVector(values []float32) bool {
	var norm float64
	for _, value := range values {
		norm += float64(value) * float64(value)
	}
	// A mean of normalized caption vectors may have norm below one. Preserve
	// that disagreement instead of renormalizing and inflating its score.
	return norm > 0 && norm <= 1.0002 && !math.IsNaN(norm) && !math.IsInf(norm, 0)
}

func packedClauseScore(query core.AudioClauseVector, pooled []float32, evidence *librarypack.CLAPEvidence) (float64, bool) {
	dot := func(vector []float32) (float64, bool) {
		if len(vector) != len(query.Values) || !validClauseVector(query.Values) || !validClauseVector(vector) {
			return 0, false
		}
		var score float64
		for i, value := range vector {
			score += float64(value) * float64(query.Values[i])
		}
		return math.Max(-1, math.Min(1, score)), true
	}
	if evidence == nil {
		return dot(pooled)
	}
	var weighted, duration float64
	strongest := math.Inf(-1)
	for _, segment := range evidence.Segments {
		if segment.Validity != "valid" || segment.ObservedSeconds <= 0 {
			continue
		}
		score, available := dot(segment.Vector)
		if !available {
			continue
		}
		weighted += score * segment.ObservedSeconds
		duration += segment.ObservedSeconds
		strongest = math.Max(strongest, score)
	}
	if duration <= 0 {
		return 0, false
	}
	if query.Clause.Negative {
		// A strong unwanted trait in one observed interval must not disappear
		// into quieter or unrelated excerpts. This remains an uncalibrated score.
		return strongest, true
	}
	return weighted / duration, true
}

func normalizedTextVector(input []float32) []float32 {
	var norm float64
	for _, value := range input {
		norm += float64(value) * float64(value)
	}
	if norm <= 0 || math.IsNaN(norm) || math.IsInf(norm, 0) {
		return nil
	}
	out := make([]float32, len(input))
	for i, value := range input {
		out[i] = float32(float64(value) / math.Sqrt(norm))
	}
	return out
}

var _ ports.LibrarySemanticCatalog = (*CompositeCatalog)(nil)
