package llama

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/ports"
)

func TestMeasuredBudgetCapsRepairToRuntimeContext(t *testing.T) {
	var sawTemplate, sawTokens bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apply-template":
			sawTemplate = true
			var body struct {
				Messages []chatMessage `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body.Messages) == 0 || !strings.Contains(body.Messages[len(body.Messages)-1].Content, "Protected source facts") {
				t.Error("budget omitted protected fact message")
			}
			_, _ = w.Write([]byte(`{"prompt":"native template including special tokens"}`))
		case "/tokenize":
			sawTokens = true
			var body struct {
				Content string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Content != "native template including special tokens" {
				t.Error("tokenized raw messages instead of applied template")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tokens": make([]int, 2300)})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := NewClientWithContext(srv.URL, 4096)
	got, err := c.outputBudget(context.Background(), buildMessages(ports.IntentInput{Prompt: "15 classical pieces"}), 2400)
	if err != nil || got != 1668 || !sawTemplate || !sawTokens {
		t.Fatalf("budget=%d err=%v measured=%v/%v", got, err, sawTemplate, sawTokens)
	}
}

func TestBudgetWithoutTokenizerDoesNotUndercountNonLatinText(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	c := NewClientWithContext(srv.URL, 4096)
	if _, err := c.outputBudget(context.Background(), []chatMessage{{Role: "user", Content: strings.Repeat("音楽", 1000)}}, 1800); err == nil {
		t.Fatal("unsafe English-only token estimate admitted oversized non-Latin request")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.outputBudget(ctx, []chatMessage{{Role: "user", Content: "music"}}, 1800); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestAuxiliaryCompletionUsesSameMeasuredContextCap(t *testing.T) {
	var requested int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apply-template":
			_, _ = w.Write([]byte(`{"prompt":"templated large intent"}`))
		case "/tokenize":
			_ = json.NewEncoder(w).Encode(map[string]any{"tokens": make([]int, 3400)})
		case "/v1/chat/completions":
			var req chatRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			requested = req.NPredict
			_, _ = w.Write([]byte(completion("bounded proposal")))
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	result, err := NewClientWithContext(srv.URL, 4096).Complete(context.Background(), "propose retrieval anchors", "intent including source facts", 700)
	if err != nil || result != "bounded proposal" || requested != 568 {
		t.Fatalf("auxiliary budget=%d result=%q err=%v", requested, result, err)
	}
}
