package multichannel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/enrich/musicbrainz"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/preview/deezer"
	"github.com/platten/playlistai/internal/similarity/brute"
)

type generateSample struct {
	Prompt        string   `json:"prompt"`
	Genre         string   `json:"genre"`
	Artist        string   `json:"artist"`
	Vocal         string   `json:"vocal"`
	Mood          string   `json:"mood"`
	NegativeMood  string   `json:"negativeMood"`
	JourneyGenres []string `json:"journeyGenres"`
}

// Read the exact prompts rendered by Generate; UI changes cannot silently
// leave the acceptance suite testing a different set of examples.
func generateSamples(t *testing.T) []generateSample {
	t.Helper()
	raw, err := os.ReadFile("../../../frontend/src/lib/generateSamples.json")
	if err != nil {
		t.Fatal(err)
	}
	var samples []generateSample
	if err := json.Unmarshal(raw, &samples); err != nil {
		t.Fatal(err)
	}
	if len(samples) == 0 {
		t.Fatal("no Generate examples")
	}
	return samples
}

func checkSampleIntent(t *testing.T, sample generateSample, intent core.MusicIntent) {
	t.Helper()
	if sample.Genre != "" && (len(intent.EssentialCriteria) == 0 || core.CanonicalStyle(intent.EssentialCriteria[0].Value) != sample.Genre) {
		t.Fatalf("genre lost: %+v", intent)
	}
	if sample.Artist != "" && (len(intent.References) != 1 || intent.References[0].Query != sample.Artist) {
		t.Fatalf("artist lost: %+v", intent.References)
	}
	if sample.Vocal != "" && !core.WantsInstrumental(intent) {
		t.Fatal("vocal restriction lost")
	}
	for mood, influence := range map[string]core.Influence{sample.Mood: core.InfluencePositive, sample.NegativeMood: core.InfluenceNegative} {
		if mood == "" {
			continue
		}
		found := false
		for _, preference := range intent.Preferences.Moods {
			found = found || preference.Value == mood && preference.Influence == influence
		}
		if !found {
			t.Fatalf("%s mood %q lost", influence, mood)
		}
	}
	if len(sample.JourneyGenres) > 0 && len(core.JourneyCriteria(intent.EssentialCriteria)) != len(sample.JourneyGenres) {
		t.Fatal("journey stages lost")
	}
}

// Synthetic features prove pipeline behavior, not musical relevance.
func TestEveryGenerateSampleBuildsPlaylist(t *testing.T) {
	for _, sample := range generateSamples(t) {
		t.Run(sample.Prompt, func(t *testing.T) {
			ctx := context.Background()
			intent, err := rules.New().Parse(ctx, ports.IntentInput{Prompt: sample.Prompt})
			if err != nil {
				t.Fatal(err)
			}
			checkSampleIntent(t, sample, intent)
			intent.VerificationPolicy, intent.Seed = core.BestAvailable, "42"
			var entries []fakes.CatalogTrack
			for i := 0; i < core.DefaultCount+2; i++ {
				artist := fmt.Sprintf("Fixture %d", i)
				if i == 0 {
					artist = "Bonobo"
				}
				entries = append(entries, fakes.CatalogTrack{ID: fmt.Sprint(i), Display: artist + " - Sample", Audio: []float32{1, 0}, Track: []float32{1, 0}})
			}
			cat := fakes.NewCatalog(2, entries...)
			store, err := audio.OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			encoder := &audioFixtureEncoder{}
			intent.Knowledge = &core.KnowledgeSnapshot{ID: "synthetic-generate-samples"}
			for i, entry := range entries {
				meta, _ := cat.Meta(entry.ID)
				genre := "ambient electronic"
				if len(sample.JourneyGenres) > 0 {
					genre = sample.JourneyGenres[i%len(sample.JourneyGenres)]
				}
				intent.Knowledge.Candidates = append(intent.Knowledge.Candidates, meta.Ref)
				intent.Knowledge.Tracks = append(intent.Knowledge.Tracks, core.EnrichedTrack{Ref: meta.Ref, Matched: true, IdentityStatus: core.ResolutionResolved, GenreTags: []core.AttributedGenreTag{{Name: genre, Votes: 3, Source: "fixture"}}})
				record := core.AudioAnalysis{TrackID: entry.ID, CatalogVersion: cat.CatalogVersion(), TrackKey: core.ProvisionalRecordingKey(meta.Ref), Model: encoder.Identity(), Identity: core.PreviewIdentity{Provider: "deezer", ProviderID: entry.ID, Status: core.ResolutionResolved}, AudioSHA256: strings.Repeat("0", 64), Segments: []core.AudioSegment{{StartSeconds: 0, EndSeconds: 10, Embedding: []float32{1, 0}}}}
				record.ID = audio.Fingerprint(record)
				if err := store.Put(ctx, record); err != nil {
					t.Fatal(err)
				}
			}
			service := &audio.Service{Resolver: &noPreviewFetch{}, Analyzer: encoder, Store: store, Authorized: true, ParityValidated: true}
			engine := New(cat, brute.New(cat), cat, DefaultConfig()).WithCandidateSource(&fixtureDiscovery{}).WithAudioProvider(func() *audio.Service { return service })
			playlist, err := engine.Build(ctx, intent)
			if err != nil || len(playlist.Tracks) != core.DefaultCount {
				t.Fatalf("sample returned %d/%d tracks: %+v, notices=%+v, %v", len(playlist.Tracks), core.DefaultCount, playlist.Outcome, playlist.Notices, err)
			}
			again, err := engine.Build(ctx, playlist.Intent)
			if err != nil || trackIDs(again.Tracks) != trackIDs(playlist.Tracks) {
				t.Fatal("replay changed", err)
			}
		})
	}
}

func TestGenerateSamplesLive(t *testing.T) {
	dir := os.Getenv("PLAYLISTAI_TEST_GENERATE_CATALOG")
	if dir == "" {
		t.Skip("opt-in real catalog/metadata/CLAP smoke")
	}
	cat, err := catalog.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	bundle := os.Getenv("PLAYLISTAI_TEST_CLAP_BUNDLE")
	manifest, err := audio.ReadRuntimeBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	worker := &audio.Worker{Executable: os.Getenv("PLAYLISTAI_TEST_CLAP_WORKER"), BundleDir: bundle, Model: manifest.Model}
	defer worker.Close()
	healthContext, stopHealth := context.WithTimeout(context.Background(), 30*time.Second)
	defer stopHealth()
	if err := worker.Health(healthContext); err != nil {
		t.Fatalf("CLAP worker health (use cmd/audioworker): %v", err)
	}
	store, err := audio.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	knowledge, err := musicbrainz.New(musicbrainz.Config{UserAgent: "PlaylistAI/0.7.0 (https://github.com/platten/playlistai)", CachePath: filepath.Join(t.TempDir(), "metadata.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	defer knowledge.Close()
	service := &audio.Service{Resolver: deezer.New(deezer.Config{}), Analyzer: worker, Store: store, Recordings: knowledge, Authorized: true, ParityValidated: manifest.Parity.Valid()}
	for _, sample := range generateSamples(t) {
		t.Run(sample.Prompt, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 17*time.Minute)
			defer cancel()
			started := time.Now()
			intent, _ := rules.New().Parse(ctx, ports.IntentInput{Prompt: sample.Prompt})
			checkSampleIntent(t, sample, intent)
			intent.VerificationPolicy, intent.Seed = core.BestAvailable, "42"
			intent, err = knowledge.PrepareMusic(ctx, intent, cat, cat, nil)
			if err != nil {
				t.Fatal(err)
			}
			playlist, err := New(cat, brute.New(cat), cat, DefaultConfig()).WithCandidateSource(knowledge).WithAudioProvider(func() *audio.Service { return service }).Build(ctx, intent)
			t.Logf("tracks=%d/%d outcome=%+v elapsed=%s lookup=%v", len(playlist.Tracks), intent.Controls.TotalTrackCount, playlist.Outcome, time.Since(started), intent.Knowledge.Notices)
			if k := playlist.Intent.Knowledge; k != nil {
				for _, pool := range k.ArtistPools {
					t.Logf("pool=%q artists=%d sources=%d", pool.Genre, len(pool.Artists), len(pool.Sources))
				}
				t.Logf("drawn=%d grounded=%d notices=%+v", len(k.Discovery), len(k.Tracks), playlist.Notices)
			}
			if a := playlist.AudioEvidence; a != nil {
				t.Logf("analysis new=%d cached=%d assessed=%d", a.NewAnalyses, a.CacheHits, len(a.Assessments))
			}
			if err != nil || len(playlist.Tracks) == 0 {
				t.Fatalf("sample failed: %v", err)
			}
		})
	}
}
