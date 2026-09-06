// Package llama is an IntentParser backed by a local llama.cpp `llama-server`
// child process. It talks the OpenAI-compatible /v1/chat/completions endpoint
// with a GBNF grammar so the model can only emit a valid intent object.
package llama

import (
	"bufio"
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
		// Generous: the first request pays uncached prompt processing for the
		// system + few-shot prefix, and CPU-only boxes are slow. A failure
		// here isn't fatal — app.Container.ParseIntent falls back to rules.
		hc: &http.Client{Timeout: 120 * time.Second},
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Messages           []chatMessage  `json:"messages"`
	Grammar            string         `json:"grammar,omitempty"`
	ChatTemplateKwargs map[string]any `json:"chat_template_kwargs,omitempty"`
	Temperature        float64        `json:"temperature"`
	NPredict           int            `json:"n_predict"`
	CachePrompt        bool           `json:"cache_prompt"`
	Stream             bool           `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

// Parse sends the prompt (system instruction + few-shot examples) and returns
// the model's intent.
func (c *Client) Parse(ctx context.Context, in ports.IntentInput) (core.MusicIntent, error) {
	return c.parse(ctx, in, nil)
}

// ParseWithProgress is Parse plus an onDelta callback, invoked with the running
// character count of the model's output as tokens stream in — the Generate
// screen turns this into a live "understanding your request" bar. onDelta may
// be nil.
func (c *Client) ParseWithProgress(ctx context.Context, in ports.IntentInput, onDelta func(chars int)) (core.MusicIntent, error) {
	return c.parse(ctx, in, onDelta)
}

func (c *Client) parse(ctx context.Context, in ports.IntentInput, onDelta func(chars int)) (core.MusicIntent, error) {
	body := chatRequest{
		Messages:           buildMessages(in),
		Grammar:            schema.GBNF,
		ChatTemplateKwargs: map[string]any{"enable_thinking": false},
		Temperature:        0.2,
		NPredict:           400,
		CachePrompt:        true,
		Stream:             true,
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
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.hc.Do(req)
	if err != nil {
		return core.MusicIntent{}, fmt.Errorf("llama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return core.MusicIntent{}, fmt.Errorf("llama: HTTP %d: %s", resp.StatusCode, snippet(raw))
	}

	var content string
	if strings.Contains(resp.Header.Get("Content-Type"), "event-stream") {
		content, err = readSSE(resp.Body, onDelta)
	} else {
		content, err = readWhole(resp.Body, onDelta)
	}
	if err != nil {
		return core.MusicIntent{}, err
	}
	if strings.TrimSpace(content) == "" {
		return core.MusicIntent{}, fmt.Errorf("llama: empty completion")
	}
	return schema.Parse([]byte(content))
}

// readSSE consumes an OpenAI-style `data: {...}` stream, accumulating
// choices[].delta.content and reporting the running length via onDelta.
func readSSE(body io.Reader, onDelta func(int)) (string, error) {
	var out strings.Builder
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		data, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			return out.String(), nil
		}
		var chunk streamChunk
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" {
				out.WriteString(ch.Delta.Content)
				if onDelta != nil {
					onDelta(out.Len())
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("llama: stream read: %w", err)
	}
	return out.String(), nil
}

// readWhole handles a non-streaming JSON response (test servers, proxies that
// buffer). onDelta, if set, fires once with the final length.
func readWhole(body io.Reader, onDelta func(int)) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("llama: response read: %w", err)
	}
	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return "", fmt.Errorf("llama: bad response: %w", err)
	}
	if len(cr.Choices) == 0 {
		return "", nil
	}
	content := cr.Choices[0].Message.Content
	if onDelta != nil && content != "" {
		onDelta(len(content))
	}
	return content, nil
}

// Complete runs a plain (no-grammar, non-streaming) chat completion and returns
// the assistant text. Used for short auxiliary generations like a playlist
// title — not the intent parse, which is grammar-constrained.
func (c *Client) Complete(ctx context.Context, system, user string, maxTokens int) (string, error) {
	if maxTokens <= 0 {
		maxTokens = 32
	}
	body := chatRequest{
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		ChatTemplateKwargs: map[string]any{"enable_thinking": false},
		Temperature:        0.4,
		NPredict:           maxTokens,
		CachePrompt:        false,
		Stream:             false,
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("llama: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return "", fmt.Errorf("llama: HTTP %d: %s", resp.StatusCode, snippet(raw))
	}
	return readWhole(resp.Body, nil)
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
