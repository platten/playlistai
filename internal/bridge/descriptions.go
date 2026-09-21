package bridge

import (
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
)

// Apply the user's interpretation after parsing, before artist lookup. Store it
// in the canonical intent so cached parses and saved playlist replay stay apart.
func applyDescriptionSelections(intent core.MusicIntent, selections []ResolutionSelection) core.MusicIntent {
	for _, selection := range selections {
		if !selection.KeepAsDescription {
			continue
		}
		intent = intent.Normalized() // copy slices and source evidence before editing
		matches := func(value string) bool {
			return strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(selection.Query))
		}
		kind, value, concept := "style", selection.Query, ""
		for _, atom := range lexicon.Extract(selection.Query).Atoms {
			switch atom.Kind {
			case "genre", "style", "mood", "texture", "instrumentation", "vocal":
				if len(atom.Evidence) == 1 && matches(atom.Evidence[0].Text) {
					kind, value, concept = atom.Kind, atom.Value, atom.ConceptID
				}
			}
		}
		added := make(map[string]bool)
		add := func(ref core.IntentReference, scope string) {
			key := scope + "\x00" + string(ref.Influence)
			if added[key] {
				return // an endpoint can also appear in references and waypoints
			}
			added[key] = true
			preference := core.IntentPreference{Value: value, ConceptID: concept, Influence: ref.Influence, Explicit: true, Evidence: ref.Evidence, Scope: scope, Strength: "preferred"}
			if intent.Translation != nil {
				for _, atom := range intent.Translation.Atoms {
					if matches(atom.Value) && atom.Scope == scope && (atom.Strength == "required" || atom.Strength == "essential") {
						preference.Strength = atom.Strength
					}
				}
			}
			for _, constraint := range intent.HardConstraints {
				if matches(constraint.Value) && (constraint.Kind == "require_artist" || constraint.Kind == "exclude_artist") {
					preference.Strength = "required"
				}
			}
			if preference.Strength != "preferred" && preference.Influence == core.InfluencePositive {
				intent.EssentialCriteria = append(intent.EssentialCriteria, core.MusicalCriterion{Kind: kind, Value: value, ConceptID: concept, Scope: scope, Strength: preference.Strength, Evidence: ref.Evidence})
			}
			switch kind {
			case "genre":
				intent.Preferences.Genres = append(intent.Preferences.Genres, preference)
			case "mood":
				intent.Preferences.Moods = append(intent.Preferences.Moods, preference)
			case "texture":
				intent.Preferences.TextureDescriptions = append(intent.Preferences.TextureDescriptions, preference)
			case "instrumentation":
				intent.Preferences.Instrumentation = append(intent.Preferences.Instrumentation, preference)
			case "vocal":
				intent.Preferences.VocalPreferences = append(intent.Preferences.VocalPreferences, preference)
			default:
				intent.Preferences.Styles = append(intent.Preferences.Styles, preference)
			}
		}
		isChosen := func(ref core.IntentReference) bool { return ref.Kind == core.ReferenceArtist && matches(ref.Query) }
		filter := func(refs []core.IntentReference, scope string) []core.IntentReference {
			out := make([]core.IntentReference, 0, len(refs))
			for _, ref := range refs {
				if !isChosen(ref) {
					out = append(out, ref)
					continue
				}
				// Source atoms retain endpoint scope even when the reference also
				// appears in the ordinary reference list.
				refScope := scope
				if intent.Start != nil && isChosen(*intent.Start) {
					refScope = "journey_start"
				} else if intent.Destination != nil && isChosen(*intent.Destination) {
					refScope = "journey_end"
				}
				if intent.Translation != nil {
					for _, atom := range intent.Translation.Atoms {
						if matches(atom.Value) && atom.Scope != "" {
							refScope = atom.Scope
							break
						}
					}
				}
				add(ref, refScope)
			}
			return out
		}
		intent.References = filter(intent.References, "playlist")
		intent.RequiredTracks = filter(intent.RequiredTracks, "playlist")
		intent.Journey.Waypoints = filter(intent.Journey.Waypoints, "journey_via")
		if intent.Start != nil && isChosen(*intent.Start) {
			add(*intent.Start, "journey_start")
			intent.Start = nil
		}
		if intent.Destination != nil && isChosen(*intent.Destination) {
			add(*intent.Destination, "journey_end")
			intent.Destination = nil
		}
		anchors := intent.InferredAnchors[:0]
		for _, anchor := range intent.InferredAnchors {
			if !isChosen(anchor.Reference) {
				anchors = append(anchors, anchor)
			}
		}
		intent.InferredAnchors = anchors
		constraints := intent.HardConstraints[:0]
		for _, constraint := range intent.HardConstraints {
			if matches(constraint.Value) {
				if constraint.Kind == "require_artist" {
					continue // represented by the required musical criterion above
				}
				if constraint.Kind == "exclude_artist" {
					constraint.Kind, constraint.Value = "exclude_"+kind, value
					if kind == "genre" {
						constraint.Kind = "exclude_style"
					}
					constraint.Supported, constraint.RuntimeEnforced = false, false
				}
			}
			constraints = append(constraints, constraint)
		}
		intent.HardConstraints = constraints
		if intent.Translation != nil {
			for i, atom := range intent.Translation.Atoms {
				if !matches(atom.Value) {
					continue
				}
				switch atom.Kind {
				case "artist", "entity_mention", "start", "destination", "require_artist", "exclude_artist":
					atom.Kind, atom.Value, atom.ConceptID = kind, value, concept
					atom.Grounding = nil
					if atom.Strength == "" {
						atom.Strength = "preferred"
					}
					intent.Translation.Atoms[i] = atom
				}
			}
			intent.Translation.Repairs = append(intent.Translation.Repairs, "Kept a proposed artist name as a musical description at the user's request.")
		}
		intent = intent.Normalized() // rebuild compatibility seeds and exclusions
	}
	return intent
}
