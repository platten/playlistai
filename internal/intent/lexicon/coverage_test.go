package lexicon

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestExplicitGenreMixUsesPlaylistCoverage(t *testing.T) {
	for _, prompt := range []string{
		"Give me 10 tracks mixing house, techno, and UK garage.",
		"Make a 10-song playlist combining bossa nova, samba, and Brazilian jazz.",
		"Give me 10 songs spanning bluegrass, country, and Americana.",
		"Make a 10-song playlist mixing Japanese city pop, funk, and disco.",
		"Give me 10 songs blending hip-hop, neo-soul, and jazz.",
		"Make a 10-track instrumental playlist for focused work, blending modern classical, ambient, and gentle electronic music. Start sparse, build a subtle pulse in the middle, and finish calmly. No vocals.",
	} {
		t.Run(prompt, func(t *testing.T) {
			source := Extract(prompt)
			var marked []core.IntentAtom
			for _, atom := range source.Atoms {
				if atom.CoverageGroup != "" {
					marked = append(marked, atom)
				}
			}
			if len(marked) != 3 {
				t.Fatalf("want three coverage members, got %+v; all atoms: %+v", marked, source.Atoms)
			}
			for _, atom := range marked {
				if atom.CoverageGroup != marked[0].CoverageGroup {
					t.Fatal("split coverage set")
				}
			}
			intent := Reconcile(core.MusicIntent{}, source)
			genreCount := 0
			for _, c := range intent.EssentialCriteria {
				if c.Kind != "genre" && c.Kind != "style" {
					if c.CoverageGroup != "" {
						t.Fatal("non-genre joined coverage set")
					}
					continue
				}
				genreCount++
				if c.CoverageGroup != marked[0].CoverageGroup {
					t.Fatalf("coverage lost: %+v", c)
				}
			}
			if genreCount != 3 {
				t.Fatalf("genre criteria lost: %+v", intent.EssentialCriteria)
			}
			if strings.Contains(prompt, "No vocals") && !core.WantsInstrumental(intent) {
				t.Fatal("no-vocals requirement lost")
			}
			if !strings.Contains(BaselineFactsMessage(source), "playlist-coverage-group=") || !strings.Contains(FactsMessage(source), "playlist-coverage-group=") {
				t.Fatal("model context lost coverage")
			}
			if !reflect.DeepEqual(source, WithGenreCoverage(source)) {
				t.Fatal("annotation is not idempotent")
			}
		})
	}
}

func TestGenreCoveragePreservesStrictAndNonGenreMeaning(t *testing.T) {
	for _, prompt := range []string{
		"Each track should blend house, techno, and UK garage.",
		"Give me tracks blending house, techno, and UK garage within every track.",
		"Every song must combine house and techno.",
		"All 10 tracks must blend house and techno.",
		"Each of the tracks must blend house and techno.",
		"Every single track must blend house and techno.",
		"Mix house and techno. Each track must contain both genres.",
		"Mix house and techno; each track must contain both genres.",
		"Give me house, techno, and UK garage.",
		"No mixing house and techno.",
		"Don't make a mix of house and techno.",
		"Mix relaxing and energetic music.",
		"Start with house and end with techno.",
		"Play 'Mix house and techno' by Fixture Artist.",
	} {
		for _, a := range Extract(prompt).Atoms {
			if a.CoverageGroup != "" {
				t.Errorf("unexpected coverage for %q: %+v", prompt, a)
			}
		}
	}
}

func TestUnrelatedPerTrackRequirementKeepsCollectiveGenres(t *testing.T) {
	for _, prompt := range []string{
		"Mix house and techno. Each track must include both piano and guitar.",
		"Mix house and techno. Include piano for every track.",
	} {
		source := Extract(prompt)
		marked := 0
		for _, atom := range source.Atoms {
			if atom.CoverageGroup != "" {
				if atom.Kind != "genre" {
					t.Fatal("instrument entered genre coverage")
				}
				marked++
			}
		}
		if marked != 2 {
			t.Fatalf("unrelated instruments changed collective genres: %+v", source.Atoms)
		}
	}
}

func TestGenreCoverageRechecksRecognizedSpansWithoutMutatingSnapshot(t *testing.T) {
	source := Extract("Mix house and techno.")
	before, _ := json.Marshal(source)
	modified := source.Clone()
	modified.Atoms = append(modified.Atoms, modified.Atoms[0]) // Overlapping recognition cannot panic.
	_ = WithGenreCoverage(modified)
	for i := range modified.Atoms {
		if modified.Atoms[i].Value == "techno" {
			modified.Atoms[i].Kind = "artist"
		}
	}
	got := WithGenreCoverage(modified)
	for _, a := range got.Atoms {
		if a.CoverageGroup != "" {
			t.Fatal("protected name retained genre coverage")
		}
	}
	after, _ := json.Marshal(source)
	if string(before) != string(after) {
		t.Fatal("snapshot mutated")
	}
}
