package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
)

func verificationTrack() core.EnrichedTrack {
	return core.EnrichedTrack{Ref: core.TrackRef{ID: "local", Artist: "Artist", Title: "Song"}, RecordingID: acousticTestID, IdentityStatus: core.ResolutionResolved, ArtistIDs: []string{contextArtistID}}
}

func TestVerifyRecordingAcquiresScopedFactsAndReusesCache(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/ws/2/recording/" + acousticTestID:
			_, _ = fmt.Fprintf(w, `{"id":%q,"title":"Song","first-release-date":"1971","artist-credit":[{"name":"Artist","artist":{"id":%q}}],"genres":[{"name":"jazz","count":3}],"tags":[{"name":"favorite","count":4}],"relations":[{"type":"vocal","artist":{"id":%q}},{"type":"instrument","attributes":["piano"],"artist":{"id":%q}},{"type":"instrument","ended":true,"attributes":["drums"],"artist":{"id":%q}},{"type":"wikidata","url":{"resource":"https://www.wikidata.org/wiki/Q123"}},{"type":"performance","work":{"id":%q}}]}`, acousticTestID, contextArtistID, contextArtistID, contextArtistID, contextArtistID, contextAlbumID)
		case "/wiki/Special:EntityData/Q123.json":
			_, _ = fmt.Fprintf(w, `{"entities":{"Q123":{"id":"Q123","lastrevid":42,"claims":{"P4404":[{"mainsnak":{"snaktype":"value","datavalue":{"value":%q}}}],"P136":[{"rank":"normal","mainsnak":{"snaktype":"value","datavalue":{"value":{"id":"Q456"}}},"references":[{"snaks":{"P854":[{"snaktype":"value","datavalue":{"value":"https://label.example/song"}}]}}]},{"rank":"normal","mainsnak":{"snaktype":"value","datavalue":{"value":{"id":"Q999"}}}}]}}}}`, acousticTestID)
		case "/wiki/Special:EntityData/Q456.json":
			_, _ = fmt.Fprint(w, `{"entities":{"Q456":{"id":"Q456","lastrevid":7,"labels":{"en":{"value":"ambient"}}}}}`)
		case "/ws/2/work/" + contextAlbumID:
			_, _ = fmt.Fprintf(w, `{"id":%q,"title":"Composition","relations":[{"type":"composer","artist":{"id":%q,"name":"Composer"}}]}`, contextAlbumID, contextArtistID)
		default:
			t.Errorf("unexpected lookup %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(Config{UserAgent: "fixture", MirrorURL: server.URL, WikidataURL: server.URL, Interval: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	criteria := []core.MusicalCriterion{{Kind: "composer", Value: "Composer"}}
	got, err := client.VerifyRecording(context.Background(), verificationTrack(), criteria)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Claims) != 8 {
		t.Fatalf("claims=%+v", got.Claims)
	}
	found := map[string]core.RecordingClaim{}
	for _, claim := range got.Claims {
		if claim.RecordingID != acousticTestID || claim.Source.URL == "" || claim.Source.Revision == "" || claim.Locator == "" || claim.RetrievedAt == "" {
			t.Fatalf("unattributed claim: %+v", claim)
		}
		found[claim.Kind+":"+claim.Value] = claim
	}
	if found["instrumentation:drums"].Method != "recording_credit" {
		t.Fatal("completed recording session lost its historical instrument credit")
	}
	if found["tag:favorite"].Method != "community_tag" || found["genre:jazz"].Source.License != "CC-BY-NC-SA-3.0" || found["vocal:vocal"].Method != "recording_credit" || found["instrumentation:piano"].Scope != "recording" || found["genre:ambient"].Source.Revision != "42" || found["composer:Composer"].Scope != "work" || found["composer:Composer"].EntityID != contextAlbumID {
		t.Fatalf("scope/provenance lost: %+v", found)
	}
	again, err := client.VerifyRecording(context.Background(), got, criteria)
	if err != nil || len(again.Claims) != len(got.Claims) || calls.Load() != 4 {
		t.Fatalf("repeat duplicated facts or requests: claims=%d calls=%d err=%v", len(again.Claims), calls.Load(), err)
	}
}

func TestVerifyRecordingRejectsUnresolvedAndMismatchedIdentity(t *testing.T) {
	for _, defect := range []string{"unresolved", "recording", "title", "artist"} {
		t.Run(defect, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				id, title, artist := acousticTestID, "Song", "Artist"
				switch defect {
				case "recording":
					id = contextAlbumID
				case "title":
					title = "Another Song"
				case "artist":
					artist = "Other Artist"
				}
				_, _ = fmt.Fprintf(w, `{"id":%q,"title":%q,"artist-credit":[{"name":%q}],"genres":[{"name":"jazz","count":3}]}`, id, title, artist)
			}))
			defer server.Close()
			client := newClient(t, server.URL, time.Nanosecond)
			track := verificationTrack()
			if defect == "unresolved" {
				track.IdentityStatus = core.ResolutionAmbiguous
			}
			got, err := client.VerifyRecording(context.Background(), track, nil)
			if len(got.Claims) != 0 || defect != "unresolved" && err == nil || defect == "unresolved" && calls.Load() != 0 {
				t.Fatalf("unsafe identity: %+v err=%v calls=%d", got, err, calls.Load())
			}
		})
	}
}

func TestWikidataRecordingClaimsRequireReciprocalIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/ws/2/") {
			_, _ = fmt.Fprintf(w, `{"id":%q,"title":"Song","artist-credit":[{"name":"Artist"}],"relations":[{"type":"wikidata","url":{"resource":"https://www.wikidata.org/wiki/Q123"}}]}`, acousticTestID)
			return
		}
		_, _ = fmt.Fprintf(w, `{"entities":{"Q123":{"id":"Q123","lastrevid":42,"claims":{"P4404":[{"mainsnak":{"snaktype":"value","datavalue":{"value":%q}}}]}}}}`, contextAlbumID)
	}))
	defer server.Close()
	client, err := New(Config{UserAgent: "fixture", MirrorURL: server.URL, WikidataURL: server.URL, Interval: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	got, err := client.VerifyRecording(context.Background(), verificationTrack(), nil)
	if err != nil || len(got.Claims) != 0 {
		t.Fatalf("conflicting reciprocal identity: %+v %v", got, err)
	}
}

func TestSourcedWikidataStatementsExcludeImportsAndQualifiers(t *testing.T) {
	for _, tc := range []struct {
		name, extra string
		want        bool
	}{
		{"reference URL", `,"references":[{"snaks":{"P854":[{"snaktype":"value","datavalue":{"value":"https://label.example/song"}}]}}]`, true},
		{"stated in", `,"references":[{"snaks":{"P248":[{"snaktype":"value","datavalue":{"value":{"id":"Q123"}}}]}}]`, true},
		{"import only", `,"references":[{"snaks":{"P143":[{"snaktype":"value","datavalue":{"value":{"id":"Q328"}}}]}}]`, false},
		{"qualified", `,"qualifiers":{"P518":[]},"references":[{"snaks":{"P248":[{"snaktype":"value","datavalue":{"value":{"id":"Q123"}}}]}}]`, false},
		{"missing source", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var statement wikidataStatement
			if err := json.Unmarshal([]byte(`{"rank":"normal","mainsnak":{"snaktype":"value","datavalue":{"value":{"id":"Q456"}}}`+tc.extra+`}`), &statement); err != nil {
				t.Fatal(err)
			}
			if got := sourcedStatement(statement); got != tc.want {
				t.Fatalf("sourced=%v want=%v", got, tc.want)
			}
			statement.Rank = "deprecated"
			if sourcedStatement(statement) {
				t.Fatal("deprecated claim accepted")
			}
		})
	}
}

func TestVerifyRecordingKeepsCompletedClaimsWhenOptionalBudgetExhausts(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/ws/2/recording/"+acousticTestID {
			t.Errorf("optional lookup exceeded inherited request budget: %s", r.URL)
		}
		_, _ = fmt.Fprintf(w, `{"id":%q,"title":"Song","artist-credit":[{"name":"Artist"}],"relations":[{"type":"vocal","artist":{"id":%q}},{"type":"performance","work":{"id":%q}}]}`, acousticTestID, contextArtistID, contextAlbumID)
	}))
	defer server.Close()
	client := newClient(t, server.URL, time.Nanosecond)
	ctx := context.WithValue(context.Background(), contextRequestLimitKey{}, 1)
	got, err := client.VerifyRecording(ctx, verificationTrack(), []core.MusicalCriterion{{Kind: "composer", Value: "Composer"}})
	if err != nil || len(got.Claims) != 1 || got.Claims[0].Kind != "vocal" || calls.Load() != 1 {
		t.Fatalf("optional failure lost completed evidence or exceeded budget: %+v err=%v requests=%d", got.Claims, err, calls.Load())
	}
}

func TestVerifyRecordingKeepsCompletedClaimsButPropagatesParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws/2/work/"+contextAlbumID {
			cancel()
			<-r.Context().Done()
			return
		}
		_, _ = fmt.Fprintf(w, `{"id":%q,"title":"Song","artist-credit":[{"name":"Artist"}],"relations":[{"type":"vocal","artist":{"id":%q}},{"type":"performance","work":{"id":%q}}]}`, acousticTestID, contextArtistID, contextAlbumID)
	}))
	defer server.Close()
	client := newClient(t, server.URL, time.Nanosecond)
	got, err := client.VerifyRecording(ctx, verificationTrack(), []core.MusicalCriterion{{Kind: "composer", Value: "Composer"}})
	if err != context.Canceled || len(got.Claims) != 1 || got.Claims[0].Kind != "vocal" {
		t.Fatalf("parent cancellation lost facts or was swallowed: %+v err=%v", got.Claims, err)
	}
}

func TestVerifyRecordingPreservesPerformerAndEngineerRoles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"id":%q,"title":"Song","artist-credit":[{"name":"Artist","artist":{"id":%q}}],"relations":[{"type":"instrument","attributes":["piano"],"artist":{"id":%q,"name":"Pianist"}},{"type":"recording","artist":{"id":%q,"name":"Engineer"}},{"type":"performing orchestra","artist":{"id":%q,"name":"Orchestra"}},{"type":"conductor","artist":{"id":%q,"name":"Conductor"}},{"type":"producer","artist":{"id":%q,"name":"Producer"}},{"type":"performer","artist":{"id":"invalid","name":"Unresolved performer"}}]}`, acousticTestID, contextArtistID, contextArtistID, contextAlbumID, contextArtistID, contextArtistID, contextArtistID)
	}))
	defer server.Close()
	client := newClient(t, server.URL, time.Nanosecond)
	track := verificationTrack()
	// The existing catalog display stays intact; no separator implies a role.
	track.Ref.Artist = "Pianist;Engineer"
	got, err := client.VerifyRecording(context.Background(), track, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Ref != track.Ref {
		t.Fatalf("provider rewrote local identity: %+v", got.Ref)
	}
	roles := map[string]string{}
	for _, claim := range got.Claims {
		if claim.Kind == "performer" || claim.Kind == "engineer" {
			if claim.Method != "recording_credit" || claim.Scope != "recording" || claim.RecordingID != track.RecordingID || !contextMBID.MatchString(claim.ArtistID) || claim.Source.Revision == "" || claim.Locator == "" {
				t.Fatalf("unattributed role: %+v", claim)
			}
			roles[claim.Value] = claim.Kind
		}
	}
	if len(roles) != 4 || roles["Pianist"] != "performer" || roles["Orchestra"] != "performer" || roles["Conductor"] != "performer" || roles["Engineer"] != "engineer" {
		t.Fatalf("performing and technical roles conflated: %+v", roles)
	}
}

func TestRecordingDurationContradiction(t *testing.T) {
	for _, tc := range []struct {
		name   string
		local  int64
		remote any
		source string
		want   bool
	}{
		{"sameMBIDdifferentVersion", 576093, int64(380573), "local:stream", true},
		{"sameMBIDnearDuration", 576093, int64(574773), "local:container", false},
		{"missing", 576093, nil, "local:stream", false},
		{"invalid", 576093, int64(-1), "local:stream", false},
		{"preview", 30000, int64(380573), "preview", false}, {"mixed preview", 30000, int64(380573), "local:PREVIEW", false},
		{"missingLocal", 0, int64(380573), "local:stream", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"id": acousticTestID, "title": "Song", "length": tc.remote, "artist-credit": []map[string]any{{"name": "Artist"}}, "genres": []map[string]any{{"name": "jazz", "count": 1}}})
			}))
			defer server.Close()
			c := newClient(t, server.URL, time.Nanosecond)
			defer c.Close()
			track := verificationTrack()
			track.Matched = true
			track.Claims = []core.RecordingClaim{{Kind: "genre", Value: "stale", State: core.EvidenceMatch}}
			track.FullRecordingDuration = &core.RecordingDuration{Milliseconds: tc.local, Source: tc.source, RecordingID: track.RecordingID}
			got, err := c.VerifyRecording(context.Background(), track, nil)
			if err != nil {
				t.Fatal(err)
			}
			if (got.IdentityStatus == core.ResolutionAmbiguous) != tc.want {
				t.Fatalf("status=%s expectedConflict=%v", got.IdentityStatus, tc.want)
			}
			if got.RecordingID != track.RecordingID || got.Ref != track.Ref {
				t.Fatal("identity retagged")
			}
			if tc.want && (got.Matched || len(got.Claims) != 0) {
				t.Fatal("conflict retained facts")
			}
			// The same cached raw document must be assessed against the current local duration.
			track.FullRecordingDuration.Milliseconds = 380573
			got, err = c.VerifyRecording(context.Background(), track, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "sameMBIDdifferentVersion" && got.IdentityStatus != core.ResolutionResolved {
				t.Fatal("cached contradiction became permanent")
			}
		})
	}
}

func TestDurationBinding(t *testing.T) {
	track := verificationTrack()
	track.Ref.RecordingIdentity = "musicbrainz:" + track.RecordingID
	for _, id := range []string{track.Ref.ID, track.RecordingID, track.Ref.RecordingIdentity, strings.ToUpper(track.Ref.RecordingIdentity)} {
		if !recordingDurationBelongsTo(track, id) {
			t.Fatal(id)
		}
	}
	if recordingDurationBelongsTo(track, "another") {
		t.Fatal("unrelated duration accepted")
	}
}

func TestRecordingISRCConflictIsNotProviderFailure(t *testing.T) {
	for _, kind := range []string{"conflict", "foreignDocument", "providerFailure", "missing", "invalid", "overlap"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "providerFailure" {
					http.Error(w, "unavailable", http.StatusBadRequest)
					return
				}
				id := acousticTestID
				if kind == "foreignDocument" {
					id = contextAlbumID
				}
				isrcs := []string{"USAAA0100002"}
				if kind == "missing" {
					isrcs = nil
				}
				if kind == "invalid" {
					isrcs = []string{"bad"}
				}
				if kind == "overlap" {
					isrcs = append(isrcs, "USAAA0100001")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "title": "Song", "isrcs": isrcs, "artist-credit": []map[string]any{{"name": "Artist"}}})
			}))
			defer server.Close()
			c := newClient(t, server.URL, time.Nanosecond)
			defer c.Close()
			track := verificationTrack()
			track.Matched = true
			track.ISRC = "USAAA0100001"
			track.Claims = []core.RecordingClaim{{Kind: "genre", Value: "stale"}}
			got, err := c.VerifyRecording(context.Background(), track, nil)
			if got.Ref != track.Ref || got.RecordingID != track.RecordingID {
				t.Fatal("identity retagged")
			}
			if kind == "conflict" {
				if err != nil || got.IdentityStatus != core.ResolutionAmbiguous || got.Matched || len(got.Claims) != 0 {
					t.Fatalf("conflict=%+v err=%v", got, err)
				}
				return
			}
			if got.IdentityStatus != core.ResolutionResolved {
				t.Fatal("unrelated or unknown response marked catalog ambiguous")
			}
			if (kind == "foreignDocument" || kind == "providerFailure") != (err != nil) {
				t.Fatalf("kind=%s err=%v", kind, err)
			}
		})
	}
}
