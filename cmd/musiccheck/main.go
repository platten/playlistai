// Command musiccheck runs reviewed prompt expectations against a real local
// language model and catalog. Outputs are observations, not listening scores.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/enrich/musicbrainz"
	"github.com/platten/playlistai/internal/intent/llama"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/reco/multichannel"
	"github.com/platten/playlistai/internal/resolution"
	"github.com/platten/playlistai/internal/similarity/brute"
)

type promptCase struct {
	Prompt          string   `json:"prompt"`
	Genre           string   `json:"genre"`
	Artist          string   `json:"artist"`
	ExcludedArtists []string `json:"excludedArtists"`
	StartYear       int      `json:"startYear"`
	EndYear         int      `json:"endYear"`
	Basis           string   `json:"basis"`
	Destination     string   `json:"destination"`
	Vocal           string   `json:"vocal"`
	Mood            string   `json:"mood"`
	Texture         string   `json:"texture"`
}
type result struct {
	Prompt       string           `json:"prompt"`
	Errors       []string         `json:"errors"`
	Milliseconds int64            `json:"milliseconds"`
	Playlist     core.Playlist    `json:"playlist"`
	Intent       core.MusicIntent `json:"intent"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	model := flag.String("model", "", "GGUF path")
	runtime := flag.String("runtime", "", "llama-server path")
	catalogDir := flag.String("catalog", "", "catalog directory")
	fixture := flag.String("prompts", "internal/evaluation/testdata/music-prompts-v8.json", "prompt expectations JSON")
	output := flag.String("output", "/tmp/music-prompts-report.json", "report JSON")
	cache := flag.String("cache", "/tmp/music-prompts-metadata.sqlite", "metadata cache")
	online := flag.Bool("online", false, "allow extracted music-term metadata lookups")
	single := flag.String("case", "", "optional exact prompt")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	parser, err := llama.New(ctx, llama.Options{BinaryPath: *runtime, ModelPath: *model, NCtx: 8192, GPULayers: 0, StartTimeout: 3 * time.Minute})
	if err != nil {
		return err
	}
	defer parser.Close()
	cat, err := catalog.Open(*catalogDir)
	if err != nil {
		return err
	}
	defer cat.Close()
	engine := multichannel.New(cat, brute.New(cat), cat, multichannel.DefaultConfig()).WithAnchorProposer(parser.ProposeAnchors)
	var mb *musicbrainz.Client
	if *online {
		mb, err = musicbrainz.New(musicbrainz.Config{UserAgent: "PlaylistAI/0.6 (https://github.com/platten/playlistai)", CachePath: *cache})
		if err != nil {
			return err
		}
		defer mb.Close()
	}
	raw, err := os.ReadFile(*fixture)
	if err != nil {
		return err
	}
	var cases []promptCase
	if err = json.Unmarshal(raw, &cases); err != nil {
		return err
	}
	results := []result{}
	failed := false
	for _, c := range cases {
		if *single != "" && c.Prompt != *single {
			continue
		}
		started := time.Now()
		r := result{Prompt: c.Prompt}
		intent, parseErr := parser.Parse(ctx, ports.IntentInput{Prompt: c.Prompt})
		if parseErr != nil {
			r.Errors = append(r.Errors, parseErr.Error())
		} else {
			intent.VerificationPolicy = core.BestAvailable
			r.Errors = append(r.Errors, checkIntent(c, intent)...)
			if mb != nil {
				intent, err = mb.ResolveMusic(ctx, intent, cat, cat, nil)
				if err != nil {
					r.Errors = append(r.Errors, err.Error())
				}
			}
			intent, _ = resolution.Apply(cat, intent)
			r.Intent = intent
			r.Playlist, err = engine.Build(ctx, intent)
			if err != nil {
				r.Errors = append(r.Errors, err.Error())
			}
			if len(r.Playlist.Tracks) == 0 {
				r.Errors = append(r.Errors, "empty playlist")
			}
			for _, track := range r.Playlist.Tracks {
				for _, excluded := range c.ExcludedArtists {
					if strings.Contains(strings.ToLower(track.Display()), strings.ToLower(excluded)) {
						r.Errors = append(r.Errors, "excluded artist in output")
					}
				}
			}
			if c.Destination != "" && len(r.Playlist.Tracks) > 0 && !strings.EqualFold(r.Playlist.Tracks[len(r.Playlist.Tracks)-1].Artist, c.Destination) {
				r.Errors = append(r.Errors, "wrong final artist")
			}
		}
		r.Milliseconds = time.Since(started).Milliseconds()
		results = append(results, r)
		report, _ := json.MarshalIndent(results, "", "  ")
		if err = os.WriteFile(*output, append(report, '\n'), 0600); err != nil {
			return err
		}
		fmt.Printf("%s: tracks=%d issues=%v elapsed=%dms\n", c.Prompt, len(r.Playlist.Tracks), r.Errors, r.Milliseconds)
		failed = failed || len(r.Errors) > 0
	}
	if failed {
		return fmt.Errorf("one or more live prompt checks failed; see %s", *output)
	}
	return nil
}
func checkIntent(c promptCase, m core.MusicIntent) []string {
	var issues []string
	contains := func(v, w string) bool { return strings.Contains(strings.ToLower(v), strings.ToLower(w)) }
	if c.Genre != "" {
		var words []string
		for _, g := range m.Preferences.Genres {
			words = append(words, strings.ToLower(g.Value))
		}
		found := true
		for _, word := range strings.Fields(strings.ToLower(c.Genre)) {
			found = found && strings.Contains(strings.Join(words, " "), word)
		}
		if !found {
			issues = append(issues, "genre not preserved")
		}
	}
	if c.Artist != "" {
		found := false
		for _, r := range m.References {
			found = found || r.Kind == core.ReferenceArtist && strings.EqualFold(r.Query, c.Artist)
		}
		if !found {
			issues = append(issues, "artist not preserved")
		}
	}
	for _, artist := range c.ExcludedArtists {
		found := false
		for _, a := range m.Constraints.ArtistsExclude {
			found = found || strings.EqualFold(a, artist)
		}
		if !found {
			issues = append(issues, "exclusion missing: "+artist)
		}
	}
	if c.StartYear > 0 {
		found := false
		for _, p := range m.Temporal {
			found = found || p.StartYear == c.StartYear && p.EndYear == c.EndYear && p.Basis == c.Basis
		}
		if !found {
			issues = append(issues, "period not preserved")
		}
	}
	if c.Destination != "" && (m.Destination == nil || !strings.EqualFold(m.Destination.Query, c.Destination)) {
		issues = append(issues, "destination not preserved")
	}
	if c.Vocal != "" && (m.Preferences.VocalPreference == nil || !contains(m.Preferences.VocalPreference.Value, c.Vocal)) {
		issues = append(issues, "vocal preference missing")
	}
	for _, item := range []struct {
		want   string
		actual []core.IntentPreference
	}{{c.Mood, m.Preferences.Moods}, {c.Texture, m.Preferences.TextureDescriptions}} {
		if item.want != "" {
			found := false
			for _, p := range item.actual {
				found = found || contains(p.Value, item.want)
				for _, evidence := range p.Evidence {
					found = found || contains(evidence.Text, item.want)
				}
			}
			if !found {
				issues = append(issues, "preference missing: "+item.want)
			}
		}
	}
	return issues
}
