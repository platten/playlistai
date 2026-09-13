package mbindex

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// BenchmarkBuildSynthetic includes archive decompression, import, stage merge,
// index creation and VACUUM. Fixture generation is excluded. Its repetitive
// ignored metadata models dump fields omitted from the compact index, not the
// production snapshot's compression ratio or full-catalog random I/O pattern.
func BenchmarkBuildSynthetic(b *testing.B) {
	root := b.TempDir()
	artists := make([]any, 1000)
	for i := range artists {
		artists[i] = map[string]any{
			"id": fmt.Sprintf("artist-%06d", i), "name": fmt.Sprintf("Artist %d", i),
			"tags": []dumpTag{{Name: "dream pop", Count: 5}},
		}
	}
	recordings := make([]any, 10000)
	for i := range recordings {
		recordings[i] = map[string]any{
			"id": fmt.Sprintf("recording-%08d", (i*7919)%len(recordings)), "title": fmt.Sprintf("Track %d", i),
			"length": 180000, "isrcs": []string{fmt.Sprintf("USABC26%05d", i)},
			"artist-credit":   []any{map[string]any{"name": fmt.Sprintf("Artist %d", i%len(artists)), "artist": map[string]any{"id": fmt.Sprintf("artist-%06d", i%len(artists)), "name": fmt.Sprintf("Artist %d", i%len(artists))}}},
			"tags":            []dumpTag{{Name: "dream pop", Count: 5}, {Name: "electronic", Count: 3}},
			"unused-metadata": strings.Repeat("ignored MusicBrainz relationships and release payload ", 80),
		}
	}
	artistPath, recordingPath := filepath.Join(root, "artist.tar.xz"), filepath.Join(root, "recording.tar.xz")
	writeDump(b, artistPath, "artist", artists)
	writeDump(b, recordingPath, "recording", recordings)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		info, err := Build(context.Background(), BuildOptions{Output: filepath.Join(root, "index.sqlite"), Snapshot: "20260912-001001", ArtistArchive: artistPath, RecordingArchive: recordingPath, Replace: true})
		if err != nil {
			b.Fatal(err)
		}
		if info.Artists != 1000 || info.Recordings != 10000 || info.RecordingTags != 20000 {
			b.Fatalf("unexpected counts: %+v", info)
		}
	}
}
