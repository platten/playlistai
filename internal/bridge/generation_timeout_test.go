package bridge

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestGenerationOperationErrorExplainsDeadline(t *testing.T) {
	err := generationOperationError(context.DeadlineExceeded)
	if !strings.Contains(err.Error(), "two-minute time limit") || !strings.Contains(err.Error(), "specific artist or track") {
		t.Fatalf("deadline error is not actionable: %v", err)
	}
	if got := generationOperationError(context.Canceled); !errors.Is(got, context.Canceled) {
		t.Fatalf("user cancellation changed: %v", got)
	}
}
