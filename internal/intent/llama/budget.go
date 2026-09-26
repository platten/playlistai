package llama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/ports"
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
	observation, err := c.measureBudget(ctx, messages, want)
	return observation.OutputAllowance, err
}

func (c *Client) measureBudget(ctx context.Context, messages []chatMessage, want int) (ports.ParseAttemptObservation, error) {
	observation := ports.ParseAttemptObservation{}
	for _, m := range messages {
		observation.MandatoryByteBound += len(m.Content) + 64
	}
	if c.contextSize <= 0 {
		observation.OutputAllowance = want
		return observation, ctx.Err()
	}
	// Byte-level tokenizers need at most one token per UTF-8 byte. Reserve
	// template overhead separately; never apply an English-only chars/4 guess
	// to JSON, unusual spelling, or non-Latin requests.
	tokens := observation.MandatoryByteBound
	if c.measureTokens {
		// Both calls share one deadline. Native tokenization can briefly queue
		// behind server housekeeping after a prior completion, so retain enough
		// time to avoid rejecting an otherwise valid default-context request.
		measureCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
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
				observation.MandatoryTokens = &tokens
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return observation, err
	}
	remaining := c.contextSize - tokens - 128
	if remaining < min(want, 256) {
		return observation, fmt.Errorf("llama: request needs more context: measured input or conservative byte bound %d, configured context %d", tokens, c.contextSize)
	}
	observation.OutputAllowance = min(want, remaining)
	return observation, nil
}

// Admit only whole records into space left after the mandatory-only output
// allowance. No tokenizer RPC is added, and unavailable measurements admit none.
func (c *Client) admitHints(messages []chatMessage, evidence *core.ParsingContextEvidence, observation *ports.ParseAttemptObservation) {
	if evidence == nil {
		return
	}
	observation.OmittedHints = evidence.OmittedRecords + len(evidence.Hints)
	if observation.MandatoryTokens == nil || c.contextSize <= 0 || len(messages) == 0 {
		return
	}
	const boundaryAllowance = 32
	space := min(lexicon.MaxContextBytes, c.contextSize-*observation.MandatoryTokens-observation.OutputAllowance-128-boundaryAllowance)
	text := lexicon.ContextHeader
	for i, hint := range evidence.Hints {
		if len(observation.UsedHintIndexes) >= lexicon.MaxContextRecords || len(text)+len(hint.Text) > space {
			continue
		}
		text += hint.Text
		observation.UsedHintIndexes = append(observation.UsedHintIndexes, i)
		observation.OmittedHints--
	}
	if len(observation.UsedHintIndexes) > 0 {
		messages[len(messages)-1].Content += text
		observation.OptionalByteBound = len(text)
	}
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
