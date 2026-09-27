package app

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/musicgraph"
	"github.com/platten/playlistai/internal/ports"
)

// This is identity preparation, never a musical-suitability gate. Every chosen
// artist seed must carry that MBID in catalog credits or in a prepared recording
// relationship; a same-name catalog hit alone is insufficient.
func (c *Container) prepareAutomaticIntent(ctx context.Context, intent core.MusicIntent, cat ports.Catalog, resolver ports.ReferenceResolver) (core.MusicIntent, error) {
	if resolver == nil || cat == nil {
		return intent, errors.New("app: artist seed preparation requires catalog and resolver")
	}
	graph, err := c.graphForIntent(ctx, intent)
	if err != nil {
		return intent, err
	}
	intent = intent.Normalized()
	prepare := func(ref *core.IntentReference) {
		if ctx.Err() != nil || ref.Kind != core.ReferenceArtist || ref.Grounding == nil {
			return
		}
		artist, ok := automaticArtist(*ref)
		if !ok || librarypack.CanonicalMBID(artist.ID) == "" {
			// Pack-local identities have their own resolver namespace. Typed
			// recording credits and prepared graph relationships contain UUIDs,
			// so scanning them cannot authenticate a synthetic local artist ID.
			return
		}
		if resolved := ref.Resolution; resolved != nil && resolved.Status == core.ResolutionResolved && resolved.CatalogVersion == resolver.CatalogVersion() &&
			resolved.Selected != nil && resolved.Selected.Kind == core.ReferenceArtist && resolved.Selected.EntityID == artist.ID && len(resolved.Selected.Representatives) > 0 && resolved.Selected.Representatives[0].TrackID != "" {
			// Reuse the same identity/snapshot contract as resolution.ApplyContext.
			// A new catalog or a different artist decision must be checked again.
			ref.TrackID = resolved.Selected.Representatives[0].TrackID
			return
		}
		var representatives []core.TrackRef
		seen := map[string]bool{}
		seenRecordings := map[string]bool{}
		metadataCache := map[string]core.EnrichedTrack{}
		metadataRead := map[string]bool{}
		add := func(track core.TrackRef, requireCredit ...bool) {
			if track.ID == "" || seen[track.ID] || len(representatives) >= 5 || ctx.Err() != nil {
				return
			}
			needCredit := len(requireCredit) > 0 && requireCredit[0]
			if source, ok := cat.(ports.LibraryMetadataCatalog); ok {
				value, found := metadataCache[track.ID]
				if !metadataRead[track.ID] {
					var err error
					value, found, err = source.LibraryRecordingMetadata(ctx, track.ID)
					if err != nil {
						return
					}
					metadataRead[track.ID] = true
					if found {
						metadataCache[track.ID] = value
					}
				}
				if found {
					if value.Ref.ID != track.ID || !value.Matched || value.IdentityStatus != core.ResolutionResolved {
						return
					}
					if len(value.ArtistIDs) > 0 && !slices.Contains(value.ArtistIDs, artist.ID) {
						return
					}
					trackMBID := librarypack.CanonicalMBID(strings.TrimPrefix(track.RecordingIdentity, "musicbrainz:"))
					if trackMBID != "" && value.RecordingID != "" && trackMBID != librarypack.CanonicalMBID(value.RecordingID) {
						return
					}
				}
				if needCredit && (!found || !slices.Contains(value.ArtistIDs, artist.ID)) {
					return
				}
			} else if needCredit {
				return
			}
			recordingKey := track.RecordingIdentity
			if recordingKey == "" {
				recordingKey = track.ID
			}
			if seenRecordings[recordingKey] {
				return
			}
			seenRecordings[recordingKey] = true
			seen[track.ID] = true
			representatives = append(representatives, track)
		}

		if indexed, ok := cat.(ports.ArtistIdentityCatalog); ok {
			tracks, lookupErr := indexed.ArtistRecordingsByMBID(ctx, artist.ID, 512)
			if lookupErr == nil {
				for _, track := range selectAutomaticSeeds(ctx, cat, tracks, graph) {
					add(track)
					if len(representatives) == 5 {
						break
					}
				}
			}
		}
		if indexed, ok := cat.(ports.RecordingIdentityCatalog); ok && graph != nil {
			for _, recording := range graph.TopRecordings(ctx, artist.ID, 20) {
				tracks, lookupErr := indexed.RecordingsByMBID(ctx, recording.MBID, 1)
				if lookupErr != nil {
					break
				}
				for _, track := range tracks {
					add(track)
				}
				if len(representatives) == 5 {
					break
				}
			}
		}
		if len(representatives) < 5 {
			if indexed, ok := cat.(ports.ArtistRecordingCatalog); ok {
				tracks, lookupErr := indexed.ArtistRecordings(ctx, artist.Name)
				if lookupErr == nil {
					// Direct local rows carry the same required artist-credit
					// proof without rejoining every base alias to its discography.
					// Keep the catalog's order within each source and its slice
					// immutable; this preference never substitutes for the ID check.
					tracks = slices.Clone(tracks)
					slices.SortStableFunc(tracks, func(a, b core.TrackRef) int {
						direct := func(ref core.TrackRef) bool {
							return strings.HasPrefix(ref.ID, "pack:") || strings.HasPrefix(ref.ID, "local:")
						}
						if direct(a) == direct(b) {
							return 0
						}
						if direct(a) {
							return -1
						}
						return 1
					})
					for _, track := range tracks[:min(len(tracks), 512)] {
						if len(representatives) == 5 {
							break
						}
						add(track, true)
					}
				}
			}
		}
		if len(representatives) < 5 && ctx.Err() == nil && c.ListenBrainzStatus().Connected {
			if indexed, ok := cat.(ports.RecordingIdentityCatalog); ok {
				if client, err := c.ListenBrainzClient(); err == nil {
					records, _, fetchErr := client.FetchTopRecordings(ctx, artist.ID)
					if fetchErr == nil {
						for _, record := range records {
							tracks, lookupErr := indexed.RecordingsByMBID(ctx, record.MBID, 5)
							if lookupErr != nil {
								break
							}
							for _, track := range tracks {
								add(track)
							}
							if len(representatives) == 5 {
								break
							}
						}
					}
					// Metadata lookup proposes only a locator. The existing exact
					// recording resolver and positive local artist credits must
					// independently authenticate every resulting seed.
					if len(representatives) < 5 {
						for _, unknown := range cat.Resolve(artist.Name, 5) {
							if ctx.Err() != nil || len(representatives) == 5 {
								break
							}
							meta, ok := ports.CatalogMeta(ctx, cat, unknown.ID)
							if !ok || recordingMBID(meta) != "" {
								continue
							}
							proposal, err := client.ProposeRecordingMapping(ctx, meta.Ref.Artist, meta.Ref.Title, meta.Album)
							if err != nil || proposal.RecordingMBID == "" {
								continue
							}
							resolved := ports.ResolveReferenceContext(ctx, resolver, core.IntentReference{Kind: core.ReferenceTrack, TrackID: "musicbrainz:" + proposal.RecordingMBID})
							if resolved.Status != core.ResolutionResolved || resolved.Selected == nil {
								continue
							}
							for _, representative := range resolved.Selected.Representatives {
								identified, ok := ports.CatalogMeta(ctx, cat, representative.TrackID)
								if ok && recordingMBID(identified) == proposal.RecordingMBID {
									add(identified.Ref, true)
								}
							}
						}
					}
				}
			}
		}
		if len(representatives) == 0 {
			return
		}
		candidate := core.ResolutionCandidate{Kind: core.ReferenceArtist, EntityID: artist.ID, Artist: artist.Name, Confidence: 1,
			Evidence: []core.ResolutionEvidence{{Match: "recording_artist_identity", MatchedText: artist.ID}}}
		for _, track := range representatives {
			candidate.Representatives = append(candidate.Representatives, core.WeightedTrack{TrackID: track.ID, Weight: 1 / float64(len(representatives))})
		}
		ref.Resolution = &core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: resolver.CatalogVersion(), Selected: &candidate}
		ref.TrackID = representatives[0].ID
	}
	for i := range intent.References {
		prepare(&intent.References[i])
	}
	for i := range intent.InferredAnchors {
		prepare(&intent.InferredAnchors[i].Reference)
	}
	for i := range intent.Journey.Waypoints {
		prepare(&intent.Journey.Waypoints[i])
	}
	for i := range intent.RequiredTracks {
		prepare(&intent.RequiredTracks[i])
	}
	if intent.Start != nil {
		prepare(intent.Start)
	}
	if intent.Destination != nil {
		prepare(intent.Destination)
	}
	return intent.Normalized(), ctx.Err()
}

func automaticArtist(ref core.IntentReference) (core.IdentityCandidate, bool) {
	if artist, ok := ref.Grounding.DecidedArtist(); ok {
		return artist, true
	}
	if g := ref.Grounding; g != nil && !g.Truncated && len(g.Candidates) == 1 && g.Candidates[0].Kind == core.ReferenceArtist {
		return g.Candidates[0], true
	}
	return core.IdentityCandidate{}, false
}

// Rank only authenticated identities. Audio availability and popularity select
// useful representatives, while a second pass preserves release diversity.
func selectAutomaticSeeds(ctx context.Context, cat ports.Catalog, tracks []core.TrackRef, graph *musicgraph.Reader) []core.TrackRef {
	type seed struct {
		track      core.TrackRef
		audio      bool
		popularity float64
		album      string
	}
	choices := make([]seed, 0, len(tracks))
	for _, track := range tracks {
		if ctx.Err() != nil {
			return nil
		}
		meta, ok := ports.CatalogMeta(ctx, cat, track.ID)
		if !ok {
			continue
		}
		choice := seed{track: track}
		if meta.AlbumReliable {
			choice.album = core.NormalizeIdentityPart(meta.Album)
		}
		if local, ok := cat.(ports.LibrarySemanticCatalog); ok {
			vector, found, err := local.LibraryCLAPVector(ctx, track.ID)
			choice.audio = err == nil && found && len(vector.Values) > 0 && vector.Source.SpaceID != ""
		}
		if !choice.audio {
			if local, ok := cat.(ports.LibraryAudioCatalog); ok {
				vector, found, err := local.LibraryVector(ctx, track.ID)
				choice.audio = err == nil && found && len(vector.Values) > 0 && vector.Source.SpaceID != ""
			}
		}
		if !choice.audio {
			vector, found := cat.Vectors(track.ID)
			choice.audio = found && len(vector.Audio) > 0
		}
		if graph != nil {
			choice.popularity, _ = graph.PopularityRank(ctx, recordingMBID(meta))
		}
		choices = append(choices, choice)
	}
	slices.SortStableFunc(choices, func(a, b seed) int {
		if a.audio != b.audio {
			if a.audio {
				return -1
			}
			return 1
		}
		if a.popularity > b.popularity {
			return -1
		}
		if a.popularity < b.popularity {
			return 1
		}
		return 0
	})
	var selected []core.TrackRef
	seen, albums := map[string]bool{}, map[string]bool{}
	for pass := 0; pass < 2; pass++ {
		for _, choice := range choices {
			identity := choice.track.ID
			if seen[identity] || pass == 0 && choice.album != "" && albums[choice.album] {
				continue
			}
			seen[identity] = true
			albums[choice.album] = true
			selected = append(selected, choice.track)
		}
	}
	return selected
}
