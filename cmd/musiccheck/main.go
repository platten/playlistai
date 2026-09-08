// Command musiccheck runs reviewed prompt expectations against a real local
// language model and catalog. Outputs are observations, not listening scores.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/audioruntime"
	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/enrich/musicbrainz"
	"github.com/platten/playlistai/internal/intent/llama"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/preview/deezer"
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
	Artists         []string `json:"artists"`
	Album           string   `json:"album"`
	Track           string   `json:"track"`
	JourneyGenres   []string `json:"journeyGenres"`
}
type result struct {
	Prompt          string           `json:"prompt"`
	Errors          []string         `json:"errors"`
	Milliseconds    int64            `json:"milliseconds"`
	Replayed        bool             `json:"replayed,omitempty"`
	CachedAudioOnly bool             `json:"cachedAudioOnly,omitempty"`
	Playlist        core.Playlist    `json:"playlist"`
	Intent          core.MusicIntent `json:"intent"`
}

type cachedPreviewsOnly struct{}

func (cachedPreviewsOnly) ResolveAudioPreview(context.Context, core.TrackRef, core.EnrichedTrack) (core.ResolvedAudioPreview, error) {
	return core.ResolvedAudioPreview{}, core.ErrUnavailable
}

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--audio-worker" {
		if err := audioruntime.Run(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
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
	bundle := flag.String("bundle", "", "optional parity-validated CLAP bundle; permits Deezer preview analysis")
	analysisDir := flag.String("analysis-dir", "/tmp/musiccheck-analysis", "persistent derived-feature directory (no audio files)")
	count := flag.Int("count", 0, "override track count for every evaluated prompt")
	replay := flag.String("replay", "", "reuse LLM intents and metadata from an earlier musiccheck report")
	cacheOnly := flag.Bool("cached-audio-only", false, "check reusable CLAP features without retrieving new previews")
	flag.Parse()
	if *count < 0 || *cacheOnly && *bundle == "" {
		return fmt.Errorf("count must be nonnegative; cached-audio-only requires a bundle")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 90*time.Minute)
	defer cancel()
	var parser *llama.Parser
	var err error
	prior := map[string]core.MusicIntent{}
	if *replay != "" {
		raw, e := os.ReadFile(*replay)
		if e != nil {
			return e
		}
		var results []result
		if e := json.Unmarshal(raw, &results); e != nil {
			return e
		}
		for _, r := range results {
			if len(r.Playlist.Tracks) > 0 {
				prior[r.Prompt] = r.Playlist.Intent
			} else {
				prior[r.Prompt] = r.Intent
			}
		}
	} else {
		parser, err = llama.New(ctx, llama.Options{BinaryPath: *runtime, ModelPath: *model, NCtx: 8192, GPULayers: 0, StartTimeout: 3 * time.Minute})
		if err != nil {
			return err
		}
		defer parser.Close()
	}
	cat, err := catalog.Open(*catalogDir)
	if err != nil {
		return err
	}
	defer cat.Close()
	engine := multichannel.New(cat, brute.New(cat), cat, multichannel.DefaultConfig())
	if parser != nil {
		engine.WithAnchorProposer(parser.ProposeAnchors)
	}
	var mb *musicbrainz.Client
	if *online {
		mb, err = musicbrainz.New(musicbrainz.Config{UserAgent: "PlaylistAI/0.6 (https://github.com/platten/playlistai)", CachePath: *cache})
		if err != nil {
			return err
		}
		defer mb.Close()
	}
	if *bundle != "" {
		manifest, e := audio.ReadRuntimeBundle(*bundle)
		if e != nil {
			return e
		}
		worker := &audio.Worker{Executable: manifest.File(*bundle, "worker"), BundleDir: *bundle, Model: manifest.Model}
		defer func() { _ = worker.Close() }()
		if e := worker.Health(ctx); e != nil {
			return e
		}
		store, e := audio.OpenStore(*analysisDir)
		if e != nil {
			return e
		}
		defer func() { _ = store.Close() }()
		service := &audio.Service{Resolver: deezer.New(deezer.Config{}), Analyzer: worker, Store: store, Policy: manifest.Policy, Authorized: true, ParityValidated: manifest.Parity.Valid()}
		if *cacheOnly {
			service.Resolver = cachedPreviewsOnly{}
		}
		if mb != nil {
			service.Recordings = mb
		}
		engine.WithAudioProvider(func() *audio.Service { return service })
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
		fmt.Printf("Checking: %s\n", c.Prompt)
		r := result{Prompt: c.Prompt, Replayed: *replay != "", CachedAudioOnly: *cacheOnly}
		var intent core.MusicIntent
		var parseErr error
		if parser != nil {
			intent, parseErr = parser.Parse(ctx, ports.IntentInput{Prompt: c.Prompt})
		} else {
			var ok bool
			intent, ok = prior[c.Prompt]
			if !ok {
				parseErr = fmt.Errorf("prompt missing from replay report")
			}
		}
		fmt.Printf("  Parsed in %s\n", time.Since(started).Round(time.Millisecond))
		if parseErr != nil {
			r.Errors = append(r.Errors, parseErr.Error())
		} else {
			intent.VerificationPolicy = core.BestAvailable
			if *count > 0 {
				intent.Controls.TotalTrackCount = *count
			}
			intent.Seed = "42"
			r.Errors = append(r.Errors, checkIntent(c, intent)...)
			if mb != nil {
				intent, err = mb.ResolveMusic(ctx, intent, cat, cat, nil)
				if err != nil {
					r.Errors = append(r.Errors, err.Error())
				}
			}
			intent, _ = resolution.Apply(cat, intent)
			fmt.Printf("  Resolved in %s\n", time.Since(started).Round(time.Millisecond))
			r.Intent = intent
			r.Playlist, err = engine.Build(ctx, intent)
			if err != nil {
				r.Errors = append(r.Errors, err.Error())
			}
			if len(r.Playlist.Tracks) == 0 {
				r.Errors = append(r.Errors, "empty playlist")
			}
			if *bundle != "" && len(audio.Clauses(intent)) > 0 && len(r.Playlist.Tracks) > 0 {
				if r.Playlist.AudioEvidence == nil {
					r.Errors = append(r.Errors, "CLAP evidence missing")
				} else {
					for _, track := range r.Playlist.Tracks {
						checked := false
						for _, a := range r.Playlist.AudioEvidence.Assessments {
							checked = checked || a.TrackID == track.ID && a.Eligible && a.AnalysisID != ""
						}
						if !checked {
							r.Errors = append(r.Errors, "selected track lacks CLAP assessment: "+track.ID)
						}
					}
				}
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
	if len(results) == 0 {
		return fmt.Errorf("no prompt cases selected")
	}
	if failed {
		return fmt.Errorf("one or more live prompt checks failed; see %s", *output)
	}
	return nil
}
func checkIntent(c promptCase, m core.MusicIntent) []string {
	var issues []string
	contains := func(v, w string) bool { return strings.Contains(strings.ToLower(v), strings.ToLower(w)) }
	for _, artist := range c.Artists {
		issues = append(issues, checkIntent(promptCase{Artist: artist}, m)...)
	}
	for _, reference := range []struct {
		kind  core.ReferenceKind
		value string
	}{{core.ReferenceAlbum, c.Album}, {core.ReferenceTrack, c.Track}} {
		if reference.value == "" {
			continue
		}
		found := false
		for _, r := range m.References {
			found = found || r.Kind == reference.kind && contains(r.Query, reference.value)
		}
		if !found {
			issues = append(issues, "reference kind not preserved: "+reference.value)
		}
	}
	if len(c.JourneyGenres) > 0 {
		criteria := core.JourneyCriteria(m.EssentialCriteria)
		if m.Mode != core.ModeJourney || len(criteria) != len(c.JourneyGenres) {
			issues = append(issues, "journey stages not preserved")
		} else {
			for i, genre := range c.JourneyGenres {
				if !contains(criteria[i].Value, genre) {
					issues = append(issues, "wrong journey stage: "+genre)
				}
			}
		}
	}
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
