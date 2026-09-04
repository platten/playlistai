// Package llama is an IntentParser backed by a local llama.cpp `llama-server`
// child process. It talks the OpenAI-compatible /v1/chat/completions endpoint
// with a GBNF grammar so the model can only emit a valid intent object.
package llama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/schema"
	"github.com/platten/playlistai/internal/ports"
)

// Client is a stateless HTTP client for a running llama-server.
type Client struct {
	baseURL string
	hc      *http.Client
}

// NewClient returns a client for baseURL (e.g. http://127.0.0.1:8080).
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		hc:      &http.Client{Timeout: 60 * time.Second},
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Messages    []chatMessage `json:"messages"`
	Grammar     string        `json:"grammar"`
	Temperature float64       `json:"temperature"`
	NPredict    int           `json:"n_predict"`
	CachePrompt bool          `json:"cache_prompt"`
	Stream      bool          `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error json.RawMessage `json:"error"`
}

// Parse sends the prompt (with the system instruction and few-shot examples) and
// returns the model's intent.
func (c *Client) Parse(ctx context.Context, in ports.IntentInput) (core.MusicIntent, error) {
	body := chatRequest{
		Messages:    buildMessages(in),
		Grammar:     schema.GBNF,
		Temperature: 0.2,
		NPredict:    400,
		CachePrompt: true,
		Stream:      false,
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return core.MusicIntent{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return core.MusicIntent{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return core.MusicIntent{}, fmt.Errorf("llama: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return core.MusicIntent{}, fmt.Errorf("llama: HTTP %d: %s", resp.StatusCode, snippet(raw))
	}

	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return core.MusicIntent{}, fmt.Errorf("llama: bad response: %w", err)
	}
	if len(cr.Choices) == 0 || strings.TrimSpace(cr.Choices[0].Message.Content) == "" {
		return core.MusicIntent{}, fmt.Errorf("llama: empty completion")
	}

	m, err := schema.Parse([]byte(cr.Choices[0].Message.Content))
	if err != nil {
		return core.MusicIntent{}, err
	}
	return m, nil
}

// Healthy reports whether the server answers /health with 200.
func (c *Client) Healthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode == http.StatusOK
}

func buildMessages(in ports.IntentInput) []chatMessage {
	msgs := []chatMessage{{Role: "system", Content: schema.SystemPrompt}}
	for _, ex := range schema.FewShot {
		msgs = append(msgs,
			chatMessage{Role: "user", Content: ex.Prompt},
			chatMessage{Role: "assistant", Content: ex.JSON},
		)
	}
	msgs = append(msgs, chatMessage{Role: "user", Content: userMessage(in)})
	return msgs
}

func userMessage(in ports.IntentInput) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(in.Prompt))
	if in.NowPlaying != nil {
		b.WriteString("\n\n(now playing: ")
		b.WriteString(strings.TrimSpace(in.NowPlaying.Artist + " — " + in.NowPlaying.Title))
		b.WriteString(")")
	}
	if len(in.RecentTracks) > 0 {
		b.WriteString("\n(recent: ")
		names := make([]string, 0, len(in.RecentTracks))
		for _, t := range in.RecentTracks {
			names = append(names, t.Artist+" — "+t.Title)
		}
		b.WriteString(strings.Join(names, "; "))
		b.WriteString(")")
	}
	return b.String()
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
