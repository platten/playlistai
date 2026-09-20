package multichannel

import (
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// These keys are only a soft diversity aid, never recording identities or
// musical evidence. Alias links require a resolved catalog artist; joint
// credits require an independently present complete artist name. In particular
// we do not split every "and" or "&" in a band's name into invented artists.
type performerKeys struct {
	aliases map[string]string
	known   map[string]bool
}

func newPerformerKeys(intent core.MusicIntent, tracks []core.TrackRef) performerKeys {
	p := performerKeys{aliases: map[string]string{}, known: map[string]bool{}}
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

func namedReferenceArtists(cat ports.Catalog, intent core.MusicIntent) []core.TrackRef {
	var tracks []core.TrackRef
	for _, reference := range explicitArtistReferences(intent) {
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
			if meta, ok := cat.Meta(reference.TrackID); ok {
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
	artist := keys.keys(track.Artist)
	if len(artist) == 0 {
		return false // missing identity is not proof of a different artist
	}
	for _, reference := range named {
		if performersOverlap(artist, keys.keys(reference.Artist)) {
			return false
		}
	}
	return true
}

func includesOtherArtists(cat ports.Catalog, intent core.MusicIntent, tracks []core.TrackRef) bool {
	if !core.RequiresOtherArtists(intent) {
		return true
	}
	named := namedReferenceArtists(cat, intent)
	keys := newPerformerKeys(intent, append(append([]core.TrackRef(nil), tracks...), named...))
	for _, track := range tracks {
		if outsideNamedArtists(keys, track, named) {
			return true
		}
	}
	return false
}
