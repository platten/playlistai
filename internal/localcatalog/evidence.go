package localcatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
)

func (c *Catalog) EvidenceSource() core.LibraryEvidenceSource {
	raw, _ := json.Marshal(c.manifest.MERT)
	sum := sha256.Sum256(raw)
	return core.LibraryEvidenceSource{PackID: c.manifest.PackID, SpaceID: hex.EncodeToString(sum[:]), Generation: c.manifest.MERTGeneration, Scope: c.manifest.MERT.Scope}
}

func (c *Catalog) CLAPEvidenceSource() core.LibraryEvidenceSource {
	return c.clapEvidenceSource(c.manifest.CLAPModel)
}

func (c *Catalog) clapEvidenceSource(model *core.AudioModelIdentity) core.LibraryEvidenceSource {
	unknownPack := ""
	if model == nil {
		// Unknown runtime provenance is comparable only within this immutable
		// pack; equal paired weights cannot establish cross-pack equivalence.
		unknownPack = c.manifest.PackID
	}
	raw, _ := json.Marshal(struct {
		Space       librarypack.VectorSpace
		Model       *core.AudioModelIdentity
		UnknownPack string
	}{c.manifest.CLAP, model, unknownPack})
	sum := sha256.Sum256(raw)
	return core.LibraryEvidenceSource{PackID: c.manifest.PackID, SpaceID: hex.EncodeToString(sum[:]), Generation: c.manifest.CLAPGeneration, Scope: c.manifest.CLAP.Scope}
}

func (c *Catalog) LibraryVector(ctx context.Context, id string) (core.LibraryVector, bool, error) {
	localID, err := c.localID(id)
	if err != nil {
		return core.LibraryVector{}, false, nil
	}
	generation, done, err := c.withGeneration()
	if err != nil {
		return core.LibraryVector{}, false, err
	}
	defer done()
	vector, ok, err := generation.Vector(ctx, localID)
	return core.LibraryVector{Source: c.EvidenceSource(), Values: vector}, ok, err
}

func (c *CompositeCatalog) LibraryVector(ctx context.Context, id string) (core.LibraryVector, bool, error) {
	if c.local.owns(id) {
		return c.local.LibraryVector(ctx, id)
	}
	if c.mode == ModeLibraryOnly {
		return core.LibraryVector{}, false, nil
	}
	if meta, ok := c.baseMetadataContext(ctx, id); ok {
		if match, found := c.matchBase(ctx, meta); found {
			if vector, exists, err := c.local.LibraryVector(ctx, match.ID); exists || err != nil {
				return vector, exists, err
			}
		}
	}
	if source, ok := c.base.(interface {
		LibraryVector(context.Context, string) (core.LibraryVector, bool, error)
	}); ok {
		return source.LibraryVector(ctx, id)
	}
	return core.LibraryVector{}, false, nil
}

func (c *CompositeCatalog) LibraryDSPPreference(ctx context.Context, id string, intent core.MusicIntent) (float64, bool) {
	if c.local.owns(id) {
		return c.local.DSPPreferenceScore(ctx, id, intent)
	}
	if c.mode == ModeLibraryOnly {
		return 0, false
	}
	if meta, ok := c.baseMetadataContext(ctx, id); ok {
		if match, found := c.matchBase(ctx, meta); found {
			if score, exists := c.local.DSPPreferenceScore(ctx, match.ID, intent); exists {
				return score, true
			}
		}
	}
	if source, ok := c.base.(interface {
		LibraryDSPPreference(context.Context, string, core.MusicIntent) (float64, bool)
	}); ok {
		return source.LibraryDSPPreference(ctx, id, intent)
	}
	return 0, false
}

func (c *CompositeCatalog) matchBase(ctx context.Context, meta core.TrackMeta) (Track, bool) {
	refs, err := c.local.ArtistRecordings(ctx, meta.Ref.Artist)
	if err != nil {
		return Track{}, false
	}
	for _, ref := range refs {
		track, ok, err := c.local.Lookup(ctx, ref.ID)
		if err != nil || !ok {
			continue
		}
		if coreIdentityMatchesLocal(meta.Ref, track) {
			return track, true
		}
		other := Track{ISRC: meta.ISRC, MusicBrainzRecording: meta.MusicBrainzRecording, AcoustID: meta.AcoustID}
		if librarypack.SameRecording(packTrack(track), packTrack(other)) {
			return track, true
		}
	}
	return Track{}, false
}

// LibraryObservation exposes only recorded coverage. Legacy pooled MERT packs
// do not record observed duration, so their coverage deliberately stays unknown.
func (c *Catalog) LibraryObservation(ctx context.Context, id, family string) (core.AudioObservation, error) {
	localID, err := c.localID(id)
	if err != nil {
		return core.AudioObservation{}, nil
	}
	generation, done, err := c.withGeneration()
	if err != nil {
		return core.AudioObservation{}, err
	}
	defer done()
	track, found, err := generation.Lookup(ctx, localID)
	if err != nil || !found {
		return core.AudioObservation{}, err
	}
	observation := core.AudioObservation{}
	raw, _ := json.Marshal(struct {
		Source        core.LibraryEvidenceSource
		Track, Family string
		DSP           json.RawMessage
	}{c.EvidenceSource(), id, family, track.DSP})
	digest := sha256.Sum256(raw)
	observation.Fingerprint = hex.EncodeToString(digest[:])
	if family == "dsp" {
		var record portableDSPRecord
		if json.Unmarshal(track.DSP, &record) == nil {
			for _, window := range record.Windows {
				if window.ObservedSeconds > 0 && !math.IsNaN(window.ObservedSeconds) && !math.IsInf(window.ObservedSeconds, 0) {
					observation.Coverage.CoveredSeconds += window.ObservedSeconds
				}
			}
			observation.Coverage.Available = observation.Coverage.CoveredSeconds > 0
			observation.Coverage.Source = c.manifest.PackID
		}
	}
	return observation, ctx.Err()
}

func (c *CompositeCatalog) LibraryObservation(ctx context.Context, id, family string) (core.AudioObservation, error) {
	if c.local.owns(id) {
		return c.local.LibraryObservation(ctx, id, family)
	}
	if c.mode == ModeLibraryOnly {
		return core.AudioObservation{}, nil
	}
	if meta, ok := c.baseMetadataContext(ctx, id); ok {
		if match, found := c.matchBase(ctx, meta); found {
			return c.local.LibraryObservation(ctx, match.ID, family)
		}
	}
	if source, ok := c.base.(interface {
		LibraryObservation(context.Context, string, string) (core.AudioObservation, error)
	}); ok {
		return source.LibraryObservation(ctx, id, family)
	}
	return core.AudioObservation{}, ctx.Err()
}
