package resolution

import (
	"context"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
)

type cancelingResolver struct {
	started chan struct{}
	calls   int
}

func (*cancelingResolver) CatalogVersion() string { return "cancel-fixture" }
func (*cancelingResolver) ResolveReference(core.IntentReference) core.ReferenceResolution {
	panic("generation lost its resolution context")
}
func (r *cancelingResolver) ResolveReferenceContext(ctx context.Context, _ core.IntentReference) core.ReferenceResolution {
	r.calls++
	close(r.started)
	<-ctx.Done()
	return core.ReferenceResolution{Status: core.ResolutionUnresolved}
}

func TestApplyContextCancelsWithoutInventingMissingReferences(t *testing.T) {
	r := &cancelingResolver{started: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, References: []core.IntentReference{
		{Kind: core.ReferenceArtist, Query: "First", Influence: core.InfluencePositive},
		{Kind: core.ReferenceArtist, Query: "Second", Influence: core.InfluencePositive},
	}}
	done := make(chan []Issue, 1)
	go func() {
		_, issues := ApplyContext(ctx, r, intent)
		done <- issues
	}()
	<-r.started
	cancel()
	select {
	case issues := <-done:
		if r.calls != 1 || len(issues) != 0 {
			t.Fatalf("cancellation became missing music or continued resolution: calls=%d issues=%+v", r.calls, issues)
		}
	case <-time.After(time.Second):
		t.Fatal("reference resolution did not return on cancellation")
	}
}
