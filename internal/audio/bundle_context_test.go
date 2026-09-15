package audio

import (
	"context"
	"errors"
	"testing"
)

func TestBundleValidationPreservesCancellation(t *testing.T) {
	manager, dir, _ := storedFixtureBundle(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadBundleContext(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("CLAP canceled read: %v", err)
	}
	if _, _, err := manager.ActiveContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("CLAP canceled activation: %v", err)
	}
	mert := &MERTBundleManager{Directory: dir}
	if _, err := ReadMERTBundleContext(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("MERT canceled read: %v", err)
	}
	if _, _, err := mert.ActiveContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("MERT canceled activation: %v", err)
	}
}

func TestBundleArtifactCopyHonorsCanceledContext(t *testing.T) {
	_, source, m := storedFixtureBundle(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := copyBundleArtifactContext(ctx, source, t.TempDir(), m.Artifacts[0]); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled artifact copy: %v", err)
	}
}
