package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
)

const acousticTestID = "099b148e-fe99-4b79-be6e-5078e4bb7415"
const acousticTestOtherID = "12345678-1234-4234-8234-123456789012"

// Synthetic API-shaped fixtures test plumbing, not musical quality. Deliberate
// contradictory classifiers must stay predictions, never become trusted tags.
const acousticLowFixture = `{"metadata":{"audio_properties":{"length":120,"md5_encoded":"private-hash"},"tags":{"file_name":"private-path"},"version":{"essentia":"test","extractor":"fixture"}},"rhythm":{"bpm":120,"danceability":2.4},"tonal":{"key_key":"C","key_scale":"major","key_strength":0.7},"lowlevel":{"average_loudness":0,"dynamic_complexity":3}}`
const acousticHighFixture = `{"metadata":{"tags":{"file_name":"private-path"},"version":{"highlevel":{"essentia":"test"}}},"highlevel":{"genre_dortmund":{"value":"electronic","probability":0.9,"all":{"electronic":0.9,"rock":0.1},"version":{"models_essentia_git_sha":"fixture"}},"voice_instrumental":{"value":"voice","probability":0.8,"all":{"voice":0.8,"instrumental":0.2},"version":{"extractor":"fixture"}},"gender":{"value":"male","probability":1,"all":{"male":1},"version":{"extractor":"fixture"}}}}`

func acousticTrack(id string) core.EnrichedTrack {
	return core.EnrichedTrack{Ref: core.TrackRef{ID: id}, Matched: true, IdentityStatus: core.ResolutionResolved, RecordingID: id}
}

func acousticTestClient(t *testing.T, base, cache string) *Client {
	t.Helper()
	c, err := New(Config{UserAgent: "PlaylistAI acoustic test", AcousticBrainzURL: base, CachePath: cache})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func acousticHandler(t *testing.T, calls *atomic.Int32) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing User-Agent")
		}
		ids := strings.Split(r.URL.Query().Get("recording_ids"), ";")
		if len(ids) > 25 {
			t.Errorf("oversized batch: %d", len(ids))
		}
		fixture := acousticHighFixture
		if r.URL.Path == "/api/v1/low-level" {
			fixture = acousticLowFixture
			if r.URL.Query().Get("features") != acousticFeatures {
				t.Error("missing bounded projection")
			}
		}
		out := map[string]map[string]json.RawMessage{}
		for _, id := range ids {
			if id != acousticTestOtherID {
				out[id] = map[string]json.RawMessage{"0": json.RawMessage(fixture)}
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	}
}

func TestAcousticCharacteristicsCacheAndIdentity(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(acousticHandler(t, &calls))
	defer server.Close()
	c := acousticTestClient(t, server.URL, "")
	tracks := []core.EnrichedTrack{acousticTrack(acousticTestID), acousticTrack(acousticTestOtherID), acousticTrack("not-an-mbid"), acousticTrack(acousticTestID)}
	tracks[3].IdentityStatus = core.ResolutionAmbiguous
	c.acousticTracks(context.Background(), tracks, 25)
	a := tracks[0].Acoustic
	if a == nil || a.LowStatus != "available" || a.HighStatus != "available" || a.Low == nil || *a.Low.BPM != 120 || *a.Low.Danceability != 2.4 || a.Low.AverageLoudness == nil || *a.Low.AverageLoudness != 0 || *a.Low.AnalyzedSeconds != 120 {
		t.Fatalf("lost measured features: %+v", a)
	}
	if len(a.Predictions) != 2 || a.Predictions["voice_instrumental"].Value != "voice" || len(tracks[0].GenreTags) != 0 {
		t.Fatalf("unsafe classifier promotion: %+v", tracks[0])
	}
	if tracks[1].Acoustic.LowStatus != "missing" || tracks[1].Acoustic.HighStatus != "missing" {
		t.Fatal("missing recordings were not distinguished")
	}
	if tracks[2].Acoustic != nil || tracks[3].Acoustic != nil {
		t.Fatal("unresolved identity received characteristics")
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want two bulk requests", calls.Load())
	}
	// A different batch must reuse individual positive AND negative entries.
	c.acousticTracks(context.Background(), []core.EnrichedTrack{acousticTrack(acousticTestOtherID)}, 25)
	c.acousticTracks(context.Background(), []core.EnrichedTrack{acousticTrack(acousticTestID)}, 25)
	if calls.Load() != 2 {
		t.Fatal("fresh cache was bypassed")
	}
	for _, entry := range c.memory {
		if strings.Contains(entry.body, "private-") || strings.Contains(entry.body, "gender") {
			t.Fatal("unnecessary metadata persisted")
		}
	}
	// Fields survive saved intent serialization without changing eligibility tags.
	intent := core.MusicIntent{Knowledge: &core.KnowledgeSnapshot{Tracks: tracks}}
	raw, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	var restored core.MusicIntent
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Knowledge.Tracks[0].Acoustic.Predictions["voice_instrumental"].Score != .8 {
		t.Fatal("history lost characteristics")
	}
}

func TestAcousticCachePersistenceClearAndExpiry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(acousticHandler(t, &calls))
	defer server.Close()
	cache := filepath.Join(t.TempDir(), "metadata.sqlite")
	c := acousticTestClient(t, server.URL, cache)
	c.acousticTracks(context.Background(), []core.EnrichedTrack{acousticTrack(acousticTestID)}, 1)
	reopened := acousticTestClient(t, server.URL, cache)
	reopened.acousticTracks(context.Background(), []core.EnrichedTrack{acousticTrack(acousticTestID)}, 1)
	if calls.Load() != 2 {
		t.Fatal("SQLite cache not reused")
	}
	if _, err := c.db.Exec("UPDATE mb_cache SET fetched_at=?", time.Now().Add(-8*24*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	reopened.acousticTracks(context.Background(), []core.EnrichedTrack{acousticTrack(acousticTestID)}, 1)
	if calls.Load() != 4 {
		t.Fatal("expired evidence was not refreshed")
	}
	if err := c.ClearCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	reopened.acousticTracks(context.Background(), []core.EnrichedTrack{acousticTrack(acousticTestID)}, 1)
	if calls.Load() != 6 {
		t.Fatal("clear cache did not remove AcousticBrainz data")
	}
}

func TestAcousticUnavailableIsNotNegativeEvidence(t *testing.T) {
	for _, body := range []string{`{"error":"unavailable"}`, `<html>maintenance</html>`, `null`, `{"unexpected":{}}`, `{"` + acousticTestID + `":{"0":null}}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			c := acousticTestClient(t, server.URL, "")
			tracks := []core.EnrichedTrack{acousticTrack(acousticTestID)}
			c.acousticTracks(context.Background(), tracks, 1)
			if tracks[0].Acoustic.LowStatus != "unavailable" || tracks[0].Acoustic.HighStatus != "unavailable" || len(c.memory) != 0 {
				t.Fatalf("outage cached as absence: %+v", tracks[0].Acoustic)
			}
		})
	}
}

func TestAcousticBatchBoundAndOffline(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(acousticHandler(t, &calls))
	defer server.Close()
	c := acousticTestClient(t, server.URL, "")
	tracks := make([]core.EnrichedTrack, 26)
	for i := range tracks {
		tracks[i] = acousticTrack(fmt.Sprintf("12345678-1234-4234-8234-%012d", i+1))
	}
	c.acousticTracks(context.WithValue(context.Background(), cacheOnlyKey{}, true), tracks, 26)
	if calls.Load() != 0 {
		t.Fatal("offline lookup accessed network")
	}
	// Fresh track objects are used for a new generation; replay retains unknowns.
	for i := range tracks {
		tracks[i].Acoustic = nil
	}
	c.acousticTracks(context.Background(), tracks, 26)
	if calls.Load() != 4 {
		t.Fatalf("calls = %d, want two pairs of bounded batches", calls.Load())
	}
	for _, track := range tracks {
		if track.Acoustic.Low == nil {
			t.Fatal("lost second batch")
		}
	}
}

func TestAcousticRateBackoffAndCancellation(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("X-RateLimit-Reset-In", "60")
		w.WriteHeader(429)
	}))
	defer server.Close()
	c := acousticTestClient(t, server.URL, "")
	c.acousticTracks(context.Background(), []core.EnrichedTrack{acousticTrack(acousticTestID)}, 1)
	c.acousticTracks(context.Background(), []core.EnrichedTrack{acousticTrack(acousticTestOtherID)}, 1)
	if calls.Load() != 1 || len(c.memory) != 0 {
		t.Fatal("rate limit was retried or cached as missing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tracks := []core.EnrichedTrack{acousticTrack(acousticTestID)}
	c.acousticTracks(ctx, tracks, 1)
	if tracks[0].Acoustic != nil || calls.Load() != 1 {
		t.Fatal("canceled call ran")
	}
}

func TestAcousticProjectionRejectsInvalidNumbersAndVersions(t *testing.T) {
	doc, err := acousticProjection(json.RawMessage(`{"metadata":{"version":{"extractor":"test"}},"rhythm":{"bpm":-10,"danceability":2},"tonal":{"key_strength":4}}`), "low-level")
	if err != nil || doc.Rhythm.BPM != nil || doc.Tonal.Strength != nil || *doc.Rhythm.Danceability != 2 {
		t.Fatal("invalid measurement validation")
	}
	doc, err = acousticProjection(json.RawMessage(`{"highlevel":{"mood_happy":{"value":"happy","probability":2,"all":{"happy":2},"version":{"extractor":"test"}},"voice_instrumental":{"value":"voice","probability":1,"all":{"voice":1}}}}`), "high-level")
	if err != nil || len(doc.High) != 0 {
		t.Fatal("invalid classifier accepted")
	}
}

func TestAcousticEnrichmentAndCachedRecording(t *testing.T) {
	var calls atomic.Int32
	ab := httptest.NewServer(acousticHandler(t, &calls))
	defer ab.Close()
	mb := newMBServer(t, map[string]mbRecording{"genesis": {ID: acousticTestID, Score: 100, Title: "Genesis", ArtistCredit: []mbArtistCredit{{Name: "Justice"}}}})
	c := newClient(t, mb.URL, time.Millisecond)
	var err error
	c.acoustic, err = newAcousticClient(ab.URL)
	if err != nil {
		t.Fatal(err)
	}
	ref := core.TrackRef{ID: "catalog-id", Artist: "Justice", Title: "Genesis"}
	tracks, err := c.Enrich(context.Background(), []core.TrackRef{ref}, nil)
	if err != nil || len(tracks) != 1 || tracks[0].Acoustic == nil || tracks[0].Acoustic.Low == nil {
		t.Fatalf("enrichment did not attach analysis: %v, %+v", err, tracks)
	}
	cached, ok := c.CachedRecording(ref)
	if !ok || cached.Acoustic == nil || cached.Acoustic.Low == nil || calls.Load() != 2 {
		t.Fatal("cached recording lost analysis or accessed network")
	}
}

func TestAcousticCancellationAndClearDuringFetch(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = fmt.Fprintf(w, `{"%s":{"0":%s}}`, acousticTestID, acousticLowFixture)
	}))
	defer server.Close()
	c := acousticTestClient(t, server.URL, "")
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.acousticBatch(context.Background(), []string{acousticTestID}, "low-level")
	}()
	<-started
	if err := c.ClearCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-done
	if len(c.memory) != 0 {
		t.Fatal("in-flight fetch refilled cleared cache")
	}

	blocked := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer blocked.Close()
	c2 := acousticTestClient(t, blocked.URL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	c2.acousticTracks(ctx, []core.EnrichedTrack{acousticTrack(acousticTestID)}, 1)
	if ctx.Err() == nil || time.Since(start) > time.Second || len(c2.memory) != 0 {
		t.Fatal("in-flight cancellation was not honored")
	}
}

func TestAcousticStreamBudgetAndStaleFallback(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(acousticHandler(t, &calls))
	defer server.Close()
	c := acousticTestClient(t, server.URL, "")
	stream := &candidateStream{client: c, acousticSpent: 8 * time.Second, snapshot: core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{acousticTrack(acousticTestID)}}}
	stream.enrichAcoustic(context.Background())
	if calls.Load() != 0 {
		t.Fatal("stream exceeded optional time budget")
	}
	c.acousticTracks(context.Background(), stream.snapshot.Tracks, 1)
	for key, entry := range c.memory {
		entry.fetched = time.Now().Add(-8 * 24 * time.Hour).Unix()
		c.memory[key] = entry
	}
	server.Close()
	tracks := []core.EnrichedTrack{acousticTrack(acousticTestID)}
	c.acousticTracks(context.Background(), tracks, 1)
	if tracks[0].Acoustic == nil || tracks[0].Acoustic.Low == nil {
		t.Fatal("outage discarded cached measurements")
	}
	for _, entry := range c.memory {
		if time.Since(time.Unix(entry.fetched, 0)) < 7*24*time.Hour {
			t.Fatal("stale cache timestamp was renewed")
		}
	}
}

// Opt-in primary-provider smoke test, never a musical relevance benchmark.
func TestLiveAcousticBrainz(t *testing.T) {
	if os.Getenv("PLAYLISTAI_LIVE_ACOUSTICBRAINZ") != "1" {
		t.Skip("set PLAYLISTAI_LIVE_ACOUSTICBRAINZ=1 for the live archived API smoke test")
	}
	c, err := New(Config{UserAgent: "PlaylistAI/AcousticBrainz-smoke (+https://github.com/platten/playlistai)", AcousticBrainzURL: AcousticBrainzURL})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tracks := []core.EnrichedTrack{acousticTrack(acousticTestID)}
	started := time.Now()
	c.acousticTracks(context.Background(), tracks, 1)
	if tracks[0].Acoustic == nil || tracks[0].Acoustic.Low == nil || tracks[0].Acoustic.HighStatus != "available" {
		t.Fatalf("live API unavailable or incompatible: %+v", tracks[0].Acoustic)
	}
	t.Logf("two-endpoint cold lookup: %s; %d classifiers; duration evidence: %v", time.Since(started), len(tracks[0].Acoustic.Predictions), tracks[0].Acoustic.Low.AnalyzedSeconds != nil)
	started = time.Now()
	c.acousticTracks(context.Background(), []core.EnrichedTrack{acousticTrack(acousticTestID)}, 1)
	t.Logf("warm per-recording lookup: %s", time.Since(started))
}
