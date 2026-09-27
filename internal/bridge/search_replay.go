package bridge

import (
	"encoding/json"
	"fmt"

	"github.com/platten/playlistai/internal/core"
)

// An unchanged saved request reuses a frozen result and pool. Changing controls
// is an explicit new generation and does not reuse changing provider evidence.
func (a *API) frozenReplay(req BuildPlaylistRequest, intent core.MusicIntent, recent []core.TrackRef) (*core.Playlist, error) {
	if req.Search == nil {
		return nil, nil
	}
	identity := req.Reproducibility
	current, err := generationIdentity(intent, identity.CatalogVersion, identity.AlgorithmVersion, identity.ProfileVersion, identity.ProfileSnapshot, recent)
	if err != nil {
		return nil, err
	}
	if current.IntentFingerprint != identity.IntentFingerprint {
		return nil, nil
	}
	if current.ContextFingerprint != identity.ContextFingerprint {
		// Older saved requests could contain only IDs even though their hash
		// used complete catalog references. Fill missing fields only; a full
		// exact fingerprint match is still required before historical replay.
		completed := append([]core.TrackRef(nil), recent...)
		if catalog := a.runtime().Catalog; catalog != nil {
			for i, track := range completed {
				if meta, ok := catalog.Meta(track.ID); ok {
					if track.Artist == "" {
						completed[i].Artist = meta.Ref.Artist
					}
					if track.Title == "" {
						completed[i].Title = meta.Ref.Title
					}
					if track.RecordingIdentity == "" {
						completed[i].RecordingIdentity = meta.Ref.RecordingIdentity
					}
				}
			}
		}
		current, err = generationIdentity(intent, identity.CatalogVersion, identity.AlgorithmVersion, identity.ProfileVersion, identity.ProfileSnapshot, resolveRecentSelections(nil, completed))
		if err != nil {
			return nil, err
		}
		if current.ContextFingerprint != identity.ContextFingerprint {
			return nil, nil
		}
	}
	// Exact historical delivery reads the frozen result, not today's catalog
	// or engine. Regeneration without a valid snapshot still checks versions.
	if err := req.Search.Validate(); err != nil {
		return nil, err
	}
	if identity.SearchSnapshot != req.Search.ID {
		return nil, fmt.Errorf("saved search identity does not match; regenerate explicitly")
	}
	withEvidenceIdentity(&current, req.Search.Result.AudioEvidence)
	current.ID = audioIdentity(current.ID, req.Search.ID)
	if req.Search.Result.EnhancedAudio != nil {
		current.ID = audioIdentity(current.ID, req.Search.Result.EnhancedAudio.Fingerprint())
	}
	if current.ID != identity.ID {
		return nil, fmt.Errorf("saved generation identity does not match its frozen evidence; regenerate explicitly")
	}
	// Copy before attaching the snapshot, so the persisted Result stays acyclic.
	blob, err := json.Marshal(req.Search)
	if err != nil {
		return nil, err
	}
	var snapshot core.SearchSnapshot
	if err := json.Unmarshal(blob, &snapshot); err != nil {
		return nil, err
	}
	result := *snapshot.Result
	result.Search = &snapshot
	return &result, nil
}
