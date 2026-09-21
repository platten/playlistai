package multichannel

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/enrich/musicbrainz"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/similarity/brute"
)

// The exported MusicBrainz configuration supports loopback mirrors but not
// transport injection. This fixture uses a loopback-only HTTP server, the real
// provider client and its throttle/cache, and no internet/model/assets.
type pipelineFixture struct {
	cat      *fakes.Catalog
	intent   core.MusicIntent
	server   *httptest.Server
	requests atomic.Int64
}

func newPipelineFixture(tb testing.TB) *pipelineFixture {
	tb.Helper()
	fixture := &pipelineFixture{}
	var tracks []fakes.CatalogTrack
	pool := core.GenreArtistPool{Genre: "ambient", Sources: []string{"synthetic-fixture"}}
	for i := range 8 {
		id := fmt.Sprintf("a%d", i)
		tracks = append(tracks, fakes.CatalogTrack{ID: id, Display: "Artist " + id + " - Song", Audio: []float32{1, 0}, Track: []float32{0, 1}})
		pool.Artists = append(pool.Artists, core.GenreArtist{ID: id, Name: "Artist " + id})
	}
	fixture.cat = fakes.NewCatalog(2, tracks...)
	fixture.intent = core.MusicIntent{Version: core.CurrentIntentVersion, VerificationPolicy: core.VerifiedOnly, Mode: core.ModeSimilar, Seed: "42",
		Controls:    core.IntentControls{TotalTrackCount: 4, AudioWeight: .5, CooccurrenceWeight: .5},
		Preferences: core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "ambient", Influence: core.InfluencePositive}}},
		Knowledge:   &core.KnowledgeSnapshot{ArtistPools: []core.GenreArtistPool{pool}},
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.requests.Add(1)
		select {
		case <-time.After(2 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		if r.URL.Path != "/ws/2/recording" {
			http.Error(w, "unexpected fixture request", http.StatusBadRequest)
			return
		}
		id := strings.TrimPrefix(r.URL.Query().Get("query"), "arid:")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"count":1,"recordings":[{"id":"r%s","title":"Song","artist-credit":[{"name":"Artist %s","artist":{"id":"%s"}}]}]}`, id, id, id)
	}))
	tb.Cleanup(fixture.server.Close)
	return fixture
}

type pipelineSource struct {
	client   *musicbrainz.Client
	parallel bool
}

func (s pipelineSource) OpenCandidates(intent core.MusicIntent, cat ports.Catalog, resolver ports.ReferenceResolver) ports.MusicCandidateStream {
	stream := s.client.OpenCandidates(intent, cat, resolver)
	if s.parallel {
		return stream
	}
	return ordinaryPipelineStream{MusicCandidateStream: stream}
}

// Embedding only the ordinary interface deliberately hides prefetch; provider
// evidence is forwarded so scheduling is the only difference between runs.
type ordinaryPipelineStream struct{ ports.MusicCandidateStream }

func (s ordinaryPipelineStream) Evidence(id string) []core.RetrievalEvidence {
	return s.MusicCandidateStream.(ports.MusicCandidateEvidence).Evidence(id)
}

type pipelineAnalysisStore struct{ ports.AnalysisStore }

func (pipelineAnalysisStore) Find(ctx context.Context, catalog, id, key string, model core.AudioModelIdentity) (core.AudioAnalysis, bool, error) {
	select {
	case <-time.After(2 * time.Millisecond):
	case <-ctx.Done():
		return core.AudioAnalysis{}, false, ctx.Err()
	}
	record := core.AudioAnalysis{TrackID: id, CatalogVersion: catalog, TrackKey: key, Model: model, Identity: core.PreviewIdentity{Provider: "fixture", ProviderID: id, Status: core.ResolutionResolved}, AudioSHA256: strings.Repeat("0", 64), Segments: []core.AudioSegment{{StartSeconds: 0, EndSeconds: 10, Embedding: []float32{1, 0}}}}
	record.ID = audio.Fingerprint(record)
	return record, true, nil
}
func (pipelineAnalysisStore) PutAssessment(context.Context, core.AudioAssessment) error { return nil }

func (f *pipelineFixture) run(tb testing.TB, parallel bool) (core.Playlist, time.Duration, time.Duration, int64) {
	tb.Helper()
	client, err := musicbrainz.New(musicbrainz.Config{UserAgent: "playlist-ai-pipeline-fixture", MirrorURL: f.server.URL, Interval: 2 * time.Millisecond})
	if err != nil {
		tb.Fatal(err)
	}
	defer client.Close()
	service := &audio.Service{Resolver: &noPreviewFetch{}, Analyzer: &audioFixtureEncoder{}, Store: pipelineAnalysisStore{}, Authorized: true, ParityValidated: true, Policy: audio.Policy{Version: "synthetic/v1", DevelopmentSet: "control-flow-only", MinimumPositive: .6, MaximumNegative: .2}}
	engine := New(f.cat, brute.New(f.cat), f.cat, DefaultConfig()).WithCandidateSource(pipelineSource{client: client, parallel: parallel}).WithAudioProvider(func() *audio.Service { return service })
	before := f.requests.Load()
	started := time.Now()
	var first time.Duration
	playlist, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: f.intent, OnChecked: func(core.TrackRef) {
		if first == 0 {
			first = time.Since(started)
		}
	}})
	elapsed := time.Since(started)
	if err != nil || len(playlist.Tracks) != 4 || first == 0 {
		tb.Fatalf("pipeline tracks=%d first=%s err=%v outcome=%+v", len(playlist.Tracks), first, err, playlist.Outcome)
	}
	return playlist, first, elapsed, f.requests.Load() - before
}

func TestPipelinePrefetchEquivalent(t *testing.T) {
	fixture := newPipelineFixture(t)
	serial, _, _, serialRequests := fixture.run(t, false)
	parallel, _, _, parallelRequests := fixture.run(t, true)
	if !equalPipelineResult(serial, parallel) {
		t.Fatal("serial and parallel playlist, evidence or replay snapshot changed")
	}
	if serialRequests != 4 || parallelRequests < 4 || parallelRequests > 8 {
		t.Fatalf("requests serial=%d parallel=%d", serialRequests, parallelRequests)
	}
}

func BenchmarkBuildRecommendationPrefetch(b *testing.B) {
	fixture := newPipelineFixture(b)
	baseline, _, _, _ := fixture.run(b, false)
	for _, idle := range []bool{false, true} {
		provider := "busy"
		if idle {
			provider = "idle"
		}
		for _, parallel := range []bool{false, true} {
			b.Run(fmt.Sprintf("provider=%s/parallel=%t", provider, parallel), func(b *testing.B) {
				var firstTotal, playlistTotal time.Duration
				var requests int64
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					if idle {
						// The process-wide provider limiter survives Client.Close.
						// Let the previous generation's final speculative dispatch
						// expire outside both the benchmark and backend timers.
						b.StopTimer()
						time.Sleep(3 * time.Millisecond)
						b.StartTimer()
					}
					playlist, first, elapsed, calls := fixture.run(b, parallel)
					if !equalPipelineResult(playlist, baseline) {
						b.Fatal("playlist or replay snapshot changed")
					}
					firstTotal += first
					playlistTotal += elapsed
					requests += calls
				}
				b.ReportMetric(float64(firstTotal.Nanoseconds())/float64(b.N), "ns/first-checked")
				b.ReportMetric(float64(playlistTotal.Nanoseconds())/float64(b.N), "ns/playlist")
				b.ReportMetric(float64(requests)/float64(b.N), "requests/op")
			})
		}
	}
}

// Execution duration is telemetry, not a recommendation/replay identity.
func equalPipelineResult(left, right core.Playlist) bool {
	if left.AudioEvidence != nil {
		copy := *left.AudioEvidence
		copy.ElapsedMilliseconds = 0
		left.AudioEvidence = &copy
	}
	if right.AudioEvidence != nil {
		copy := *right.AudioEvidence
		copy.ElapsedMilliseconds = 0
		right.AudioEvidence = &copy
	}
	return reflect.DeepEqual(left, right)
}
