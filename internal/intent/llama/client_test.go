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
		Messages           []chatMessage  `json:"messages"`
		Grammar            string         `json:"grammar"`
		ChatTemplateKwargs map[string]any `json:"chat_template_kwargs"`
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
	if thinking, ok := got.ChatTemplateKwargs["enable_thinking"].(bool); !ok || thinking {
		t.Fatalf("enable_thinking = %#v, want false", got.ChatTemplateKwargs["enable_thinking"])
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

func TestClientParseStreamingWithProgress(t *testing.T) {
	t.Parallel()
	full := `{"seeds":["Bonobo"],"mode":"similar","count":18,"creativity":0.5,"noise":0.1,"lookback":3,"exclude_artists":[],"no_repeat_artist":true,"notes":"n"}`
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, tok := range chunkString(full, 7) {
			b, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{{"delta": map[string]string{"content": tok}}},
			})
			_, _ = io.WriteString(w, "data: "+string(b)+"\n\n")
			if fl != nil {
				fl.Flush()
			}
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	s := httptest.NewServer(mux)
	defer s.Close()

	var deltas []int
	m, err := NewClient(s.URL).ParseWithProgress(
		context.Background(),
		ports.IntentInput{Prompt: "like Bonobo"},
		func(n int) { deltas = append(deltas, n) },
	)
	if err != nil {
		t.Fatalf("ParseWithProgress: %v", err)
	}
	if len(m.Seeds.Queries) != 1 || m.Seeds.Queries[0] != "Bonobo" || m.Count != 18 {
		t.Fatalf("intent = %+v", m)
	}
	if len(deltas) < 2 {
		t.Fatalf("expected multiple progress deltas, got %v", deltas)
	}
	for i := 1; i < len(deltas); i++ {
		if deltas[i] < deltas[i-1] {
			t.Fatalf("progress went backwards: %v", deltas)
		}
	}
	if deltas[len(deltas)-1] != len(full) {
		t.Fatalf("final delta %d, want %d", deltas[len(deltas)-1], len(full))
	}
}

func TestReadSSEReturnsAtDoneWithoutWaitingForConnectionClose(t *testing.T) {
	t.Parallel()
	reader, writer := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		content, err := readSSE(reader, nil)
		if err != nil || content != "ok" {
			t.Errorf("readSSE content=%q err=%v", content, err)
		}
	}()
	_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("readSSE waited for the connection to close after [DONE]")
	}
	_ = writer.Close()
}

func chunkString(s string, n int) []string {
	var out []string
	for i := 0; i < len(s); i += n {
		end := i + n
		if end > len(s) {
			end = len(s)
		}
		out = append(out, s[i:end])
	}
	return out
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
