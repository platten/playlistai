package schema

import (
	"encoding/json"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestModelCannotSplitOrReplaceGroundedIdentity(t *testing.T) {
	const prompt = "like The Ambient And The Loud Machine"
	evidence := []core.SourceEvidence{{Text: "The Ambient And The Loud Machine", Start: 5, End: len(prompt), Explicit: true}}
	grounding := &core.IdentityGrounding{Provider: "MusicBrainz", MatchedSpelling: evidence[0].Text, MatchType: "canonical", SnapshotVersion: "musicbrainz-offline/v1:20260920-120000", Candidates: []core.IdentityCandidate{{Kind: core.ReferenceArtist, ID: "artist-1", Name: evidence[0].Text}}}
	source := core.IntentTranslation{Version: "grounded-test/v1", OriginalText: prompt, Atoms: []core.IntentAtom{{ID: "artist:5:37", Kind: "artist", Value: evidence[0].Text, Scope: "playlist", Polarity: "positive", Strength: "preferred", Evidence: evidence, Grounding: grounding}}}
	// The model proposes a split list and an invented required output. Source
	// reconciliation must replace both with the one grounded artist reference.
	bad := WireReference{Kind: "artist", Value: "The Ambient", Span: "The Ambient", Explicit: true, Influence: "positive"}
	w := Wire{Genres: []WirePreference{}, References: []WireReference{bad, {Kind: "artist", Value: "The Loud Machine", Span: "The Loud Machine", Explicit: true, Influence: "positive"}}, RequiredTracks: []WireReference{{Kind: "track", Value: "Invented Song", Span: evidence[0].Text, Explicit: true, Influence: "positive"}}, Mode: "similar", TotalCount: 10}
	raw, _ := json.Marshal(w)
	intent, err := ParseForPromptWithSource(raw, prompt, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(intent.References) != 1 || intent.References[0].Query != evidence[0].Text || intent.References[0].Grounding == nil || len(intent.RequiredTracks) != 0 {
		t.Fatalf("model changed grounded identity: refs=%+v required=%+v", intent.References, intent.RequiredTracks)
	}
}
