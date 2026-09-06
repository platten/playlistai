package schema

import (
	"encoding/json"
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
	for i, ex := range FewShot {
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
	for _, key := range []string{"seeds", "required_tracks", "mode", "count", "creativity", "noise", "lookback", "exclude_artists", "no_repeat_artist", "exclude_seed_artists", "notes"} {
		if !contains(GBNF, `\"`+key+`\":`) {
			t.Fatalf("GBNF is missing key %q", key)
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
