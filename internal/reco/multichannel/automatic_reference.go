package multichannel

import (
	"context"
	"sort"
	"strings"

	"github.com/platten/playlistai/internal/core"
)

type automaticReferenceAnchor struct {
	estimated     bool
	artist        string
	artistRequest bool
	tracks        []core.WeightedTrack
}

type automaticReferenceGate struct {
	batch   *automaticBatch
	anchors []automaticReferenceAnchor
	own     map[string]string
	graph   map[string][]core.RetrievalEvidence
}

// Reference admission is separate from descriptive musical fit. Explicit
// preferred references allow grounded estimates; missing legacy strengths keep
// the conservative calibrated policy.
// Everything here reads the immutable batch and the completed prepared graph
// page; inferred anchors and private taste never become requested identities.
func newAutomaticReferenceGate(ctx context.Context, b *automaticBatch, intent core.MusicIntent, graph []core.Candidate) automaticReferenceGate {
	g := automaticReferenceGate{batch: b, own: map[string]string{}, graph: map[string][]core.RetrievalEvidence{}}
	if intent.PreparedMusicSnapshot != "" {
		for _, c := range graph {
			g.graph[c.Track.ID] = append(g.graph[c.Track.ID], c.Sources...)
		}
	}
	refs := append([]core.IntentReference(nil), intent.References...)
	// A typed category journey's endpoint is not a playlist-wide request for
	// that artist. Its explicit identity/order remains enforced by assembly.
	if len(automaticScopes(intent)) == 0 {
		refs = append(refs, intent.Journey.Waypoints...)
		for _, ref := range []*core.IntentReference{intent.Start, intent.Destination} {
			if ref != nil {
				refs = append(refs, *ref)
			}
		}
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		key := referenceKey(ref)
		if ref.Influence != core.InfluencePositive || ref.Kind != core.ReferenceArtist && ref.Kind != core.ReferenceTrack || seen[key] {
			continue
		}
		seen[key] = true
		a := automaticReferenceAnchor{estimated: ref.Strength == "preferred", artistRequest: ref.Kind == core.ReferenceArtist, tracks: referenceTrackIdentitiesContext(ctx, b, ref, false)}
		if ref.Kind == core.ReferenceArtist && ref.Resolution != nil && ref.Resolution.Status == core.ResolutionResolved && ref.Resolution.Selected != nil {
			id := ref.Resolution.Selected.EntityID
			if validArtistMBID(id) {
				a.artist = strings.ToLower(id)
			}
		}
		for _, track := range a.tracks {
			g.own[track.TrackID] = a.artist
		}
		if ref.Kind == core.ReferenceTrack {
			artists := map[string]bool{}
			for _, track := range a.tracks {
				r := b.recordings[track.TrackID]
				if r.Ref.ID != track.TrackID || !r.Matched || r.IdentityStatus != core.ResolutionResolved {
					continue
				}
				for _, id := range r.ArtistIDs {
					if validArtistMBID(id) {
						artists[strings.ToLower(id)] = true
					}
				}
			}
			var ids []string
			for id := range artists {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids[:min(8, len(ids))] {
				a.artist = id
				g.anchors = append(g.anchors, a)
			}
			if len(ids) > 0 {
				continue
			}
		}
		g.anchors = append(g.anchors, a)
	}
	// A specifically requested recording can intentionally differ from the
	// reference artist. It still must pass every factual/musical constraint.
	for _, ref := range append(append([]core.IntentReference(nil), intent.RequiredTracks...), refs...) {
		if ref.Influence == core.InfluenceNegative || ref.Kind != core.ReferenceTrack {
			continue
		}
		for _, track := range referenceTrackIdentitiesContext(ctx, b, ref, false) {
			g.own[track.TrackID] = ""
		}
	}
	endpoints := append([]core.IntentReference(nil), intent.Journey.Waypoints...)
	for _, ref := range []*core.IntentReference{intent.Start, intent.Destination} {
		if ref != nil {
			endpoints = append(endpoints, *ref)
		}
	}
	for _, ref := range endpoints {
		if ref.Influence == core.InfluenceNegative {
			continue
		}
		artist := ""
		if ref.Kind == core.ReferenceArtist {
			if ref.Resolution == nil || ref.Resolution.Status != core.ResolutionResolved || ref.Resolution.Selected == nil {
				continue
			}
			if id := ref.Resolution.Selected.EntityID; validArtistMBID(id) {
				artist = strings.ToLower(id)
			}
		}
		for _, track := range resolvedRequiredTracksContext(ctx, b, []core.IntentReference{ref}) {
			g.own[track.ID] = artist
		}
	}
	return g
}

func (g automaticReferenceGate) active() bool { return len(g.anchors) > 0 }

func automaticRecordingArtists(b *automaticBatch, id string) map[string]bool {
	ids := map[string]bool{}
	if recording := b.recordings[id]; recording.Ref.ID == id && recording.IdentityStatus == core.ResolutionResolved {
		for _, artist := range recording.ArtistIDs {
			if validArtistMBID(artist) {
				ids[strings.ToLower(artist)] = true
			}
		}
	}
	for _, artist := range catalogArtistIdentities(b.meta[id].Annotations) {
		if validArtistMBID(artist.id) {
			ids[strings.ToLower(artist.id)] = true
		}
	}
	return ids
}

func (g automaticReferenceGate) match(c core.Candidate) (bool, string) {
	if !g.active() {
		return true, ""
	}
	artists := automaticRecordingArtists(g.batch, c.Track.ID)
	for id, requiredArtist := range g.own {
		if requiredArtist != "" && len(artists) > 0 && !artists[requiredArtist] {
			continue
		}
		left, right := g.batch.meta[id].Ref, g.batch.meta[c.Track.ID].Ref
		if id == c.Track.ID || left.RecordingIdentity != "" && strings.EqualFold(left.RecordingIdentity, right.RecordingIdentity) {
			return true, "Requested catalog recording identity; no additional sonic qualities are asserted."
		}
	}
	for _, anchor := range g.anchors {
		if anchor.artistRequest && anchor.artist != "" && artists[anchor.artist] {
			return true, "Requested artist identity is present in recording credits; no additional sonic qualities are asserted."
		}
		if anchor.estimated && g.estimatedAudioNeighbor(c, anchor) {
			return true, "Compatible reference-audio comparison; uncalibrated estimated discovery."
		}
		calibration, supported := g.audioNeighbor(c, anchor)
		if anchor.artist == "" || !supported && !anchor.estimated {
			continue
		}
		for _, source := range g.graph[c.Track.ID] {
			parent, neighbor, ok := strings.Cut(source.QueryID, ":")
			limit := 8
			if !anchor.artistRequest && source.Channel == "musicgraph_artist" {
				parent, neighbor, ok, limit = source.QueryID, source.QueryID, true, 20
			} else if source.Channel != "musicgraph_neighbor" {
				continue
			}
			if !ok || parent != anchor.artist || !validArtistMBID(neighbor) || source.Rank < 1 || source.Rank > limit || !finite(source.Score) || source.Score <= 0 {
				continue
			}
			if len(artists) > 0 && !artists[neighbor] {
				continue // a known conflicting catalog artist is not a graph match
			}
			if anchor.estimated {
				return true, "Pinned artist relationship with an exact recording join; estimated discovery, not verified musical resemblance."
			}
			return true, "A pinned artist relationship and direct seed-audio comparison meet calibration " + calibration.Version + " in space " + calibration.ModelFingerprint + "; sampled-audio similarity, not a probability."
		}
	}
	return false, "Explicit reference similarity lacks matching identity or independent prepared graph and calibrated seed-audio evidence."
}

func (g automaticReferenceGate) estimatedAudioNeighbor(c core.Candidate, anchor automaticReferenceAnchor) bool {
	for _, vectors := range []map[string]core.LibraryVector{g.batch.clap, g.batch.mert} {
		for _, ref := range anchor.tracks {
			if !g.seedMatchesAnchor(ref.TrackID, anchor) {
				continue
			}
			if score, ok := libraryCosine(vectors[c.Track.ID], vectors[ref.TrackID]); ok && score > 0 {
				return true
			}
		}
	}
	return false
}

func (g automaticReferenceGate) seedMatchesAnchor(id string, anchor automaticReferenceAnchor) bool {
	recording := g.batch.recordings[id]
	if recording.Ref.ID != id || !recording.Matched || recording.IdentityStatus != core.ResolutionResolved {
		return false
	}
	if anchor.artistRequest && anchor.artist != "" {
		artists := automaticRecordingArtists(g.batch, id)
		if len(artists) > 0 && !artists[anchor.artist] {
			return false
		}
	}
	return true
}

// Compare the candidate itself, irrespective of which retrieval channel found
// it. A graph relationship and positive cosine do not substitute for validation.
func (g automaticReferenceGate) audioNeighbor(c core.Candidate, anchor automaticReferenceAnchor) (core.AudioSimilarityCalibration, bool) {
	for _, vectors := range []map[string]core.LibraryVector{g.batch.clap, g.batch.mert} {
		candidate := vectors[c.Track.ID]
		for _, ref := range anchor.tracks {
			if !g.seedMatchesAnchor(ref.TrackID, anchor) {
				continue // A quarantined or unresolved seed cannot authenticate another recording.
			}
			vector := vectors[ref.TrackID]
			score, compatible := libraryCosine(candidate, vector)
			if !compatible {
				continue
			}
			for _, calibration := range g.batch.calibrations {
				if calibration.Supports(score, candidate.Source.SpaceID, "reference", "reference_similarity") {
					return calibration, true
				}
			}
		}
	}
	return core.AudioSimilarityCalibration{}, false
}
