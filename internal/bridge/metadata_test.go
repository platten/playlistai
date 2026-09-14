package bridge

import (
	"context"
	"errors"
	"testing"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/enrich/musicbrainz"
	"github.com/platten/playlistai/internal/ports"
)

type metadataFixture struct {
	cleared bool
}

func (m *metadataFixture) ResolveMusic(_ context.Context, intent core.MusicIntent, _ ports.Catalog, _ ports.ReferenceResolver, _ ports.Progress) (core.MusicIntent, error) {
	return intent, nil
}
func (m *metadataFixture) ClearCache(context.Context) error { m.cleared = true; return nil }
func (m *metadataFixture) MetadataStatus() musicbrainz.MetadataStatus {
	return musicbrainz.MetadataStatus{}
}

func TestMetadataSettingsClearInvalidatesActiveAndLateWork(t *testing.T) {
	m := &metadataFixture{}
	a := New(&app.Container{Knowledge: m}, nil)
	var contexts []context.Context
	for _, group := range []string{"intent-preview", generationOperation} {
		ctx, _, finish := a.operations.begin(context.Background(), group)
		defer finish()
		contexts = append(contexts, ctx)
	}
	oldKey := a.intentCache.scopedKey("prompt")
	a.intentCache.put(oldKey, parsedIntentEntry{})
	if err := a.ClearMusicMetadataCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !m.cleared {
		t.Fatal("cache clear scope incorrect")
	}
	for _, ctx := range contexts {
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatal("active work not canceled")
		}
	}
	a.intentCache.put(oldKey, parsedIntentEntry{}) // simulate a late parser response
	if _, ok := a.intentCache.get(a.intentCache.scopedKey("prompt")); ok {
		t.Fatal("old metadata parse became reusable")
	}
}
