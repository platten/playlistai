package localcatalog

import (
	"context"
	"math"
	"strconv"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/musicconcepts"
)

// MusicalMetadata contains only unambiguous, validated tag values. Nil means
// unknown; conflicting sources remain in Annotations and are named in Conflicts.
// Descriptors retain their source scale and are not probabilities.
type MusicalMetadata struct {
	BPM             *float64
	OriginalYear    *int
	EditionYear     *int
	CompositionYear *int
	Key             string
	Descriptors     map[string]float64
	Conflicts       map[string]bool
	Annotations     []core.MetadataAnnotation
}

func (c *Catalog) TypedMetadata(ctx context.Context, id string) (MusicalMetadata, bool) {
	a := c.Annotations(ctx, id)
	return NormalizeMusicalMetadata(a), len(a) > 0
}

func NormalizeMusicalMetadata(annotations []core.MetadataAnnotation) MusicalMetadata {
	out := MusicalMetadata{Annotations: append([]core.MetadataAnnotation(nil), annotations...), Descriptors: map[string]float64{}, Conflicts: map[string]bool{}}
	values := map[string]float64{}
	var precise *float64
	for _, a := range annotations {
		kind := a.Kind
		if kind == "key" {
			key := normalizedMusicalKey(a.Value)
			if key != "" {
				if out.Key != "" && out.Key != key {
					out.Conflicts[kind] = true
				}
				out.Key = key
			}
			continue
		}
		var n float64
		var err error
		switch {
		case kind == "original_release_date" || kind == "edition_date" || kind == "composition_date":
			value := strings.TrimSpace(a.Value)
			if len(value) < 4 || len(value) > 4 && value[4] != '-' {
				continue
			}
			if strings.Trim(value[:4], "0123456789") != "" {
				continue
			}
			n, err = strconv.ParseFloat(value[:4], 64)
			if n < 1 || n > 9999 {
				continue
			}
		case kind == "tempo":
			n, err = strconv.ParseFloat(strings.TrimSpace(a.Value), 64)
			if n <= 0 || n > 1000 {
				continue
			}
		case strings.HasPrefix(kind, "acousticbrainz:"):
			n, err = strconv.ParseFloat(strings.TrimSpace(a.Value), 64)
			if a.Scale == "-100..100" {
				if n < -100 || n > 100 {
					continue
				}
			} else if a.Scale == "0..100" {
				if n < 0 || n > 100 {
					continue
				}
			} else {
				continue
			}
		default:
			continue
		}
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			continue
		}
		if old, ok := values[kind]; ok && old != n {
			// Rounded BPM and decimal FBPM can describe the same estimate.
			if kind != "tempo" || math.Round(old) != math.Round(n) || old != math.Trunc(old) && n != math.Trunc(n) {
				out.Conflicts[kind] = true
			}
		}
		values[kind] = n
		if kind == "tempo" && strings.EqualFold(a.SourceKey, "fbpm") {
			v := n
			precise = &v
		}
	}
	for kind, n := range values {
		if out.Conflicts[kind] {
			continue
		}
		switch kind {
		case "tempo":
			v := n
			out.BPM = &v
			if precise != nil {
				out.BPM = precise
			}
		case "original_release_date":
			v := int(n)
			out.OriginalYear = &v
		case "edition_date":
			v := int(n)
			out.EditionYear = &v
		case "composition_date":
			v := int(n)
			out.CompositionYear = &v
		default:
			out.Descriptors[kind] = n
		}
	}
	if out.Conflicts["key"] {
		out.Key = ""
	}
	return out
}

func normalizedMusicalKey(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	v = strings.ReplaceAll(strings.ReplaceAll(v, "♯", "#"), "♭", "b")
	v = strings.Join(strings.Fields(v), "")
	minor := strings.HasSuffix(v, "minor") || strings.HasSuffix(v, "min") || strings.HasSuffix(v, "m")
	for _, suffix := range []string{"major", "minor", "maj", "min", "m"} {
		if strings.HasSuffix(v, suffix) {
			v = strings.TrimSuffix(v, suffix)
			break
		}
	}
	if len(v) < 1 || len(v) > 2 || v[0] < 'a' || v[0] > 'g' || len(v) == 2 && v[1] != '#' && v[1] != 'b' {
		return ""
	}
	enharmonic := map[string]string{"db": "c#", "eb": "d#", "gb": "f#", "ab": "g#", "bb": "a#", "cb": "b", "fb": "e", "e#": "f", "b#": "c"}
	if canonical, ok := enharmonic[v]; ok {
		v = canonical
	}
	if minor {
		return v + " minor"
	}
	return v + " major"
}

func criterionPostingTerm(c core.MusicalCriterion) string {
	kind := c.Kind
	if kind == "style" {
		kind = "genre"
	}
	value := musicconcepts.Canonical(c.Kind, c.Value)
	if kind == "key" {
		value = normalizedMusicalKey(value)
	}
	return "@criterion:" + kind + ":" + normalizeUnicode(value)
}

func annotationCriterionEvidence(a []core.MetadataAnnotation, criterion core.MusicalCriterion) core.EvidenceState {
	if criterion.Kind == "composer" {
		// A performer, title, or album-level credit cannot establish who wrote
		// this recording. Every explicit track-composer value must agree.
		found := false
		for _, item := range a {
			if item.Kind != "composer" {
				continue
			}
			found = true
			if normalizeUnicode(item.Value) != normalizeUnicode(criterion.Value) {
				return core.EvidenceMismatch
			}
		}
		if found {
			return core.EvidenceMatch
		}
		return core.EvidenceUnknown
	}
	meta := NormalizeMusicalMetadata(a)
	if meta.Conflicts[criterion.Kind] {
		return core.EvidenceUnknown
	}
	if criterion.Kind == "key" {
		if key := normalizedMusicalKey(criterion.Value); key != "" && meta.Key == key {
			return core.EvidenceMatch
		}
		return core.EvidenceUnknown
	}
	for _, item := range a {
		if annotationMatches(item, criterion) {
			return core.EvidenceMatch
		}
	}
	return core.EvidenceUnknown
}

func annotationPostingTerms(a core.MetadataAnnotation) []string {
	c := core.MusicalCriterion{Kind: a.Kind, Value: a.Value}
	if !supportsAnnotationCriterion(c) {
		return nil
	}
	result := []string{criterionPostingTerm(c)}
	seen := map[string]bool{}
	var visit func(musicconcepts.Concept)
	visit = func(concept musicconcepts.Concept) {
		if seen[concept.ID] {
			return
		}
		seen[concept.ID] = true
		result = append(result, criterionPostingTerm(core.MusicalCriterion{Kind: a.Kind, Value: concept.Value}))
		for _, id := range concept.Parents {
			if parent, ok := musicconcepts.FindID(id); ok {
				visit(parent)
			}
		}
	}
	if concept, ok := musicconcepts.Find(a.Kind, a.Value); ok {
		visit(concept)
	}
	return result
}
