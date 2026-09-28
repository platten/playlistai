package discoveryasset

import (
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
)

func TestSelectionMemoryIncludesClassifierVocabularies(t *testing.T) {
	track := librarypack.Track{ID: "track", Artist: "Artist", Title: "Song"}
	before := trackMemory(track)
	track.ClassifierEvidence = []core.MusicClassifierEvidence{{Heads: []core.MusicClassifierHead{{Classes: []string{strings.Repeat("label", 1000)}, Scores: []float32{.5}}}}}
	if trackMemory(track) < before+20000 {
		t.Fatal("classifier vocabulary bypassed selection memory budget")
	}
}
