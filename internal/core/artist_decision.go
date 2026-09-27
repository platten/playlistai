package core

import "strings"

const ArtistDecisionPolicy = "artist-default/v1"

// ArtistPopularity is an observation from one immutable audience snapshot.
// Missing counts are unknown, never zero. These counts do not describe fit.
type ArtistPopularity struct {
	Snapshot        string `json:"snapshot"`
	UniqueListeners *int64 `json:"uniqueListeners,omitempty"`
	Listens         *int64 `json:"listens,omitempty"`
}

// ArtistDecision records a reversible default, not explicit user confirmation.
// Grounding retains its alternatives and original source spelling.
type ArtistDecision struct {
	SelectedID    string `json:"selectedId"`
	Method        string `json:"method"`
	PolicyVersion string `json:"policyVersion"`
	Snapshot      string `json:"snapshot"`
	Provisional   bool   `json:"provisional,omitempty"`
	Context       string `json:"context,omitempty"`
}

func (p *ArtistPopularity) Valid() bool {
	return p != nil && p.Snapshot != "" && (p.UniqueListeners == nil || *p.UniqueListeners >= 0) && (p.Listens == nil || *p.Listens >= 0)
}

func (p *ArtistPopularity) Clone() *ArtistPopularity {
	if p == nil {
		return nil
	}
	out := *p
	if p.UniqueListeners != nil {
		value := *p.UniqueListeners
		out.UniqueListeners = &value
	}
	if p.Listens != nil {
		value := *p.Listens
		out.Listens = &value
	}
	return &out
}

// CompareArtistPopularity compares only like-for-like observations. A known
// listener count outranks unknown coverage provisionally. Listen counts break
// listener ties only when both are known; a missing tie-breaker is not zero.
func CompareArtistPopularity(a, b *ArtistPopularity) int {
	ak, bk := a.Valid() && a.UniqueListeners != nil, b.Valid() && b.UniqueListeners != nil
	if ak != bk {
		if ak {
			return 1
		}
		return -1
	}
	if !ak || a.Snapshot != b.Snapshot {
		return 0
	}
	if *a.UniqueListeners != *b.UniqueListeners {
		if *a.UniqueListeners > *b.UniqueListeners {
			return 1
		}
		return -1
	}
	if a.Listens != nil && b.Listens != nil && *a.Listens != *b.Listens {
		if *a.Listens > *b.Listens {
			return 1
		}
		return -1
	}
	return 0
}

// DecideArtist uses exact recognized names/aliases only. It never guesses a
// missing identity or discards truncated rival sets. Context and user choices
// precede audience defaults; lexical/MBID ordering never breaks a true tie.
func DecideArtist(g *IdentityGrounding) *ArtistDecision {
	if g == nil || g.Confirmed || g.Truncated || len(g.Candidates) == 0 {
		return nil
	}
	if _, ok := g.CorroboratedArtist(); ok {
		return nil
	}
	if g.Decision != nil && g.Decision.Method == "context" {
		if _, ok := g.DecidedArtist(); ok {
			d := *g.Decision
			return &d
		}
	}
	best := -1
	snapshot := ""
	seen := map[string]bool{}
	for i, c := range g.Candidates {
		if c.Kind != ReferenceArtist || c.ID == "" || c.Name == "" || seen[c.ID] {
			return nil
		}
		seen[c.ID] = true
		if c.MatchType != "canonical" && c.MatchType != "alias" {
			continue
		}
		if c.Popularity.Valid() && c.Popularity.UniqueListeners != nil {
			if snapshot != "" && snapshot != c.Popularity.Snapshot {
				return nil
			}
			snapshot = c.Popularity.Snapshot
			if best < 0 || CompareArtistPopularity(c.Popularity, g.Candidates[best].Popularity) > 0 {
				best = i
			}
		}
	}
	if best >= 0 {
		provisional, unique := false, true
		for i, c := range g.Candidates {
			if i == best || c.MatchType != "canonical" && c.MatchType != "alias" {
				continue
			}
			if !c.Popularity.Valid() || c.Popularity.UniqueListeners == nil {
				provisional = true
				continue
			}
			if CompareArtistPopularity(g.Candidates[best].Popularity, c.Popularity) == 0 {
				unique = false
			}
		}
		if unique {
			return &ArtistDecision{SelectedID: g.Candidates[best].ID, Method: "popularity", PolicyVersion: ArtistDecisionPolicy, Snapshot: snapshot, Provisional: provisional}
		}
	}
	canonical := -1
	for i, c := range g.Candidates {
		if c.MatchType == "canonical" && NormalizeIdentityPart(c.Name) == NormalizeIdentityPart(g.MatchedSpelling) {
			if canonical >= 0 {
				return nil
			}
			canonical = i
		}
	}
	if canonical >= 0 && g.SnapshotVersion != "" {
		return &ArtistDecision{SelectedID: g.Candidates[canonical].ID, Method: "canonical", PolicyVersion: ArtistDecisionPolicy, Snapshot: g.SnapshotVersion}
	}
	return nil
}

// ArtistContextDecision accepts an explicit adjacent disambiguator, such as
// “John Williams (classical guitarist)”, rather than inferring a musical role.
func ArtistContextDecision(g *IdentityGrounding, qualifier string) *ArtistDecision {
	if g == nil || g.Truncated || g.Confirmed || g.SnapshotVersion == "" {
		return nil
	}
	q := NormalizeIdentityPart(qualifier)
	if q == "" || len(q) > 160 {
		return nil
	}
	for _, word := range strings.Fields(q) {
		if word == "not" || word == "no" || word == "without" || word == "except" {
			return nil
		}
	}
	selected := ""
	for _, c := range g.Candidates {
		if c.Kind != ReferenceArtist || !strings.Contains(" "+NormalizeIdentityPart(c.Disambiguation)+" ", " "+q+" ") {
			continue
		}
		if selected != "" {
			return nil
		}
		selected = c.ID
	}
	if selected == "" {
		return nil
	}
	return &ArtistDecision{SelectedID: selected, Method: "context", PolicyVersion: ArtistDecisionPolicy, Snapshot: g.SnapshotVersion, Context: qualifier}
}

// DecidedArtist validates the persisted automatic choice without refreshing its
// popularity. An independently corroborated identity always takes precedence.
func (g *IdentityGrounding) DecidedArtist() (IdentityCandidate, bool) {
	if c, ok := g.CorroboratedArtist(); ok {
		return c, true
	}
	if g == nil || g.Confirmed || g.Truncated || g.Decision == nil || g.Decision.PolicyVersion != ArtistDecisionPolicy {
		return IdentityCandidate{}, false
	}
	d := g.Decision
	clone := *g
	clone.Decision = nil
	var expected *ArtistDecision
	if d.Method == "context" {
		expected = ArtistContextDecision(&clone, d.Context)
	} else {
		expected = DecideArtist(&clone)
	}
	if expected == nil || *expected != *d {
		return IdentityCandidate{}, false
	}
	for _, c := range g.Candidates {
		if c.ID == d.SelectedID {
			return c, true
		}
	}
	return IdentityCandidate{}, false
}

// IdentityChoices includes alternatives retained after an explicit correction.
// Returned values are detached so changing a preview cannot edit saved intent.
func (g *IdentityGrounding) IdentityChoices() []IdentityCandidate {
	if g == nil {
		return nil
	}
	copy := cloneIdentityGrounding(g)
	seen := map[string]bool{}
	var out []IdentityCandidate
	for _, candidates := range [][]IdentityCandidate{copy.Candidates, copy.Alternatives} {
		for _, candidate := range candidates {
			key := string(candidate.Kind) + "\x00" + candidate.ID
			if !seen[key] {
				seen[key] = true
				out = append(out, candidate)
			}
		}
	}
	return out
}

// WithConfirmedIdentity preserves the legacy singleton Confirmed contract while
// keeping the other offered identities available for a subsequent correction.
func (g *IdentityGrounding) WithConfirmedIdentity(kind ReferenceKind, id string) (*IdentityGrounding, bool) {
	choices := g.IdentityChoices()
	for i, candidate := range choices {
		if candidate.Kind != kind || candidate.ID != id {
			continue
		}
		out := cloneIdentityGrounding(g)
		out.Candidates = []IdentityCandidate{candidate}
		out.Alternatives = append([]IdentityCandidate(nil), choices[:i]...)
		out.Alternatives = append(out.Alternatives, choices[i+1:]...)
		out.Confirmed, out.Truncated, out.Decision, out.Corroboration = true, false, nil, nil
		return out, true
	}
	return nil, false
}
