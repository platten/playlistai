package schema

import "testing"

func TestWireReferencePreservesStrength(t *testing.T) {
	for _, strength := range []string{"", "preferred", "essential", "required"} {
		got := referenceToCore(WireReference{Kind: "artist", Value: "Radiohead", Influence: "positive", Explicit: true, Span: "Radiohead", Strength: strength})
		if got.Strength != strength {
			t.Fatal(got)
		}
	}
}
