package lexicon

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/platten/playlistai/internal/core"
)

// A qualified exclusion owns its whole literal phrase. Recognizing "drums"
// inside "no pounding drums" does not establish either a request for drums or
// an exclusion of every kind of drums. Unknown detail stays unsupported unless
// the interpretation supplies that exact phrase at this source occurrence.
type qualifiedMusic struct {
	value, scope, strength, degree, kind string
	evidence                             core.SourceEvidence
	valueStart                           int
	parents                              map[[2]int]bool
	retained                             []core.IntentAtom
}

var qualifiedMusicBoundary = regexp.MustCompile(`(?i)\b(?:and|or|no|not|neither|nor|without|except|rather|less|more|only|mostly|must|from|through|via|to|like|by|but|instead|with|include|including|avoid|skip|then|for|over)\b`)
var qualifiedMusicIntensifier = regexp.MustCompile(`(?i)\b(?:very|much|too)\s+$`)

func qualifiedMusicalOccurrences(source core.IntentTranslation) []qualifiedMusic {
	var out []qualifiedMusic
	for _, atom := range source.Atoms {
		if !plainMusicalParent(atom) || len(atom.Evidence) != 1 {
			continue
		}
		e := atom.Evidence[0]
		if !knownOccurrence(e) || e.End > len(source.OriginalText) || source.OriginalText[e.Start:e.End] != e.Text {
			continue
		}
		begin := strings.LastIndexAny(source.OriginalText[:e.Start], ",;:.!?\n") + 1
		// Try word boundaries immediately after an existing qualifier. The
		// intervening words remain literal; no adjective vocabulary is inferred.
		for start := e.Start - 1; start >= begin; start-- {
			if start > 0 && source.OriginalText[start-1] != ' ' && source.OriginalText[start-1] != '\t' {
				continue
			}
			value := source.OriginalText[start:e.End]
			if strings.TrimSpace(value) != value || !qualifiedMusicWords(value) {
				continue
			}
			prefix := source.OriginalText[:start]
			if additivePrefix.MatchString(prefix) {
				continue
			}
			operator := negativeContextStart(prefix)
			degree := "plain"
			if reduced := reducedPrefix.FindStringIndex(prefix); reduced != nil {
				operator, degree = reduced[0], "reduced"
			}
			if operator < begin {
				continue
			}
			valueStart := start
			if degree != "reduced" {
				for {
					intensifier := qualifiedMusicIntensifier.FindStringIndex(source.OriginalText[:valueStart])
					if intensifier == nil || intensifier[0] <= operator {
						break
					}
					valueStart = intensifier[0]
				}
			}
			q := qualifiedMusic{kind: atom.Kind, scope: atom.Scope, strength: "preferred", degree: degree, valueStart: valueStart, parents: map[[2]int]bool{}}
			if degree == "plain" && hardMusicalNegative(source.OriginalText[operator:start]) && !softMusicalNegative(source.OriginalText[:operator]) {
				q.strength = "required"
			}
			if strict := strictPrefix.FindStringIndex(source.OriginalText[:operator]); strict != nil && !additivePrefix.MatchString(source.OriginalText[:operator]) {
				operator, q.strength = strict[0], "required"
			}
			end := qualifiedMusicEnd(source.OriginalText, e.End)
			q.value = source.OriginalText[valueStart:end]
			q.evidence = core.SourceEvidence{Text: source.OriginalText[operator:end], Start: operator, End: end, Explicit: true}
			if !qualifiedMusicParents(source, &q) {
				continue
			}
			duplicate := false
			for _, prior := range out {
				duplicate = duplicate || prior.evidence.Start < q.evidence.End && q.evidence.Start < prior.evidence.End
			}
			if !duplicate {
				out = append(out, q)
			}
			break
		}
	}
	return out
}

func plainMusicalParent(atom core.IntentAtom) bool {
	return (atom.Kind == "instrumentation" || atom.Kind == "texture") && atom.Polarity == "positive" && (atom.Strength == "preferred" || atom.Strength == "essential") && atom.Degree == "plain" && atom.Group == "" && atom.CoverageGroup == "" && atom.Grounding == nil
}

func qualifiedMusicWords(value string) bool {
	for _, r := range value {
		if !unicode.IsLetter(r) && !unicode.IsMark(r) && !unicode.IsSpace(r) && r != '-' {
			return false
		}
	}
	return !qualifiedMusicBoundary.MatchString(value)
}

func qualifiedMusicEnd(prompt string, after int) int {
	tail := prompt[after:]
	if end := strings.IndexAny(tail, ",;:.!?\n"); end >= 0 {
		tail = tail[:end]
	}
	if boundary := qualifiedMusicBoundary.FindStringIndex(tail); boundary != nil {
		tail = tail[:boundary[0]]
	}
	return after + len(strings.TrimRightFunc(tail, unicode.IsSpace))
}

func qualifiedMusicParents(source core.IntentTranslation, q *qualifiedMusic) bool {
	for _, quoted := range source.Quoted {
		if quoted.Start < q.evidence.End && q.evidence.Start < quoted.End {
			return false
		}
	}
	for _, atom := range source.Atoms {
		for _, e := range atom.Evidence {
			if e.End <= q.evidence.Start || e.Start >= q.evidence.End {
				continue
			}
			if !plainMusicalParent(atom) || e.Start < q.valueStart || e.End > q.evidence.End || atom.Scope != q.scope {
				return false
			}
			q.parents[[2]int{e.Start, e.End}] = true
		}
	}
	return len(q.parents) > 0
}

func qualifiedMusicalPreference(p core.IntentPreference, kind string, source core.IntentTranslation, occurrences []qualifiedMusic) (core.IntentPreference, bool) {
	if !p.Explicit || len(p.Evidence) != 1 || !p.Evidence[0].Explicit {
		return p, false
	}
	e := p.Evidence[0]
	if e.Start < 0 && e.End < 0 && e.Text != "" && strings.Count(source.OriginalText, e.Text) == 1 {
		e.Start = strings.Index(source.OriginalText, e.Text)
		e.End = e.Start + len(e.Text)
	}
	if !knownOccurrence(e) || e.End > len(source.OriginalText) || source.OriginalText[e.Start:e.End] != e.Text {
		return p, false
	}
	for i := range occurrences {
		q := &occurrences[i]
		start := e.Start
		if !strings.EqualFold(p.Value, e.Text) {
			if strings.Count(e.Text, p.Value) != 1 || !strings.HasSuffix(e.Text, p.Value) {
				continue
			}
			start = e.End - len(p.Value)
		}
		// A partial phrase can broaden an exclusion: excluding "ringing
		// guitar" is stronger than excluding "ringing guitar samples".
		// Keep the original occurrence bounds immutable for later proposals.
		if start != q.valueStart || e.End != q.evidence.End || !strings.EqualFold(p.Value, q.value) || !qualifiedMusicWords(q.value) {
			continue
		}
		p.Value, p.Scope, p.Strength, p.Degree = q.value, q.scope, q.strength, q.degree
		p.Influence, p.ConceptID, p.Group, p.CoverageGroup = core.InfluenceNegative, "", "", ""
		p.Evidence = []core.SourceEvidence{q.evidence}
		found := false
		for _, atom := range q.retained {
			found = found || atom.Kind == kind
		}
		if !found {
			q.retained = append(q.retained, core.IntentAtom{Kind: kind, Value: q.value, Scope: q.scope, Polarity: "negative", Strength: q.strength, Degree: q.degree, Evidence: p.Evidence})
		}
		return p, true
	}
	return p, false
}

func qualifiedMusicOwns(atom core.IntentAtom, occurrences []qualifiedMusic) bool {
	for _, q := range occurrences {
		for _, e := range atom.Evidence {
			if plainMusicalParent(atom) && q.parents[[2]int{e.Start, e.End}] {
				return true
			}
		}
	}
	return false
}

// Rebuild only this request's saved translation. The parser's original source
// snapshot and historical snapshots remain untouched.
func applyQualifiedMusic(intent *core.MusicIntent, source *core.IntentTranslation, occurrences []qualifiedMusic) {
	if len(occurrences) == 0 {
		return
	}
	*source = source.Clone()
	atoms := source.Atoms[:0]
	for _, atom := range source.Atoms {
		if !qualifiedMusicOwns(atom, occurrences) {
			atoms = append(atoms, atom)
		}
	}
	source.Atoms = atoms
	for _, q := range occurrences {
		if len(q.retained) == 0 && q.strength == "required" {
			// A missing model interpretation must not erase a literal hard
			// exclusion or broaden it to its generic inner instrument.
			p := core.IntentPreference{Value: q.value, Scope: q.scope, Strength: q.strength, Degree: q.degree, Influence: core.InfluenceNegative, Explicit: true, Evidence: []core.SourceEvidence{q.evidence}}
			if q.kind == "instrumentation" {
				intent.Preferences.Instrumentation = append(intent.Preferences.Instrumentation, p)
			} else {
				intent.Preferences.TextureDescriptions = append(intent.Preferences.TextureDescriptions, p)
			}
			q.retained = []core.IntentAtom{{Kind: q.kind, Value: q.value, Scope: q.scope, Polarity: "negative", Strength: q.strength, Degree: q.degree, Evidence: p.Evidence}}
		}
		for _, atom := range q.retained {
			atom.ID = fmt.Sprintf("qualified-%s-%d-%d", atom.Kind, q.evidence.Start, q.evidence.End)
			source.Atoms = append(source.Atoms, atom)
			if q.strength == "required" && (q.scope == "" || q.scope == "playlist") {
				intent.HardConstraints = append(intent.HardConstraints, core.HardConstraint{Kind: "exclude_" + atom.Kind, Value: atom.Value, Evidence: atom.Evidence})
			}
		}
		if len(q.retained) > 0 && q.strength != "required" {
			continue
		}
		reason := "The qualified musical wording is preserved, but its full interpretation is unresolved."
		if len(q.retained) > 0 {
			reason = "The strict qualified musical exclusion is preserved; full recording evidence is required before it can be enforced."
		}
		intent.Unsupported = append(intent.Unsupported, core.UnsupportedRequirement{Text: q.evidence.Text, Reason: reason, Evidence: []core.SourceEvidence{q.evidence}})
		source.Repairs = append(source.Repairs, "Preserved qualified musical requirement without broadening its instrument: "+q.evidence.Text)
	}
}

// Claim literal qualified exclusions before AND/OR grouping can turn their
// generic inner instrument into a positive alternative. Standalone occurrences
// keep the existing reconciliation path and its model-kind compatibility.
func preserveCoordinatedQualifiedNegatives(source *core.IntentTranslation) {
	for _, q := range qualifiedMusicalOccurrences(*source) {
		if q.degree == "reduced" || !definingCoordination.MatchString(source.OriginalText[q.evidence.End:]) {
			continue
		}
		atoms := source.Atoms[:0]
		for _, a := range source.Atoms {
			if !qualifiedMusicOwns(a, []qualifiedMusic{q}) {
				atoms = append(atoms, a)
			}
		}
		atoms = append(atoms, core.IntentAtom{ID: fmt.Sprintf("qualified-%s-%d-%d", q.kind, q.evidence.Start, q.evidence.End), Kind: q.kind, Value: q.value, Scope: q.scope, Polarity: "negative", Strength: q.strength, Degree: q.degree, Evidence: []core.SourceEvidence{q.evidence}})
		source.Atoms = atoms
	}
}

// A negated list owns any literal qualifier between its connector and the
// next known musical noun; no adjective dictionary is needed to preserve it.
func preserveNegativeContinuation(prompt string, previous, next *core.IntentAtom) {
	left, right := previous.Evidence[0].End, next.Evidence[0].Start
	if left > right || next.Polarity != "positive" || (next.Kind != "instrumentation" && next.Kind != "texture") {
		return
	}
	connector := definingCoordination.FindStringIndex(prompt[left:])
	if connector == nil {
		return
	}
	start := left + connector[1]
	if start > right || !qualifiedMusicWords(prompt[start:next.Evidence[0].End]) {
		return
	}
	end := qualifiedMusicEnd(prompt, next.Evidence[0].End)
	next.Value = prompt[start:end]
	next.ConceptID = ""
	next.Evidence = []core.SourceEvidence{{Text: prompt[start:end], Start: start, End: end, Explicit: true}}
}
