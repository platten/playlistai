package rules

import "testing"

func TestHyphenatedPlaylistCounts(t *testing.T) {
	for _, prompt := range []string{"Make a 10-song playlist.", "A ten-track playlist", "Give me a 10‐song journey", "Make a 10‑track mix", "10 jazz songs", "10 heavy metal songs", "10 drum and bass tracks"} {
		if count, ok := TrackCount(prompt); !ok || count != 10 {
			t.Fatalf("count lost for %q: %d %v", prompt, count, ok)
		}
	}
	if count, ok := TrackCount(`Music like the album "Ten-Song Demo", 5 tracks.`); !ok || count != 5 {
		t.Fatal("quoted album title overrode output count")
	}
	if _, ok := TrackCount("Songs that are 10 minutes long"); ok {
		t.Fatal("duration became a track count")
	}
	if text := maskTrackCounts("10 jazz songs"); text != "   jazz      " {
		t.Fatalf("masking count removed musical meaning: %q", text)
	}
	for _, prompt := range []string{"two artists and ten songs", "10 two tone songs", "10 two-step tracks"} {
		if count, ok := TrackCount(prompt); !ok || count != 10 {
			t.Fatalf("nested quantity/genre changed count in %q: %d %v", prompt, count, ok)
		}
	}
	for _, prompt := range []string{"19th century songs", "2-step garage tracks"} {
		if count, ok := TrackCount(prompt); ok {
			t.Fatalf("period/genre became count %d: %q", count, prompt)
		}
	}
}
