package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func TestEnhancedOperationsCancelWhileAnotherOperationHoldsLock(t *testing.T) {
	for _, operation := range []string{"prepare", "refresh", "analyze"} {
		t.Run(operation, func(t *testing.T) {
			c := &Container{}
			c.enhanced.opMu.Lock()
			defer c.enhanced.opMu.Unlock()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var err error
				switch operation {
				case "prepare":
					_, err = c.PrepareEnhancedAudio(ctx, core.MusicIntent{}, core.TasteProfile{}, nil)
				case "refresh":
					_, err = c.RefreshEnhancedAudio(ctx, core.MusicIntent{}, core.TasteProfile{}, nil, nil)
				case "analyze":
					_, err = c.AnalyzeEnhancedTracks(ctx, nil, false, ports.NopProgress{})
				}
				done <- err
			}()
			select {
			case err := <-done:
				t.Fatalf("operation bypassed its held lock: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation became another outcome: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("canceled operation still waited for the unrelated lock holder")
			}
		})
	}
}
