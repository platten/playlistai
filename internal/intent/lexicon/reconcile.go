package lexicon

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/platten/playlistai/internal/core"
)

// Reconcile gives unambiguous source facts precedence over conflicting model
// fields. Unknown model interpretations outside those occurrences survive.
// It never resolves an entity or asserts that a musical property was measured.
func Reconcile(intent core.MusicIntent, extracted core.IntentTranslation) core.MusicIntent {
	refs := func(in []core.IntentReference) []core.IntentReference {
		out := make([]core.IntentReference, 0, len(in))
		for _, r := range in {
			if !Owned(r.Query, r.Evidence, extracted.Atoms) {
				out = append(out, r)
			}
		}
		return out
	}
	intent.References = refs(intent.References)
	intent.Journey.Waypoints = refs(intent.Journey.Waypoints)
	// Similarity references are not mandatory output tracks. A model may not
	// promote the same source mention into an include instruction or endpoint.
	intent.RequiredTracks = refs(intent.RequiredTracks)
	if intent.Start != nil && Owned(intent.Start.Query, intent.Start.Evidence, extracted.Atoms) {
		intent.Start = nil
	}
	if intent.Destination != nil && Owned(intent.Destination.Query, intent.Destination.Evidence, extracted.Atoms) {
		intent.Destination = nil
	}
	prefs := func(in []core.IntentPreference) []core.IntentPreference {
		out := make([]core.IntentPreference, 0, len(in))
		for _, p := range in {
			if !Owned(p.Value, p.Evidence, extracted.Atoms) {
				out = append(out, p)
			}
		}
		return out
	}
	intent.Preferences.Genres = prefs(intent.Preferences.Genres)
	intent.Preferences.Styles = prefs(intent.Preferences.Styles)
	intent.Preferences.Moods = prefs(intent.Preferences.Moods)
	intent.Preferences.Instrumentation = prefs(intent.Preferences.Instrumentation)
	intent.Preferences.TextureDescriptions = prefs(intent.Preferences.TextureDescriptions)
	vocals := intent.Preferences.VocalPreferences
	if len(vocals) == 0 && intent.Preferences.VocalPreference != nil {
		vocals = []core.IntentPreference{*intent.Preferences.VocalPreference}
	}
	intent.Preferences.VocalPreferences = prefs(vocals)
	intent.Preferences.VocalPreference = nil
	criteria := make([]core.MusicalCriterion, 0, len(intent.EssentialCriteria))
	for _, c := range intent.EssentialCriteria {
		if !Owned(c.Value, c.Evidence, extracted.Atoms) {
			criteria = append(criteria, c)
		}
	}
	intent.EssentialCriteria = criteria
	constraints := make([]core.HardConstraint, 0, len(intent.HardConstraints))
	for _, c := range intent.HardConstraints {
		if c.Kind == "require_artist" {
			continue // rebuilt only from an attached affirmative output-domain atom
		}
		if c.Kind != "" && c.Value != "" && (strings.HasPrefix(c.Kind, "require_") || c.Kind == "journey" || c.Kind == "journey_order" || c.Kind == "waypoint_order" || c.Kind == "transition_order" || !Owned(c.Value, c.Evidence, extracted.Atoms)) {
			constraints = append(constraints, c)
		}
	}
	intent.HardConstraints = constraints
	unsupported := make([]core.UnsupportedRequirement, 0, len(intent.Unsupported))
	for _, u := range intent.Unsupported {
		if !Owned(u.Text, u.Evidence, extracted.Atoms) {
			unsupported = append(unsupported, u)
		}
	}
	intent.Unsupported = unsupported
	// Calendar requirements must have source evidence. A model may understand
	// a literal date phrase beyond the dictionary, but cannot invent an era.
	periods := make([]core.TemporalRequirement, 0, len(intent.Temporal))
	for _, p := range intent.Temporal {
		if !Owned("", p.Evidence, extracted.Atoms) {
			if grounded, ok := groundedModelPeriod(p, intent.OriginalDescription, extracted.Atoms); ok {
				periods = append(periods, grounded)
			}
		}
	}
	intent.Temporal = periods
	intent.DurationSeconds = 0
	intent.DurationToleranceSeconds = 0
	intent.TrackCountExplicit = false
	hasCount, hasDuration := false, false
	for _, a := range extracted.Atoms {
		switch a.Kind {
		case "count":
			intent.Controls.TotalTrackCount, _ = strconv.Atoi(a.Value)
			intent.TrackCountExplicit = true
			hasCount = true
		case "duration":
			intent.DurationSeconds, _ = strconv.Atoi(a.Value)
			intent.DurationToleranceSeconds = core.DefaultDurationToleranceSeconds
			hasDuration = true
		case "duration_range":
			intent.Unsupported = append(intent.Unsupported, core.UnsupportedRequirement{Text: a.Value, Reason: "A duration range is preserved, but the current playlist duration control requires one target duration.", Evidence: a.Evidence})
			hasDuration = true
		case "temporal":
			var basis string
			var first, last int
			parts := strings.Split(a.Value, ":")
			if len(parts) == 3 {
				basis = parts[0]
				first, _ = strconv.Atoi(parts[1])
				last, _ = strconv.Atoi(parts[2])
				intent.Temporal = append(intent.Temporal, core.TemporalRequirement{Basis: basis, StartYear: first, EndYear: last, Scope: a.Scope, Evidence: a.Evidence})
			}
		case "temporal_exclusion":
			intent.Unsupported = append(intent.Unsupported, core.UnsupportedRequirement{Text: a.Evidence[0].Text, Reason: "The requested period exclusion is preserved; the current calendar filter supports positive intervals only.", Evidence: a.Evidence})
		case "temporal_alternatives":
			intent.Unsupported = append(intent.Unsupported, core.UnsupportedRequirement{Text: a.Evidence[0].Text, Reason: "Separate year alternatives are preserved; the current calendar filter cannot enforce a union of distinct years.", Evidence: a.Evidence})
		case "relative_energy":
			intent.Unsupported = append(intent.Unsupported, core.UnsupportedRequirement{Text: a.Evidence[0].Text, Reason: "The relative energy preference is preserved; comparison with the reference requires compatible measured energy evidence.", Evidence: a.Evidence})
		case "reference_era":
			intent.Unsupported = append(intent.Unsupported, core.UnsupportedRequirement{Text: a.Evidence[0].Text, Reason: "The relative artist era is preserved; a supported career-period reference is needed before it can constrain retrieval.", Evidence: a.Evidence})
		case "required_track":
			intent.RequiredTracks = append(intent.RequiredTracks, core.IntentReference{Kind: core.ReferenceTrack, Query: a.Value, Influence: core.InfluencePositive, Evidence: a.Evidence})
		case "require_artist":
			intent.HardConstraints = append(intent.HardConstraints, core.HardConstraint{Kind: "require_artist", Value: a.Value, Evidence: a.Evidence})
			intent.References = append(intent.References, core.IntentReference{Kind: core.ReferenceArtist, Query: a.Value, Influence: core.InfluencePositive, Evidence: a.Evidence})
		case "entity_mention":
			keptReferences := intent.References[:0]
			for _, r := range intent.References {
				if !negativeCandidateOwnsReference(intent.OriginalDescription, a, r) {
					keptReferences = append(keptReferences, r)
				}
			}
			intent.References = keptReferences
			if !hasCandidateInterpretation(intent, a) {
				kept := intent.References[:0]
				for _, r := range intent.References {
					if r.Influence != core.Influence(a.Polarity) || !wordsContain(a.Value, r.Query) {
						kept = append(kept, r)
					}
				}
				intent.References = kept
				intent.References = append(intent.References, candidateReference(a))
			}
			applyCandidateRole(&intent, a)
		case "artist", "track", "album", "start", "destination":
			kind := core.ReferenceKind(a.Kind)
			if a.Kind == "start" || a.Kind == "destination" {
				kind = core.ReferenceArtist
				if _, _, ok := core.QualifiedReferenceParts(a.Value); ok {
					kind = core.ReferenceTrack
					for _, e := range a.Evidence {
						if strings.Contains(strings.ToLower(e.Text), "album ") {
							kind = core.ReferenceAlbum
						}
					}
				}
			}
			r := core.IntentReference{Kind: kind, Query: a.Value, Influence: core.InfluencePositive, Evidence: a.Evidence}
			intent.References = append(intent.References, r)
			if a.Scope == "journey_via" {
				intent.Journey.Waypoints = append(intent.Journey.Waypoints, r)
			}
			if a.Kind == "start" {
				intent.Start = &r
				intent.Mode = core.ModeJourney
				intent.Journey.Waypoints = append(intent.Journey.Waypoints, r)
			}
			if a.Kind == "destination" {
				intent.Destination = &r
				intent.Mode = core.ModeJourney
				intent.Journey.Waypoints = append(intent.Journey.Waypoints, r)
			}
		case "exclude_artist":
			intent.HardConstraints = append(intent.HardConstraints, core.HardConstraint{Kind: "exclude_artist", Value: a.Value, Supported: true, Evidence: a.Evidence})
			intent.References = append(intent.References, core.IntentReference{Kind: core.ReferenceArtist, Query: a.Value, Influence: core.InfluenceNegative, Evidence: a.Evidence})
		case "spacing":
			intent.HardConstraints = append(intent.HardConstraints, core.HardConstraint{Kind: "no_back_to_back_artist", Value: "true", Supported: true, Evidence: a.Evidence})
		case "energy":
			n, _ := strconv.ParseFloat(a.Value, 64)
			pos := 0.0
			if a.Scope == "journey_via" {
				pos = .5
			}
			if a.Scope == "journey_end" {
				pos = 1
			}
			if a.Scope == "journey_start" {
				intent.Journey.EnergyTrajectory = nil
			}
			intent.Journey.EnergyTrajectory = append(intent.Journey.EnergyTrajectory, core.EnergyPoint{Position: pos, Energy: n})
			intent.Mode = core.ModeJourney
		default:
			if !musicalKind(a.Kind) || a.Kind == "activity" {
				continue
			}
			p := core.IntentPreference{Value: a.Value, Influence: core.Influence(a.Polarity), Explicit: true, Evidence: a.Evidence, ConceptID: a.ConceptID, Scope: a.Scope, Strength: a.Strength, Degree: a.Degree, Group: a.Group}
			switch a.Kind {
			case "genre":
				intent.Preferences.Genres = append(intent.Preferences.Genres, p)
			case "style":
				intent.Preferences.Styles = append(intent.Preferences.Styles, p)
			case "mood":
				intent.Preferences.Moods = append(intent.Preferences.Moods, p)
			case "instrumentation":
				intent.Preferences.Instrumentation = append(intent.Preferences.Instrumentation, p)
			case "texture":
				intent.Preferences.TextureDescriptions = append(intent.Preferences.TextureDescriptions, p)
			case "vocal":
				intent.Preferences.VocalPreferences = append(intent.Preferences.VocalPreferences, p)
			}
			if a.Polarity == "positive" && (a.Strength == "essential" || a.Strength == "required") {
				intent.EssentialCriteria = append(intent.EssentialCriteria, core.MusicalCriterion{Kind: a.Kind, Value: a.Value, Scope: a.Scope, Evidence: a.Evidence, ConceptID: a.ConceptID, Strength: a.Strength, Group: a.Group})
			}
			if a.Polarity == "negative" && a.Strength == "required" && (a.Scope == "" || a.Scope == "playlist") {
				kind := "exclude_" + a.Kind
				if a.Kind == "genre" {
					kind = "exclude_style"
				}
				if a.Kind == "vocal" {
					kind = "exclude_vocal"
				}
				intent.HardConstraints = append(intent.HardConstraints, core.HardConstraint{Kind: kind, Value: a.Value, Evidence: a.Evidence})
			}
			if a.Kind == "vocal" && a.Value == "instrumental" && a.Strength == "required" && (a.Scope == "" || a.Scope == "playlist") {
				for _, e := range a.Evidence {
					if strings.Contains(strings.ToLower(e.Text), "no ") || strings.Contains(strings.ToLower(e.Text), "without ") {
						intent.HardConstraints = append(intent.HardConstraints, core.HardConstraint{Kind: "exclude_vocals", Value: "vocals", Evidence: a.Evidence})
						break
					}
				}
			}
		}
	}
	if hasDuration && !hasCount {
		intent.Controls.TotalTrackCount = core.DefaultCount
	}
	if len(intent.Preferences.VocalPreferences) > 0 {
		first := intent.Preferences.VocalPreferences[0]
		intent.Preferences.VocalPreference = &first
	}
	if intent.Mode == core.ModeJourney && intent.Start == nil && intent.Destination == nil && len(intent.Journey.Waypoints) == 0 && len(intent.Journey.EnergyTrajectory) == 0 && len(core.JourneyCriteria(intent.EssentialCriteria)) == 0 {
		intent.Mode = core.ModeSimilar
	}
	copy := extracted
	copy.Repairs = append([]string{"Explicit source facts take precedence over conflicting model fields."}, extracted.Repairs...)
	intent.Translation = &copy
	return intent
}

// Owned reports whether an existing field conflicts with an occurrence that
// was extracted independently. Exact occurrence evidence and the value must
// overlap; an unrelated unknown phrase with a broad source span survives.
func Owned(value string, evidence []core.SourceEvidence, atoms []core.IntentAtom) bool {
	for _, a := range atoms {
		if a.Kind == "count" || a.Kind == "entity_mention" {
			continue
		}
		matchedValue := wordsContain(value, a.Value) || wordsContain(a.Value, value)
		for _, e := range evidence {
			for _, source := range a.Evidence {
				if knownOccurrence(e) && knownOccurrence(source) && (e.End <= source.Start || e.Start >= source.End) {
					continue // equal wording at another occurrence does not own this field
				}
				if strings.TrimSpace(e.Text) != "" && wordsContain(e.Text, source.Text) && wordsContain(source.Text, e.Text) {
					return true // a complete, exact occurrence has a protected role
				}
			}
		}
		if artist, title, ok := core.QualifiedReferenceParts(value); ok {
			if aa, tt, qualified := core.QualifiedReferenceParts(a.Value); qualified {
				matchedValue = matchedValue || wordsContain(artist, aa) && wordsContain(aa, artist) && wordsContain(title, tt) && wordsContain(tt, title)
			}
		}
		if !matchedValue {
			for _, e := range a.Evidence {
				matchedValue = matchedValue || wordsContain(value, e.Text) || wordsContain(e.Text, value)
			}
		}
		if !matchedValue {
			continue
		}
		if len(evidence) == 0 {
			return true // replace an ungrounded duplicate with actual source facts
		}
		for _, e := range evidence {
			for _, source := range a.Evidence {
				if knownOccurrence(e) && knownOccurrence(source) {
					if e.Start < source.End && e.End > source.Start {
						return true
					}
					continue
				}
				if wordsContain(e.Text, source.Text) || wordsContain(source.Text, e.Text) {
					return true
				}
			}
		}
	}
	return false
}

func knownOccurrence(e core.SourceEvidence) bool { return e.Start >= 0 && e.End > e.Start }
func wordsContain(text, value string) bool {
	words := func(s string) string {
		s = strings.Map(func(r rune) rune {
			if unicode.Is(unicode.Mn, r) {
				return -1
			}
			return r
		}, norm.NFKD.String(s))
		return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }), " ")
	}
	v := words(value)
	return v != "" && strings.Contains(" "+words(text)+" ", " "+v+" ")
}

// FactsMessage is intentionally compact; the full versioned evidence remains
// in the local translation snapshot rather than consuming model context.
func FactsMessage(x core.IntentTranslation) string {
	if len(x.Atoms) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nProtected source facts. Copy span only from the quoted source text, never from a label or normalized value. A similarity reference does not require that artist in the output.\n")
	for _, a := range x.Atoms {
		if a.Kind == "entity_mention" {
			fmt.Fprintf(&b, "Possible whole artist mention, not a locked identity or list: value=%q; source=%q. Its %s %s role is %s. Preserve the full name unless the request clearly lists separate artists.\n", a.Value, a.Evidence[0].Text, a.Polarity, a.Scope, a.Strength)
			continue
		}
		if a.Kind == "count" {
			fmt.Fprintf(&b, "Track count: %s; source=%q\n", a.Value, a.Evidence[0].Text)
			continue
		}
		if a.Kind == "duration" {
			fmt.Fprintf(&b, "Duration in seconds: %s (not a track count); source=%q\n", a.Value, a.Evidence[0].Text)
			continue
		}
		kind := a.Kind
		if kind == "artist" || kind == "album" || kind == "track" {
			kind = "similarity reference (" + kind + ")"
		}
		fmt.Fprintf(&b, "%s=%q; %s, %s, %s; source=%q", kind, a.Value, a.Polarity, a.Scope, a.Strength, a.Evidence[0].Text)
		if a.Degree != "" && a.Degree != "plain" {
			fmt.Fprintf(&b, " | %s", a.Degree)
		}
		if a.Group != "" {
			b.WriteString("; alternative")
		}
		b.WriteByte('\n')
	}
	return b.String()
}
