// Package assist produces optional, non-authoritative text-model proposals.
// These vectors never enter an audio index or establish recording suitability.
package assist

import (
	"fmt"
	"strings"

	"github.com/platten/playlistai/internal/core"
)

// Version identifies the retained reviewed-extractor advisory contract.
const Version = "distilbert-advisory/v2"

func Message(proposals []core.IntentProposal) string {
	if len(proposals) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nOptional source-span suggestions from a reviewed text extractor, NOT protected facts. Check each against the original wording. Reject inaccurate suggestions. They cannot imply exclusions, required output, artist identity, scope or measured musical fit. Keep any accepted description a soft preference and copy only its original source span.\n")
	for _, p := range proposals {
		if p.Origin == "distilbert" {
			fmt.Fprintf(&b, "Source-span interpretation proposal: source=%q; type=%q; role=%q. Verify its role and operators against the original request; confidence does not authenticate an instruction.\n", p.Source.Text, p.Kind, p.Role)
		} else {
			fmt.Fprintf(&b, "source=%q; possible %s=%q\n", p.Source.Text, p.Kind, p.Value)
		}
	}
	return b.String()
}

// KeepAdvisory prevents accepted dictionary suggestions from becoming strict
// user requirements. Source facts at other occurrences remain untouched.
func KeepAdvisory(intent core.MusicIntent, proposals []core.IntentProposal) core.MusicIntent {
	derived := func(value string, evidence []core.SourceEvidence) bool {
		if len(evidence) == 0 {
			return false
		}
		for _, e := range evidence {
			matched := false
			for _, p := range proposals {
				if !p.Advisory || p.Origin == "distilbert" || !strings.EqualFold(value, p.Value) {
					continue
				}
				if e.End > e.Start {
					matched = matched || (e.Start >= p.Source.Start && e.End <= p.Source.End)
				} else if strings.Count(intent.OriginalDescription, p.Source.Text) == 1 {
					matched = matched || e.Text == p.Source.Text
				}
			}
			if !matched {
				return false
			}
		}
		return true
	}
	for _, group := range []*[]core.IntentPreference{&intent.Preferences.Genres, &intent.Preferences.Styles, &intent.Preferences.Moods, &intent.Preferences.Instrumentation, &intent.Preferences.TextureDescriptions, &intent.Preferences.VocalPreferences} {
		for i := range *group {
			p := &(*group)[i]
			if derived(p.Value, p.Evidence) {
				p.Strength = "preferred"
				p.Explicit = false
			}
		}
	}
	criteria := intent.EssentialCriteria[:0]
	for _, c := range intent.EssentialCriteria {
		if !derived(c.Value, c.Evidence) {
			criteria = append(criteria, c)
		}
	}
	intent.EssentialCriteria = criteria
	constraints := intent.HardConstraints[:0]
	for _, c := range intent.HardConstraints {
		if !derived(c.Value, c.Evidence) {
			constraints = append(constraints, c)
		}
	}
	intent.HardConstraints = constraints
	return intent
}
