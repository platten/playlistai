package schema

import (
	"regexp"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
)

// Reconcile the same source snapshot before validation, not after a failed
// model completion has already discarded a literal name or instruction.
func reconcileSource(w *Wire, snapshot *core.IntentTranslation, prompt string) {
	discardUngroundedProposals(w, snapshot, prompt)
	m := lexicon.Reconcile(w.ToCore(), *snapshot)
	span := func(e []core.SourceEvidence) string {
		if len(e) > 0 {
			return e[0].Text
		}
		return ""
	}
	refs := func(in []core.IntentReference) []WireReference {
		out := make([]WireReference, 0, len(in))
		for _, r := range in {
			out = append(out, WireReference{Kind: string(r.Kind), Value: r.Query, Influence: string(r.Influence), Explicit: true, Span: span(r.Evidence)})
		}
		return out
	}
	prefs := func(in []core.IntentPreference) []WirePreference {
		out := make([]WirePreference, 0, len(in))
		for _, p := range in {
			out = append(out, WirePreference{Value: p.Value, Influence: string(p.Influence), Explicit: p.Explicit, Span: span(p.Evidence), Scope: p.Scope, Strength: p.Strength, ConceptID: p.ConceptID, Degree: p.Degree, Group: p.Group})
		}
		return out
	}
	w.References = refs(m.References)
	w.RequiredTracks = refs(m.RequiredTracks)
	w.JourneyWaypoints = refs(m.Journey.Waypoints)
	w.Destination = nil
	if m.Destination != nil {
		w.Destination = refs([]core.IntentReference{*m.Destination})
	}
	w.Genres = prefs(m.Preferences.Genres)
	w.Styles = prefs(m.Preferences.Styles)
	w.Moods = prefs(m.Preferences.Moods)
	w.Instrumentation = prefs(m.Preferences.Instrumentation)
	w.Textures = prefs(m.Preferences.TextureDescriptions)
	w.VocalPreference = WirePreference{}
	if m.Preferences.VocalPreference != nil {
		w.VocalPreference = prefs([]core.IntentPreference{*m.Preferences.VocalPreference})[0]
	}
	w.EssentialCriteria = nil
	for _, c := range m.EssentialCriteria {
		w.EssentialCriteria = append(w.EssentialCriteria, WireCriterion{Kind: c.Kind, Value: c.Value, Scope: c.Scope, Span: span(c.Evidence), Strength: c.Strength, Group: c.Group, ConceptID: c.ConceptID})
	}
	w.HardConstraints = nil
	for _, c := range m.HardConstraints {
		w.HardConstraints = append(w.HardConstraints, WireConstraint{Kind: c.Kind, Value: c.Value, Span: span(c.Evidence)})
	}
	w.Unsupported = nil
	for _, u := range m.Unsupported {
		w.Unsupported = append(w.Unsupported, WireUnsupported{Text: u.Text, Reason: u.Reason, Span: span(u.Evidence)})
	}
	w.Temporal = m.Temporal
	w.TotalCount = m.Controls.TotalTrackCount
	w.Mode = string(m.Mode)
	w.EnergyTrajectory = nil
	for _, p := range m.Journey.EnergyTrajectory {
		w.EnergyTrajectory = append(w.EnergyTrajectory, WireEnergy{Position: p.Position, Energy: p.Energy})
	}
}

// A fabricated source span cannot make a model suggestion a user requirement.
// Unknown wording actually present in the request remains open vocabulary.
// For dictionary facts the independent literal occurrence supplies provenance;
// other absent suggestions are discarded and the repair is recorded explicitly.
func discardUngroundedProposals(w *Wire, snapshot *core.IntentTranslation, prompt string) {
	removed := false
	invalid := func(value string, span *string) bool {
		if containsReferenceWords(prompt, *span) {
			return false
		}
		if containsReferenceWords(prompt, value) {
			return false // a fabricated span for real wording still needs validation
		}
		if lexicon.Owned(value, nil, snapshot.Atoms) {
			*span = "" // canonical rewrite will be replaced by its literal atom
			return false
		}
		if !containsReferenceWords(prompt, value) {
			removed = true
			return true
		}
		return false // a claimed but malformed source span still needs validation
	}
	for _, group := range []*[]WirePreference{&w.Genres, &w.Styles, &w.Moods, &w.Instrumentation, &w.Textures} {
		kept := (*group)[:0]
		for _, p := range *group {
			if group == &w.Genres && !containsReferenceWords(p.Span, p.Value) && !lexicon.Owned(p.Value, nil, snapshot.Atoms) {
				removed = true
				continue
			}
			if !invalid(p.Value, &p.Span) {
				kept = append(kept, p)
			}
		}
		*group = kept
	}
	if w.VocalPreference.Value != "" && invalid(w.VocalPreference.Value, &w.VocalPreference.Span) {
		w.VocalPreference = WirePreference{}
	}
	criteria := w.EssentialCriteria[:0]
	for _, c := range w.EssentialCriteria {
		if !containsReferenceWords(c.Span, c.Value) && !lexicon.Owned(c.Value, nil, snapshot.Atoms) {
			removed = true
			continue // an artist/full-prompt span does not authenticate its genres
		}
		if !invalid(c.Value, &c.Span) {
			criteria = append(criteria, c)
		}
	}
	w.EssentialCriteria = criteria
	required := w.RequiredTracks[:0]
	for _, r := range w.RequiredTracks {
		if r.Kind == "track" && requiredOutputCue(prompt, r) {
			required = append(required, r)
		} else {
			removed = true
		}
	}
	w.RequiredTracks = required
	destination := w.Destination[:0]
	for _, r := range w.Destination {
		if endpointOutputCue(prompt, r) {
			destination = append(destination, r)
		} else {
			removed = true
		}
	}
	w.Destination = destination
	for _, group := range []*[]WireReference{&w.References, &w.RequiredTracks, &w.Destination, &w.JourneyWaypoints} {
		kept := (*group)[:0]
		for _, r := range *group {
			if referenceGrounded(prompt, r) || !invalid(r.Value, &r.Span) {
				kept = append(kept, r)
			}
		}
		*group = kept
	}
	constraints := w.HardConstraints[:0]
	for _, c := range w.HardConstraints {
		if !invalid(c.Value, &c.Span) {
			constraints = append(constraints, c)
		}
	}
	w.HardConstraints = constraints
	if removed {
		const repair = "Discarded model-proposed requirements that had no source wording in the request."
		if !strings.Contains(strings.Join(snapshot.Repairs, "\n"), repair) {
			snapshot.Repairs = append(snapshot.Repairs, repair)
		}
		w.Notes = ""
	}
}

var includeOutputPattern = regexp.MustCompile(`(?i)\b(?:must include|include|including|make sure to include)\s+([^;.!?\n]+)`)
var negativeIncludePrefix = regexp.MustCompile(`(?i)\b(?:not|don't|don’t|never)\s*$`)

func requiredOutputCue(prompt string, r WireReference) bool {
	for _, loc := range includeOutputPattern.FindAllStringSubmatchIndex(prompt, -1) {
		if negativeIncludePrefix.MatchString(prompt[:loc[0]]) {
			continue
		}
		if referenceGrounded(prompt[loc[2]:loc[3]], r) {
			return true
		}
	}
	return false
}

func endpointOutputCue(prompt string, r WireReference) bool {
	for _, pos := range literalPositions(prompt, r.Value) {
		prefix := prompt[:pos[0]]
		if namedEndPrefix.MatchString(prefix) {
			return true
		}
		if journeyToPrefix.MatchString(prefix) && regexp.MustCompile(`(?i)\b(?:from|transitioning|moving|leading)\b`).MatchString(prefix) {
			return true
		}
	}
	return false
}
