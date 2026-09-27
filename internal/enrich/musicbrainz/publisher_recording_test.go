package musicbrainz

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/platten/playlistai/internal/core"
)

const publisherTestURL = "https://music.apple.com/gb/song/1589310682"

func publisherFixture() (core.EnrichedTrack, recordingDocument, publisherResponse) {
	length := int64(378840)
	track := verificationTrack()
	track.RecordingID = "464dd116-69ef-43db-954a-f7398317f45b"
	track.ArtistIDs = []string{"cb67438a-7f50-4f2b-a6f1-2bb2729fd538"}
	track.Ref.Artist, track.Ref.Title = "Air", "Don’t Be Light"
	track.ISRC = "FRZ042101046"
	doc := recordingDocument{mbRecording: mbRecording{ID: track.RecordingID, Title: track.Ref.Title, Length: &length, ISRCs: []string{track.ISRC}}}
	var credit mbArtistCredit
	credit.Name, credit.Artist.Name, credit.Artist.ID = "Air", "Air", track.ArtistIDs[0]
	doc.ArtistCredit = []mbArtistCredit{credit}
	var relation recordingRelation
	relation.Type, relation.Direction, relation.TargetType = "streaming", "forward", "url"
	relation.URL.Resource = publisherTestURL
	doc.Relations = []recordingRelation{relation}
	response := publisherResponse{Count: 1, Results: []publisherSong{{WrapperType: "track", Kind: "song", TrackID: 1589310682, Artist: "Air", Title: "Don't Be Light (2021 Remastered)", Duration: 378291, Genre: "Electronic"}}}
	return track, doc, response
}

func publisherHTTPResponse(request *http.Request, status int, value any) *http.Response {
	raw, _ := json.Marshal(value)
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw))), Request: request}
}

func publisherTestClient(t *testing.T, doc recordingDocument, response publisherResponse) (*Client, *int, *int) {
	t.Helper()
	c, err := New(Config{UserAgent: "fixture", MirrorURL: "http://127.0.0.1:1", Interval: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	c.limiter = &requestLimiter{gate: make(chan struct{}, 1)}
	mbCalls, appleCalls := new(int), new(int)
	c.hc.Transport.(*limitedTransport).base = transportFunc(func(r *http.Request) (*http.Response, error) {
		*mbCalls++
		if r.URL.Path != "/ws/2/recording/"+doc.ID {
			t.Errorf("unexpected MB request: %s", r.URL)
		}
		return publisherHTTPResponse(r, http.StatusOK, doc), nil
	})
	transport := c.publisherClient.Transport.(*limitedTransport)
	transport.client = &Client{interval: time.Nanosecond, limiter: &requestLimiter{gate: make(chan struct{}, 1)}}
	transport.base = transportFunc(func(r *http.Request) (*http.Response, error) {
		*appleCalls++
		if r.Method != http.MethodGet || r.URL.String() != publisherBase+"/lookup?country=gb&id=1589310682" || r.Header.Get("User-Agent") == "" {
			t.Errorf("unexpected publisher request: %s %s", r.Method, r.URL)
		}
		return publisherHTTPResponse(r, http.StatusOK, response), nil
	})
	return c, mbCalls, appleCalls
}

func publisherGenreClaims(track core.EnrichedTrack) []core.RecordingClaim {
	var claims []core.RecordingClaim
	for _, claim := range track.Claims {
		if claim.Method == "publisher_field" {
			claims = append(claims, claim)
		}
	}
	return claims
}

func TestPublisherRecordingExactLinkedSongAndFrozenLineage(t *testing.T) {
	track, doc, response := publisherFixture()
	c, mbCalls, appleCalls := publisherTestClient(t, doc, response)
	got, err := c.VerifyRecording(context.Background(), track, nil)
	if err != nil || len(got.Claims) != 2 || *mbCalls != 1 || *appleCalls != 1 {
		t.Fatalf("linked song: %+v err=%v calls=%d/%d", got.Claims, err, *mbCalls, *appleCalls)
	}
	identity, genre := got.Claims[0], got.Claims[1]
	if identity.Kind != "recording_identity" || identity.Method != "publisher_song_link" || identity.Value != genre.Source.URL || identity.Source.Provider != "musicbrainz" || identity.Source.License != "CC0-1.0" || identity.Locator != "relations[0].url.resource: "+publisherTestURL {
		t.Fatalf("missing relationship lineage: %+v", identity)
	}
	if genre.Kind != "genre" || genre.Value != "Electronic" || genre.Method != "publisher_field" || genre.Source.Provider != "apple" || genre.Source.License != publisherTerms || genre.Locator != "results[0].primaryGenreName" || genre.Source.URL != publisherBase+"/lookup?country=gb&id=1589310682" {
		t.Fatalf("publisher field lost scope: %+v", genre)
	}
	for _, claim := range got.Claims {
		if claim.RecordingID != track.RecordingID || claim.EntityID != track.RecordingID || claim.Scope != "recording" || claim.State != core.EvidenceMatch || len(claim.Source.Revision) != 64 || claim.RetrievedAt == "" || claim.ExtractorVersion != recordingExtractorVersion {
			t.Fatalf("incomplete frozen attribution: %+v", claim)
		}
	}
	if got.Ref != track.Ref || got.ISRC != track.ISRC || got.FullRecordingDuration != nil {
		t.Fatal("publisher rewrote catalog identity or claimed exact master duration")
	}
	// A repeat uses both exact response caches even with no new request budget.
	ctx := context.WithValue(context.Background(), contextRequestLimitKey{}, 0)
	again, err := c.VerifyRecording(ctx, got, nil)
	firstJSON, _ := json.Marshal(got.Claims)
	secondJSON, _ := json.Marshal(again.Claims)
	if err != nil || string(firstJSON) != string(secondJSON) || *mbCalls != 1 || *appleCalls != 1 {
		t.Fatalf("cache replay changed claims or dispatched: %s %v %d/%d", secondJSON, err, *mbCalls, *appleCalls)
	}
}

func TestPublisherSongURLIsNarrowAndNeverInfersAlbumOrArtist(t *testing.T) {
	for _, raw := range []string{
		"http://music.apple.com/gb/song/1589310682", "https://music.apple.com:443/gb/song/1589310682", "https://music.apple.com.evil/gb/song/1589310682",
		"https://user@music.apple.com/gb/song/1589310682", publisherTestURL + "?i=123", publisherTestURL + "?", publisherTestURL + "#x", publisherTestURL + "/",
		"https://music.apple.com/gb/song/dont-be-light/1589310682", "https://music.apple.com/gb/album/1589310682?i=1589310682", "https://music.apple.com/gb/artist/1589310682",
		"https://music.apple.com/gb/song/01589310682", "https://music.apple.com/gb/song/+1589310682", "https://music.apple.com/gb/song/0", "https://music.apple.com/gb/song/-1",
		"https://music.apple.com/gb/song/999999999999999999999", "https://music.apple.com/gb/song/%31", "https://music.apple.com/GB/song/1589310682", "https://music.apple.com/g1/song/1589310682",
	} {
		if path, ok := publisherLookupPath(raw); ok {
			t.Errorf("unsafe/non-song URL accepted: %s -> %s", raw, path)
		}
	}
	if path, ok := publisherLookupPath(publisherTestURL); !ok || path != "/lookup?country=gb&id=1589310682" {
		t.Fatalf("canonical song rejected: %q %v", path, ok)
	}
}

func TestPublisherRecordingRejectsIdentityAndVersionConflicts(t *testing.T) {
	for _, defect := range []string{"other artist", "multiple artists", "no artist id", "other title", "live", "remix", "edit", "combined remaster remix", "MB hidden live", "duration", "missing duration", "local duration", "track isrc", "MB isrc", "no genre", "oversized genre"} {
		t.Run(defect, func(t *testing.T) {
			track, doc, response := publisherFixture()
			song := &response.Results[0]
			switch defect {
			case "other artist":
				song.Artist = "Another Air"
			case "multiple artists":
				doc.ArtistCredit = append(doc.ArtistCredit, doc.ArtistCredit[0])
			case "no artist id":
				doc.ArtistCredit[0].Artist.ID = ""
			case "other title":
				song.Title = "Another Song"
			case "live", "remix", "edit":
				song.Title = "Don't Be Light (" + defect + ")"
			case "combined remaster remix":
				song.Title = "Don't Be Light (2021 Remastered Remix)"
			case "MB hidden live":
				doc.Disambiguation = "live, 2001 Paris"
			case "duration":
				song.Duration = 300000
			case "missing duration":
				doc.Length = nil
			case "local duration":
				track.FullRecordingDuration = &core.RecordingDuration{Milliseconds: 300000, Source: "catalog", RecordingID: track.RecordingID}
			case "track isrc":
				song.ISRC = "USABC2400001"
			case "MB isrc":
				track.ISRC = ""
				song.ISRC = "USABC2400001"
			case "no genre":
				song.Genre = " "
			case "oversized genre":
				song.Genre = strings.Repeat("a", 121)
			}
			c, _, appleCalls := publisherTestClient(t, doc, response)
			got, err := c.VerifyRecording(context.Background(), track, nil)
			if defect == "local duration" {
				if err != nil || got.Ref != track.Ref || got.RecordingID != track.RecordingID || got.IdentityStatus != core.ResolutionAmbiguous || got.Matched || len(got.Claims) != 0 || *appleCalls != 0 {
					t.Fatalf("duration conflict was not quarantined before publisher lookup: %+v err=%v calls=%d", got, err, *appleCalls)
				}
				return
			}
			if err != nil || len(publisherGenreClaims(got)) != 0 || *appleCalls != 1 {
				t.Fatalf("unsafe publisher match: %+v err=%v calls=%d", got.Claims, err, *appleCalls)
			}
		})
	}
	for _, title := range []string{"Don't Be Light", "Don’t Be Light (Remastered)", "Don't Be Light [2021 Remaster]", "Don't Be Light - Remastered 2021"} {
		track, doc, response := publisherFixture()
		response.Results[0].Title, response.Results[0].ISRC = title, "invalid-is-unknown"
		if !publisherSongMatches(track, doc, response.Results[0]) {
			t.Errorf("compatible recording title rejected: %q", title)
		}
	}
}

func TestPublisherRecordingNeverUsesIneligibleRelationship(t *testing.T) {
	for _, defect := range []string{"ended", "backward", "artist target", "unrelated type", "album"} {
		t.Run(defect, func(t *testing.T) {
			track, doc, response := publisherFixture()
			switch defect {
			case "ended":
				doc.Relations[0].Ended = true
			case "backward":
				doc.Relations[0].Direction = "backward"
			case "artist target":
				doc.Relations[0].TargetType = "artist"
			case "unrelated type":
				doc.Relations[0].Type = "discography entry"
			case "album":
				doc.Relations[0].URL.Resource = "https://music.apple.com/gb/album/1589310682"
			}
			c, _, appleCalls := publisherTestClient(t, doc, response)
			got, err := c.VerifyRecording(context.Background(), track, nil)
			if err != nil || len(got.Claims) != 0 || *appleCalls != 0 {
				t.Fatalf("unexpected publisher request: %+v %v calls=%d", got.Claims, err, *appleCalls)
			}
		})
	}
}

func TestPublisherRecordingNaturalTitleWordsDoNotEstablishVersion(t *testing.T) {
	for _, version := range []string{"live", "remix", "edit", "acoustic", "demo", "instrumental", "karaoke"} {
		for _, qualified := range []bool{false, true} {
			t.Run(version+map[bool]string{false: " natural title", true: " explicit qualifier"}[qualified], func(t *testing.T) {
				track, doc, response := publisherFixture()
				title := version + " Forever"
				if qualified {
					title += " (" + version + ")"
				}
				track.Ref.Title, doc.Title, response.Results[0].Title = title, title, title
				doc.Disambiguation = version + ", 2001 Paris"
				c, _, appleCalls := publisherTestClient(t, doc, response)
				got, err := c.VerifyRecording(context.Background(), track, nil)
				if err != nil || (len(publisherGenreClaims(got)) == 1) != qualified || *appleCalls != 1 {
					t.Fatalf("version qualifier=%v: claims=%+v err=%v calls=%d", qualified, got.Claims, err, *appleCalls)
				}
			})
		}
	}
}

func TestPublisherRecordingRejectsBadEnvelopesAndDoesNotFanOut(t *testing.T) {
	for _, defect := range []string{"wrong id", "album result", "video", "ambiguous", "wrong count", "unrelated json", "provider error", "error with result", "missing count", "empty", "redirect", "404"} {
		t.Run(defect, func(t *testing.T) {
			track, doc, response := publisherFixture()
			doc.Relations = append(doc.Relations, doc.Relations[0])
			doc.Relations[1].URL.Resource = "https://music.apple.com/us/song/12345"
			c, _, appleCalls := publisherTestClient(t, doc, response)
			c.publisherClient.Transport.(*limitedTransport).base = transportFunc(func(r *http.Request) (*http.Response, error) {
				*appleCalls++
				status := http.StatusOK
				var value any
				switch defect {
				case "wrong id":
					response.Results[0].TrackID++
				case "album result":
					response.Results[0].WrapperType = "collection"
				case "video":
					response.Results[0].Kind = "music-video"
				case "ambiguous":
					response.Results = append(response.Results, response.Results[0])
					response.Count = 2
				case "wrong count":
					response.Count = 0
				case "unrelated json":
					value = map[string]any{"results": nil}
				case "provider error":
					value = map[string]any{"error": "outage"}
				case "error with result":
					value = map[string]any{"error": "outage", "resultCount": 1, "results": response.Results}
				case "missing count":
					value = map[string]any{"results": []publisherSong{}}
				case "empty":
					response.Results, response.Count = []publisherSong{}, 0
				case "redirect":
					status = http.StatusFound
				case "404":
					status = http.StatusNotFound
				}
				if value == nil {
					value = response
				}
				resp := publisherHTTPResponse(r, status, value)
				resp.Header.Set("Location", "https://example.invalid/should-not-follow")
				return resp, nil
			})
			got, err := c.VerifyRecording(context.Background(), track, nil)
			if err != nil || len(got.Claims) != 0 || *appleCalls != 1 {
				t.Fatalf("unsafe lookup fallback: %+v err=%v calls=%d", got.Claims, err, *appleCalls)
			}
			path, _ := publisherLookupPath(publisherTestURL)
			cached := c.readCache(context.Background(), metadataKey(publisherBase, path, publisherNamespace))
			if (cached.body != "") != (defect == "empty") {
				t.Fatalf("invalid envelope cached or empty miss lost: %s", cached.body)
			}
		})
	}
}

func TestPublisherRecordingBudgetCancellationAndHistoricalCache(t *testing.T) {
	track, doc, response := publisherFixture()
	doc.Genres = []mbTag{{Name: "electronic", Count: 1}}
	c, mbCalls, appleCalls := publisherTestClient(t, doc, response)
	ctx := context.WithValue(context.Background(), contextRequestLimitKey{}, 1)
	got, err := c.VerifyRecording(ctx, track, nil)
	if err != nil || len(got.Claims) != 1 || *mbCalls != 1 || *appleCalls != 0 {
		t.Fatalf("exhausted budget discarded MB or dispatched: %+v %v %d/%d", got.Claims, err, *mbCalls, *appleCalls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.VerifyRecording(ctx, track, nil); !errors.Is(err, context.Canceled) || *appleCalls != 0 {
		t.Fatalf("cancellation dispatched: %v %d", err, *appleCalls)
	}
	got, err = c.VerifyRecording(context.Background(), track, nil)
	if err != nil || len(publisherGenreClaims(got)) != 1 || *appleCalls != 1 {
		t.Fatalf("initial cache population failed: %+v %v", got.Claims, err)
	}
	path, _ := publisherLookupPath(publisherTestURL)
	key := metadataKey(publisherBase, path, publisherNamespace)
	c.dbMu.Lock()
	entry := c.memory[key]
	entry.fetched = time.Now().Add(-48 * time.Hour).Unix()
	c.memory[key] = entry
	c.dbMu.Unlock()
	// At48h a budget-exhausted lookup may reuse the existing historical body,
	// but its persisted timestamp must remain old. This does not claim freshness.
	ctx = context.WithValue(context.Background(), contextRequestLimitKey{}, 0)
	got, err = c.VerifyRecording(ctx, track, nil)
	claims := publisherGenreClaims(got)
	if err != nil || len(claims) != 1 || claims[0].RetrievedAt != time.Unix(entry.fetched, 0).UTC().Format(time.RFC3339) || *appleCalls != 1 {
		t.Fatalf("stale attribution lost or request escaped budget: %+v %v %d", got.Claims, err, *appleCalls)
	}
	// With a budget, the source-specific24h TTL refreshes this48h body.
	if _, err := c.VerifyRecording(context.Background(), track, nil); err != nil || *appleCalls != 2 {
		t.Fatalf("publisher TTL was not applied: %v %d", err, *appleCalls)
	}
}

func TestPublisherRecordingCancellationDuringRequestRetainsMBFacts(t *testing.T) {
	track, doc, response := publisherFixture()
	doc.Genres = []mbTag{{Name: "electronic", Count: 1}}
	c, _, appleCalls := publisherTestClient(t, doc, response)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.publisherClient.Transport.(*limitedTransport).base = transportFunc(func(r *http.Request) (*http.Response, error) {
		*appleCalls++
		cancel()
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	got, err := c.VerifyRecording(ctx, track, nil)
	if !errors.Is(err, context.Canceled) || len(got.Claims) != 1 || got.Claims[0].Method != "community_tag" || *appleCalls != 1 {
		t.Fatalf("cancellation lost earlier facts: %+v %v calls=%d", got.Claims, err, *appleCalls)
	}
}

func TestPublisherThrottleIsSharedAndCancellable(t *testing.T) {
	first, second := newPublisherClient(), newPublisherClient()
	one, two := first.Transport.(*limitedTransport), second.Transport.(*limitedTransport)
	if one.client.interval != 3*time.Second || one.client.limiter != two.client.limiter || second.Timeout != 8*time.Second {
		t.Fatal("Apple dispatch throttle is not shared or bounded")
	}
	synctest.Test(t, func(t *testing.T) {
		limiter := &requestLimiter{gate: make(chan struct{}, 1)}
		var starts []time.Time
		for _, transport := range []*limitedTransport{one, two} {
			transport.client = &Client{interval: 3 * time.Second, limiter: limiter}
			transport.base = transportFunc(func(r *http.Request) (*http.Response, error) {
				starts = append(starts, time.Now())
				return publisherHTTPResponse(r, http.StatusOK, map[string]any{}), nil
			})
		}
		for _, client := range []*http.Client{first, second} {
			resp, err := client.Get(publisherBase + "/lookup?country=gb&id=1")
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
		}
		if len(starts) != 2 || starts[1].Sub(starts[0]) < 3*time.Second {
			t.Fatalf("requests exceed20/min spacing: %v", starts)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, publisherBase+"/lookup?country=gb&id=2", nil)
		if _, err := first.Do(req); !errors.Is(err, context.DeadlineExceeded) || len(starts) != 2 {
			t.Fatalf("throttle ignored cancellation: %v dispatches=%d", err, len(starts))
		}
	})
}

func TestPublisherRetryCannotExceedOneAdditionalDispatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		track, doc, response := publisherFixture()
		c, _, appleCalls := publisherTestClient(t, doc, response)
		c.publisherClient.Transport.(*limitedTransport).base = transportFunc(func(r *http.Request) (*http.Response, error) {
			*appleCalls++
			return publisherHTTPResponse(r, http.StatusServiceUnavailable, map[string]any{"error": "try later"}), nil
		})
		got, err := c.VerifyRecording(context.Background(), track, nil)
		if err != nil || len(got.Claims) != 0 || *appleCalls != 1 {
			t.Fatalf("retry escaped dispatch cap: claims=%+v err=%v calls=%d", got.Claims, err, *appleCalls)
		}
	})
}
