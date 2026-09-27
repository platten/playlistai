package musicgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const artistA = "11111111-1111-4111-8111-111111111111"
const artistB = "22222222-2222-4222-8222-222222222222"
const recordingA = "33333333-3333-4333-8333-333333333333"
const recordingB = "44444444-4444-4444-8444-444444444444"

func count(n int64) *int64 { return &n }
func fixtureSource(endpoint string) Source {
	return Source{Provider: "listenbrainz", URL: endpoint, RetrievedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), ResponseSHA256: strings.Repeat("a", 64), License: "CC0-1.0"}
}
func fixture() Snapshot {
	source := fixtureSource(apiBase + "/1/popularity/artist")
	return Snapshot{Version: Version, PreparedAt: source.RetrievedAt, Artists: []Artist{{MBID: artistA, Counts: Counts{UniqueListeners: count(12), ListenCount: count(20)}, Source: source}, {MBID: artistB, Source: source}}, Recordings: []Recording{{MBID: recordingA, ArtistMBIDs: []string{artistA}, Counts: Counts{UniqueListeners: count(1), ListenCount: count(10)}, Source: fixtureSource(topURL(artistA))}, {MBID: recordingB, ArtistMBIDs: []string{artistA}, Counts: Counts{UniqueListeners: count(4), ListenCount: count(5)}, Source: fixtureSource(topURL(artistA))}}, TopRecordings: []ArtistRecordings{{ArtistMBID: artistA, RecordingMBIDs: []string{recordingA, recordingB}, Source: fixtureSource(topURL(artistA))}}}
}

func TestSnapshotRoundTripAndImmutableLookups(t *testing.T) {
	ctx := context.Background()
	s := fixture()
	path := filepath.Join(t.TempDir(), "graph.json")
	hash, err := Write(ctx, path, s)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, path, hash)
	if err != nil {
		t.Fatal(err)
	}
	if r.SnapshotIdentity() != hash {
		t.Fatal("snapshot identity lost")
	}
	stats, err := r.LookupArtistPopularity(ctx, []string{artistA, artistB})
	if err != nil {
		t.Fatal(err)
	}
	if *stats[artistA].UniqueListeners != 12 || stats[artistB].UniqueListeners != nil || stats[artistB].Listens != nil || stats[artistA].Snapshot != hash {
		t.Fatal(stats)
	}
	*stats[artistA].UniqueListeners = 999
	a, _ := r.Artist(ctx, artistA)
	if *a.UniqueListeners != 12 {
		t.Fatal("mutable count escaped")
	}
	top := r.TopRecordings(ctx, artistA, 1)
	if len(top) != 1 || top[0].MBID != recordingB {
		t.Fatal(top)
	}
	top[0].ArtistMBIDs[0] = artistB
	current, _ := r.Recording(ctx, recordingB)
	if current.ArtistMBIDs[0] != artistA {
		t.Fatal("mutable artist list escaped")
	}
	if rank, ok := r.PopularityRank(ctx, recordingB); !ok || rank != 1 {
		t.Fatal(rank, ok)
	}
	m := r.Manifest()
	m.TopRecordings[0].RecordingMBIDs[0] = "corrupt"
	*m.Recordings[0].UniqueListeners = 99
	if r.Manifest().TopRecordings[0].RecordingMBIDs[0] != recordingA {
		t.Fatal("mutable manifest escaped")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = r.LookupArtistPopularity(ctx, []string{artistA}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if r.TopRecordings(ctx, artistA, 10) != nil {
		t.Fatal("canceled lookup returned data")
	}
}

func TestSnapshotRejectsCorruptionAndAtomicFailure(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "graph.json")
	s := fixture()
	hash, err := Write(ctx, path, s)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if _, err = Write(ctx, path, s); err == nil {
		t.Fatal("existing artifact overwritten")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("prior artifact changed")
	}
	if _, err = Open(ctx, path, strings.Repeat("f", 64)); err == nil {
		t.Fatal("wrong hash accepted")
	}
	if _, err = Open(ctx, path, ""); err == nil {
		t.Fatal("unverified reader accepted")
	}
	for _, change := range []func(*Snapshot){func(v *Snapshot) { v.Version = "future" }, func(v *Snapshot) { v.Artists[0].MBID = "artist/name" }, func(v *Snapshot) { v.Artists[0].UniqueListeners = count(-1) }, func(v *Snapshot) { v.TopRecordings[0].RecordingMBIDs = []string{artistB} }, func(v *Snapshot) { v.Recordings[0].ArtistMBIDs = []string{artistB} }, func(v *Snapshot) { v.Artists[0].Source.URL = apiBase + "/1/user/alice/listens" }} {
		bad := fixture()
		change(&bad)
		if _, err = Write(ctx, path+".bad", bad); err == nil {
			t.Fatal("invalid snapshot accepted")
		}
		if _, err = os.Stat(path + ".bad"); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("failed write published output")
		}
	}
	if err = os.WriteFile(path, append(before, 'x'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(ctx, path, hash); err == nil {
		t.Fatal("corruption accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = Write(canceled, path+".cancel", s); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatal("temporary files leaked", entries)
	}
}

func TestSnapshotDeterministicOrderAndUnknownFields(t *testing.T) {
	dir := t.TempDir()
	a := fixture()
	b := fixture()
	b.Artists[0], b.Artists[1] = b.Artists[1], b.Artists[0]
	b.Recordings[0], b.Recordings[1] = b.Recordings[1], b.Recordings[0]
	x, err := Write(context.Background(), filepath.Join(dir, "a"), a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := Write(context.Background(), filepath.Join(dir, "b"), b)
	if err != nil || x != y {
		t.Fatal(x, y, err)
	}
	raw, _ := json.Marshal(a)
	raw = append(raw[:len(raw)-1], []byte(`,"genre":"jazz"}`)...)
	if _, err = Decode(strings.NewReader(string(raw))); err == nil {
		t.Fatal("musical facts admitted into graph schema")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(transport.CloseIdleConnections)
	httpClient := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "api.listenbrainz.org" || req.URL.Scheme != "https" {
			t.Errorf("unexpected destination %s", req.URL)
		}
		copyReq := req.Clone(req.Context())
		copyURL := *req.URL
		copyURL.Scheme = target.Scheme
		copyURL.Host = target.Host
		copyReq.URL = &copyURL
		return transport.RoundTrip(copyReq)
	})}
	c, err := NewClient(httpClient, UserAgent)
	if err != nil {
		t.Fatal(err)
	}
	c.limiter = &requestLimiter{gate: make(chan struct{}, 1), interval: time.Millisecond}
	return c
}

func TestArtistBatchNullCountsAndForgedIDs(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantErr    bool
	}{
		{"null", fmt.Sprintf(`[{"artist_mbid":%q,"total_user_count":null,"total_listen_count":null}]`, artistA), false},
		{"known", fmt.Sprintf(`[{"artist_mbid":%q,"total_user_count":3,"total_listen_count":5}]`, artistA), false},
		{"forged", fmt.Sprintf(`[{"artist_mbid":%q,"total_user_count":3}]`, artistB), true},
		{"missing", `[]`, true},
		{"negative", fmt.Sprintf(`[{"artist_mbid":%q,"total_user_count":-1}]`, artistA), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.Path != "/1/popularity/artist" || r.Header.Get("User-Agent") != UserAgent || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("request contract", r)
				}
				fmt.Fprint(w, tc.body)
			})
			rows, err := c.FetchArtists(context.Background(), []string{artistA})
			if (err != nil) != tc.wantErr || calls != 1 {
				t.Fatal(rows, err, calls)
			}
			if !tc.wantErr && (!rows[0].Source.valid() || rows[0].Source.ResponseSHA256 != digest([]byte(tc.body))) {
				t.Fatal("source receipt lost")
			}
			if tc.name == "null" && (rows[0].UniqueListeners != nil || rows[0].ListenCount != nil) {
				t.Fatal("null became zero")
			}
			if _, err = c.FetchArtists(context.Background(), []string{"not-an-id"}); err == nil || calls != 1 {
				t.Fatal("invalid ID dispatched")
			}
		})
	}
}

func TestRequestRetryBoundsAndCancellation(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		fmt.Fprintf(w, `[{"artist_mbid":%q}]`, artistA)
	})
	if _, err := c.FetchArtists(context.Background(), []string{artistA}); err != nil || calls.Load() != 2 {
		t.Fatal(err, calls.Load())
	}
	c = testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset-In", "60")
		fmt.Fprintf(w, `[{"artist_mbid":%q}]`, artistA)
	})
	calls.Store(0)
	if _, err := c.FetchArtists(context.Background(), []string{artistA}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.FetchArtists(ctx, []string{artistA}); !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatal(err, calls.Load())
	}
	c = testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
	})
	calls.Store(0)
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.FetchArtists(ctx, []string{artistA}); !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatal(err, calls.Load())
	}
}

func TestResponseSizeRedirectAndForegroundFallback(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, strings.Repeat("x", 100)) })
	c.maxBytes = 64
	if _, err := c.FetchArtists(context.Background(), []string{artistA}); err == nil {
		t.Fatal("oversize response accepted")
	}
	c = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/private", http.StatusFound)
	})
	if _, err := c.FetchArtists(context.Background(), []string{artistA}); err == nil {
		t.Fatal("redirect followed")
	}
	path := filepath.Join(t.TempDir(), "cached")
	hash, err := Write(context.Background(), path, fixture())
	if err != nil {
		t.Fatal(err)
	}
	cached, err := Open(context.Background(), path, hash)
	if err != nil {
		t.Fatal(err)
	}
	c = testClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) })
	got, err := c.ForegroundArtists(context.Background(), cached, []string{artistA, artistB})
	if err != nil || *got[artistA].UniqueListeners != 12 || got[artistB].UniqueListeners != nil {
		t.Fatal(got, err)
	}
	if !reflect.DeepEqual(got[artistA].Source, fixture().Artists[0].Source) {
		t.Fatal("fallback falsely refreshed provenance")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = c.ForegroundArtists(ctx, cached, []string{artistA}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPrepareExactGraphRelationships(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/1/lb-radio/artist/" + artistA:
			if r.URL.Query().Get("max_similar_artists") != "20" || r.URL.Query().Get("pop_begin") != "0" || r.URL.Query().Get("pop_end") != "100" {
				t.Error(r.URL)
			}
			fmt.Fprintf(w, `{%q:[{"recording_mbid":%q,"similar_artist_mbid":%q,"total_listen_count":5}],%q:[{"recording_mbid":%q,"similar_artist_mbid":%q,"total_listen_count":2}]}`, artistA, recordingA, artistA, artistB, recordingB, artistB)
		case "/1/popularity/recording":
			fmt.Fprintf(w, `[{"recording_mbid":%q,"total_user_count":4,"total_listen_count":5},{"recording_mbid":%q,"total_user_count":null,"total_listen_count":null}]`, recordingA, recordingB)
		case "/1/popularity/artist":
			fmt.Fprintf(w, `[{"artist_mbid":%q,"total_user_count":4,"total_listen_count":5},{"artist_mbid":%q,"total_user_count":null,"total_listen_count":null}]`, artistA, artistB)
		default:
			t.Error("unexpected request", r.URL)
			w.WriteHeader(404)
		}
	})
	s, err := c.Prepare(context.Background(), []string{artistA})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || len(s.Recordings) != 2 || len(s.Neighbors) != 1 || s.Neighbors[0].ArtistMBID != artistB {
		t.Fatal(s, calls.Load())
	}
	path := filepath.Join(t.TempDir(), "graph")
	hash, err := Write(context.Background(), path, s)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := Open(context.Background(), path, hash)
	if err != nil {
		t.Fatal(err)
	}
	top := reader.TopRecordings(context.Background(), artistA, 10)
	if len(top) != 1 || top[0].MBID != recordingA || top[0].UniqueListeners == nil || *top[0].UniqueListeners != 4 || top[0].Source.URL != radioURL(artistA) || top[0].CountsSource == nil || top[0].CountsSource.URL != apiBase+"/1/popularity/recording" || top[0].Source.ResponseSHA256 == top[0].CountsSource.ResponseSHA256 {
		t.Fatalf("lost distinct relationship/count receipts: %+v", top)
	}
	top[0].CountsSource.ResponseSHA256 = "changed"
	if reader.TopRecordings(context.Background(), artistA, 10)[0].CountsSource.ResponseSHA256 == "changed" {
		t.Fatal("count source mutation escaped")
	}
	unknown, ok := reader.Recording(context.Background(), recordingB)
	if !ok || unknown.ListenCount != nil || unknown.UniqueListeners != nil || unknown.CountsSource == nil {
		t.Fatal("missing aggregate counts must remain unknown", unknown)
	}
	n := reader.SimilarArtists(context.Background(), artistA, 5)
	if len(n) != 1 || n[0].RecordingMBIDs[0] != recordingB {
		t.Fatal(n)
	}
	n[0].RecordingMBIDs[0] = recordingA
	if reader.SimilarArtists(context.Background(), artistA, 5)[0].RecordingMBIDs[0] != recordingB {
		t.Fatal("neighbor mutation escaped")
	}
}

func TestWrongRadioShapeAndAmbiguousIdentityRejected(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `[{"recording_mbid":%q,"artist_mbids":[%q]}]`, recordingA, artistB)
	})
	if _, _, err := c.FetchTopRecordings(context.Background(), artistA); err == nil {
		t.Fatal("non-radio response accepted")
	}
	c = testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{%q:[{"recording_mbid":%q,"similar_artist_mbid":%q}]}`, artistA, recordingA, artistB)
	})
	if _, _, err := c.FetchNeighbors(context.Background(), artistA); err == nil {
		t.Fatal("mismatched radio group accepted")
	}
}

func TestRecordingBatchNullCountsAndIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		ids        []string
		wantErr    bool
	}{
		{"null", fmt.Sprintf(`[{"recording_mbid":%q,"total_user_count":null,"total_listen_count":null}]`, recordingA), []string{recordingA}, false},
		{"known", fmt.Sprintf(`[{"recording_mbid":%q,"total_user_count":3,"total_listen_count":5}]`, recordingA), []string{recordingA}, false},
		{"forged", fmt.Sprintf(`[{"recording_mbid":%q,"total_user_count":3}]`, recordingB), []string{recordingA}, true},
		{"duplicate", fmt.Sprintf(`[{"recording_mbid":%q},{"recording_mbid":%q}]`, recordingA, recordingA), []string{recordingA, recordingB}, true},
		{"missing", `[]`, []string{recordingA}, true},
		{"negative", fmt.Sprintf(`[{"recording_mbid":%q,"total_user_count":-1}]`, recordingA), []string{recordingA}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/1/popularity/recording" || r.Header.Get("Authorization") != "" {
					t.Error("request contract", r.URL)
				}
				var input struct {
					IDs []string `json:"recording_mbids"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil || !reflect.DeepEqual(input.IDs, tc.ids) {
					t.Error("request IDs", input, err)
				}
				fmt.Fprint(w, tc.body)
			})
			rows, source, err := c.FetchRecordingCounts(context.Background(), tc.ids)
			if (err != nil) != tc.wantErr {
				t.Fatal(rows, err)
			}
			if err == nil && (source.ResponseSHA256 != digest([]byte(tc.body)) || source.URL != apiBase+"/1/popularity/recording") {
				t.Fatal(source)
			}
			if tc.name == "null" && (rows[recordingA].UniqueListeners != nil || rows[recordingA].ListenCount != nil) {
				t.Fatal("null fabricated as zero")
			}
		})
	}
}

func TestPrepareRecordsOmittedRadioAndPreservesPopularity(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"unauthorized", "", http.StatusUnauthorized},
		{"unavailable", "", http.StatusServiceUnavailable},
		{"duplicate", fmt.Sprintf(`{%q:[{"recording_mbid":%q,"similar_artist_mbid":%q},{"recording_mbid":%q,"similar_artist_mbid":%q}]}`, artistA, recordingA, artistA, recordingA, artistA), 200},
		{"forged", fmt.Sprintf(`{%q:[{"recording_mbid":"bad-id","similar_artist_mbid":%q}]}`, artistA, artistA), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "" {
					t.Error("credentials sent")
				}
				switch r.URL.Path {
				case "/1/lb-radio/artist/" + artistA:
					w.WriteHeader(tc.status)
					fmt.Fprint(w, tc.body)
				case "/1/popularity/artist":
					fmt.Fprintf(w, `[{"artist_mbid":%q,"total_user_count":3,"total_listen_count":5}]`, artistA)
				default:
					t.Error("unexpected request", r.URL)
					w.WriteHeader(404)
				}
			})
			s, err := c.Prepare(context.Background(), []string{artistA})
			if err != nil || calls.Load() != 2 || len(s.Artists) != 1 || *s.Artists[0].UniqueListeners != 3 || len(s.Recordings) != 0 || len(s.OmittedDiscovery) != 1 {
				t.Fatal(s, err, calls.Load())
			}
			o := s.OmittedDiscovery[0]
			if o.ArtistMBID != artistA || (tc.status == 200 && (o.Reason != "invalid_response" || o.Source == nil || o.Source.ResponseSHA256 != digest([]byte(tc.body)))) || (tc.status != 200 && (o.Reason != "unavailable" || o.Source != nil)) {
				t.Fatal(o)
			}
			path := filepath.Join(t.TempDir(), "graph.json")
			hash, err := Write(context.Background(), path, s)
			if err != nil {
				t.Fatal(err)
			}
			r, err := Open(context.Background(), path, hash)
			if err != nil {
				t.Fatal(err)
			}
			if r.Info().OmittedDiscovery != 1 || len(r.Manifest().OmittedDiscovery) != 1 {
				t.Fatal(r.Info())
			}
			if o.Source != nil {
				m := r.Manifest()
				m.OmittedDiscovery[0].Source.ResponseSHA256 = "changed"
				if r.Manifest().OmittedDiscovery[0].Source.ResponseSHA256 == "changed" {
					t.Fatal("omission receipt aliases reader")
				}
			}
		})
	}
}

func TestPrepareRadioCancellationAndBadAggregateStillAbort(t *testing.T) {
	t.Run("parent cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var calls atomic.Int32
		c := testClient(t, func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); cancel(); fmt.Fprint(w, `{}`) })
		if _, err := c.Prepare(ctx, []string{artistA}); !errors.Is(err, context.Canceled) || calls.Load() != 1 {
			t.Fatal(err, calls.Load())
		}
	})
	t.Run("aggregate identity", func(t *testing.T) {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/1/lb-radio/") {
				w.WriteHeader(503)
				return
			}
			fmt.Fprintf(w, `[{"artist_mbid":%q}]`, artistB)
		})
		if _, err := c.Prepare(context.Background(), []string{artistA}); err == nil {
			t.Fatal("forged aggregate allowed partial publication")
		}
	})
}

func TestRecordingCountSourceMustMatchPublicEndpoint(t *testing.T) {
	s := fixture()
	source := fixtureSource(apiBase + "/1/popularity/recording")
	s.Recordings[0].CountsSource = &source
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	source.URL = apiBase + "/1/popularity/artist"
	if err := s.Validate(); err == nil {
		t.Fatal("artist aggregate laundered into recording counts")
	}
}
