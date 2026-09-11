package llama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/logging"
	"github.com/platten/playlistai/internal/ports"
)

func TestCompletePreservesNonStreamingContractAndOptInDiagnostics(t *testing.T) {
	requests := make(chan chatRequest, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("incorrect completion HTTP contract: %s %s %v", r.Method, r.URL.Path, r.Header)
		}
		var request chatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- request
		_, _ = io.WriteString(w, completion("Nuit électrique"))
	}))
	defer server.Close()
	client := NewClient(server.URL + "/")
	store := &logging.Store{}
	ctx := logging.WithDiagnostics(context.Background(), store)
	for _, enabled := range []bool{false, true} {
		store.SetDebug(enabled)
		got, err := client.Complete(ctx, "private system fixture", "private listener fixture", 0)
		if err != nil || got != "Nuit électrique" {
			t.Fatalf("completion changed text: %q %v", got, err)
		}
		request := <-requests
		if request.NPredict != 32 || request.Stream || request.CachePrompt || request.Grammar != "" || request.Temperature != .4 || request.ChatTemplateKwargs["enable_thinking"] != false {
			t.Fatalf("unbounded or streaming auxiliary request: %+v", request)
		}
		if !reflect.DeepEqual(request.Messages, []chatMessage{{Role: "system", Content: "private system fixture"}, {Role: "user", Content: "private listener fixture"}}) {
			t.Fatalf("auxiliary generation rewrote prompt roles: %+v", request.Messages)
		}
		entries := store.Read(0)
		if !enabled && len(entries) != 0 {
			t.Fatal("private completion retained without diagnostics consent")
		}
		if enabled && (len(entries) != 3 || !strings.Contains(entries[0].Text, "private listener fixture") || !strings.Contains(entries[2].Text, "Nuit électrique")) {
			t.Fatalf("opt-in completion diagnostics incomplete: %+v", entries)
		}
	}
	store.SetDebug(false)
	if len(store.Read(0)) != 0 {
		t.Fatal("opt-out retained private auxiliary generation")
	}
}

func TestCompleteErrorsRemainBoundedAndEmptyCompletionSupportsFallback(t *testing.T) {
	for _, tc := range []struct {
		name, body, wantError string
		status                int
		shortBody             bool
	}{
		{"status", strings.Repeat("x", 250) + "PRIVATE_TAIL", "HTTP 503", http.StatusServiceUnavailable, false},
		{"malformed JSON", "{", "bad response", http.StatusOK, false},
		{"read failure", "{", "response read", http.StatusOK, true},
		{"empty choices", `{"choices":[]}`, "", http.StatusOK, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				if tc.shortBody {
					w.Header().Set("Content-Length", "100")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			got, err := NewClient(server.URL).Complete(context.Background(), "system", "user", 24)
			if got != "" || calls.Load() != 1 {
				t.Fatalf("auxiliary failure retried or returned data: %q calls=%d", got, calls.Load())
			}
			if tc.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) || strings.Contains(err.Error(), "PRIVATE_TAIL") || len(err.Error()) > 300 {
				t.Fatalf("error missing or unbounded: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewClient("http://127.0.0.1:1").Complete(ctx, "", "", 5); !errors.Is(err, context.Canceled) {
		t.Fatalf("completion lost cancellation: %v", err)
	}
	client := NewClient("://invalid")
	if _, err := client.Complete(context.Background(), "", "", 5); err == nil || client.Healthy(context.Background()) {
		t.Fatal("malformed endpoint accepted")
	}
}

func TestWholeCompletionReportsOneFinalProgressValueAndFinishReason(t *testing.T) {
	var progress []int
	content := "mélodie"
	body := fmt.Sprintf(`{"choices":[{"message":{"content":%q},"finish_reason":"length"}]}`, content)
	got, err := readWholeCompletion(strings.NewReader(body), func(n int) { progress = append(progress, n) })
	if err != nil || got.Content != content || got.FinishReason != "length" || !reflect.DeepEqual(progress, []int{len(content)}) {
		t.Fatalf("buffered completion lost progress/truncation: %+v %v %v", got, progress, err)
	}
}

func TestAnchorProposalsPreserveInputAndBoundCandidateOnlyOutput(t *testing.T) {
	intent := core.MusicIntent{OriginalDescription: "ambient electronic, no repeated rejected tracks", EssentialCriteria: []core.MusicalCriterion{{Kind: "style", Value: "electronic", Scope: "playlist"}}, Preferences: core.SemanticPreferences{Moods: []core.IntentPreference{{Value: "calm"}}}, GenreExpansions: []core.GenreExpansion{{Genre: "electronic", Characteristics: "synthetic texture"}}}
	before, _ := json.Marshal(intent)
	requests := make(chan chatRequest, 1)
	server := chatServer(t, func(body []byte) (int, string) {
		var request chatRequest
		if err := json.Unmarshal(body, &request); err != nil {
			t.Error(err)
		}
		requests <- request
		return 200, completion(`[{"track":"Artist - Rejected","role":"old","reason":"skip"},{"track":"New Artist - Piece","role":"texture","reason":"retrieval only"},{"track":"new artist - piece","role":"duplicate","reason":"skip"}]`)
	})
	parser := NewWithClient(NewClient(server.URL))
	defer parser.Close()
	anchors, err := parser.ProposeAnchors(context.Background(), intent, []string{"artist - rejected"})
	if err != nil || len(anchors) != 1 {
		t.Fatalf("duplicates/rejections leaked: %+v %v", anchors, err)
	}
	anchor := anchors[0]
	if anchor.Reference.Kind != core.ReferenceTrack || anchor.Reference.Influence != core.InfluencePositive || anchor.Reference.Query != "New Artist - Piece" || anchor.Reference.TrackID != "" || anchor.Reference.Resolution != nil || anchor.Suitability.State != "" || anchor.Role != "texture" || anchor.Reason != "retrieval only" {
		t.Fatalf("retrieval proposal invented identity or verification: %+v", anchor)
	}
	request := <-requests
	if request.NPredict != 700 || request.Grammar != anchorGrammar || request.Stream || len(request.Messages) != 2 {
		t.Fatalf("proposal request lost bounded grammar: %+v", request)
	}
	var payload struct {
		Description     string
		Criteria        []core.MusicalCriterion
		Preferences     core.SemanticPreferences
		GenreExpansions []core.GenreExpansion
		Rejected        []string
	}
	if err := json.Unmarshal([]byte(request.Messages[1].Content), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Description != intent.OriginalDescription || !reflect.DeepEqual(payload.Criteria, intent.EssentialCriteria) || !reflect.DeepEqual(payload.Preferences, intent.Preferences) || !reflect.DeepEqual(payload.GenreExpansions, intent.GenreExpansions) || !reflect.DeepEqual(payload.Rejected, []string{"artist - rejected"}) {
		t.Fatalf("proposal request dropped original data: %+v", payload)
	}
	after, _ := json.Marshal(intent)
	if string(before) != string(after) {
		t.Fatal("anchor proposal mutated listener intent")
	}
}

func TestAnchorProposalFailuresDoNotLeakPartialCandidates(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		status        int
		wantError     bool
	}{
		{"too many", `[{},{},{},{}]`, 200, true},
		{"truncated", `[{"track":"Artist -`, 200, true},
		{"wrong shape", `{}`, 200, true},
		{"provider failure", "unavailable", 503, true},
		{"invalid entries", `[{"track":"Artist Only","role":"x","reason":"y"},{"track":"Artist - Song","role":"","reason":"y"},{"track":"Artist - Song","role":"x","reason":""}]`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := chatServer(t, func([]byte) (int, string) { calls.Add(1); return tc.status, completion(tc.content) })
			parser := NewWithClient(NewClient(server.URL))
			defer parser.Close()
			got, err := parser.ProposeAnchors(context.Background(), core.MusicIntent{}, nil)
			if (err != nil) != tc.wantError || len(got) != 0 || calls.Load() != 1 {
				t.Fatalf("invalid proposal response was retried or trusted: %+v calls=%d err=%v", got, calls.Load(), err)
			}
		})
	}
	for _, parser := range []*Parser{{}, NewWithClient(nil)} {
		if _, err := parser.ProposeAnchors(context.Background(), core.MusicIntent{}, nil); !errors.Is(err, core.ErrUnavailable) {
			t.Fatal(err)
		}
		if title := parser.Title(context.Background(), "fixture"); title != "" {
			t.Fatal("unavailable title did not fall back")
		}
		if parser.RuntimeMemoryBytes() != 0 {
			t.Fatal("client-only parser reported a managed process")
		}
	}
}

func TestTitleUsesSmallBudgetAndFallsBackAfterFailureOrClose(t *testing.T) {
	var calls atomic.Int32
	server := chatServer(t, func(body []byte) (int, string) {
		var request chatRequest
		if err := json.Unmarshal(body, &request); err != nil {
			t.Error(err)
		}
		if request.NPredict != 24 || request.Grammar != "" || len(request.Messages) != 2 || request.Messages[0].Content != titleSystemPrompt || request.Messages[1].Content != "quiet study" {
			t.Errorf("title contract: %+v", request)
		}
		if calls.Add(1) == 1 {
			return 200, completion("Quiet Horizons")
		}
		return 503, "unavailable"
	})
	parser := NewWithClient(NewClient(server.URL))
	parser.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	if got := parser.Title(context.Background(), "quiet study"); got != "Quiet Horizons" {
		t.Fatal(got)
	}
	if got := parser.Title(context.Background(), "quiet study"); got != "" {
		t.Fatal("failed title was returned", got)
	}
	if err := parser.Close(); err != nil {
		t.Fatal(err)
	}
	if parser.Info().Ready || parser.Title(context.Background(), "quiet study") != "" || calls.Load() != 2 {
		t.Fatal("closed parser still contacted server")
	}
	if _, err := parser.ProposeAnchors(context.Background(), core.MusicIntent{}, nil); !errors.Is(err, core.ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestParserProgressWrapperRetainsContextAndReturnsUnmanagedErrors(t *testing.T) {
	full := explicitArtistCompletion("Justice", 5)
	var calls atomic.Int32
	server := chatServer(t, func(body []byte) (int, string) {
		var request chatRequest
		if err := json.Unmarshal(body, &request); err != nil {
			t.Error(err)
		}
		last := request.Messages[len(request.Messages)-1].Content
		for _, text := range []string{"Justice", "now playing: Current — Song", "recent: Earlier — First; Earlier — Second"} {
			if !strings.Contains(last, text) {
				t.Errorf("request lost playback context %q: %q", text, last)
			}
		}
		if calls.Add(1) > 2 {
			return 503, "offline"
		}
		return 200, completion(full)
	})
	parser := NewWithClient(NewClient(server.URL))
	defer parser.Close()
	input := ports.IntentInput{Prompt: "Justice", NowPlaying: &core.TrackRef{Artist: "Current", Title: "Song"}, RecentTracks: []core.TrackRef{{Artist: "Earlier", Title: "First"}, {Artist: "Earlier", Title: "Second"}}}
	reports := 0
	progress := ports.ProgressFunc(func(op string, done, total int64, note string) {
		reports++
		if op != IntentProgressOp || done != int64(len(full)) || total != -1 || note != "understanding your request" {
			t.Errorf("incorrect parser progress: %s %d/%d %s", op, done, total, note)
		}
	})
	got, err := parser.ParseWithProgress(context.Background(), input, progress)
	if err != nil || got.Count != 5 || reports != 1 {
		t.Fatalf("progress parse failed: %+v reports=%d err=%v", got, reports, err)
	}
	if _, err := parser.ParseWithProgress(context.Background(), input, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := parser.Parse(context.Background(), input); err == nil || !strings.Contains(err.Error(), "HTTP 503") || calls.Load() != 3 {
		t.Fatalf("unmanaged failure did not propagate exactly once: %v calls=%d", err, calls.Load())
	}
}

func TestStreamingReaderKeepsFinishReasonAndRejectsOversizedFrames(t *testing.T) {
	stream := "event: message\ndata:\ndata: invalid\ndata: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\ndata: {\"choices\":[{\"finish_reason\":\"length\"}]}\n"
	got, err := readSSECompletion(strings.NewReader(stream), nil)
	if err != nil || got.Content != "ok" || got.FinishReason != "length" {
		t.Fatalf("stream lost buffered content/truncation: %+v %v", got, err)
	}
	if _, err := readSSECompletion(strings.NewReader("data: "+strings.Repeat("x", 1<<20)), nil); err == nil || !strings.Contains(err.Error(), "stream read") {
		t.Fatalf("oversized frame accepted: %v", err)
	}
	truncated := &TruncatedCompletionError{FinishReason: "length", Attempts: 2}
	if !strings.Contains(truncated.Error(), "2 bounded attempts") || !strings.Contains(truncated.Error(), `finish_reason="length"`) {
		t.Fatalf("truncation diagnostic lost recovery bounds: %v", truncated)
	}
}
