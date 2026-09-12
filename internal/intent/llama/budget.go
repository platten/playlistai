package llama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// NewClientWithContext enables tokenizer-based budgeting for a managed or
// explicitly configured native runtime. It does not alter interpretation.
func NewClientWithContext(baseURL string, contextSize int) *Client {
	c := NewClient(baseURL)
	if contextSize > 0 {
		c.contextSize = contextSize
		c.measureTokens = true
	}
	return c
}

func (c *Client) outputBudget(ctx context.Context, messages []chatMessage, want int) (int, error) {
	if c.contextSize <= 0 {
		return want, ctx.Err()
	}
	// Byte-level tokenizers need at most one token per UTF-8 byte. Reserve
	// template overhead separately; never apply an English-only chars/4 guess
	// to JSON, unusual spelling, or non-Latin requests.
	tokens := 0
	for _, m := range messages {
		tokens += len(m.Content) + 64
	}
	if c.measureTokens {
		measureCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		var template struct {
			Prompt string `json:"prompt"`
		}
		if err := c.budgetRPC(measureCtx, "/apply-template", map[string]any{"messages": messages, "add_generation_prompt": true, "chat_template_kwargs": map[string]any{"enable_thinking": false}}, &template); err == nil && template.Prompt != "" {
			var measured struct {
				Tokens []json.RawMessage `json:"tokens"`
			}
			if err := c.budgetRPC(measureCtx, "/tokenize", map[string]any{"content": template.Prompt}, &measured); err == nil && len(measured.Tokens) > 0 {
				tokens = len(measured.Tokens)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	remaining := c.contextSize - tokens - 128
	if remaining < min(want, 256) {
		return 0, fmt.Errorf("llama: request needs more context: measured input or conservative byte bound %d, configured context %d", tokens, c.contextSize)
	}
	return min(want, remaining), nil
}

func (c *Client) budgetRPC(ctx context.Context, path string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("llama: %s returned %d", path, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(output)
}
