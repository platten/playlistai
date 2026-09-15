package app

import (
	"context"
	"testing"
	"time"
)

func awaitAudioSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("audio lifecycle did not progress")
	}
}

func TestAudioStartupReturnsWhileInitializingAndCloseCancels(t *testing.T) {
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	started, stopped := make(chan struct{}), make(chan struct{})
	c.analysis.startup.start(c, context.Background(), func(ctx context.Context) { close(started); <-ctx.Done(); close(stopped) })
	awaitAudioSignal(t, started)
	if !c.AudioStartupPending() || c.Ready() {
		t.Fatal("pending startup reported ready")
	}
	status, err := c.GetAnalysisStatus(context.Background())
	if err != nil || !status.Loading {
		t.Fatalf("missing loading status: %+v %v", status, err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	awaitAudioSignal(t, stopped)
	if c.AudioStartupPending() {
		t.Fatal("shutdown retained pending startup")
	}
}

func TestLateAudioStartupCompletionCannotClearNewerPendingState(t *testing.T) {
	c, err := New(context.Background(), testConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	oldStarted, releaseOld, oldFinished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	c.analysis.startup.start(c, context.Background(), func(context.Context) { close(oldStarted); <-releaseOld; close(oldFinished) })
	awaitAudioSignal(t, oldStarted)
	newStarted := make(chan struct{})
	c.analysis.startup.start(c, context.Background(), func(ctx context.Context) { close(newStarted); <-ctx.Done() })
	awaitAudioSignal(t, newStarted)
	close(releaseOld)
	awaitAudioSignal(t, oldFinished)
	if !c.AudioStartupPending() {
		t.Fatal("superseded startup cleared current pending state")
	}
}

func TestModelRemovalCancelsPendingAudioInitialization(t *testing.T) {
	for _, model := range []string{"clap", "mert"} {
		t.Run(model, func(t *testing.T) {
			c, err := New(context.Background(), testConfig(t), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = c.Close() }()
			startup, opMu, remove := &c.analysis.startup, &c.analysis.opMu, c.RemoveAnalysisModel
			if model == "mert" {
				startup, opMu, remove = &c.enhanced.startup, &c.enhanced.opMu, c.RemoveMERT
			}
			started, stopped := make(chan struct{}), make(chan struct{})
			startup.start(c, context.Background(), func(ctx context.Context) {
				opMu.Lock()
				defer opMu.Unlock()
				close(started)
				<-ctx.Done()
				close(stopped)
			})
			awaitAudioSignal(t, started)
			removed := make(chan error, 1)
			go func() { removed <- remove() }()
			awaitAudioSignal(t, stopped)
			select {
			case err := <-removed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("removal blocked behind canceled startup")
			}
			if c.analysis.manifest != nil || c.enhanced.manifest != nil {
				t.Fatal("removed model became active")
			}
		})
	}
}
