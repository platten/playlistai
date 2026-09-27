package musicgraph

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestAuthenticatedTopCacheAndDisconnect(t *testing.T) {
	token := artistA
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.RawQuery != "" {
			t.Error("top lookup must not assume count support")
		}
		if r.Header.Get("Authorization") != "Token "+token {
			t.Error("missing credential")
		}
		fmt.Fprintf(w, `[{"recording_mbid":%q,"artist_mbids":[%q,%q],"total_listen_count":10,"total_user_count":3}]`, recordingA, artistA, artistB)
	})
	c.token = func() string { return token }
	for range 2 {
		rows, source, err := c.FetchTopRecordings(context.Background(), artistA)
		if err != nil || len(rows) != 1 || len(rows[0].ArtistMBIDs) != 2 || source.URL != topURL(artistA) {
			t.Fatal(rows, source, err)
		}
	}
	if calls != 1 {
		t.Fatal("cache missed", calls)
	}
	token = ""
	// A disconnected client must not use the authenticated endpoint or its cache.
	c.http.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" || !strings.Contains(r.URL.Path, "/lb-radio/") {
			t.Fatal("disconnection ignored")
		}
		return nil, fmt.Errorf("offline")
	})
	if _, _, err := c.FetchTopRecordings(context.Background(), artistA); err == nil {
		t.Fatal("unexpected success")
	}
}
func TestAuthenticatedFailureFallsBackWithoutCredential(t *testing.T) {
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if strings.Contains(r.URL.Path, "top-recordings") {
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("credential sent to public fallback")
		}
		fmt.Fprintf(w, `{%q:[{"recording_mbid":%q,"similar_artist_mbid":%q,"total_listen_count":1}]}`, artistA, recordingA, artistA)
	})
	c.token = func() string { return artistA }
	rows, _, err := c.FetchTopRecordings(context.Background(), artistA)
	if err != nil || len(rows) != 1 || calls != 2 {
		t.Fatal(rows, err, calls)
	}
}
func TestAuthenticatedTopRejectsConflictingIdentity(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `[{"recording_mbid":%q,"artist_mbids":[%q]}]`, recordingA, artistB)
	})
	c.token = func() string { return artistA }
	if _, _, err := c.fetchAuthenticatedTop(context.Background(), artistA); err == nil {
		t.Fatal("conflicting artist accepted")
	}
}
func TestValidateTokenDoesNotChangeConnection(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1/validate-token" || r.Header.Get("Authorization") != "Token "+artistB {
			t.Fatal("incorrect validation request")
		}
		fmt.Fprint(w, `{"valid":true}`)
	})
	c.token = func() string { return artistA }
	if err := c.ValidateToken(context.Background(), artistB); err != nil {
		t.Fatal(err)
	}
	if c.token() != artistA {
		t.Fatal("connection changed before validation commit")
	}
	if err := c.ValidateToken(context.Background(), "private-invalid-value"); err == nil || strings.Contains(err.Error(), "private-invalid-value") {
		t.Fatal("unsafe validation error", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.ValidateToken(ctx, artistB); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestRecordingMappingIsBoundedAuthenticatedAndCached(t *testing.T) {
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/1/metadata/lookup/" || r.Method != "POST" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Token "+artistA {
			t.Fatal("unsafe mapping request")
		}
		fmt.Fprintf(w, `[{"index":0,"artist_name_arg":"Artist","recording_name_arg":"Song","recording_mbid":%q,"artist_mbids":[%q],"recording_name":"Fuzzy Song","artist_credit_name":"Artist"}]`, recordingA, artistA)
	})
	c.token = func() string { return artistA }
	for range 2 {
		proposal, err := c.ProposeRecordingMapping(context.Background(), "Artist", "Song", "")
		if err != nil || proposal.RecordingMBID != recordingA || proposal.Title != "Fuzzy Song" {
			t.Fatal(proposal, err)
		}
	}
	if calls != 1 {
		t.Fatal("mapping cache missed")
	}
	c.token = func() string { return "" }
	if _, err := c.ProposeRecordingMapping(context.Background(), "Artist", "Song", ""); err == nil {
		t.Fatal("disconnected mapping lookup accepted")
	}
}
