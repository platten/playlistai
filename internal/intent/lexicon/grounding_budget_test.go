package lexicon

import (
	"fmt"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestBaselineGroundingMessageBoundsIdentityChoices(t *testing.T) {
	var candidates []core.IdentityCandidate
	for i := range 64 {
		candidates = append(candidates, core.IdentityCandidate{Kind: core.ReferenceArtist, ID: fmt.Sprintf("identity-%02d", i), Name: "Shared Name"})
	}
	source := core.IntentTranslation{Atoms: []core.IntentAtom{{Kind: "artist", Value: "Shared Name", Evidence: []core.SourceEvidence{{Text: "Shared Name"}}, Grounding: &core.IdentityGrounding{Candidates: candidates}}}}
	full := BaselineFactsMessage(source)
	if strings.Count(full, "identity-") != 8 || !strings.Contains(full, "identity-07") || strings.Contains(full, "identity-08") || !strings.Contains(full, "additional identities omitted; identity unresolved") {
		t.Fatalf("model identity presentation lost its bound or omission notice: %s", full)
	}
	if len(source.Atoms[0].Grounding.Candidates) != 64 {
		t.Fatal("model formatting discarded saved/UI identity evidence")
	}
	previous := source.Clone()
	previous.Atoms[0].Grounding.Candidates = previous.Atoms[0].Grounding.Candidates[:8]
	previous.Atoms[0].Grounding.Truncated = true
	if BaselineFactsMessage(previous) != full {
		t.Fatal("more retained identities changed the bounded model input")
	}
	previous.Atoms[0].Grounding.Truncated = false
	if strings.Contains(BaselineFactsMessage(previous), "omitted") {
		t.Fatal("complete eight-candidate result falsely reported omitted identities")
	}
}
