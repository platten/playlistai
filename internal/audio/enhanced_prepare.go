package audio

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/platten/playlistai/internal/core"
)

// PrepareEnhancedEvidence is the shared desktop/diagnostic implementation for
// bounded DSP and MERT acquisition. It stores only derived evidence, preserves
// completed work on cancellation, and performs cache-only refreshes when
// acquire is false.
func PrepareEnhancedEvidence(
	ctx context.Context,
	preview *Service,
	mert *MERTService,
	catalog string,
	model core.AudioRepresentationIdentity,
	refs []core.TrackRef,
	acquire bool,
	previous *core.EnhancedAudioSnapshot,
) (*core.EnhancedAudioSnapshot, error) {
	if preview == nil || catalog == "" {
		return nil, nil
	}
	if acquire {
		ctx = WithLazyEnhancedBudget(ctx, EnhancedTrackLimit, EnhancedTimeLimit)
	}
	input := core.EnhancedAudioInput{
		CatalogVersion: catalog, DSPVersion: DSPAnalysisVersion, Model: model,
		DSP: map[string]core.DSPAnalysis{}, Representations: map[string]core.AudioRepresentation{},
	}
	if !acquire && previous != nil {
		prior := previous.Input()
		if prior.CatalogVersion != input.CatalogVersion || prior.Model != input.Model || prior.PolicyVersion != core.EnhancedAudioPolicyVersion {
			return nil, fmt.Errorf("enhanced audio refresh requires the same catalog, model and policy as the initial snapshot")
		}
		input.PositiveCentroid, input.NegativeCentroid = prior.PositiveCentroid, prior.NegativeCentroid
		input.MERTSearch = prior.MERTSearch
	}
	seen := map[string]bool{}
	unique := make([]core.TrackRef, 0, len(refs))
	for _, ref := range refs {
		// Shared/user library packs already carry their own immutable DSP and
		// MERT evidence. Reacquiring a provider preview for a namespaced pack row
		// would duplicate that work into the base-catalog cache and can never
		// produce evidence with the pack's catalog identity.
		if strings.HasPrefix(ref.ID, "pack:") || strings.HasPrefix(ref.ID, "local:") || seen[ref.ID] {
			continue
		}
		seen[ref.ID] = true
		unique = append(unique, ref)
	}
	budget := EnhancedBudgetFor(ctx)
	missing := make([]core.TrackRef, 0, len(unique))
	for _, ref := range unique {
		if err := ctx.Err(); err != nil {
			completed, freezeErr := core.NewEnhancedAudioSnapshot(input)
			if freezeErr != nil {
				return nil, freezeErr
			}
			return completed, err
		}
		dspHit := preview.DSPStore == nil
		if preview.DSPStore != nil {
			dspCached, hit, _ := preview.DSPStore.Find(ctx, catalog, ref.ID, core.ProvisionalRecordingKey(ref), DSPAnalysisVersion)
			dspHit = hit
			if hit {
				input.DSP[ref.ID] = dspCached
			}
		}
		mertHit := mert == nil
		if mert != nil {
			cached, hit, _ := mert.Store.Find(ctx, catalog, ref.ID, core.ProvisionalRecordingKey(ref), input.Model)
			mertHit = hit
			if hit {
				input.Representations[ref.ID] = cached
			}
		}
		if acquire && (!dspHit || !mertHit) && ctx.Err() == nil && budget.Allow(ref.ID) {
			missing = append(missing, ref)
		}
	}
	if len(missing) > 0 {
		jobs := make(chan core.TrackRef)
		var workers sync.WaitGroup
		workers.Add(min(len(missing), AnalysisParallelism()))
		for range min(len(missing), AnalysisParallelism()) {
			go func() {
				defer workers.Done()
				for ref := range jobs {
					budgetCtx, cancel := budget.Context(ctx)
					if mert != nil {
						_, _, _, _ = mert.AnalyzeEnhancedPreview(budgetCtx, ref, catalog)
					} else {
						_, _, _ = preview.AnalyzeDSPPreview(budgetCtx, ref, catalog)
					}
					cancel()
				}
			}()
		}
		for _, ref := range missing {
			select {
			case jobs <- ref:
			case <-ctx.Done():
				close(jobs)
				workers.Wait()
				completed, freezeErr := core.NewEnhancedAudioSnapshot(input)
				if freezeErr != nil {
					return nil, freezeErr
				}
				return completed, ctx.Err()
			}
		}
		close(jobs)
		workers.Wait()
	}
	for _, ref := range unique {
		if preview.DSPStore != nil {
			if a, ok, err := preview.DSPStore.Find(ctx, catalog, ref.ID, core.ProvisionalRecordingKey(ref), DSPAnalysisVersion); err == nil && ok {
				input.DSP[ref.ID] = a
			}
		}
		if mert != nil {
			if a, ok, err := mert.Store.Find(ctx, catalog, ref.ID, core.ProvisionalRecordingKey(ref), input.Model); err == nil && ok {
				input.Representations[ref.ID] = a
			}
		}
	}
	if err := ctx.Err(); err != nil {
		completed, freezeErr := core.NewEnhancedAudioSnapshot(input)
		if freezeErr != nil {
			return nil, freezeErr
		}
		return completed, err
	}
	return core.NewEnhancedAudioSnapshot(input)
}
