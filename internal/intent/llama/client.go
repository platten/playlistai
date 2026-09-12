// Package llama is an IntentParser backed by a local llama.cpp `llama-server`
// child process. It talks the OpenAI-compatible /v1/chat/completions endpoint
// with a GBNF grammar so the model can only emit a valid intent object.
package llama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/intent/schema"
	"github.com/platten/playlistai/internal/logging"
	"github.com/platten/playlistai/internal/ports"
)

// Client is a stateless HTTP client for a running llama-server.
type Client struct {
	baseURL       string
	hc            *http.Client
	contextSize   int
	measureTokens bool
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
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

type completionResult struct {
	Content      string
	FinishReason string
}

// TruncatedCompletionError is distinguishable by lifecycle callers so a
// grammar-valid prefix is never silently reinterpreted by the rules fallback.
type TruncatedCompletionError struct {
	FinishReason string
	Attempts     int
}

func (e *TruncatedCompletionError) Error() string {
	return fmt.Sprintf("llama: intent completion truncated after %d bounded attempts (finish_reason=%q)", e.Attempts, e.FinishReason)
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
	correction := ""
	for attempt, tokenBudget := range []int{1800, 2400} {
		intent, result, err := c.parseAttemptCorrected(ctx, in, onDelta, tokenBudget, correction)
		if err == nil && result.FinishReason != "length" {
			return intent, nil
		}
		if result.FinishReason != "length" {
			if attempt == 0 && result.Content == "" && ctx.Err() == nil && retryableParseTransport(err) {
				continue
			}
			if attempt == 1 || result.Content == "" {
				return core.MusicIntent{}, err
			}
			correction = "The previous interpretation was invalid: " + err.Error() + ". Reinterpret the original request. Copy spans verbatim from it, retain every category and modifier, and use Artist - Title for inferred tracks."
			continue
		}
		if attempt == 1 {
			return core.MusicIntent{}, &TruncatedCompletionError{FinishReason: result.FinishReason, Attempts: attempt + 1}
		}
	}
	return core.MusicIntent{}, fmt.Errorf("llama: intent parse failed")
}

func retryableParseTransport(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var requestError *url.Error
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &requestError)
}

func (c *Client) parseAttemptCorrected(ctx context.Context, in ports.IntentInput, onDelta func(chars int), tokenBudget int, correction string) (core.MusicIntent, completionResult, error) {
	body := chatRequest{
		Messages:           buildMessages(in),
		Grammar:            schema.GBNF,
		ChatTemplateKwargs: map[string]any{"enable_thinking": false},
		Temperature:        0, // interpretation should preserve instructions, not sample creative variants
		NPredict:           tokenBudget,
		CachePrompt:        true,
		Stream:             true,
	}
	if correction != "" {
		body.Messages[len(body.Messages)-1].Content += "\n\nValidation feedback (not part of the music request): " + correction
	}
	budget, budgetErr := c.outputBudget(ctx, body.Messages, tokenBudget)
	if budgetErr != nil {
		return core.MusicIntent{}, completionResult{}, budgetErr
	}
	body.NPredict = budget
	buf, err := json.Marshal(body)
	if err != nil {
		return core.MusicIntent{}, completionResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return core.MusicIntent{}, completionResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	started := time.Now()
	logging.Diagnostic(ctx, "api.call", map[string]any{"provider": "llama.cpp", "method": http.MethodPost, "url": req.URL.String(), "operation": "parse_intent", "tokenBudget": tokenBudget, "corrected": correction != ""})
	resp, err := c.hc.Do(req)
	if err != nil {
		logging.Diagnostic(ctx, "api.response", map[string]any{"provider": "llama.cpp", "method": http.MethodPost, "url": req.URL.String(), "operation": "parse_intent", "elapsedMilliseconds": time.Since(started).Milliseconds(), "error": err.Error()})
		return core.MusicIntent{}, completionResult{}, fmt.Errorf("llama: %w", err)
	}
	defer resp.Body.Close()
	logging.Diagnostic(ctx, "api.response", map[string]any{"provider": "llama.cpp", "method": http.MethodPost, "url": req.URL.String(), "operation": "parse_intent", "status": resp.StatusCode, "contentLength": resp.ContentLength, "elapsedMilliseconds": time.Since(started).Milliseconds()})

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return core.MusicIntent{}, completionResult{}, fmt.Errorf("llama: HTTP %d: %s", resp.StatusCode, snippet(raw))
	}

	var result completionResult
	if strings.Contains(resp.Header.Get("Content-Type"), "event-stream") {
		result, err = readSSECompletion(resp.Body, onDelta)
	} else {
		result, err = readWholeCompletion(resp.Body, onDelta)
	}
	if err != nil {
		return core.MusicIntent{}, result, err
	}
	if strings.TrimSpace(result.Content) == "" {
		return core.MusicIntent{}, result, fmt.Errorf("llama: empty completion (finish_reason=%q)", result.FinishReason)
	}
	logging.Diagnostic(ctx, "llm.response", map[string]any{"operation": "parse_intent", "content": result.Content, "finishReason": result.FinishReason})
	intent, err := schema.ParseForPrompt([]byte(result.Content), in.Prompt)
	return intent, result, err
}

// readSSE consumes an OpenAI-style `data: {...}` stream, accumulating
// choices[].delta.content and reporting the running length via onDelta.
func readSSE(body io.Reader, onDelta func(int)) (string, error) {
	result, err := readSSECompletion(body, onDelta)
	return result.Content, err
}

func readSSECompletion(body io.Reader, onDelta func(int)) (completionResult, error) {
	var out strings.Builder
	var finishReason string
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
			return completionResult{Content: out.String(), FinishReason: finishReason}, nil
		}
		var chunk streamChunk
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		for _, ch := range chunk.Choices {
			if ch.FinishReason != "" {
				finishReason = ch.FinishReason
			}
			if ch.Delta.Content != "" {
				out.WriteString(ch.Delta.Content)
				if onDelta != nil {
					onDelta(out.Len())
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return completionResult{}, fmt.Errorf("llama: stream read: %w", err)
	}
	return completionResult{Content: out.String(), FinishReason: finishReason}, nil
}

// readWhole handles a non-streaming JSON response (test servers, proxies that
// buffer). onDelta, if set, fires once with the final length.
func readWhole(body io.Reader, onDelta func(int)) (string, error) {
	result, err := readWholeCompletion(body, onDelta)
	return result.Content, err
}

func readWholeCompletion(body io.Reader, onDelta func(int)) (completionResult, error) {
	raw, err := io.ReadAll(io.LimitReader(body, 1<<20))
	if err != nil {
		return completionResult{}, fmt.Errorf("llama: response read: %w", err)
	}
	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return completionResult{}, fmt.Errorf("llama: bad response: %w", err)
	}
	if len(cr.Choices) == 0 {
		return completionResult{}, nil
	}
	content := cr.Choices[0].Message.Content
	if onDelta != nil && content != "" {
		onDelta(len(content))
	}
	return completionResult{Content: content, FinishReason: cr.Choices[0].FinishReason}, nil
}

// Complete runs a plain (no-grammar, non-streaming) chat completion and returns
// the assistant text. Used for short auxiliary generations like a playlist
// title — not the intent parse, which is grammar-constrained.
func (c *Client) Complete(ctx context.Context, system, user string, maxTokens int) (string, error) {
	return c.complete(ctx, system, user, maxTokens, "")
}

func (c *Client) complete(ctx context.Context, system, user string, maxTokens int, grammar string) (string, error) {
	if maxTokens <= 0 {
		maxTokens = 32
	}
	body := chatRequest{
		Grammar: grammar,
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
	budget, err := c.outputBudget(ctx, body.Messages, maxTokens)
	if err != nil {
		return "", err
	}
	body.NPredict = budget
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

	started := time.Now()
	logging.Diagnostic(ctx, "api.call", map[string]any{"provider": "llama.cpp", "method": http.MethodPost, "url": req.URL.String(), "operation": "completion", "systemPrompt": system, "userPrompt": user, "tokenBudget": maxTokens, "grammar": grammar != ""})
	resp, err := c.hc.Do(req)
	if err != nil {
		logging.Diagnostic(ctx, "api.response", map[string]any{"provider": "llama.cpp", "method": http.MethodPost, "url": req.URL.String(), "operation": "completion", "elapsedMilliseconds": time.Since(started).Milliseconds(), "error": err.Error()})
		return "", fmt.Errorf("llama: %w", err)
	}
	defer resp.Body.Close()
	logging.Diagnostic(ctx, "api.response", map[string]any{"provider": "llama.cpp", "method": http.MethodPost, "url": req.URL.String(), "operation": "completion", "status": resp.StatusCode, "contentLength": resp.ContentLength, "elapsedMilliseconds": time.Since(started).Milliseconds()})
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return "", fmt.Errorf("llama: HTTP %d: %s", resp.StatusCode, snippet(raw))
	}
	out, err := readWhole(resp.Body, nil)
	logging.Diagnostic(ctx, "llm.response", map[string]any{"operation": "completion", "content": out, "error": errorString(err)})
	return out, err
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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
	content := userMessage(in)
	if in.Locale != "" {
		content += "\n\nRequest locale (context, not music instructions): " + in.Locale
	}
	content += lexicon.FactsMessage(lexicon.Extract(in.Prompt))
	msgs = append(msgs, chatMessage{Role: "user", Content: content})
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
