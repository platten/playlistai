package musicconcepts

import (
	"reflect"
	"slices"
	"testing"
)

func TestExactAliasesDoNotCollapseTaxonomyOrPolarity(t *testing.T) {
	for _, alias := range []string{"hip hop", "HIP-HOP", "hiphop", "  hip   hop "} {
		if got := Canonical("style", alias); got != "hip hop" {
			t.Fatalf("canonical(%q) = %q", alias, got)
		}
	}
	for _, value := range []string{"electronic", "electronica", "ambient", "ambient electronic", "deep house"} {
		if got := Canonical("genre", value); got != value {
			t.Fatalf("taxonomy collapsed %q to %q", value, got)
		}
	}
	for _, value := range []string{"mostly instrumental", "no singing", "not too aggressive", "Uncataloged Personal Style"} {
		if _, ok := Find("vocal", value); ok || Canonical("vocal", value) != value {
			t.Fatalf("unknown/compositional meaning was replaced: %q", value)
		}
	}
	if Canonical("genre", "  Uncataloged   Personal Style  ") != "Uncataloged Personal Style" {
		t.Fatal("unknown wording/case was not retained")
	}
}

func TestEveryAliasHasStableIdentityAndProviderPlan(t *testing.T) {
	for _, c := range Concepts() {
		if c.Source == "" || c.License == "" {
			t.Fatalf("missing provenance: %s", c.ID)
		}
		for _, alias := range append([]string{c.Value}, c.Aliases...) {
			got, ok := Find(c.Kind, alias)
			if !ok || got.ID != c.ID || !reflect.DeepEqual(got.Providers, c.Providers) {
				t.Fatalf("alias %q changes provider meaning of %s", alias, c.ID)
			}
		}
		if _, ok := c.Providers.AcousticBrainz["genre_electronic"]; ok {
			t.Fatalf("conditional electronic classifier promoted to direct proof: %s", c.ID)
		}
	}
	electronica, _ := Find("genre", "electronica")
	if !slices.Contains(electronica.Parents, "genre.electronic") || len(electronica.Providers.AcousticBrainz) != 0 || slices.Contains(electronica.Providers.MusicBrainz, "electronic") {
		t.Fatalf("parent relationship became an exact mapping: %+v", electronica)
	}
	for kind, model := range map[string]string{"mood": "mood_relaxed", "texture": "timbre"} {
		value := "relaxed"
		if kind == "texture" {
			value = "dark"
		}
		c, _ := Find(kind, value)
		if c.Providers.AcousticBrainz[model] == "" {
			t.Fatalf("missing reviewed %s mapping: %+v", kind, c)
		}
	}
	darkMood, _ := Find("mood", "dark")
	if len(darkMood.Providers.AcousticBrainz) > 0 || len(darkMood.Providers.DSP) > 0 {
		t.Fatal("emotional darkness equated with spectral darkness")
	}
}

func TestRegistryResultsCannotMutateEmbeddedData(t *testing.T) {
	original, _ := Find("genre", "hip-hop")
	changed, _ := FindID(original.ID)
	changed.Aliases[0] = "corrupt"
	changed.Providers.MusicBrainz[0] = "corrupt"
	ambient, _ := Find("genre", "ambient electronica")
	changedAmbient, _ := FindID(ambient.ID)
	changedAmbient.Providers.MusicBrainzDiscovery[0] = "corrupt"
	changed.Providers.AcousticBrainz["genre_rosamerica"] = "corrupt"
	all := Concepts()
	all[0].Value = "corrupt"
	again, _ := Find("genre", "hip-hop")
	againAmbient, _ := Find("genre", "ambient electronica")
	if !reflect.DeepEqual(original, again) || !reflect.DeepEqual(ambient, againAmbient) || Concepts()[0].Value == "corrupt" {
		t.Fatal("callers can mutate registry state")
	}
}

func TestCompoundRockAndSoulStaySpecificGenres(t *testing.T) {
	for _, alias := range []string{"rock & roll", "rock and roll"} {
		c, ok := Find("genre", alias)
		if !ok || c.ID != "genre.rock-and-roll" || c.Value != "rock & roll" || len(c.Providers.AcousticBrainz) != 0 || slices.Contains(c.Providers.MusicBrainz, "rock") {
			t.Fatalf("compound genre collapsed to a broader classifier: %+v", c)
		}
	}
	soul, ok := Find("genre", "soul")
	if !ok || soul.ID != "genre.soul" || len(soul.Providers.AcousticBrainz) != 0 || !reflect.DeepEqual(soul.Providers.MusicBrainz, []string{"soul"}) {
		t.Fatalf("soul was unknown or equated with rhythm-and-blues: %+v", soul)
	}
}

func TestDreamyIsMoodWithoutGuessedProviderClassifier(t *testing.T) {
	c, ok := Find("mood", "dreamy")
	if !ok || c.ID != "mood.dreamy" || len(c.Providers.AcousticBrainz) != 0 || len(c.Providers.DSP) != 0 || len(c.Providers.MusicBrainz) != 0 {
		t.Fatalf("dreamy was unknown or given an unreviewed classifier: %+v", c)
	}
}

func TestTrebleVocabularyUsesBandEnergyNotBrightness(t *testing.T) {
	for _, tt := range []struct {
		alias, id, mapping string
	}{
		{"treble-heavy", "texture.treble-emphasis", "treble_energy_ratio:positive"},
		{"more treble", "texture.treble-emphasis", "treble_energy_ratio:positive"},
		{"less treble", "texture.reduced-treble", "treble_energy_ratio:negative"},
		{"treble roll-off", "texture.reduced-treble", "treble_energy_ratio:negative"},
	} {
		concept, ok := Find("texture", tt.alias)
		if !ok || concept.ID != tt.id || !reflect.DeepEqual(concept.Providers.DSP, []string{tt.mapping}) {
			t.Fatalf("treble alias %q: %+v", tt.alias, concept)
		}
		if slices.Contains(concept.Providers.DSP, "spectral_centroid_hz:positive") || slices.Contains(concept.Providers.DSP, "spectral_centroid_hz:negative") {
			t.Fatalf("treble alias %q was mapped to brightness: %+v", tt.alias, concept)
		}
	}
}
