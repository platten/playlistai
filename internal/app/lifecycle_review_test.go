package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/intent/llama"
	"github.com/platten/playlistai/internal/intent/modelmgr"
	"github.com/platten/playlistai/internal/ports"
)

func TestBlockedDownloadCannotUndoLaterModelAction(t *testing.T) {
	for _, action := range []string{"clear", "replacement"} {
		t.Run(action, func(t *testing.T) {
			c, err := New(context.Background(), testConfig(t), nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = c.Close() })
			path := modelFixture(t)
			started := make(chan context.Context, 1)
			resume := make(chan struct{})
			defer close(resume)
			var starts atomic.Int32
			c.modelFactory = func(context.Context, llama.Options) (managedParser, error) {
				starts.Add(1)
				return &managedFixture{IntentParser: fakes.IntentParser{Meta: ports.ParserInfo{Backend: "fixture"}}}, nil
			}
			c.modelDownloader = func(ctx context.Context, _ modelmgr.Model, _ string, _ ports.Progress) (string, error) {
				started <- ctx
				<-resume
				return path, nil
			}
			finished := make(chan error, 1)
			go func() { finished <- c.DownloadModel(context.Background(), modelmgr.Catalog()[0].ID, nil) }()
			var downloadContext context.Context
			select {
			case downloadContext = <-started:
			case <-time.After(time.Second):
				t.Fatal("download never started")
			}
			expectedID := ""
			expectedStarts := int32(0)
			if action == "clear" {
				err = c.ClearModel()
			} else {
				expectedID = "new-selection"
				expectedStarts = 1
				err = c.SetModel(context.Background(), path, expectedID)
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-downloadContext.Done():
			case <-time.After(time.Second):
				t.Fatal("new action did not cancel pending download")
			}
			// Let a non-cooperative downloader report success despite cancellation.
			// The transaction must still reject startup/publication from that result.
			resume <- struct{}{}
			select {
			case err = <-finished:
			case <-time.After(time.Second):
				t.Fatal("superseded download did not finish")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("superseded download error: %v", err)
			}
			if starts.Load() != expectedStarts {
				t.Fatal("superseded download started a model")
			}
			if _, id := c.CurrentModel(); id != expectedID {
				t.Fatalf("late download changed active model to %q", id)
			}
			prefs, err := config.LoadPrefsChecked(c.cfg.DataDir)
			if err != nil || prefs.ModelID != expectedID {
				t.Fatalf("late download changed persisted choice: %+v %v", prefs, err)
			}
		})
	}
}
