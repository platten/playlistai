package schema

import (
	"github.com/platten/playlistai/internal/intent/rules"
)

func preserveArtistOnly(w *Wire, prompt string) {
	artist := rules.OnlyArtist(prompt)
	if artist == "" {
		return
	}
	// The recognized phrase explicitly names this entity, independently of
	// whether the local model emitted the artist reference correctly.
	refs := []WireReference{{Kind: "artist", Value: artist, Influence: "positive", Explicit: true, Span: artist}}
	for _, ref := range w.References {
		if ref.Influence == "negative" {
			refs = append(refs, ref)
		}
	}
	w.References = refs
	for _, c := range w.HardConstraints {
		if c.Kind == "require_artist" && c.Value == artist {
			return
		}
	}
	w.HardConstraints = append(w.HardConstraints, WireConstraint{Kind: "require_artist", Value: artist, Span: artist})
}
