package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/intent/schema"
	"github.com/platten/playlistai/internal/preview/deezer"
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
			engine := multichannel.New(cat, fakes.NewSimilarityEngine(cat), cat, multichannel.DefaultConfig())
			if core.WantsInstrumental(intent) {
				service := fixtureVocalService(t, cat)
				engine = engine.WithAudioProvider(func() *audio.Service { return service })
			}
			playlist, err := engine.Build(context.Background(), intent)
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

// Synthetic class vectors exercise prompt control flow, not musical accuracy.
type fixtureVocalEncoder struct{}

func (fixtureVocalEncoder) Identity() core.AudioModelIdentity {
	return core.AudioModelIdentity{Model: "fixture", Revision: "1", Runtime: "fixture", Preprocessing: audio.PreprocessingVersion, Dimension: 2}
}
func (fixtureVocalEncoder) EmbedAudio(context.Context, []float32) ([]float32, error) {
	return nil, fmt.Errorf("cached fixtures must not download audio")
}
func (fixtureVocalEncoder) EmbedText(_ context.Context, text string) ([]float32, error) {
	if strings.Contains(text, "instrumental") || strings.Contains(text, "only on instruments") {
		return []float32{1, 0}, nil
	}
	return []float32{0, 1}, nil
}
func fixtureVocalService(t *testing.T, cat *fakes.Catalog) *audio.Service {
	t.Helper()
	store, err := audio.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	encoder := fixtureVocalEncoder{}
	for row := 0; row < cat.Len(); row++ {
		meta, _ := cat.Meta(cat.ID(row))
		record := core.AudioAnalysis{CatalogVersion: cat.CatalogVersion(), TrackID: meta.Ref.ID, TrackKey: core.ProvisionalRecordingKey(meta.Ref), Model: encoder.Identity(), Identity: core.PreviewIdentity{Provider: "deezer", ProviderID: meta.Ref.ID, Status: core.ResolutionResolved}, AudioSHA256: strings.Repeat("0", 64), Segments: []core.AudioSegment{{EndSeconds: 10, Embedding: []float32{1, 0}}}}
		record.ID = audio.Fingerprint(record)
		if err := store.Put(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	return &audio.Service{Analyzer: encoder, Store: store, Resolver: deezer.New(deezer.Config{}), Authorized: true, ParityValidated: true}
}

// The live report is the model test. These checks keep the runner from reporting
// success when a typed reference or journey direction was silently dropped.
func TestCreativePromptContractChecksRejectLostMeaning(t *testing.T) {
	raw, err := os.ReadFile("../../internal/evaluation/testdata/creative-prompts-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []promptCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 15 {
		t.Fatalf("got %d prompts", len(cases))
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if seen[c.Prompt] || strings.TrimSpace(c.Prompt) == "" {
			t.Fatal("empty or duplicate prompt")
		}
		seen[c.Prompt] = true
		if c.Artist != "" || len(c.Artists) > 0 || c.Genre != "" || c.Album != "" || c.Track != "" || c.Vocal != "" || len(c.JourneyGenres) > 0 {
			if len(checkIntent(c, core.MusicIntent{})) == 0 {
				t.Fatalf("empty interpretation passed: %s", c.Prompt)
			}
		}
	}
	c := promptCase{Album: "Fixture Album", Track: "Fixture Track", Artists: []string{"Fixture Artist", "Second Artist"}, JourneyGenres: []string{"category alpha", "category omega"}}
	intent := core.MusicIntent{Mode: core.ModeJourney, References: []core.IntentReference{
		{Kind: core.ReferenceAlbum, Query: "Fixture Artist - Fixture Album"},
		{Kind: core.ReferenceTrack, Query: "Fixture Artist - Fixture Track"},
		{Kind: core.ReferenceArtist, Query: "Fixture Artist"},
		{Kind: core.ReferenceArtist, Query: "Second Artist"},
	}, EssentialCriteria: []core.MusicalCriterion{{Kind: "genre", Value: "category alpha", Scope: "journey_start"}, {Kind: "genre", Value: "category omega", Scope: "journey_end"}}}
	if issues := checkIntent(c, intent); len(issues) > 0 {
		t.Fatal(issues)
	}
	intent.EssentialCriteria[0].Scope, intent.EssentialCriteria[1].Scope = "journey_end", "journey_start"
	if len(checkIntent(c, intent)) == 0 {
		t.Fatal("reversed journey passed")
	}
}
