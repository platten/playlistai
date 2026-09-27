package core

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const (
	corroborationArtistA = "aaaaaaaa-1111-4111-8111-111111111111"
	corroborationArtistB = "bbbbbbbb-2222-4222-8222-222222222222"
	corroborationAnchorA = "cccccccc-3333-4333-8333-333333333333"
	corroborationAnchorB = "dddddddd-4444-4444-8444-444444444444"
	corroborationRecordA = "eeeeeeee-5555-4555-8555-555555555555"
	corroborationRecordB = "ffffffff-6666-4666-8666-666666666666"
)

func identityProofFixture() *IdentityGrounding {
	return &IdentityGrounding{Provider: "MusicBrainz", MatchedSpelling: "Shared Artist", MatchType: "canonical", SnapshotVersion: "fixture-v1",
		Candidates: []IdentityCandidate{{Kind: ReferenceArtist, ID: corroborationArtistA, Name: "Shared Artist"}, {Kind: ReferenceArtist, ID: corroborationArtistB, Name: "Shared Artist"}},
		Corroboration: &IdentityCorroboration{SelectedID: corroborationArtistA, Method: ArtistCoperformanceMethod, Supports: []IdentityCorroborationSupport{{
			AnchorID: corroborationAnchorA, RecordingIDs: []string{corroborationRecordA, corroborationRecordB},
			Source: ContextSource{Provider: "musicbrainz", URL: "https://musicbrainz.org/ws/2/artist/" + corroborationAnchorA + "?inc=recording-rels%2Bartist-credits&fmt=json", Revision: strings.Repeat("ab", 32), License: "CC0-1.0"},
		}}},
	}
}

func TestCorroboratedArtistRequiresCompleteAttributedIndependentProof(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*IdentityGrounding)
	}{
		{"missing proof", func(g *IdentityGrounding) { g.Corroboration = nil }},
		{"truncated universe", func(g *IdentityGrounding) { g.Truncated = true }},
		{"user confirmed", func(g *IdentityGrounding) { g.Confirmed = true }},
		{"other provider", func(g *IdentityGrounding) { g.Provider = "paipack" }},
		{"false provider substring", func(g *IdentityGrounding) { g.Provider = "NotMusicBrainz+paipack" }},
		{"single candidate", func(g *IdentityGrounding) { g.Candidates = g.Candidates[:1] }},
		{"too many candidates", func(g *IdentityGrounding) {
			for i := len(g.Candidates); i < 65; i++ {
				g.Candidates = append(g.Candidates, IdentityCandidate{Kind: ReferenceArtist, ID: fmt.Sprintf("%08x-7777-4777-8777-777777777777", i), Name: "Other"})
			}
		}},
		{"duplicate candidate", func(g *IdentityGrounding) { g.Candidates[1].ID = strings.ToUpper(g.Candidates[0].ID) }},
		{"not an artist", func(g *IdentityGrounding) { g.Candidates[1].Kind = ReferenceTrack }},
		{"malformed candidate", func(g *IdentityGrounding) { g.Candidates[1].ID = "artist:local" }},
		{"empty name", func(g *IdentityGrounding) { g.Candidates[1].Name = " " }},
		{"unknown selected identity", func(g *IdentityGrounding) { g.Corroboration.SelectedID = corroborationAnchorB }},
		{"other method", func(g *IdentityGrounding) { g.Corroboration.Method = "model_opinion" }},
		{"no supports", func(g *IdentityGrounding) { g.Corroboration.Supports = nil }},
		{"too many supports", func(g *IdentityGrounding) {
			g.Corroboration.Supports = append(g.Corroboration.Supports, g.Corroboration.Supports[0], g.Corroboration.Supports[0])
		}},
		{"duplicate anchor", func(g *IdentityGrounding) {
			g.Corroboration.Supports = append(g.Corroboration.Supports, g.Corroboration.Supports[0])
		}},
		{"candidate is anchor", func(g *IdentityGrounding) { g.Corroboration.Supports[0].AnchorID = corroborationArtistB }},
		{"malformed anchor", func(g *IdentityGrounding) { g.Corroboration.Supports[0].AnchorID = "arbitrary" }},
		{"no supporting recording", func(g *IdentityGrounding) { g.Corroboration.Supports[0].RecordingIDs = nil }},
		{"single recording", func(g *IdentityGrounding) { g.Corroboration.Supports[0].RecordingIDs = []string{corroborationRecordA} }},
		{"duplicate recording", func(g *IdentityGrounding) {
			g.Corroboration.Supports[0].RecordingIDs = []string{corroborationRecordA, strings.ToUpper(corroborationRecordA)}
		}},
		{"malformed recording", func(g *IdentityGrounding) { g.Corroboration.Supports[0].RecordingIDs[0] = "recording:1" }},
		{"nil UUID", func(g *IdentityGrounding) {
			g.Corroboration.Supports[0].RecordingIDs[0] = "00000000-0000-0000-0000-000000000000"
		}},
		{"source provider", func(g *IdentityGrounding) { g.Corroboration.Supports[0].Source.Provider = "local-model" }},
		{"source license", func(g *IdentityGrounding) { g.Corroboration.Supports[0].Source.License = "unknown" }},
		{"mutable revision", func(g *IdentityGrounding) { g.Corroboration.Supports[0].Source.Revision = "latest" }},
		{"nonhex revision", func(g *IdentityGrounding) { g.Corroboration.Supports[0].Source.Revision = strings.Repeat("x", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			grounding := identityProofFixture()
			tc.change(grounding)
			if _, ok := grounding.CorroboratedArtist(); ok {
				t.Fatal("invalid proof selected a homonym")
			}
		})
	}
	for _, sourceURL := range []string{
		"http://musicbrainz.org/ws/2/artist/" + corroborationAnchorA,
		"https://evil.example/ws/2/artist/" + corroborationAnchorA,
		"https://musicbrainz.org.evil.example/ws/2/artist/" + corroborationAnchorA,
		"https://musicbrainz.org/ws/2/artist/" + corroborationAnchorB,
		"https://musicbrainz.org/ws/2/recording/" + corroborationAnchorA,
		"https://musicbrainz.org/artist/" + corroborationAnchorA,
		"https://musicbrainz.org/WS/2/ARTIST/" + corroborationAnchorA,
		"https://user@musicbrainz.org/ws/2/artist/" + corroborationAnchorA,
		"https://musicbrainz.org/ws/2/artist/" + corroborationAnchorA + "#part",
	} {
		grounding := identityProofFixture()
		grounding.Corroboration.Supports[0].Source.URL = sourceURL
		if _, ok := grounding.CorroboratedArtist(); ok {
			t.Errorf("accepted unauthenticated source %s", sourceURL)
		}
	}
	var absent *IdentityGrounding
	if _, ok := absent.CorroboratedArtist(); ok {
		t.Fatal("absent proof selected an artist")
	}
}

func TestCorroborationCountsDistinctRecordingsAcrossAnchors(t *testing.T) {
	grounding := identityProofFixture()
	proof := grounding.Corroboration
	second := proof.Supports[0]
	second.AnchorID = corroborationAnchorB
	second.Source.URL = strings.ReplaceAll(second.Source.URL, corroborationAnchorA, corroborationAnchorB)
	second.RecordingIDs = []string{corroborationRecordA}
	proof.Supports = append(proof.Supports, second)
	before := cloneIdentityGrounding(grounding)
	if artist, ok := grounding.CorroboratedArtist(); !ok || artist.ID != corroborationArtistA {
		t.Fatalf("valid proof lost selected artist: %+v %v", artist, ok)
	}
	if !reflect.DeepEqual(grounding, before) || grounding.Confirmed || len(grounding.Candidates) != 2 {
		t.Fatal("corroboration rewrote the original candidate universe or consent")
	}
	proof.Supports[0].RecordingIDs = []string{corroborationRecordA}
	if _, ok := grounding.CorroboratedArtist(); ok {
		t.Fatal("one recording repeated by two anchors became two independent performances")
	}
}

func TestCorroborationSurvivesHistoryAndDeepCopyWithoutChangingLegacyJSON(t *testing.T) {
	grounding := identityProofFixture()
	intent := MusicIntent{Version: CurrentIntentVersion, References: []IntentReference{{Kind: ReferenceArtist, Query: "Shared Artist", Influence: InfluencePositive, Grounding: grounding}}}.Normalized()
	blob, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	var restored MusicIntent
	if err := json.Unmarshal(blob, &restored); err != nil {
		t.Fatal(err)
	}
	if got, ok := restored.References[0].Grounding.CorroboratedArtist(); !ok || got.ID != corroborationArtistA {
		t.Fatal("saved proof no longer validates")
	}
	intent.References[0].Grounding.Corroboration.Supports[0].RecordingIDs[0] = "changed"
	if grounding.Corroboration.Supports[0].RecordingIDs[0] != corroborationRecordA {
		t.Fatal("normalization shared mutable proof with the caller")
	}
	frozen, err := FreezeSearch(SearchSnapshot{PolicyVersion: EnhancedSearchPolicyVersion}, Playlist{Intent: restored})
	if err != nil {
		t.Fatal(err)
	}
	restored.References[0].Grounding.Corroboration.SelectedID = corroborationArtistB
	if frozen.Validate() != nil || frozen.Result.Intent.References[0].Grounding.Corroboration.SelectedID != corroborationArtistA {
		t.Fatal("snapshot shared mutable corroboration")
	}
	blob, err = json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	var saved SearchSnapshot
	if err := json.Unmarshal(blob, &saved); err != nil || saved.Validate() != nil {
		t.Fatalf("saved snapshot lost its evidence fingerprint: %v", err)
	}
	if selected, ok := saved.Result.Intent.References[0].Grounding.CorroboratedArtist(); !ok || selected.ID != corroborationArtistA {
		t.Fatal("snapshot round trip lost valid corroboration")
	}
	saved.Result.Intent.References[0].Grounding.Corroboration.Supports[0].RecordingIDs[0] = corroborationAnchorB
	if saved.Validate() == nil {
		t.Fatal("tampered corroborating recording retained the old snapshot fingerprint")
	}
	legacy := identityProofFixture()
	legacy.Corroboration = nil
	blob, err = json.Marshal(legacy)
	if err != nil || strings.Contains(string(blob), "corroboration") {
		t.Fatalf("legacy no-proof serialization changed: %s %v", blob, err)
	}
}

func TestCorroborationAcceptsMusicBrainzUniverseInCombinedRecognition(t *testing.T) {
	for _, provider := range []string{"MusicBrainz", "MusicBrainz+paipack", "paipack+MusicBrainz"} {
		g := identityProofFixture()
		g.Provider = provider
		for i := len(g.Candidates); i < 64; i++ {
			g.Candidates = append(g.Candidates, IdentityCandidate{Kind: ReferenceArtist, ID: fmt.Sprintf("%08x-7777-4777-8777-777777777777", i), Name: "Other"})
		}
		if _, ok := g.CorroboratedArtist(); !ok || g.Provider != provider {
			t.Fatalf("combined complete MusicBrainz universe rejected or rewritten: %s", provider)
		}
		g.Candidates[63].ID = "local-artist:shared"
		if _, ok := g.CorroboratedArtist(); ok {
			t.Fatal("combined provider label authenticated a synthetic candidate")
		}
	}
}
