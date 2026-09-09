package schema

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestParseValid(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"seeds":["Justice","Daft Punk"],"required_tracks":["Air - La femme d'argent"],"mode":"journey","count":30,"creativity":0.7,"noise":0.4,"lookback":2,"exclude_artists":["Skrillex"],"no_repeat_artist":true,"exclude_seed_artists":true,"notes":"note"}`)
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.Seeds.Queries) != 2 || m.Seeds.Queries[0] != "Justice" {
		t.Fatalf("seeds = %#v", m.Seeds.Queries)
	}
	if m.Mode != core.ModeJourney || m.Count != 30 || m.Lookback != 2 {
		t.Fatalf("intent = %+v", m)
	}
	if m.Creativity != 0.7 || m.Noise != 0.4 {
		t.Fatalf("floats = %v/%v", m.Creativity, m.Noise)
	}
	if len(m.Constraints.ArtistsExclude) != 1 || !m.Constraints.NoRepeatArtistBackToBack {
		t.Fatalf("constraints = %+v", m.Constraints)
	}
	if len(m.Required.Queries) != 1 || !m.Constraints.ExcludeSeedArtists {
		t.Fatalf("required/seed exclusion = %+v / %+v", m.Required, m.Constraints)
	}
	if m.NotesForUser != "note" {
		t.Fatalf("notes = %q", m.NotesForUser)
	}
}

func TestParseV3PreservesSemanticEvidenceAndUnsupported(t *testing.T) {
	t.Parallel()
	m, err := Parse([]byte(legacyExamples[2].JSON))
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != core.CurrentIntentVersion || len(m.Preferences.TextureDescriptions) != 3 {
		t.Fatalf("semantic contract not preserved: %+v", m)
	}
	if len(m.Unsupported) == 0 || m.Unsupported[0].Text != "no abstract drone" {
		t.Fatalf("unsupported requirement missing: %+v", m.Unsupported)
	}
	if len(m.Preferences.Moods) != 2 || m.Preferences.Moods[1].Influence != core.InfluenceNegative {
		t.Fatalf("mood negation lost: %+v", m.Preferences.Moods)
	}
	if len(m.Preferences.TextureDescriptions[0].Evidence) == 0 {
		t.Fatal("texture evidence missing")
	}
	if len(m.References) != 0 || len(m.InferredAnchors) != 2 || m.InferredAnchors[0].Reference.Query != "Four Tet - Two Thousand and Seventeen" || m.InferredAnchors[0].Reference.Evidence[0].Explicit {
		t.Fatalf("seedless prompt did not separate its inferred starting points: refs=%+v anchors=%+v", m.References, m.InferredAnchors)
	}
	if len(m.EssentialCriteria) != 1 || m.EssentialCriteria[0].Value != "ambient electronic" {
		t.Fatalf("essential category missing: %+v", m.EssentialCriteria)
	}
}

func TestSemanticValidationRejectsNegativeRequiredTrack(t *testing.T) {
	t.Parallel()
	wire := Wire{
		RequiredTracks: []WireReference{{Kind: "track", Value: "A - B", Influence: "negative", Explicit: true, Span: "not A - B"}},
		Mode:           "similar", TotalCount: 10, AudioWeight: 0.5, CooccurrenceWeight: 0.5,
	}
	if err := wire.ToCore().Validate(); err == nil {
		t.Fatal("negative required track passed semantic validation")
	}
}

func TestParseForPromptRejectsInventedExplicitReference(t *testing.T) {
	t.Parallel()
	wire := legacyExamples[3].JSON
	wire = strings.Replace(wire, `"references":[]`, `"references":[{"kind":"artist","value":"Electronic","influence":"positive","explicit":true,"span":"electronic"}]`, 1)
	if _, err := ParseForPrompt([]byte(wire), "electronic music"); err == nil {
		t.Fatal("model-invented explicit artist passed source validation")
	}
	if _, err := ParseForPrompt([]byte(legacyExamples[0].JSON), legacyExamples[0].Prompt); err != nil {
		t.Fatalf("grounded explicit reference rejected: %v", err)
	}
}

func TestParseForPromptRequiresEssentialCategoryAcrossQualifiedPrompts(t *testing.T) {
	for _, prompt := range []string{"electronic music", "electronic music, no rock", "electronic music with some rock influence"} {
		t.Run(prompt, func(t *testing.T) {
			raw := []byte(legacyExamples[3].JSON)
			var wire Wire
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			wire.References = nil
			wire.EssentialCriteria = nil
			changed, _ := json.Marshal(wire)
			if _, err := ParseForPrompt(changed, prompt); err == nil || !strings.Contains(err.Error(), "defining category") {
				t.Fatalf("dropped essential category accepted: %v", err)
			}
		})
	}
}

func TestParseForPromptPreservesExclusionAndDeliberateInfluence(t *testing.T) {
	base := func() Wire {
		var wire Wire
		if err := json.Unmarshal([]byte(legacyExamples[3].JSON), &wire); err != nil {
			t.Fatal(err)
		}
		return wire
	}
	excluded := base()
	excluded.Styles = []WirePreference{{Value: "electronic", Influence: "positive", Explicit: true, Span: "electronic"}}
	raw, _ := json.Marshal(excluded)
	if _, err := ParseForPrompt(raw, "electronic music, no rock"); err == nil || !strings.Contains(err.Error(), "style exclusion") {
		t.Fatalf("dropped no-rock meaning accepted: %v", err)
	}
	hybrid := base()
	raw, _ = json.Marshal(hybrid)
	if _, err := ParseForPrompt(raw, "electronic music with some rock influence"); err == nil || !strings.Contains(err.Error(), "style influence") {
		t.Fatalf("dropped rock influence accepted: %v", err)
	}
}

func TestParseForPromptRequiresBothCategoryJourneyStages(t *testing.T) {
	raw := []byte(legacyExamples[3].JSON)
	var wire Wire
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	wire.References = nil
	wire.Mode = "journey"
	wire.EssentialCriteria = []WireCriterion{{Kind: "style", Value: "electronic", Scope: "journey_start", Span: "electronic"}}
	changed, _ := json.Marshal(wire)
	if _, err := ParseForPrompt(changed, "from electronic to rock"); err == nil || !strings.Contains(err.Error(), "journey_end") {
		t.Fatalf("dropped journey destination accepted: %v", err)
	}
}

func TestParseFencedAndProseWrapped(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"```json\n{\"seeds\":[\"Air\"],\"mode\":\"similar\",\"count\":15,\"creativity\":0.5,\"noise\":0.1,\"lookback\":3,\"exclude_artists\":[],\"no_repeat_artist\":true,\"notes\":\"n\"}\n```",
		"Here is the intent: {\"seeds\":[\"Air\"],\"mode\":\"similar\",\"count\":15,\"creativity\":0.5,\"noise\":0.1,\"lookback\":3,\"exclude_artists\":[],\"no_repeat_artist\":true,\"notes\":\"n\"} — hope that helps!",
	} {
		m, err := Parse([]byte(raw))
		if err != nil {
			t.Fatalf("Parse(%q): %v", raw, err)
		}
		if len(m.Seeds.Queries) != 1 || m.Seeds.Queries[0] != "Air" || m.Count != 15 {
			t.Fatalf("bad extract from %q: %+v", raw, m)
		}
	}
}

func TestParseClampsAndDefaultsViaNormalized(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"seeds":[""],"mode":"weird","count":9999,"creativity":5,"noise":-1,"lookback":50,"exclude_artists":[],"no_repeat_artist":false,"notes":""}`)
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Count != core.MaxCount || m.Lookback != core.MaxLookback {
		t.Fatalf("not clamped: %+v", m)
	}
	if m.Creativity != 1 || m.Noise != 0 {
		t.Fatalf("floats not clamped: %v/%v", m.Creativity, m.Noise)
	}
	if len(m.Seeds.Queries) != 0 {
		t.Fatalf("blank seed not dropped: %#v", m.Seeds.Queries)
	}
	// "weird" mode with 0 seeds -> Normalized picks "similar"
	if m.Mode != core.ModeSimilar {
		t.Fatalf("mode = %q", m.Mode)
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "no json here", "{ not valid json ", `{"seeds": [1,2,3]}`} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Fatalf("expected error for %q", raw)
		}
	}
}

func TestFewShotExamplesAreValid(t *testing.T) {
	t.Parallel()
	for i, ex := range legacyExamples {
		var w Wire
		if err := json.Unmarshal([]byte(ex.JSON), &w); err != nil {
			t.Fatalf("few-shot %d is not valid JSON: %v", i, err)
		}
		if w.Mode != "similar" && w.Mode != "journey" {
			t.Fatalf("few-shot %d bad mode %q", i, w.Mode)
		}
		if _, err := Parse([]byte(ex.JSON)); err != nil {
			t.Fatalf("few-shot %d does not Parse: %v", i, err)
		}
	}
	if SystemPrompt == "" {
		t.Fatal("empty system prompt")
	}
}

func TestGBNFMentionsEveryKey(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"references", "inferred_anchors", "required_tracks", "essential_criteria", "styles", "moods", "instrumentation", "vocal_preference", "textures", "hard_constraints", "unsupported_requirements", "mode", "journey_waypoints", "energy_trajectory", "total_count", "audio_weight", "cooccurrence_weight", "discovery", "artist_diversity", "transition_smoothness", "notes"} {
		if !contains(GBNF, `\"`+key+`\":`) {
			t.Fatalf("GBNF is missing key %q", key)
		}
	}
}

func TestGBNFBoundsWhitespaceAndScalarExpansion(t *testing.T) {
	for _, rule := range []string{
		`ws ::= [ \t\n]{0,8}`,
		`[^"\\\x00-\x1F]`,
		`int ::= "-"? ("0" | [1-9] [0-9]{0,3})`,
		`("." [0-9]{1,6})?`,
	} {
		if !contains(GBNF, rule) {
			t.Fatalf("missing bounded JSON rule: %s", rule)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
