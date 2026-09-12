package multichannel

import (
	"context"
	"errors"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// EnhancedAudioProvider runs at the assembly boundary, outside ranking. It must
// bound acquisition and preserve completed evidence on a user stop request.
type EnhancedAudioProvider func(context.Context, core.MusicIntent, core.TasteProfile, []core.TrackRef) (*core.EnhancedAudioSnapshot, error)

func (o *Orchestrator) WithEnhancedAudioProvider(provider EnhancedAudioProvider) *Orchestrator {
	o.enhancedProvider = provider
	return o
}
func (o *Orchestrator) WithEnhancedPreviewProvider(provider func() *audio.Service) *Orchestrator {
	o.enhancedPreviewProvider = provider
	return o
}

func (o *Orchestrator) prepareEnhanced(ctx context.Context, candidates []core.Candidate, intent core.MusicIntent, request ports.RecommendationRequest, references, required, waypoints []core.TrackRef) error {
	if intent.Controls.RecommendationMode != core.EnhancedHybrid || o.enhancedPrepared {
		return nil
	}
	o.enhancedPrepared = true
	if request.EnhancedAudio != nil {
		var err error
		o.enhancedSnapshot, err = core.NewEnhancedAudioSnapshot(request.EnhancedAudio.Input())
		return err
	}
	if o.enhancedProvider == nil {
		return nil
	}
	select {
	case <-request.StopChecking:
		o.enhancedSnapshot, _ = core.NewEnhancedAudioSnapshot(core.EnhancedAudioInput{})
		return nil
	default:
	}
	tracks := append([]core.TrackRef(nil), references...)
	tracks = append(tracks, required...)
	tracks = append(tracks, waypoints...)
	for _, reference := range negativeReferenceVectors(o.cat, intent) {
		for _, rep := range reference.reps {
			if meta, ok := o.cat.Meta(rep.id); ok {
				tracks = append(tracks, meta.Ref)
			}
		}
	}
	for _, c := range candidates {
		tracks = append(tracks, c.Track)
	}
	seen := map[string]bool{}
	unique := tracks[:0]
	for _, track := range tracks {
		if !seen[track.ID] {
			seen[track.ID] = true
			unique = append(unique, track)
		}
	}
	analysisCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-request.StopChecking:
			cancel()
		case <-analysisCtx.Done():
		}
	}()
	snapshot, err := o.enhancedProvider(analysisCtx, intent, request.Profile, unique)
	if errors.Is(err, context.Canceled) && ctx.Err() == nil && analysisCtx.Err() != nil {
		err = nil
	}
	if err != nil {
		return err
	}
	if snapshot != nil {
		o.enhancedSnapshot, err = core.NewEnhancedAudioSnapshot(snapshot.Input())
	} else {
		o.enhancedSnapshot, err = core.NewEnhancedAudioSnapshot(core.EnhancedAudioInput{})
	}
	return err
}
