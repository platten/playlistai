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
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/preview/deezer"
	"github.com/platten/playlistai/internal/reco/deejai"
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
	NegativeMoods   []string `json:"negativeMoods"`
	Texture         string   `json:"texture"`
	Artists         []string `json:"artists"`
	Album           string   `json:"album"`
	Track           string   `json:"track"`
	JourneyGenres   []string `json:"journeyGenres"`
	Count           int      `json:"count"`
	MinimumArtists  int      `json:"minimumArtists"`
	OnlyArtist      string   `json:"onlyArtist"`
}
type result struct {
	EnhancedEvidenceSHA256      string                  `json:"enhancedEvidenceSha256,omitempty"`
	EnhancedSnapshotFingerprint string                  `json:"enhancedSnapshotFingerprint,omitempty"`
	RecommendationMode          core.RecommendationMode `json:"recommendationMode"`
	ParsedIntent                core.MusicIntent        `json:"parsedIntent"`
	ReplayedParsedIntent        bool                    `json:"replayedParsedIntent,omitempty"`
	Algorithm                   string                  `json:"algorithmVersion"`
	Catalog                     string                  `json:"catalogVersion"`
	Parser                      ports.ParserInfo        `json:"parser"`
	ParseOnly                   bool                    `json:"parseOnly,omitempty"`
	ParserFallback              string                  `json:"parserFallback,omitempty"`
	ParserIssues                []string                `json:"parserIssues,omitempty"`
	Prompt                      string                  `json:"prompt"`
	Errors                      []string                `json:"errors"`
	Milliseconds                int64                   `json:"milliseconds"`
	Replayed                    bool                    `json:"replayed,omitempty"`
	CachedAudioOnly             bool                    `json:"cachedAudioOnly,omitempty"`
	AcousticBrainzEnabled       bool                    `json:"acousticBrainzEnabled"`
	Playlist                    core.Playlist           `json:"playlist"`
	Intent                      core.MusicIntent        `json:"intent"`
}

type cachedPreviewsOnly struct{}

func metadataConfig(cachePath, datasetPath string, acousticBrainz bool) musicbrainz.Config {
	cfg := musicbrainz.Config{UserAgent: "PlaylistAI/0.8 (https://github.com/platten/playlistai)", CachePath: cachePath, DatasetPath: datasetPath}
	if acousticBrainz {
		cfg.AcousticBrainzURL = musicbrainz.AcousticBrainzURL
	}
	return cfg
}

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
	if err := run(); err != nil && err != flag.ErrHelp {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	flag := flag.NewFlagSet("musiccheck", flag.ContinueOnError)
	model := flag.String("model", "", "GGUF path")
	runtime := flag.String("runtime", "", "llama-server path")
	serverURL := flag.String("server-url", "", "reuse an already-running local llama server without managing its process")
	modeFlag := flag.String("mode", string(core.AcousticBrainzFirst), "recommendation mode: acousticbrainz_first, clap_first, deejai_only, or enhanced_hybrid")
	enhancedEvidence := flag.String("enhanced-evidence", "", "optional frozen EnhancedAudioInput JSON; Enhanced Hybrid only, no online metadata or new previews")
	parseOnly := flag.Bool("parse-only", false, "evaluate interpretation only; not a playlist acceptance run")
	rulesParser := flag.Bool("rules-parser", false, "explicitly use the deterministic rules parser; default remains the local language model")
	catalogDir := flag.String("catalog", "", "catalog directory")
	fixture := flag.String("prompts", "internal/evaluation/testdata/music-prompts-v8.json", "prompt expectations JSON")
	output := flag.String("output", "/tmp/music-prompts-report.json", "report JSON")
	cache := flag.String("cache", "/tmp/music-prompts-metadata.sqlite", "metadata cache")
	dataset := flag.String("metadata", "", "optional installed catalog-matched metadata SQLite dataset")
	minimum := flag.Int("min-tracks", 1, "minimum eligible output tracks required to pass each case")
	minArtists := flag.Int("min-artists", 0, "minimum distinct artists for non-artist-only requests")
	online := flag.Bool("online", false, "allow extracted music-term metadata lookups")
	acousticBrainz := flag.Bool("acousticbrainz", true, "include optional archived AcousticBrainz evidence with -online; disable for historical baselines")
	single := flag.String("case", "", "optional exact prompt")
	bundle := flag.String("bundle", "", "optional parity-validated CLAP bundle; permits Deezer preview analysis")
	analysisDir := flag.String("analysis-dir", "/tmp/musiccheck-analysis", "persistent derived-feature directory (no audio files)")
	count := flag.Int("count", 0, "override track count for every evaluated prompt")
	replay := flag.String("replay", "", "reuse LLM intents and metadata from an earlier musiccheck report")
	replayParsed := flag.Bool("replay-parsed", false, "with -replay, use raw parsed intents rather than discovered metadata for a paired mode comparison")
	cacheOnly := flag.Bool("cached-audio-only", false, "check reusable CLAP features without retrieving new previews")
	if err := flag.Parse(os.Args[1:]); err != nil {
		return err
	}
	mode := core.RecommendationMode(*modeFlag)
	if mode == "" || !mode.Valid() || *replayParsed && *replay == "" {
		return fmt.Errorf("invalid mode or replay-parsed requires replay")
	}
	if *rulesParser && (*replay != "" || *serverURL != "" || *model != "" || *runtime != "") {
		return fmt.Errorf("rules-parser cannot be combined with replay or language model options")
	}
	if *enhancedEvidence != "" && (mode != core.EnhancedHybrid || *online || (*bundle != "" && !*cacheOnly)) {
		return fmt.Errorf("enhanced-evidence requires enhanced_hybrid, offline metadata, and cached-audio-only when a CLAP bundle is supplied")
	}
	if *count < 0 || *minimum < 1 || *minArtists < 0 || (*count > 0 && *minimum > *count) || *cacheOnly && *bundle == "" {
		return fmt.Errorf("count must be nonnegative and at least min-tracks; min-tracks must be positive; cached-audio-only requires a bundle")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 90*time.Minute)
	defer cancel()
	var parser *llama.Parser
	var err error
	prior := map[string]core.MusicIntent{}
	priorReports := map[string]result{}
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
			priorReports[r.Prompt] = r
			if *replayParsed {
				if r.ParsedIntent.Version == 0 {
					return fmt.Errorf("replay report has no raw parsed intent for %q", r.Prompt)
				}
				prior[r.Prompt] = r.ParsedIntent
			} else if len(r.Playlist.Tracks) > 0 {
				prior[r.Prompt] = r.Playlist.Intent
			} else {
				prior[r.Prompt] = r.Intent
			}
		}
	}
	if *serverURL != "" {
		client := llama.NewClient(*serverURL)
		if !client.Healthy(ctx) {
			return fmt.Errorf("local llama server is not healthy")
		}
		parser = llama.NewWithClient(client)
		defer parser.Close()
	} else if *replay == "" && !*rulesParser {
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
	enhancedSnapshot, enhancedHash, err := readEnhancedEvidence(*enhancedEvidence, cat.CatalogVersion())
	if err != nil {
		return err
	}
	engine := multichannel.New(cat, brute.New(cat), cat, multichannel.DefaultConfig())
	if parser != nil {
		engine.WithAnchorProposer(parser.ProposeAnchors)
	}
	var mb *musicbrainz.Client
	if *dataset != "" && !*online && mode != core.DeejAIOnly {
		return fmt.Errorf("metadata discovery requires -online to permit provider fallback, as in the desktop")
	}
	if *online && mode != core.DeejAIOnly {
		mb, err = musicbrainz.New(metadataConfig(*cache, *dataset, *acousticBrainz))
		if err != nil {
			return err
		}
		defer mb.Close()
		engine.WithCandidateSource(mb)
	}
	if *bundle != "" && mode != core.DeejAIOnly {
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
		if err := ctx.Err(); err != nil {
			return err // Do not report unattempted cases as generation failures.
		}
		if *single != "" && c.Prompt != *single {
			continue
		}
		started := time.Now()
		fmt.Printf("Checking: %s\n", c.Prompt)
		r := result{Prompt: c.Prompt, Replayed: *replay != "", CachedAudioOnly: *cacheOnly, ParseOnly: *parseOnly, Algorithm: engine.AlgorithmVersion(), Catalog: cat.CatalogVersion()}
		r.RecommendationMode, r.ReplayedParsedIntent = mode, *replayParsed
		r.EnhancedEvidenceSHA256, r.EnhancedSnapshotFingerprint = enhancedHash, enhancedSnapshot.Fingerprint()
		if mode == core.DeejAIOnly {
			r.Algorithm = deejai.OnlyAlgorithmVersion
		}
		r.AcousticBrainzEnabled = mb != nil && *acousticBrainz && !*parseOnly
		if parser != nil {
			r.Parser = parser.Info()
		}
		if *rulesParser {
			r.Parser = rules.New().Info()
		}
		var intent core.MusicIntent
		var parseErr error
		if *replay == "" && (parser != nil || *rulesParser) {
			if *rulesParser {
				intent, parseErr = rules.New().Parse(ctx, ports.IntentInput{Prompt: c.Prompt})
			} else {
				intent, parseErr = parser.Parse(ctx, ports.IntentInput{Prompt: c.Prompt})
			}
			if parseErr != nil && ctx.Err() == nil && !*parseOnly {
				// Match the desktop fallback, while retaining the model failure
				// and still checking whether the fallback preserved the request.
				r.ParserFallback = parseErr.Error()
				fallback := rules.New()
				intent, parseErr = fallback.Parse(ctx, ports.IntentInput{Prompt: c.Prompt})
				r.Parser = fallback.Info()
			}
		} else {
			var ok bool
			intent, ok = prior[c.Prompt]
			if !ok {
				parseErr = fmt.Errorf("prompt missing from replay report")
			}
			r.Parser = priorReports[c.Prompt].Parser
			r.ParserFallback = priorReports[c.Prompt].ParserFallback
		}
		r.ParsedIntent = intent
		fmt.Printf("  Parsed in %s\n", time.Since(started).Round(time.Millisecond))
		if parseErr != nil {
			r.Errors = append(r.Errors, parseErr.Error())
		} else if *parseOnly {
			r.Intent = intent
			r.Errors = append(r.Errors, checkIntent(c, intent)...)
		} else {
			intent.VerificationPolicy = core.BestAvailable
			intent.Controls.RecommendationMode = mode
			r.ParserIssues = checkIntent(c, intent)
			expected := c
			if *count > 0 {
				r.Errors = append(r.Errors, checkIntent(promptCase{Count: c.Count}, intent)...)
				expected.Count = 0
				intent.Controls.TotalTrackCount = *count
			}
			intent.Seed = "42"
			if mb != nil {
				intent, err = mb.PrepareMusic(ctx, intent, cat, cat, nil)
				if err != nil {
					r.Errors = append(r.Errors, err.Error())
				}
			}
			intent, _ = resolution.Apply(cat, intent)
			// Metadata can corroborate a category omitted by the model. Check
			// end-to-end meaning here, while retaining raw parser issues above.
			r.Errors = append(r.Errors, checkIntent(expected, intent)...)
			fmt.Printf("  Resolved in %s\n", time.Since(started).Round(time.Millisecond))
			r.Intent = intent
			lastNote := ""
			lastDone := int64(-1)
			progress := ports.ProgressFunc(func(_ string, done, total int64, note string) {
				if note != lastNote || done != lastDone {
					fmt.Printf("  %s (%d/%d)\n", note, done, total)
					lastNote, lastDone = note, done
				}
			})
			if mode == core.DeejAIOnly {
				r.Playlist, err = deejai.BuildOnly(ctx, deejai.New(cat, brute.New(cat), cat), intent)
			} else {
				r.Playlist, err = engine.BuildRecommendation(ctx, ports.RecommendationRequest{Intent: intent, Progress: progress, EnhancedAudio: enhancedSnapshot})
			}
			if err != nil {
				r.Errors = append(r.Errors, err.Error())
			}
			r.Errors = append(r.Errors, checkPlaylist(r.Playlist, *minimum, intent.Controls.TotalTrackCount)...)
			r.Errors = append(r.Errors, checkVariety(r.Playlist, c, *minArtists)...)
			if *bundle != "" && mode != core.DeejAIOnly && len(audio.Clauses(intent)) > 0 && len(r.Playlist.Tracks) > 0 {
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
		if r.ParseOnly {
			fmt.Printf("%s: interpretation only, issues=%v elapsed=%dms\n", c.Prompt, r.Errors, r.Milliseconds)
		} else {
			fmt.Printf("%s: tracks=%d issues=%v elapsed=%dms\n", c.Prompt, len(r.Playlist.Tracks), r.Errors, r.Milliseconds)
		}
		failed = failed || len(r.Errors) > 0
	}
	if len(results) == 0 {
		return fmt.Errorf("no prompt cases selected")
	}
	report, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(*output, append(report, '\n'), 0600); err != nil {
		return err
	}
	if failed {
		return fmt.Errorf("one or more live prompt checks failed; see %s", *output)
	}
	return nil
}

func checkVariety(p core.Playlist, c promptCase, minimum int) []string {
	var issues []string
	minimum = max(c.MinimumArtists, minimum)
	artists := map[string]bool{}
	for i, track := range p.Tracks {
		if c.OnlyArtist != "" && !strings.EqualFold(track.Artist, c.OnlyArtist) {
			issues = append(issues, "artist-only restriction lost")
		}
		if key := core.NormalizeIdentityPart(track.Artist); key != "" {
			artists[key] = true
			if minimum > 0 && c.OnlyArtist == "" && i > 0 && key == core.NormalizeIdentityPart(p.Tracks[i-1].Artist) {
				issues = append(issues, "adjacent artist repeat")
			}
		}
	}
	if c.OnlyArtist != "" {
		minimum = 1
	}
	if len(artists) < minimum {
		issues = append(issues, fmt.Sprintf("artist variety lost: got %d, minimum %d", len(artists), minimum))
	}
	return issues
}
func checkIntent(c promptCase, m core.MusicIntent) []string {
	var issues []string
	if c.Count > 0 && m.Controls.TotalTrackCount != c.Count {
		issues = append(issues, fmt.Sprintf("requested count lost: got %d, want %d", m.Controls.TotalTrackCount, c.Count))
	}
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
			found = found || r.Influence != core.InfluenceNegative && r.Kind == reference.kind && contains(r.Query, reference.value)
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
			if g.Influence != core.InfluenceNegative {
				words = append(words, strings.ToLower(g.Value))
			}
		}
		for _, criterion := range m.EssentialCriteria {
			if criterion.Kind == "genre" || criterion.Kind == "style" {
				words = append(words, strings.ToLower(criterion.Value))
			}
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
			found = found || r.Influence != core.InfluenceNegative && r.Kind == core.ReferenceArtist && strings.EqualFold(r.Query, c.Artist)
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
	if c.Vocal != "" && m.Preferences.VocalPreference != nil {
		for _, criterion := range m.EssentialCriteria {
			if (criterion.Kind == "genre" || criterion.Kind == "style") && strings.EqualFold(criterion.Value, m.Preferences.VocalPreference.Value) {
				issues = append(issues, "vocal preference misclassified as a genre requirement")
			}
		}
	}
	for _, item := range []struct {
		want   string
		actual []core.IntentPreference
	}{{c.Mood, m.Preferences.Moods}, {c.Texture, m.Preferences.TextureDescriptions}} {
		if item.want != "" {
			found := false
			for _, p := range item.actual {
				if p.Influence == core.InfluenceNegative {
					continue
				}
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
	for _, mood := range c.NegativeMoods {
		found := false
		for _, p := range m.Preferences.Moods {
			if p.Influence == core.InfluenceNegative && strings.EqualFold(p.Value, mood) {
				found = true
			}
		}
		if !found {
			issues = append(issues, "negative mood missing or scored with double negation: "+mood)
		}
	}
	return issues
}

// Count acceptance is separate from musical fulfillment. Never pad, retry away
// exclusions, or label uncalibrated preview evidence as a quality judgment.
func checkPlaylist(p core.Playlist, minimum, requested int) []string {
	var issues []string
	if len(p.Tracks) < minimum {
		issues = append(issues, fmt.Sprintf("insufficient playlist: got %d tracks, minimum %d", len(p.Tracks), minimum))
	}
	if requested > 0 && len(p.Tracks) > requested {
		issues = append(issues, "playlist exceeds requested count")
	}
	seen := map[string]bool{}
	for _, track := range p.Tracks {
		key := core.ProvisionalRecordingKey(track)
		if seen[key] {
			issues = append(issues, "duplicate recording: "+track.ID)
		}
		seen[key] = true
	}
	return issues
}
