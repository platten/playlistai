package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// CommandEncoder invokes the repository's local-only Python encoder. The model
// path must already exist; the helper refuses network downloads.
type CommandEncoder struct {
	Python, Script, ModelPath, Name, Revision string
	Dimension                                 int
}

func (e CommandEncoder) Model() (string, string, int) { return e.Name, e.Revision, e.Dimension }

func (e CommandEncoder) Embed(ctx context.Context, text string) ([]float32, error) {
	output, err := exec.CommandContext(ctx, e.Python, e.Script, "--model", e.ModelPath, "--text", text).Output()
	if err != nil {
		return nil, fmt.Errorf("semantic encoder: %w", err)
	}
	var response struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("semantic encoder output: %w", err)
	}
	return response.Embedding, nil
}
