package multichannel

import (
	"context"
	"encoding/hex"
	"slices"
	"sort"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// Credit keys support diversity and spacing, never recording identity or
// musical suitability. Typed roles supplement generic billing; additional
// personnel never erase a billed band. Literal band punctuation survives.
type performerKeys struct {
	aliases map[string]string
	known   map[string]bool
	tracks  map[string][]string
}

func newPerformerKeysContext(ctx context.Context, intent core.MusicIntent, tracks []core.TrackRef, catalogs ...ports.Catalog) performerKeys {
	p := performerKeys{aliases: map[string]string{}, known: map[string]bool{}, tracks: map[string][]string{}}
	for _, track := range tracks {
		if name := core.NormalizeIdentityPart(track.Artist); name != "" {
			p.known[name] = true
		}
	}
	for _, reference := range explicitArtistReferences(intent) {
		if reference.Kind != core.ReferenceArtist || reference.Resolution == nil || reference.Resolution.Status != core.ResolutionResolved || reference.Resolution.Selected == nil {
			continue
		}
		selected := reference.Resolution.Selected
		canonical := core.NormalizeIdentityPart(selected.Artist)
		if canonical == "" {
			continue
		}
		p.known[canonical] = true
		for _, evidence := range selected.Evidence {
			if evidence.Match == "alias" {
				p.aliases[core.NormalizeIdentityPart(reference.Query)] = canonical
				p.aliases[core.NormalizeIdentityPart(evidence.MatchedText)] = canonical
			}
		}
	}
	// Additional performer credits are often incomplete. Retain the billed
	// artist/group and add known performers, excluding explicit engineer-only
	// identities without guessing roles for untyped credits.
	for _, track := range tracks {
		credits := []artistCredit{{name: track.Artist}}
		if ctx.Err() != nil {
			return p
		}
		performers, engineers := map[string]bool{}, map[string]bool{}
		performerIDs, engineerIDs := map[string]bool{}, map[string]bool{}
		if len(catalogs) > 0 && catalogs[0] != nil {
			if meta, ok := ports.CatalogMeta(ctx, catalogs[0], track.ID); ok {
				credits = append(credits, catalogArtistIdentities(meta.Annotations)...)
				for _, annotation := range meta.Annotations {
					name := core.NormalizeIdentityPart(annotation.Value)
					switch annotation.Kind {
					case "performer":
						performers[name] = true
						credits = append(credits, artistCredit{name: annotation.Value})
					case "recording_engineer":
						engineers[name] = true
					}
				}
			}
		}
		var recording core.EnrichedTrack
		if intent.Knowledge != nil {
			for _, row := range intent.Knowledge.Tracks {
				if row.Ref.ID == track.ID {
					recording = row
					// ArtistIDs is an unordered membership set. Only explicit
					// credit pairs below associate a person's name with an ID.
					for _, name := range row.AllArtists {
						credits = append(credits, artistCredit{name: name})
					}
					break
				}
			}
		}
		for _, claim := range recording.Claims {
			if claim.Method != "recording_credit" || claim.State != core.EvidenceMatch || !attributedRecordingClaim(claim, recording) {
				continue
			}
			name, id := core.NormalizeIdentityPart(claim.Value), core.NormalizeIdentityPart(claim.ArtistID)
			if !validArtistMBID(id) {
				id = ""
			}
			switch claim.Kind {
			case "performer":
				performers[name], performerIDs[id] = true, id != ""
				credits = append(credits, artistCredit{name: claim.Value, id: id})
			case "engineer":
				engineers[name], engineerIDs[id] = true, id != ""
			}
		}
		// Connect a typed local name to its aligned artist ID before filtering
		// aliases of that same person in other credits.
		for _, credit := range credits {
			if credit.id != "" {
				name := core.NormalizeIdentityPart(credit.name)
				performerIDs[credit.id] = performerIDs[credit.id] || performers[name]
				engineerIDs[credit.id] = engineerIDs[credit.id] || engineers[name]
			}
		}
		var keys []string
		for _, credit := range credits {
			name := core.NormalizeIdentityPart(credit.name)
			if (engineers[name] || engineerIDs[credit.id]) && !performers[name] && !performerIDs[credit.id] {
				continue
			}
			keys = append(keys, p.keys(credit.name)...)
			if credit.id != "" {
				keys = append(keys, "artist-id:"+credit.id)
			}
		}
		sort.Strings(keys)
		p.tracks[track.ID] = slices.Compact(keys)
	}
	return p
}

func (p performerKeys) keys(artist string) []string {
	name := core.NormalizeIdentityPart(artist)
	if name == "" {
		return nil
	}
	canonical := func(value string) string {
		if alias := p.aliases[value]; alias != "" {
			return alias
		}
		return value
	}
	keys := []string{canonical(name)}
	for known := range p.known {
		for _, separator := range []string{" & ", " feat. ", " feat ", " featuring "} {
			if strings.HasPrefix(name, known+separator) || strings.HasSuffix(name, separator+known) {
				keys = append(keys, canonical(known))
				break
			}
		}
	}
	return keys
}

type artistCredit struct{ name, id string }

func validArtistMBID(id string) bool {
	id = strings.TrimSpace(id)
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	decoded, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	return err == nil && len(decoded) == 16
}

// Aligned MusicBrainz IDs establish an encoded artist list. Without them a
// semicolon in an identity tag remains literal (for example "AC/DC; literal").
// Album artist IDs intentionally do not identify recording performers.
func catalogArtistIdentities(annotations []core.MetadataAnnotation) []artistCredit {
	var names, ids []string
	for _, annotation := range annotations {
		switch annotation.Kind {
		case "artist_credit":
			names = append(names, annotation.Value)
		case "artist_mbid":
			ids = append(ids, strings.Split(annotation.Value, ";")...)
		}
	}
	valid := len(ids) > 0
	for i := range ids {
		ids[i] = core.NormalizeIdentityPart(ids[i])
		valid = valid && validArtistMBID(ids[i])
	}
	var out []artistCredit
	for _, name := range names {
		out = append(out, artistCredit{name: name})
		parts := strings.Split(name, ";")
		if valid && len(parts) == len(ids) {
			for i, part := range parts {
				out = append(out, artistCredit{name: part, id: ids[i]})
			}
		}
	}
	return out
}

func (p performerKeys) trackKeys(track core.TrackRef) []string {
	if keys, ok := p.tracks[track.ID]; track.ID != "" && ok {
		return keys
	}
	return p.keys(track.Artist)
}

func (p performerKeys) sameArtist(left, right core.TrackRef) bool {
	return performersOverlap(p.trackKeys(left), p.trackKeys(right))
}

func (p performerKeys) artistInTail(items []sequenceItem, track core.TrackRef, gap int) bool {
	for _, item := range items[maxInt(0, len(items)-gap):] {
		if p.sameArtist(item.track, track) {
			return true
		}
	}
	return false
}

func performersOverlap(left, right []string) bool {
	for _, a := range left {
		for _, b := range right {
			if a == b {
				return true
			}
		}
	}
	return false
}

func explicitArtistReferences(intent core.MusicIntent) []core.IntentReference {
	refs := append([]core.IntentReference(nil), intent.References...)
	if intent.Start != nil {
		refs = append(refs, *intent.Start)
	}
	if intent.Destination != nil {
		refs = append(refs, *intent.Destination)
	}
	refs = append(refs, intent.Journey.Waypoints...)
	return refs
}

func namedReferenceArtistsContext(ctx context.Context, cat ports.Catalog, intent core.MusicIntent) []core.TrackRef {
	var tracks []core.TrackRef
	for _, reference := range explicitArtistReferences(intent) {
		if ctx.Err() != nil {
			break
		}
		if reference.Influence == core.InfluenceNegative {
			continue
		}
		if reference.Resolution != nil && reference.Resolution.Status == core.ResolutionResolved && reference.Resolution.Selected != nil {
			if artist := reference.Resolution.Selected.Artist; artist != "" {
				tracks = append(tracks, core.TrackRef{Artist: artist})
				continue
			}
		}
		if reference.TrackID != "" {
			if meta, ok := ports.CatalogMeta(ctx, cat, reference.TrackID); ok {
				tracks = append(tracks, meta.Ref)
				continue
			}
		}
		if reference.Kind == core.ReferenceArtist && strings.TrimSpace(reference.Query) != "" {
			tracks = append(tracks, core.TrackRef{Artist: reference.Query})
		}
	}
	return tracks
}

func outsideNamedArtists(keys performerKeys, track core.TrackRef, named []core.TrackRef) bool {
	if len(named) == 0 {
		return false
	}
	artist := keys.trackKeys(track)
	if len(artist) == 0 {
		return false // missing identity is not proof of a different artist
	}
	for _, reference := range named {
		if performersOverlap(artist, keys.trackKeys(reference)) {
			return false
		}
	}
	return true
}

func includesOtherArtistsContext(ctx context.Context, cat ports.Catalog, intent core.MusicIntent, tracks []core.TrackRef) bool {
	if !core.RequiresOtherArtists(intent) {
		return true
	}
	named := namedReferenceArtistsContext(ctx, cat, intent)
	keys := newPerformerKeysContext(ctx, intent, append(append([]core.TrackRef(nil), tracks...), named...), cat)
	for _, track := range tracks {
		if outsideNamedArtists(keys, track, named) {
			return true
		}
	}
	return false
}

func newPerformerKeys(intent core.MusicIntent, tracks []core.TrackRef, catalogs ...ports.Catalog) performerKeys {
	return newPerformerKeysContext(context.Background(), intent, tracks, catalogs...)
}
func includesOtherArtists(cat ports.Catalog, intent core.MusicIntent, tracks []core.TrackRef) bool {
	return includesOtherArtistsContext(context.Background(), cat, intent, tracks)
}
