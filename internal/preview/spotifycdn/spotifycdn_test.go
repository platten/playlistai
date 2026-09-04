package spotifycdn

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestPreviewURL(t *testing.T) {
	t.Parallel()
	p := New()

	url, ok, err := p.PreviewURL(context.Background(), core.TrackRef{ID: "a"}, "https://cdn.example/x.mp3")
	if err != nil || !ok || url != "https://cdn.example/x.mp3" {
		t.Fatalf("got %q ok=%v err=%v", url, ok, err)
	}

	url, ok, err = p.PreviewURL(context.Background(), core.TrackRef{ID: "b"}, "")
	if err != nil || ok || url != "" {
		t.Fatalf("empty bundled url should miss cleanly, got %q ok=%v err=%v", url, ok, err)
	}
}

func TestNameAndInterface(t *testing.T) {
	t.Parallel()
	if New().Name() != "spotifycdn" {
		t.Fatal("unexpected name")
	}
}
