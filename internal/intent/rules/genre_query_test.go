package rules

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/ports"
)

func TestBareGenreQuerySeparatesControlsWithoutGenreWhitelist(t *testing.T) {
	for _, name := range []string{"Classical", "Gqom", "未知ジャンル", "Novel hybrid"} {
		for _, suffix := range []string{" 10 tracks", " music, ten tracks", " twenty-five songs"} {
			prompt := "  " + name + suffix
			if got := BareGenreQuery(prompt); got != name {
				t.Fatalf("%q: category = %q", prompt, got)
			}
			intent, _ := New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
			intent = ApplyConfirmedGenre(intent, name)
			want := 10
			if suffix == " twenty-five songs" {
				want = 25
			}
			if intent.Controls.TotalTrackCount != want || len(intent.References) != 0 || len(intent.EssentialCriteria) != 1 || intent.EssentialCriteria[0].Value != name {
				t.Fatalf("count/category not preserved: %+v", intent)
			}
			e := intent.EssentialCriteria[0].Evidence[0]
			if intent.OriginalDescription[e.Start:e.End] != name {
				t.Fatalf("source span = %+v", e)
			}
		}
	}
	for _, prompt := range []string{"like Classical, 10 tracks", "music by Electronic, 10 tracks", `album "Ten Songs"`, "Classical, no rock", "from Classical to jazz"} {
		if got := BareGenreQuery(prompt); got != "" {
			t.Fatalf("explicit instructions flattened: %q => %q", prompt, got)
		}
	}
}
