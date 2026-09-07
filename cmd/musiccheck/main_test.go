package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/intent/schema"
	"github.com/platten/playlistai/internal/reco/multichannel"
	"github.com/platten/playlistai/internal/resolution"
)

// These tests exercise reviewed interpretation contracts through real parsing,
// resolution and generation with synthetic recordings. The musiccheck command
// separately measures the actual model; this is not a musical-quality test.
func TestRequestedPromptContractsGenerate(t *testing.T) {
	raw, err := os.ReadFile("../../internal/evaluation/testdata/music-prompts-v8.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []promptCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 12 {
		t.Fatalf("missing requested prompt cases: %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Prompt, func(t *testing.T) {
			artist := c.Artist
			if artist == "" {
				artist = "Synthetic Performer"
			}
			end := c.Destination
			if end == "" {
				end = "Synthetic Destination"
			}
			cat := fakes.NewCatalog(2,
				fakes.CatalogTrack{ID: "one", Display: artist + " - One", Audio: []float32{1, 0}, Track: []float32{1, 0}},
				fakes.CatalogTrack{ID: "two", Display: "Synthetic Neighbor - Two", Audio: []float32{.9, .1}, Track: []float32{.9, .1}},
				fakes.CatalogTrack{ID: "three", Display: "Synthetic Third - Three", Audio: []float32{.8, .2}, Track: []float32{.8, .2}},
				fakes.CatalogTrack{ID: "end", Display: end + " - End", Audio: []float32{.7, .3}, Track: []float32{.7, .3}},
				fakes.CatalogTrack{ID: "blocked", Display: "skrillex - Excluded fixture", Audio: []float32{1, 0}, Track: []float32{1, 0}})
			w := schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 3, AudioWeight: .5, CooccurrenceWeight: .5}
			pref := func(value string) schema.WirePreference {
				return schema.WirePreference{Value: value, Influence: "positive", Explicit: true, Span: c.Prompt}
			}
			if c.Genre != "" {
				w.Genres = append(w.Genres, pref(c.Genre))
			}
			if c.Mood != "" {
				w.Moods = append(w.Moods, pref(c.Mood))
			}
			if c.Texture != "" {
				w.Textures = append(w.Textures, pref(c.Texture))
			}
			if c.Vocal != "" {
				w.VocalPreference = pref(c.Vocal)
			}
			if c.Artist != "" {
				w.References = []schema.WireReference{{Kind: "artist", Value: artist, Explicit: true, Span: c.Prompt, Influence: "positive"}}
			} else {
				w.InferredAnchors = []schema.WireAnchor{{Kind: "track", Value: artist + " - One", Role: "synthetic retrieval", Reason: "test fixture, not musical evidence"}}
			}
			for _, excluded := range c.ExcludedArtists {
				w.HardConstraints = append(w.HardConstraints, schema.WireConstraint{Kind: "exclude_artist", Value: excluded, Span: c.Prompt})
			}
			if c.StartYear > 0 {
				scope := "playlist"
				if c.Destination != "" {
					scope = "journey_start"
				}
				w.Temporal = []core.TemporalRequirement{{Basis: c.Basis, StartYear: c.StartYear, EndYear: c.EndYear, Scope: scope}}
			}
			if c.Destination != "" {
				w.Mode = "journey"
				w.Destination = []schema.WireReference{{Kind: "artist", Value: c.Destination, Span: c.Prompt, Explicit: true, Influence: "positive"}}
			}
			completion, _ := json.Marshal(w)
			intent, err := schema.ParseForPrompt(completion, c.Prompt)
			if err != nil {
				t.Fatal(err)
			}
			if issues := checkIntent(c, intent); len(issues) > 0 {
				t.Fatal(issues)
			}
			intent, _ = resolution.Apply(cat, intent)
			playlist, err := multichannel.New(cat, fakes.NewSimilarityEngine(cat), cat, multichannel.DefaultConfig()).Build(context.Background(), intent)
			if err != nil || len(playlist.Tracks) == 0 {
				t.Fatalf("no playlist: %+v %v", playlist.Outcome, err)
			}
			if c.Destination != "" && playlist.Tracks[len(playlist.Tracks)-1].Artist != c.Destination {
				t.Fatal("destination lost")
			}
			for _, track := range playlist.Tracks {
				for _, excluded := range c.ExcludedArtists {
					if track.Artist == excluded {
						t.Fatal("exclusion violated")
					}
				}
			}
		})
	}
}
