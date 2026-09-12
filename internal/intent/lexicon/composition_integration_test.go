package lexicon_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/intent/schema"
	"github.com/platten/playlistai/internal/ports"
)

func TestComposedRequestsSurviveRulesAndModelReconciliation(t *testing.T) {
	ref := func(name string) schema.WireReference {
		return schema.WireReference{Kind: "artist", Value: name, Span: name, Influence: "positive", Explicit: true}
	}
	pref := func(value, span, polarity string) schema.WirePreference {
		return schema.WirePreference{Value: value, Span: span, Influence: polarity, Explicit: true}
	}
	tests := []struct {
		prompt string
		wire   schema.Wire
		check  func(*testing.T, core.MusicIntent)
	}{
		{"Some instrumental tracks, but vocals are fine too.", schema.Wire{VocalPreference: pref("instrumental", "Some instrumental", "positive")}, assertSoftInstrumental},
		{"Preferably instrumental music.", schema.Wire{}, assertSoftInstrumental},
		{"Not only instrumental music.", schema.Wire{}, assertSoftInstrumental},
		{"I don't want aggressive music; keep it warm.", schema.Wire{Moods: []schema.WirePreference{pref("aggressive", "I don't want aggressive", "negative")}}, assertNegativeAggressive},
		{"I do not need aggressive music.", schema.Wire{}, assertNegativeAggressive},
		{"Neither rock nor metal; give me soul instead.", schema.Wire{Genres: []schema.WirePreference{pref("rock", "Neither rock", "negative"), pref("metal", "Neither rock nor metal", "negative"), pref("soul", "soul", "positive")}}, func(t *testing.T, m core.MusicIntent) {
			for _, value := range []string{"rock", "metal"} {
				assertPreference(t, m.Preferences.Genres, value, core.InfluenceNegative)
				for _, c := range m.EssentialCriteria {
					if c.Value == value {
						t.Fatalf("excluded genre became essential: %+v", c)
					}
				}
			}
			assertPreference(t, m.Preferences.Genres, "soul", core.InfluencePositive)
		}},
		{"Neither Radiohead nor Coldplay; give me soul instead.", schema.Wire{}, func(t *testing.T, m core.MusicIntent) {
			for _, name := range []string{"Radiohead", "Coldplay"} {
				found := false
				for _, r := range m.References {
					if r.Query == name {
						if r.Influence != core.InfluenceNegative {
							t.Fatalf("excluded artist became positive: %+v", r)
						}
						found = true
					}
				}
				if !found {
					t.Fatalf("missing excluded artist %s: %+v", name, m.References)
				}
			}
		}},
		{"1 hour 30 minutes of ambient music.", schema.Wire{}, assertDuration(5400)},
		{"2 hours and 15 minutes of ambient music.", schema.Wire{}, assertDuration(8100)},
		{"Electronic tracks released between 1995 and 2003.", schema.Wire{Temporal: []core.TemporalRequirement{{Basis: "original_release", StartYear: 1995, EndYear: 2003, Scope: "playlist", Evidence: []core.SourceEvidence{{Text: "released between 1995 and 2003", Start: 18, End: 48, Explicit: true}}}}}, assertPeriod("original_release", 1995, 2003)},
		{"Classical pieces composed from 1820 to 1845.", schema.Wire{}, assertPeriod("composition", 1820, 1845)},
		{"Music like Florence and the Machine.", schema.Wire{References: []schema.WireReference{ref("Florence and the Machine")}}, assertReferences("Florence and the Machine")},
		{"Like Earth, Wind & Fire, but less disco.", schema.Wire{References: []schema.WireReference{ref("Earth, Wind & Fire")}}, assertReferences("Earth, Wind & Fire")},
		{"Music like Christian Löffler and Kiasmos.", schema.Wire{References: []schema.WireReference{ref("Christian Löffler"), ref("Kiasmos")}}, assertReferences("Christian Löffler", "Kiasmos")},
		{"Take me from Florence and the Machine to Aphex Twin.", schema.Wire{Mode: "journey", JourneyWaypoints: []schema.WireReference{ref("Florence and the Machine"), ref("Aphex Twin")}}, assertJourney("Florence and the Machine", "Aphex Twin")},
		{"Take me from Aphex Twin to Earth, Wind & Fire.", schema.Wire{Mode: "journey", JourneyWaypoints: []schema.WireReference{ref("Aphex Twin"), ref("Earth, Wind & Fire")}, Destination: []schema.WireReference{ref("Earth, Wind & Fire")}}, assertJourney("Aphex Twin", "Earth, Wind & Fire")},
		{"Take me from FLORENCE AND THE MACHINE to APHEX TWIN.", schema.Wire{}, assertJourney("FLORENCE AND THE MACHINE", "APHEX TWIN")},
		{"Neither Florence and the Machine nor Coldplay.", schema.Wire{HardConstraints: []schema.WireConstraint{{Kind: "exclude_artist", Value: "Florence and the Machine", Span: "Neither Florence and the Machine"}, {Kind: "exclude_artist", Value: "Coldplay", Span: "nor Coldplay"}}}, assertExcludedArtists("Florence and the Machine", "Coldplay")},
		{"No Earth, Wind & Fire.", schema.Wire{HardConstraints: []schema.WireConstraint{{Kind: "exclude_artist", Value: "Earth, Wind & Fire", Span: "No Earth, Wind & Fire"}}}, assertExcludedArtists("Earth, Wind & Fire")},
		{"I do not want music from the 1990s.", schema.Wire{Temporal: []core.TemporalRequirement{{Basis: "original_release", StartYear: 1990, EndYear: 1999, Scope: "playlist"}}}, assertExcludedPeriod},
		{"Don't include tracks from 1995 to 2003.", schema.Wire{}, assertExcludedPeriod},
		{"Take me from Boards of Canada to Aphex Twin.", schema.Wire{Mode: "journey", JourneyWaypoints: []schema.WireReference{ref("Boards of Canada"), ref("Aphex Twin")}, Destination: []schema.WireReference{ref("Aphex Twin")}}, func(t *testing.T, m core.MusicIntent) {
			if m.Start == nil || m.Start.Query != "Boards of Canada" || m.Destination == nil || m.Destination.Query != "Aphex Twin" {
				t.Fatalf("actual endpoints lost: start=%+v end=%+v", m.Start, m.Destination)
			}
			if m.Count != 20 || len(m.Journey.Waypoints) != 2 || m.Journey.Waypoints[0].Query != "Boards of Canada" || m.Journey.Waypoints[1].Query != "Aphex Twin" {
				t.Fatalf("journey totals/order changed: %+v", m)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.prompt, func(t *testing.T) {
			for _, backend := range []string{"rules", "schema"} {
				t.Run(backend, func(t *testing.T) {
					var m core.MusicIntent
					var err error
					if backend == "rules" {
						m, err = rules.New().Parse(context.Background(), ports.IntentInput{Prompt: tt.prompt})
					} else {
						w := tt.wire
						if w.Genres == nil {
							w.Genres = []schema.WirePreference{}
						}
						if w.Mode == "" {
							w.Mode = "similar"
						}
						w.TotalCount = 20
						raw, marshalErr := json.Marshal(w)
						if marshalErr != nil {
							t.Fatal(marshalErr)
						}
						m, err = schema.ParseForPrompt(raw, tt.prompt)
					}
					if err != nil {
						t.Fatal(err)
					}
					if err := m.Validate(); err != nil {
						t.Fatal(err)
					}
					if m.OriginalDescription != tt.prompt || m.Translation == nil {
						t.Fatal("source provenance lost")
					}
					for _, atom := range m.Translation.Atoms {
						for _, e := range atom.Evidence {
							if e.Start < 0 || e.End > len(tt.prompt) || tt.prompt[e.Start:e.End] != e.Text {
								t.Fatalf("invalid literal evidence: %+v", e)
							}
						}
					}
					tt.check(t, m)
				})
			}
		})
	}
}

func assertPreference(t *testing.T, preferences []core.IntentPreference, value string, polarity core.Influence) {
	t.Helper()
	found := false
	for _, p := range preferences {
		if p.Value == value {
			found = true
			if p.Influence != polarity {
				t.Fatalf("wrong polarity for %s: %+v", value, p)
			}
		}
	}
	if !found {
		t.Fatalf("missing %s %s: %+v", polarity, value, preferences)
	}
}

func assertSoftInstrumental(t *testing.T, m core.MusicIntent) {
	t.Helper()
	if core.WantsInstrumental(m) {
		t.Fatal("some/preferred instrumental became an all-vocals exclusion")
	}
	found := false
	for _, p := range m.Preferences.VocalRequests() {
		if p.Value == "instrumental" {
			found = true
			if p.Strength != "preferred" {
				t.Fatalf("instrumental lost softness: %+v", p)
			}
		}
	}
	if !found {
		t.Fatal("instrumental preference lost")
	}
}

func assertNegativeAggressive(t *testing.T, m core.MusicIntent) {
	t.Helper()
	assertPreference(t, m.Preferences.Moods, "aggressive", core.InfluenceNegative)
}

func assertDuration(seconds int) func(*testing.T, core.MusicIntent) {
	return func(t *testing.T, m core.MusicIntent) {
		t.Helper()
		if m.DurationSeconds != seconds || m.Count != 20 {
			t.Fatalf("duration/count changed: duration=%d count=%d", m.DurationSeconds, m.Count)
		}
	}
}

func assertPeriod(basis string, first, last int) func(*testing.T, core.MusicIntent) {
	return func(t *testing.T, m core.MusicIntent) {
		t.Helper()
		if len(m.Temporal) != 1 || m.Temporal[0].Basis != basis || m.Temporal[0].StartYear != first || m.Temporal[0].EndYear != last {
			t.Fatalf("period lost: %+v", m.Temporal)
		}
	}
}

func assertReferences(names ...string) func(*testing.T, core.MusicIntent) {
	return func(t *testing.T, m core.MusicIntent) {
		t.Helper()
		var got []string
		for _, r := range m.References {
			if r.Influence != core.InfluenceNegative {
				got = append(got, r.Query)
			}
		}
		if strings.Join(got, "|") != strings.Join(names, "|") {
			t.Fatalf("artist identity split/truncated: got=%q want=%q", got, names)
		}
	}
}

func assertJourney(start, end string) func(*testing.T, core.MusicIntent) {
	return func(t *testing.T, m core.MusicIntent) {
		t.Helper()
		if m.Start == nil || m.Start.Query != start || m.Destination == nil || m.Destination.Query != end || m.Count != 20 {
			t.Fatalf("required endpoints/count changed: start=%+v end=%+v count=%d", m.Start, m.Destination, m.Count)
		}
		if len(m.Journey.Waypoints) != 2 || m.Journey.Waypoints[0].Query != start || m.Journey.Waypoints[1].Query != end {
			t.Fatalf("whole-name journey order lost: %+v", m.Journey.Waypoints)
		}
	}
}

func assertExcludedArtists(names ...string) func(*testing.T, core.MusicIntent) {
	return func(t *testing.T, m core.MusicIntent) {
		t.Helper()
		got := map[string]bool{}
		for _, c := range m.HardConstraints {
			if c.Kind == "exclude_artist" {
				got[c.Value] = true
			}
		}
		if len(got) != len(names) {
			t.Fatalf("split or missing artist exclusions: %+v", got)
		}
		for _, name := range names {
			if !got[name] {
				t.Fatalf("missing full artist exclusion %q: %+v", name, got)
			}
		}
		for _, r := range m.References {
			if r.Influence != core.InfluenceNegative {
				t.Fatalf("negative-only request gained a positive reference: %+v", r)
			}
		}
	}
}

func assertExcludedPeriod(t *testing.T, m core.MusicIntent) {
	t.Helper()
	if len(m.Temporal) != 0 {
		t.Fatalf("excluded period became positive: %+v", m.Temporal)
	}
	for _, u := range m.Unsupported {
		if strings.Contains(u.Reason, "period exclusion") && len(u.Evidence) > 0 {
			return
		}
	}
	t.Fatalf("unrepresentable negative period was lost: %+v", m.Unsupported)
}

func TestAmbiguousEntityCandidateAcceptsCompleteGroundedModelList(t *testing.T) {
	const prompt = "Music like Alpha, Beta & Gamma."
	x := lexicon.Extract(prompt)
	if lexicon.Owned("Alpha", []core.SourceEvidence{{Text: "Alpha", Start: 11, End: 16, Explicit: true}}, x.Atoms) {
		t.Fatal("ambiguous mention acquired protected identity authority")
	}
	w := schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 12}
	for _, name := range []string{"Alpha", "Beta", "Gamma"} {
		w.References = append(w.References, schema.WireReference{Kind: "artist", Value: name, Span: name, Influence: "positive", Explicit: true})
	}
	raw, _ := json.Marshal(w)
	m, err := schema.ParseForPrompt(raw, prompt)
	if err != nil {
		t.Fatal(err)
	}
	assertReferences("Alpha", "Beta", "Gamma")(t, m)
	w.References = w.References[:1]
	raw, _ = json.Marshal(w)
	m, err = schema.ParseForPrompt(raw, prompt)
	if err != nil {
		t.Fatal(err)
	}
	assertReferences("Alpha, Beta & Gamma")(t, m)
}

func TestAmbiguousNegativeCandidatePreservesCompleteListAndExclusionRole(t *testing.T) {
	const prompt = "No Alpha, Beta & Gamma."
	w := schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 12}
	for _, name := range []string{"Alpha", "Beta", "Gamma"} {
		w.References = append(w.References, schema.WireReference{Kind: "artist", Value: name, Span: name, Influence: "negative", Explicit: true})
	}
	raw, _ := json.Marshal(w)
	m, err := schema.ParseForPrompt(raw, prompt)
	if err != nil {
		t.Fatal(err)
	}
	assertExcludedArtists("Alpha", "Beta", "Gamma")(t, m)
	for _, c := range m.HardConstraints {
		if c.Kind == "exclude_artist" && (len(c.Evidence) == 0 || !strings.Contains(c.Evidence[0].Text, "No ")) {
			t.Fatalf("exclusion cue lost: %+v", c)
		}
	}
}

func TestDurationRangesArePreservedWithoutInventingAnExactTarget(t *testing.T) {
	for _, prompt := range []string{"30-45 minutes of jazz", "1 to 2 hours of ambient music"} {
		m, err := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
		if err != nil {
			t.Fatal(err)
		}
		if m.DurationSeconds != 0 || len(m.Unsupported) != 1 || !strings.Contains(prompt, m.Unsupported[0].Text) {
			t.Fatalf("duration range collapsed or lost: %+v", m)
		}
	}
}

func TestModelCalendarWordingNeedsLiteralPositiveBoundsAndDateBasis(t *testing.T) {
	tests := []struct {
		prompt, text, basis string
		start, end          int
		want                bool
	}{
		{"Jazz recorded during 1998.", "recorded during 1998", "original_release", 1998, 1998, true},
		{"Start with jazz recorded during 1998.", "recorded during 1998", "original_release", 1998, 1998, true},
		{"Jazz not recorded during 1998.", "recorded during 1998", "original_release", 1998, 1998, false},
		{"Jazz released not in 1998.", "released not in 1998", "original_release", 1998, 1998, false},
		{"Jazz released after 1998.", "released after 1998", "original_release", 1998, 1998, false},
		{"Jazz recorded during 1998.", "recorded during 1998", "original_release", 1900, 1998, false},
		{"Jazz recorded during 1998.", "recorded during 1998", "composition", 1998, 1998, false},
		{"Jazz with a warm sound.", "recorded during 1998", "original_release", 1998, 1998, false},
		{"Jazz recorded in 1995 or 2003.", "recorded in 1995 or 2003", "original_release", 1995, 2003, false},
		{"Jazz recorded in 1995 or 2003.", "recorded in 1995", "original_release", 1995, 1995, false},
		{"Jazz recorded in 1995 and remastered in 2003.", "recorded in 1995 and remastered in 2003", "original_release", 1995, 2003, false},
		{"Jazz recorded in 1995 and remastered in 2003.", "recorded in 1995 and remastered in 2003", "original_release", 2003, 2003, false},
	}
	for _, tt := range tests {
		t.Run(tt.prompt+tt.basis, func(t *testing.T) {
			w := schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 20, Temporal: []core.TemporalRequirement{{Basis: tt.basis, StartYear: tt.start, EndYear: tt.end, Scope: "playlist", Evidence: []core.SourceEvidence{{Text: tt.text, Start: -1, End: -1, Explicit: true}}}}}
			raw, _ := json.Marshal(w)
			m, err := schema.ParseForPrompt(raw, tt.prompt)
			if err != nil {
				t.Fatal(err)
			}
			if (len(m.Temporal) == 1) != tt.want {
				t.Fatalf("wrong temporal preservation: %+v", m.Temporal)
			}
			if tt.want && strings.HasPrefix(tt.prompt, "Start") && m.Temporal[0].Scope != "journey_start" {
				t.Fatalf("temporal scope not source grounded: %+v", m.Temporal)
			}
		})
	}
}
