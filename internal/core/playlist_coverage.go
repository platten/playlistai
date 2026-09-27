package core

import (
	"fmt"
	"strings"
)

// Coverage is explicit source intent. Normalization never infers it from a
// saved prompt, so absent fields retain their original per-track semantics.
func (m MusicIntent) validateCoverageGroups() error {
	check := func(group, kind, scope string, positive bool, count *int) error {
		if group == "" {
			return nil
		}
		*count++
		if *count > MaxCount || strings.TrimSpace(group) != group || len(group) > 128 || kind != "genre" && kind != "style" || scope != "" && scope != "playlist" || !positive {
			return fmt.Errorf("intent: playlist coverage requires at most %d positive playlist genres and nonblank group identifiers", MaxCount)
		}
		return nil
	}
	count := 0
	for _, c := range m.EssentialCriteria {
		if err := check(c.CoverageGroup, c.Kind, c.Scope, true, &count); err != nil {
			return err
		}
	}
	count = 0
	groups := []struct {
		kind string
		list []IntentPreference
	}{{"genre", m.Preferences.Genres}, {"style", m.Preferences.Styles}, {"mood", m.Preferences.Moods}, {"instrumentation", m.Preferences.Instrumentation}, {"texture", m.Preferences.TextureDescriptions}, {"vocal", m.Preferences.VocalRequests()}}
	for _, group := range groups {
		for _, p := range group.list {
			if err := check(p.CoverageGroup, group.kind, p.Scope, p.Influence != InfluenceNegative, &count); err != nil {
				return err
			}
		}
	}
	if m.Translation != nil {
		count = 0
		for _, a := range m.Translation.Atoms {
			if err := check(a.CoverageGroup, a.Kind, a.Scope, a.Polarity == "positive", &count); err != nil {
				return err
			}
		}
	}
	return nil
}
