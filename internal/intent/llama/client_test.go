package llama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/schema"
	"github.com/platten/playlistai/internal/ports"
)

func chatServer(t *testing.T, handler func(reqBody []byte) (status int, respBody string)) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		status, body := handler(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func completion(content string) string {
	resp := map[string]any{"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": content}}}}
	b, _ := json.Marshal(resp)
	return string(b)
}

func TestClientParseSuccess(t *testing.T) {
	t.Parallel()
	var got struct {
		Messages []chatMessage `json:"messages"`
		Grammar  string        `json:"grammar"`
	}
	srv := chatServer(t, func(body []byte) (int, string) {
		_ = json.Unmarshal(body, &got)
		return 200, completion(`{"seeds":["Justice"],"mode":"similar","count":22,"creativity":0.6,"noise":0.2,"lookback":3,"exclude_artists":[],"no_repeat_artist":true,"notes":"n"}`)
	})

	m, err := NewClient(srv.URL).Parse(context.Background(), ports.IntentInput{Prompt: "like Justice, 22 songs"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.Seeds.Queries) != 1 || m.Seeds.Queries[0] != "Justice" || m.Count != 22 {
		t.Fatalf("intent = %+v", m)
	}

	if got.Grammar != schema.GBNF {
		t.Fatal("request did not carry the GBNF grammar")
	}
	if got.Messages[0].Role != "system" || !strings.Contains(got.Messages[0].Content, "translate") {
		t.Fatalf("first message = %+v", got.Messages[0])
	}
	last := got.Messages[len(got.Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "like Justice") {
		t.Fatalf("last message = %+v", last)
	}
	// few-shot pairs sit between system and the real prompt
	if len(got.Messages) != 1+2*len(schema.FewShot)+1 {
		t.Fatalf("message count = %d", len(got.Messages))
	}
}

func TestClientParseProseWrapped(t *testing.T) {
	t.Parallel()
	srv := chatServer(t, func([]byte) (int, string) {
		return 200, completion("Sure!\n```json\n{\"seeds\":[\"Air\"],\"mode\":\"similar\",\"count\":15,\"creativity\":0.5,\"noise\":0.1,\"lookback\":3,\"exclude_artists\":[],\"no_repeat_artist\":true,\"notes\":\"n\"}\n```")
	})
	m, err := NewClient(srv.URL).Parse(context.Background(), ports.IntentInput{Prompt: "like Air"})
	if err != nil || len(m.Seeds.Queries) != 1 || m.Seeds.Queries[0] != "Air" {
		t.Fatalf("m=%+v err=%v", m, err)
	}
}

func TestClientParseErrors(t *testing.T) {
	t.Parallel()

	t.Run("http 500", func(t *testing.T) {
		srv := chatServer(t, func([]byte) (int, string) { return 500, `{"error":"boom"}` })
		if _, err := NewClient(srv.URL).Parse(context.Background(), ports.IntentInput{Prompt: "x"}); err == nil {
			t.Fatal("want error")
		}
	})

	t.Run("no choices", func(t *testing.T) {
		srv := chatServer(t, func([]byte) (int, string) { return 200, `{"choices":[]}` })
		if _, err := NewClient(srv.URL).Parse(context.Background(), ports.IntentInput{Prompt: "x"}); err == nil {
			t.Fatal("want error")
		}
	})

	t.Run("garbage completion", func(t *testing.T) {
		srv := chatServer(t, func([]byte) (int, string) { return 200, completion("I cannot help with that.") })
		if _, err := NewClient(srv.URL).Parse(context.Background(), ports.IntentInput{Prompt: "x"}); err == nil {
			t.Fatal("want error")
		}
	})

	t.Run("context timeout", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(300 * time.Millisecond)
			_, _ = io.WriteString(w, completion(`{}`))
		})
		s := httptest.NewServer(mux)
		defer s.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if _, err := NewClient(s.URL).Parse(ctx, ports.IntentInput{Prompt: "x"}); err == nil {
			t.Fatal("want timeout error")
		}
	})
}

func TestClientHealthy(t *testing.T) {
	t.Parallel()
	srv := chatServer(t, func([]byte) (int, string) { return 200, completion(`{}`) })
	if !NewClient(srv.URL).Healthy(context.Background()) {
		t.Fatal("should be healthy")
	}
	if NewClient("http://127.0.0.1:1").Healthy(context.Background()) {
		t.Fatal("dead endpoint should not be healthy")
	}
}

func TestUserMessageIncludesContext(t *testing.T) {
	t.Parallel()
	msg := userMessage(ports.IntentInput{
		Prompt:     "keep going",
		NowPlaying: &core.TrackRef{Artist: "Kavinsky", Title: "Nightcall"},
	})
	if !strings.Contains(msg, "keep going") || !strings.Contains(msg, "Kavinsky") {
		t.Fatalf("msg = %q", msg)
	}
}
