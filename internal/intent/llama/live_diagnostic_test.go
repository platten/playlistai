package llama

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/ports"
)

// Opt-in public-prompt diagnostics; never collect private desktop requests.
func TestLivePublicArtistCompletion(t *testing.T) {
	model := os.Getenv("PLAYLISTAI_TEST_INTENT_MODEL")
	if model == "" {
		t.Skip("requires an installed local model")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	p, err := New(ctx, Options{ModelPath: model, BinaryPath: os.Getenv("PLAYLISTAI_TEST_INTENT_RUNTIME"), NCtx: 8192})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	intent, completion, err := p.cli.parseAttemptCorrected(ctx, ports.IntentInput{Prompt: "Make a 10-song playlist inspired by Daft Punk."}, nil, 1800, "")
	t.Logf("termination=%s content=%s", completion.FinishReason, completion.Content)
	if err != nil || completion.FinishReason == "length" {
		t.Fatalf("intent=%+v err=%v", intent, err)
	}
}
