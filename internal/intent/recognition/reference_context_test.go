package recognition

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/intent/schema"
	"github.com/platten/playlistai/internal/mbindex"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/resolution"
)

func TestArtistGroundingRetainsLaterHomonymsWithinProviderBound(t *testing.T) {
	for _, test := range []struct {
		count     int
		truncated bool
	}{{12, false}, {mbindex.MaxLookupCandidates, false}, {mbindex.MaxLookupCandidates + 1, false}, {12, true}} {
		t.Run(fmt.Sprintf("count=%d/truncated=%t", test.count, test.truncated), func(t *testing.T) {
			var identities []mbindex.ArtistIdentity
			for i := range test.count {
				identities = append(identities, mbindex.ArtistIdentity{MBID: fmt.Sprintf("artist-%02d", i), Name: "Shared Name", MatchType: mbindex.ArtistMatchCanonical, Disambiguation: fmt.Sprintf("musician %02d", i)})
			}
			lookup := referenceContextLookup{names: map[string][]mbindex.ArtistIdentity{"shared name": identities}, truncated: test.truncated}
			const prompt = "10 songs inspired by Shared Name"
			source := Apply(context.Background(), prompt, lexicon.Extract(prompt), lookup, nil)
			atoms := groundedAtoms(source)
			want := min(test.count, mbindex.MaxLookupCandidates)
			if len(atoms) != 1 || len(atoms[0].Grounding.Candidates) != want || atoms[0].Grounding.Truncated != (test.truncated || test.count > want) {
				t.Fatalf("provider candidates were prematurely lost: %+v", atoms)
			}
			if atoms[0].Grounding.Candidates[10].ID != "artist-10" {
				t.Fatal("later exact homonym missing from choices")
			}
			intent, err := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt, SourceFacts: &source})
			if err != nil {
				t.Fatal(err)
			}
			catalog := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "track", Display: "Shared Name - Song"})
			_, issues := resolution.Apply(catalog, intent)
			if len(issues) != 1 || issues[0].Status != core.ResolutionAmbiguous || len(issues[0].GroundingCandidates) != want {
				t.Fatalf("retaining choices guessed away ambiguity: %+v", issues)
			}

			// The expanded immutable record is not sent verbatim to the model.
			// Its mandatory message and optional hint byte bounds stay at or
			// below the previous eight-candidate presentation.
			previous := source.Clone()
			for i := range previous.Atoms {
				if grounding := previous.Atoms[i].Grounding; grounding != nil {
					grounding.Candidates = grounding.Candidates[:8]
					grounding.Truncated = true
				}
			}
			if len(lexicon.FactsMessage(source)) > len(lexicon.FactsMessage(previous)) {
				t.Fatal("retained identities expanded the mandatory model byte bound")
			}
			currentInput := lexicon.PrepareParsingContext(ports.IntentInput{SourceFacts: &source, EnrichParsingContext: true})
			previousInput := lexicon.PrepareParsingContext(ports.IntentInput{SourceFacts: &previous, EnrichParsingContext: true})
			if !reflect.DeepEqual(currentInput.SourceFacts.ParsingContext.Hints, previousInput.SourceFacts.ParsingContext.Hints) {
				t.Fatal("retained identities expanded optional model hints")
			}
		})
	}
}

type referenceContextLookup struct {
	names     map[string][]mbindex.ArtistIdentity
	truncated bool
}

func TestNegativeMusicalDescriptionsDoNotExcludeNamesakeArtists(t *testing.T) {
	lookup := referenceContextLookup{names: map[string][]mbindex.ArtistIdentity{}}
	for _, name := range []string{"sleepy", "metal", "vocals"} {
		lookup.names[name] = []mbindex.ArtistIdentity{{MBID: "artist-" + name, Name: name, MatchType: mbindex.ArtistMatchCanonical}}
	}
	for _, test := range []struct{ prompt, kind, polarity string }{
		{"Give me 10 tracks of spacious electronic music with detailed textures, a deep groove, and occasional sparkle—relaxing but not sleepy.", "mood", "negative"},
		{"Give me 10 songs without metal.", "genre", "negative"},
		{"Give me 10 songs with no vocals.", "vocal", "positive"}, // canonical instrumental requirement
	} {
		source := Apply(context.Background(), test.prompt, lexicon.Extract(test.prompt), lookup, nil)
		if atoms := groundedAtoms(source); len(atoms) != 0 {
			t.Errorf("negative description invented an artist for %q: %+v", test.prompt, atoms)
		}
		preserved := false
		for _, atom := range source.Atoms {
			preserved = preserved || atom.Kind == test.kind && atom.Polarity == test.polarity
		}
		if !preserved {
			t.Fatalf("negative musical fact lost for %q: %+v", test.prompt, source.Atoms)
		}
		intent, err := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: test.prompt, SourceFacts: &source})
		if err != nil || len(intent.References) != 0 {
			t.Fatalf("musical exclusion became an artist reference: %+v %v", intent, err)
		}
		for _, constraint := range intent.HardConstraints {
			if constraint.Kind == "exclude_artist" {
				t.Fatalf("musical exclusion became a hard artist exclusion: %+v", constraint)
			}
		}
	}
	for _, prompt := range []string{
		`Give me 10 songs without "Sleepy".`,
		"Give me 10 songs with nothing by Sleepy.",
		"Give me 10 songs but exclude the artist Sleepy.",
	} {
		source := Apply(context.Background(), prompt, lexicon.Extract(prompt), lookup, nil)
		if atoms := groundedAtoms(source); len(atoms) != 1 || atoms[0].Grounding.Candidates[0].ID != "artist-sleepy" {
			t.Fatalf("explicit namesake artist lost for %q: %+v", prompt, atoms)
		}
	}
}

func TestGenreJourneyDoesNotBecomeNamesakeArtists(t *testing.T) {
	lookup := referenceContextLookup{names: map[string][]mbindex.ArtistIdentity{}}
	for _, name := range []string{"ambient", "electronic", "downtempo", "melodic", "folk", "rock", "alternative", "soul", "funk", "disco", "back"} {
		lookup.names[name] = []mbindex.ArtistIdentity{{MBID: "artist-" + name, Name: name, MatchType: mbindex.ArtistMatchCanonical}}
	}
	for _, prompt := range []string{
		"Make a 10-song journey from ambient electronic through downtempo to melodic house, gradually increasing the energy.",
		"Give me a 10-song playlist that starts with acoustic folk, moves through folk rock, and ends with energetic alternative rock.",
		"Give me 10 songs moving from traditional soul through funk into disco. Keep the transitions smooth and avoid placing the same artist back to back.",
		"Give me a 10-song journey from blues through soul to funk.",
	} {
		source := Apply(context.Background(), prompt, lexicon.Extract(prompt), lookup, nil)
		if atoms := groundedAtoms(source); len(atoms) != 0 {
			t.Errorf("genre journey invented artists for %q: %+v", prompt, atoms)
		}
		intent, err := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt, SourceFacts: &source})
		if err != nil {
			t.Fatal(err)
		}
		if len(intent.References) != 0 || intent.Start != nil || intent.Destination != nil {
			t.Fatalf("genre stage became a resolved reference: %+v", intent)
		}
		stages := map[string]bool{}
		for _, c := range intent.EssentialCriteria {
			if c.Kind == "genre" || c.Kind == "style" {
				stages[c.Scope] = true
			}
		}
		if !stages["journey_start"] || !stages["journey_via"] || !stages["journey_end"] {
			t.Fatalf("genre journey stages lost: %+v", intent.EssentialCriteria)
		}
		if strings.Contains(prompt, "back to back") {
			spacing := false
			for _, constraint := range intent.HardConstraints {
				spacing = spacing || constraint.Kind == "no_back_to_back_artist"
			}
			if !spacing {
				t.Fatal("artist spacing instruction lost")
			}
		}
	}
}

func TestExplicitNamesakeArtistReferencesSurviveGenreJourneyGuard(t *testing.T) {
	lookup := referenceContextLookup{names: map[string][]mbindex.ArtistIdentity{
		"ambient":    {{MBID: "ambient-artist", Name: "Ambient", MatchType: mbindex.ArtistMatchCanonical}},
		"electronic": {{MBID: "electronic-band", Name: "Electronic", MatchType: mbindex.ArtistMatchCanonical}},
	}}
	for _, prompt := range []string{
		`Make a 10-song journey from "Ambient" to "Electronic".`,
		"Make a 10-song journey from music by Ambient to music by Electronic.",
		"Make a 10-song journey from the artist Ambient to the artist Electronic.",
	} {
		source := Apply(context.Background(), prompt, lexicon.Extract(prompt), lookup, nil)
		if atoms := groundedAtoms(source); len(atoms) != 2 {
			t.Fatalf("explicit namesakes suppressed for %q: %+v", prompt, source.Atoms)
		}
	}
}

func TestArtistSentencePunctuationPreservesFullIdentitySpelling(t *testing.T) {
	lookup := referenceContextLookup{names: map[string][]mbindex.ArtistIdentity{
		"coldplay": {{MBID: "coldplay", Name: "Coldplay", MatchType: mbindex.ArtistMatchCanonical}},
		"m.i.a.":   {{MBID: "mia-full", Name: "M.I.A.", MatchType: mbindex.ArtistMatchCanonical}},
		"m.i.a":    {{MBID: "mia-other", Name: "M.I.A", MatchType: mbindex.ArtistMatchCanonical}},
	}}
	for _, test := range []struct{ prompt, id string }{{"Give me 10 songs like Coldplay.", "coldplay"}, {"Give me 10 songs like M.I.A.", "mia-full"}} {
		source := Apply(context.Background(), test.prompt, lexicon.Extract(test.prompt), lookup, nil)
		atoms := groundedAtoms(source)
		if len(atoms) != 1 || len(atoms[0].Grounding.Candidates) != 1 || atoms[0].Grounding.Candidates[0].ID != test.id {
			t.Fatalf("punctuation changed identity: %+v", atoms)
		}
	}
}

func TestPossessiveArtistIsNotReplacedByShorterNamesake(t *testing.T) {
	lookup := referenceContextLookup{names: map[string][]mbindex.ArtistIdentity{
		"brian":     {{MBID: "wrong-brian", Name: "Brian", MatchType: mbindex.ArtistMatchCanonical}},
		"brian eno": {{MBID: "brian-eno", Name: "Brian Eno", MatchType: mbindex.ArtistMatchCanonical}},
	}}
	for _, suffix := range []string{"'s", "’s"} {
		prompt := "Make a journey from Brian Eno" + suffix + " ambient sound to modern classical."
		source := Apply(context.Background(), prompt, lexicon.Extract(prompt), lookup, nil)
		atoms := groundedAtoms(source)
		if len(atoms) != 1 || len(atoms[0].Grounding.Candidates) != 1 || atoms[0].Grounding.Candidates[0].ID != "brian-eno" {
			t.Fatalf("possessive split identity: %+v", atoms)
		}
		intent, err := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt, SourceFacts: &source})
		if err != nil {
			t.Fatal(err)
		}
		for _, ref := range intent.References {
			if ref.Query == "Brian" {
				t.Fatal("false shorter reference survived compilation")
			}
		}
	}
}

func TestQuotedJourneyConnectorsAreNotQuotedArtists(t *testing.T) {
	lookup := referenceContextLookup{names: map[string][]mbindex.ArtistIdentity{}}
	for _, name := range []string{"ambient", "downtempo", "melodic", "through", "to"} {
		lookup.names[name] = []mbindex.ArtistIdentity{{MBID: "artist-" + name, Name: name, MatchType: mbindex.ArtistMatchCanonical}}
	}
	prompt := `Make a journey from "Ambient" through "Downtempo" to "Melodic".`
	source := Apply(context.Background(), prompt, lexicon.Extract(prompt), lookup, nil)
	if atoms := groundedAtoms(source); len(atoms) != 3 {
		t.Fatalf("connectors became names: %+v", atoms)
	}
	// Those spellings can still be intentionally requested as quoted names.
	prompt = `Make a journey from "Through" to "TO".`
	source = Apply(context.Background(), prompt, lexicon.Extract(prompt), lookup, nil)
	if atoms := groundedAtoms(source); len(atoms) != 2 {
		t.Fatalf("quoted namesakes were suppressed: %+v", atoms)
	}
}

func (referenceContextLookup) SnapshotIdentity() mbindex.SnapshotIdentity {
	return mbindex.SnapshotIdentity{IndexVersion: mbindex.IndexVersion, Snapshot: "fixture"}
}
func (l referenceContextLookup) LookupArtistNames(_ context.Context, names []string) ([]mbindex.ArtistNameLookup, error) {
	var results []mbindex.ArtistNameLookup
	for _, name := range names {
		key := core.NormalizeIdentityPart(name)
		results = append(results, mbindex.ArtistNameLookup{Name: name, NameKey: key, Candidates: l.names[key], Truncated: l.truncated})
	}
	return results, nil
}
func (referenceContextLookup) LookupArtistRecordings(context.Context, []mbindex.ArtistRecordingQuery) ([]mbindex.ArtistRecordingLookup, error) {
	return nil, nil
}

func radioheadRecognitionFixture() referenceContextLookup {
	return referenceContextLookup{names: map[string][]mbindex.ArtistIdentity{
		"radiohead": {
			{MBID: "a74b1b7f-71a5-4011-9441-d0b5e4122711", Name: "Radiohead", MatchedName: "Radiohead", MatchType: mbindex.ArtistMatchCanonical},
			{MBID: "c74f4726-2671-4011-81b6-f70da905c05a", Name: "On a Friday", MatchedName: "Radiohead", MatchType: mbindex.ArtistMatchAlias, Disambiguation: "pre-Radiohead group, until 1991"},
		},
		"other": {
			{MBID: "other-a", Name: "Other", MatchedName: "Other", MatchType: mbindex.ArtistMatchCanonical},
			{MBID: "other-b", Name: "Other", MatchedName: "Other", MatchType: mbindex.ArtistMatchCanonical},
		},
	}}
}

func TestOtherArtistsInstructionDoesNotCreateArtistReference(t *testing.T) {
	const prompt = "Give me 10 songs similar to Radiohead, including other artists."
	source := Apply(context.Background(), prompt, lexicon.Extract(prompt), radioheadRecognitionFixture(), nil)
	intent, err := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt, SourceFacts: &source})
	if err != nil {
		t.Fatal(err)
	}
	// Even if a model interprets the determiner as a proper name, the source
	// instruction owns that occurrence and must remove the false reference.
	raw, err := json.Marshal(schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 10,
		References:      []schema.WireReference{{Kind: "artist", Value: "Radiohead", Influence: "positive", Explicit: true, Span: "Radiohead"}, {Kind: "artist", Value: "Other", Influence: "positive", Explicit: true, Span: "other"}},
		HardConstraints: []schema.WireConstraint{{Kind: "require_artist", Value: "Other", Span: "other"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	modelIntent, err := schema.ParseForPromptWithSource(raw, prompt, source)
	if err != nil {
		t.Fatal(err)
	}
	for name, intent := range map[string]core.MusicIntent{"rules": intent, "model compiler": modelIntent} {
		t.Run(name, func(t *testing.T) {
			if len(intent.References) != 1 || intent.References[0].Query != "Radiohead" || !core.RequiresOtherArtists(intent) || intent.Controls.TotalTrackCount != 10 {
				t.Fatalf("generic inclusion created an irrelevant anchor or lost request: %+v", intent)
			}
			grounding := intent.References[0].Grounding
			if grounding == nil || grounding.Truncated || len(grounding.Candidates) != 1 || grounding.Candidates[0].ID != "a74b1b7f-71a5-4011-9441-d0b5e4122711" {
				t.Fatalf("unique canonical spelling was confused with historical alias: %+v", grounding)
			}
			for _, constraint := range intent.HardConstraints {
				if constraint.Kind == "require_artist" {
					t.Fatalf("invented required artist: %+v", constraint)
				}
			}
			catalog := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "radiohead-track", Display: "Radiohead - Fixture Song"})
			resolved, issues := resolution.Apply(catalog, intent)
			if len(issues) != 0 || resolved.References[0].Resolution.Status != core.ResolutionResolved {
				t.Fatalf("ordinary request still needs irrelevant clarification: %+v", issues)
			}
		})
	}
}

func TestExplicitArtistOtherRetainsRealAmbiguity(t *testing.T) {
	for _, prompt := range []string{`music by Other`, `include "Other"`, `like the artist Other`, `like Radiohead, including Other`} {
		source := Apply(context.Background(), prompt, lexicon.Extract(prompt), radioheadRecognitionFixture(), nil)
		found := false
		for _, atom := range groundedAtoms(source) {
			if atom.Value == "Other" {
				found = atom.Grounding != nil && len(atom.Grounding.Candidates) == 2
			}
		}
		if !found {
			t.Fatalf("explicit ambiguous artist was discarded in %q: %+v", prompt, source.Atoms)
		}
	}
}

func TestCanonicalPreferenceDoesNotCollapseGenuineOrIncompleteAmbiguity(t *testing.T) {
	for _, kind := range []string{"two canonical", "aliases only", "truncated", "unclassified"} {
		t.Run(kind, func(t *testing.T) {
			lookup := radioheadRecognitionFixture()
			artists := lookup.names["radiohead"]
			switch kind {
			case "two canonical":
				artists[1].Name, artists[1].MatchType = "Radiohead", mbindex.ArtistMatchCanonical
			case "aliases only":
				artists[0].Name, artists[0].MatchType = "Another Band", mbindex.ArtistMatchAlias
			case "truncated":
				lookup.truncated = true
			case "unclassified":
				artists[0].MatchType = ""
			}
			const prompt = "like Radiohead"
			source := Apply(context.Background(), prompt, lexicon.Extract(prompt), lookup, nil)
			atoms := groundedAtoms(source)
			if len(atoms) != 1 || len(atoms[0].Grounding.Candidates) != 2 || atoms[0].Grounding.Truncated != lookup.truncated {
				t.Fatalf("ambiguity was guessed away: %+v", atoms)
			}
		})
	}
}

type predecessorRecordingLookup struct{ referenceContextLookup }

func (predecessorRecordingLookup) LookupArtistRecordings(_ context.Context, queries []mbindex.ArtistRecordingQuery) ([]mbindex.ArtistRecordingLookup, error) {
	var results []mbindex.ArtistRecordingLookup
	for _, query := range queries {
		if query.ArtistMBID == "c74f4726-2671-4011-81b6-f70da905c05a" && query.Title == "Early Demo" {
			results = append(results, mbindex.ArtistRecordingLookup{ArtistMBID: query.ArtistMBID, Title: query.Title, Candidates: []mbindex.RecordingIdentity{{MBID: "early-recording", Title: "Early Demo", ArtistCredit: "On a Friday"}}})
		}
	}
	return results, nil
}

func TestArtistScopedRecordingKeepsAliasBeforeCanonicalPreference(t *testing.T) {
	const prompt = "like Radiohead — Early Demo"
	source := Apply(context.Background(), prompt, lexicon.Extract(prompt), predecessorRecordingLookup{radioheadRecognitionFixture()}, nil)
	atoms := groundedAtoms(source)
	if len(atoms) != 1 || atoms[0].Kind != "track" || atoms[0].Value != "On a Friday — Early Demo" || len(atoms[0].Grounding.Candidates) != 1 || atoms[0].Grounding.Candidates[0].ID != "early-recording" {
		t.Fatalf("explicit recording evidence lost to artist-only canonical fallback: %+v", atoms)
	}
}
