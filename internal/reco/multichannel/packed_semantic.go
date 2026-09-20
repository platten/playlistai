package multichannel

import (
	"context"
	"math"
	"sort"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func (o *Orchestrator) preparePackedQueries(ctx context.Context, intent core.MusicIntent) error {
	if intent.Controls.RecommendationMode != core.EnhancedHybrid && intent.Controls.RecommendationMode != core.CLAPFirst {
		return nil
	}
	catalog, ok := o.cat.(ports.LibrarySemanticCatalog)
	if !ok || o.audioProvider == nil {
		return nil
	}
	service := o.audioProvider()
	if service == nil || !service.ParityValidated || service.Analyzer == nil {
		return nil
	}
	queries, err := audio.EncodeClauseQueries(ctx, service.Analyzer, intent)
	if err != nil {
		// Optional local text inference cannot disable metadata or references.
		return ctx.Err()
	}
	o.packedModel = service.Analyzer.Identity()
	catalog.BindLibraryQueries(o.packedModel, queries)
	return nil
}

func (o *Orchestrator) packedAssessment(ctx context.Context, id string) (core.AudioAssessment, bool, error) {
	if err := ctx.Err(); err != nil {
		return core.AudioAssessment{}, false, err
	}
	if !o.enhanced && o.packedModel.Weights == "" {
		return core.AudioAssessment{}, false, nil
	}
	if value, ok := o.packedAssessments[id]; ok {
		return value, value.AnalysisID != "", nil
	}
	catalog, ok := o.cat.(ports.LibrarySemanticCatalog)
	if !ok {
		return core.AudioAssessment{}, false, nil
	}
	value, found, err := catalog.LibraryAssessment(ctx, id)
	if err == nil && o.packedAssessments != nil {
		o.packedAssessments[id] = value
	}
	return value, found, err
}

func (o *Orchestrator) packedSupport(ctx context.Context, id string) bool {
	assessment, ok, err := o.packedAssessment(ctx, id)
	if err != nil || !ok {
		return false
	}
	var candidate core.Candidate
	audio.ApplyScores(&candidate, assessment)
	return candidate.Available.SemanticMatch && candidate.Scores.SemanticMatch > math.Max(0, candidate.Scores.SemanticNegativeMatch)
}

// Only soft comparisons can skip online acquisition. Pooled packed evidence
// never manufactures strict or no-vocals verification.
func (o *Orchestrator) packedOnly(ctx context.Context, id string, intent core.MusicIntent) (core.AudioAssessment, bool) {
	if !o.enhanced || !o.bestAvailable || core.WantsInstrumental(intent) {
		return core.AudioAssessment{}, false
	}
	for _, clause := range audio.Clauses(intent) {
		if clause.Strict {
			return core.AudioAssessment{}, false
		}
	}
	value, ok, err := o.packedAssessment(ctx, id)
	return value, ok && err == nil
}

func (o *Orchestrator) appendPackedEvidence(result *core.Playlist) {
	var used []core.AudioAssessment
	for _, track := range result.Tracks {
		if a, ok := o.packedAssessments[track.ID]; ok && a.AnalysisID != "" {
			used = append(used, a)
		}
	}
	if len(used) == 0 {
		return
	}
	sort.Slice(used, func(i, j int) bool { return used[i].TrackID < used[j].TrackID })
	if result.AudioEvidence == nil {
		result.AudioEvidence = &core.AudioEvidenceSnapshot{Model: o.packedModel, PolicyVersion: "packed-clap/v1+" + audio.QueryPolicyVersion}
	}
	seen := map[string]bool{}
	for _, a := range result.AudioEvidence.Assessments {
		seen[a.TrackID] = a.AnalysisID != ""
	}
	for _, a := range used {
		if !seen[a.TrackID] {
			result.AudioEvidence.Assessments = append(result.AudioEvidence.Assessments, a)
		}
	}
	// Keep both observations when preview and packed excerpts cover one track.
	// The preview's ID is a component, not the identity of the combined evidence.
	model := o.packedModel
	result.AudioEvidence.LibraryModel = &model
	result.AudioEvidence.LibraryAssessments = used
	result.AudioEvidence.ID = audio.Fingerprint(struct {
		PreviewID   string
		Model       core.AudioModelIdentity
		Policy      string
		Assessments []core.AudioAssessment
	}{result.AudioEvidence.ID, model, "packed-clap-evidence/v2+" + audio.QueryPolicyVersion, used})
	result.Notices = append(result.Notices, core.PlaylistNotice{Code: "packed_audio_similarity", Detail: "Stored library audio was compared with your descriptions. Scores guide ranking; they do not verify unheard parts or strict requirements."})
}
