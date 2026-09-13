package audio

import (
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestThesaurusCLAPQueriesPreservePolicyAndSense(t *testing.T) {
	alias := core.AudioClause{Kind: "mood", Text: "joyous", Negative: true, Scope: "journey_end"}
	if got, want := ClauseQueries(alias), ClauseQueries(core.AudioClause{Kind: "mood", Text: "happy"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("alias differs: %v %v", got, want)
	}
	alias.Strict = true
	if got := ClauseQueries(alias); !reflect.DeepEqual(got, []string{"joyous"}) {
		t.Fatalf("strict calibration changed: %v", got)
	}
	for _, tt := range []struct{ kind, term, unwanted string }{
		{"mood", "dreamlike", "relaxed"}, {"genre", "liquid dnb", "electronic"},
		{"texture", "dissonance", "atonal"},
	} {
		if got := ClauseQueries(core.AudioClause{Kind: tt.kind, Text: tt.term}); got[0] == tt.unwanted {
			t.Fatalf("sense collapsed: %v", got)
		}
	}
}
