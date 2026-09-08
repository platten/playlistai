package core

// VerificationPolicy separates useful suggestions from claims of proven fit.
type VerificationPolicy string

const (
	BestAvailable VerificationPolicy = "best_available"
	VerifiedOnly  VerificationPolicy = "verified_only"
)

type TemporalRequirement struct {
	Basis     string           `json:"basis"` // original_release | composition
	StartYear int              `json:"startYear"`
	EndYear   int              `json:"endYear"`
	Scope     string           `json:"scope"`
	Evidence  []SourceEvidence `json:"evidence"`
}

type GenreNode struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
}

type GenreRelation struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Kind   string `json:"kind"` // subgenre (child -> parent) | influence | fusion
	Source string `json:"source"`
}

type GenreGraph struct {
	Version   string          `json:"version"`
	Nodes     []GenreNode     `json:"nodes"`
	Relations []GenreRelation `json:"relations"`
}

func (g GenreGraph) ID(name string) string {
	key := NormalizeIdentityPart(name)
	for _, n := range g.Nodes {
		if n.ID == name || NormalizeIdentityPart(n.Name) == key {
			return n.ID
		}
		for _, alias := range n.Aliases {
			if NormalizeIdentityPart(alias) == key {
				return n.ID
			}
		}
	}
	return key
}

// Matches follows only subgenre edges; influence/fusion is not equivalence.
func (g GenreGraph) Matches(want, actual string) bool {
	target := g.ID(want)
	pending, seen := []string{g.ID(actual)}, map[string]bool{}
	for len(pending) > 0 {
		id := pending[0]
		pending = pending[1:]
		if id == target {
			return true
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		for _, edge := range g.Relations {
			if edge.From == id && edge.Kind == "subgenre" {
				pending = append(pending, edge.To)
			}
		}
	}
	return false
}

type KnowledgeSnapshot struct {
	// Discovery records the ordered pull stream, including rejected candidates,
	// so history replay does not consult a changing external artist search.
	Discovery         []TrackRef        `json:"discovery,omitempty"`
	DiscoveryRecorded bool              `json:"discoveryRecorded,omitempty"`
	ArtistPools       []GenreArtistPool `json:"artistPools,omitempty"`
	ID                string            `json:"id"`
	Graph             GenreGraph        `json:"graph"`
	Tracks            []EnrichedTrack   `json:"tracks"`
	Candidates        []TrackRef        `json:"candidates"`
	Sources           []string          `json:"sources"`
	Notices           []string          `json:"notices"`
}

type TrackAssessment struct {
	TrackID string        `json:"trackId"`
	State   EvidenceState `json:"state"`
	Reasons []string      `json:"reasons"`
}

// Artist tags guide retrieval; they do not classify every recording.
type GenreArtist struct {
	ID   string               `json:"id"`
	Name string               `json:"name"`
	Tags []AttributedGenreTag `json:"tags"`
}
type GenreArtistPool struct {
	Genre          string        `json:"genre"`
	Artists        []GenreArtist `json:"artists"`
	Available      int           `json:"available"`
	Complete       bool          `json:"complete"`
	Sources        []string      `json:"sources"`
	SampledArtists []string      `json:"sampledArtists"`
	SampledTracks  []string      `json:"sampledTracks"`
}
