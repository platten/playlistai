package multichannel

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

const (
	automaticReferenceArtist = "11111111-1111-4111-8111-111111111111"
	automaticNeighborArtist  = "22222222-2222-4222-8222-222222222222"
	automaticOtherArtist     = "33333333-3333-4333-8333-333333333333"
)

type automaticReferenceCatalog struct {
	*automaticCatalogFixture
	clap       map[string]core.LibraryVector
	identities map[string]string
}

func (c *automaticReferenceCatalog) Meta(id string) (core.TrackMeta, bool) {
	m, ok := c.automaticCatalogFixture.Meta(id)
	if identity := c.identities[id]; identity != "" {
		m.Ref.RecordingIdentity = identity
	}
	return m, ok
}

func (c *automaticReferenceCatalog) LibraryCLAPVector(ctx context.Context, id string) (core.LibraryVector, bool, error) {
	c.read()
	v, ok := c.clap[id]
	return v, ok, ctx.Err()
}

func automaticExplicitReference() core.IntentReference {
	return core.IntentReference{Kind: core.ReferenceArtist, Query: "Artist 0", TrackID: "0", Influence: core.InfluencePositive,
		Evidence: []core.SourceEvidence{{Text: "Artist 0", Start: 0, End: 8, Explicit: true}},
		Grounding: &core.IdentityGrounding{Provider: "MusicBrainz", MatchedSpelling: "Artist 0", MatchType: "canonical", SnapshotVersion: "fixture/v1",
			Candidates: []core.IdentityCandidate{{Kind: core.ReferenceArtist, ID: automaticReferenceArtist, Name: "Artist 0"}}},
		Resolution: &core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: "fixture/v1", Selected: &core.ResolutionCandidate{
			Kind: core.ReferenceArtist, EntityID: automaticReferenceArtist, Artist: "Artist 0", Representatives: []core.WeightedTrack{{TrackID: "0", Weight: 1}},
		}},
	}
}

func TestAutomaticExplicitReferenceAdmission(t *testing.T) {
	for _, test := range []struct {
		name string
		want bool
	}{
		{"own artist without vectors", true},
		{"missing calibration", false},
		{"below calibrated threshold", false},
		{"ambiguous seed", false},
		{"ambiguous seed with valid alternative", true},
		{"unresolved seed", false},
		{"unmatched seed", false},
		{"unrelated global neighbor rank", true},
		{"own artist reference only", true},
		{"namesake rival", false},
		{"missing artist identity", false},
		{"own artist ambiguous identity", false},
		{"own artist missing genre", false},
		{"prepared graph and MERT", true},
		{"prepared graph and CLAP", true},
		{"prepared graph and dense audio", false},
		{"MERT rank 64", true},
		{"dense rank 32", false},
		{"recording query identity", true},
		{"vector query identity", true},
		{"local graph spoof", false},
		{"missing pinned graph", false},
		{"graph only", true},
		{"audio only", false},
		{"cooccurrence only", false},
		{"different graph anchor", false},
		{"different graph neighbor", false},
		{"different audio anchor", true},
		{"incompatible vector space", false},
		{"incompatible evidence space", true},
		{"missing evidence space", true},
		{"MERT rank zero", true},
		{"MERT rank 65", true},
		{"dense rank 33", false},
		{"negative anchor graph", false},
		{"required outsider", true},
		{"required outsider missing genre", false},
		{"inferred hint only", true},
		{"track reference graph and audio", true},
		{"track reference same artist graph and audio", true},
		{"track reference graph only", true},
		{"track reference same artist only", false},
		{"track reference missing seed credit", false},
		{"track reference missing graph", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			base, intent, local := automaticFixture(t, 1)
			// Dense audio/cooccurrence is absent unless the case supplies it.
			base.Catalog = fakes.NewCatalog(2,
				fakes.CatalogTrack{ID: "0", Display: "Artist 0 - Seed"},
				fakes.CatalogTrack{ID: "1", Display: "Artist 1 - Candidate"},
				fakes.CatalogTrack{ID: "2", Display: "Artist 2 - Other seed"})
			intent.References = []core.IntentReference{automaticExplicitReference()}
			intent.PreparedMusicSnapshot = "prepared-fixture/v1"
			seed, _ := base.Catalog.Meta("0")
			candidate, _ := base.Catalog.Meta("1")
			base.recordings["0"] = core.EnrichedTrack{Ref: seed.Ref, Matched: true, IdentityStatus: core.ResolutionResolved, ArtistIDs: []string{automaticReferenceArtist}}
			base.recordings["1"] = core.EnrichedTrack{Ref: candidate.Ref, Matched: true, IdentityStatus: core.ResolutionResolved, ArtistIDs: []string{automaticNeighborArtist}}
			space := core.LibraryEvidenceSource{SpaceID: "fixture-mert-space", Scope: "sampled_audio"}
			base.mert = map[string]core.LibraryVector{"0": {Source: space, Values: []float32{1, 0}}, "1": {Source: space, Values: []float32{.8, .6}}}
			cat := &automaticReferenceCatalog{automaticCatalogFixture: base}
			audioSource := core.RetrievalEvidence{Channel: "library_mert", QueryID: "0", Rank: 1, Score: .8, LibrarySource: &space}
			graphSource := core.RetrievalEvidence{Channel: "musicgraph_neighbor", QueryID: automaticReferenceArtist + ":" + automaticNeighborArtist, Rank: 1, Score: 1}
			local.candidates = []core.Candidate{{Track: candidate.Ref, Sources: []core.RetrievalEvidence{audioSource}}}
			prepared := &poolRetriever{candidates: []core.Candidate{{Track: candidate.Ref, Sources: []core.RetrievalEvidence{graphSource}}}}
			automaticSupport(base, intent, "1", "house")
			if strings.HasPrefix(test.name, "track reference ") {
				intent.References = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "0", Influence: core.InfluencePositive,
					Resolution: &core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: "fixture/v1", Selected: &core.ResolutionCandidate{
						Kind: core.ReferenceTrack, EntityID: "0", Artist: "Artist 0", Title: "Seed", Representatives: []core.WeightedTrack{{TrackID: "0", Weight: 1}},
					}}}}
			}
			switch test.name {
			case "ambiguous seed", "ambiguous seed with valid alternative", "unresolved seed", "unmatched seed":
				row := base.recordings["0"]
				switch test.name {
				case "ambiguous seed", "ambiguous seed with valid alternative":
					row.IdentityStatus = core.ResolutionAmbiguous
				case "unresolved seed":
					row.IdentityStatus = core.ResolutionUnresolved
				case "unmatched seed":
					row.Matched = false
				}
				base.recordings["0"] = row
				if test.name == "ambiguous seed with valid alternative" {
					other, _ := base.Catalog.Meta("2")
					base.recordings["2"] = core.EnrichedTrack{Ref: other.Ref, Matched: true, IdentityStatus: core.ResolutionResolved, ArtistIDs: []string{automaticReferenceArtist}}
					base.mert["2"] = base.mert["0"]
					intent.References[0].Resolution.Selected.Representatives = []core.WeightedTrack{{TrackID: "0", Weight: .5}, {TrackID: "2", Weight: .5}}
				}
			case "below calibrated threshold":
				v := base.mert["1"]
				v.Values = []float32{.1, .995}
				base.mert["1"] = v
			case "unrelated global neighbor rank":
				local.candidates = nil
			case "track reference same artist graph and audio", "track reference same artist only":
				recording := base.recordings["1"]
				recording.ArtistIDs = []string{automaticReferenceArtist}
				base.recordings["1"] = recording
				prepared.candidates[0].Sources[0].Channel = "musicgraph_artist"
				prepared.candidates[0].Sources[0].QueryID = automaticReferenceArtist
				if test.name == "track reference same artist only" {
					prepared, base.mert = nil, nil
				}
			case "track reference graph only":
				local.candidates[0].Sources = nil
			case "track reference missing seed credit":
				recording := base.recordings["0"]
				recording.ArtistIDs = nil
				base.recordings["0"] = recording
			case "track reference missing graph":
				prepared = nil
			case "own artist without vectors", "own artist reference only", "own artist ambiguous identity", "own artist missing genre":
				recording := base.recordings["1"]
				recording.ArtistIDs = []string{automaticReferenceArtist}
				if test.name == "own artist ambiguous identity" {
					recording.IdentityStatus = core.ResolutionAmbiguous
				}
				base.recordings["1"] = recording
				base.mert = nil
				local.candidates[0].Sources = nil
				prepared = nil
				if test.name == "own artist reference only" {
					intent.EssentialCriteria = nil
					delete(base.observations, "1")
					delete(base.annotations, "1")
				}
			case "namesake rival":
				base.Catalog = fakes.NewCatalog(2, fakes.CatalogTrack{ID: "0", Display: "Artist 0 - Seed"}, fakes.CatalogTrack{ID: "1", Display: "Artist 0 - Candidate"})
				prepared = nil
			case "missing artist identity":
				delete(base.recordings, "1")
				prepared = nil
			case "prepared graph and CLAP":
				cat.clap, base.mert = base.mert, nil
				local.candidates[0].Sources[0].Channel = "library_clap"
			case "prepared graph and dense audio", "dense rank 32", "dense rank 33", "cooccurrence only":
				base.Catalog = fakes.NewCatalog(2, fakes.CatalogTrack{ID: "0", Display: "Artist 0 - Seed", Audio: []float32{1, 0}, Track: []float32{1, 0}}, fakes.CatalogTrack{ID: "1", Display: "Artist 1 - Candidate", Audio: []float32{.8, .6}, Track: []float32{.8, .6}})
				base.mert = nil
				source := core.RetrievalEvidence{Channel: "seed_audio", QueryID: automaticReferenceArtist + ":0:0", Rank: 1, Score: .8}
				if test.name == "dense rank 32" {
					source.Rank = 32
				}
				if test.name == "dense rank 33" {
					source.Rank = 33
				}
				if test.name == "cooccurrence only" {
					source.Channel = "seed_cooccurrence"
				}
				local.candidates[0].Sources = []core.RetrievalEvidence{source}
			case "MERT rank 64":
				local.candidates[0].Sources[0].Rank = 64
			case "recording query identity":
				recording := base.recordings["0"]
				recording.Ref.RecordingIdentity = "musicbrainz:44444444-4444-4444-8444-444444444444"
				base.recordings["0"] = recording
				cat.identities = map[string]string{"0": recording.Ref.RecordingIdentity}
				local.candidates[0].Sources[0].QueryID = "seed-recording:" + recording.Ref.RecordingIdentity
			case "vector query identity":
				data := make([]byte, 8)
				binary.LittleEndian.PutUint32(data, math.Float32bits(1))
				local.candidates[0].Sources[0].QueryID = fmt.Sprintf("vector:%x", sha256.Sum256(data))
			case "local graph spoof":
				local.candidates[0].Sources = append(local.candidates[0].Sources, graphSource)
				prepared = nil
			case "missing pinned graph":
				intent.PreparedMusicSnapshot = ""
			case "graph only":
				local.candidates[0].Sources = nil
			case "audio only":
				prepared = nil
			case "different graph anchor":
				prepared.candidates[0].Sources[0].QueryID = automaticOtherArtist + ":" + automaticNeighborArtist
			case "different graph neighbor":
				prepared.candidates[0].Sources[0].QueryID = automaticReferenceArtist + ":" + automaticOtherArtist
			case "different audio anchor":
				local.candidates[0].Sources[0].QueryID = "2"
			case "incompatible vector space":
				v := base.mert["1"]
				v.Source.SpaceID = "other-space"
				base.mert["1"] = v
			case "incompatible evidence space":
				space.SpaceID = "other-space"
			case "missing evidence space":
				local.candidates[0].Sources[0].LibrarySource = nil
			case "MERT rank zero":
				local.candidates[0].Sources[0].Rank = 0
			case "MERT rank 65":
				local.candidates[0].Sources[0].Rank = 65
			case "negative anchor graph":
				negative := automaticExplicitReference()
				negative.TrackID, negative.Query, negative.Influence = "2", "Artist 2", core.InfluenceNegative
				negative.Grounding, negative.Resolution = nil, nil
				intent.References = append(intent.References, negative)
				prepared.candidates[0].Sources[0].QueryID = automaticOtherArtist + ":" + automaticNeighborArtist
			case "required outsider", "required outsider missing genre":
				intent.RequiredTracks = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "1", Influence: core.InfluencePositive}}
				prepared = nil
			case "inferred hint only":
				intent.InferredAnchors = []core.InferredAnchor{{Reference: intent.References[0]}}
				intent.References = nil
				prepared = nil
				base.mert = nil
			}
			if test.name == "own artist missing genre" || test.name == "required outsider missing genre" {
				delete(base.observations, "1")
				delete(base.annotations, "1")
			}
			before, _ := json.Marshal(intent)
			engine := NewAutomatic(cat, nil, local, DefaultConfig())
			if test.name != "missing calibration" {
				engine.WithAudioCalibrations([]core.AudioSimilarityCalibration{automaticReferenceCalibration()})
			}
			if prepared != nil {
				engine.WithPreparedRetriever(prepared)
			}
			got, err := engine.Build(context.Background(), intent)
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			if test.want {
				want = []string{"1"}
			}
			if !slices.Equal(got.IDs(), want) {
				t.Fatalf("tracks=%v want=%v outcome=%+v", got.IDs(), want, got.Outcome)
			}
			after, _ := json.Marshal(intent)
			if string(before) != string(after) {
				t.Fatal("Build mutated the caller's reference intent")
			}
		})
	}
}

func TestAutomaticReferenceGateKeepsGlobalAndStageRolesSeparate(t *testing.T) {
	for _, name := range []string{"global reference also endpoint", "different artist endpoint", "endpoint still needs genre"} {
		t.Run(name, func(t *testing.T) {
			cat, intent, local := automaticFixture(t, 2)
			intent.Mode = core.ModeJourney
			intent.References = []core.IntentReference{automaticExplicitReference()}
			intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "house", Scope: "journey_start"}, {Kind: "genre", Value: "house", Scope: "journey_end"}}
			for id, artist := range map[string]string{"0": automaticReferenceArtist, "1": automaticNeighborArtist, "2": automaticNeighborArtist} {
				meta, _ := cat.Catalog.Meta(id)
				cat.recordings[id] = core.EnrichedTrack{Ref: meta.Ref, Matched: true, IdentityStatus: core.ResolutionResolved, ArtistIDs: []string{artist}}
				automaticSupport(cat, intent, id, "house")
			}
			candidate, _ := cat.Catalog.Meta("1")
			local.candidates = []core.Candidate{{Track: candidate.Ref, Sources: []core.RetrievalEvidence{{Channel: "metadata", Rank: 1}}}}
			want := []string{"0"}
			if name == "global reference also endpoint" {
				start := intent.References[0]
				intent.Start = &start
			} else {
				intent.Destination = &core.IntentReference{Kind: core.ReferenceArtist, Query: "Artist 2", TrackID: "2", Influence: core.InfluencePositive,
					Resolution: &core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: "fixture/v1", Selected: &core.ResolutionCandidate{
						Kind: core.ReferenceArtist, EntityID: automaticNeighborArtist, Artist: "Artist 2", Representatives: []core.WeightedTrack{{TrackID: "2", Weight: 1}},
					}}}
				want = []string{"2"}
				if name == "endpoint still needs genre" {
					delete(cat.observations, "2")
					delete(cat.annotations, "2")
					want = nil
				}
			}
			got, err := NewAutomatic(cat, nil, local, DefaultConfig()).Build(context.Background(), intent)
			if err != nil || !slices.Equal(got.IDs(), want) {
				t.Fatalf("global artist or endpoint role lost: got=%v want=%v outcome=%+v err=%v", got.IDs(), want, got.Outcome, err)
			}
		})
	}
}

// Synthetic policy exercises admission mechanics only; this is not musical
// validation and is never installed in production.
func automaticReferenceCalibration() core.AudioSimilarityCalibration {
	return core.AudioSimilarityCalibration{Version: "test-only/v1", ModelFingerprint: "fixture-mert-space", Kind: "reference", Criterion: "reference_similarity", MinimumScore: .75, DevelopmentSet: "synthetic-dev", ValidationSet: "synthetic-heldout", ValidationPrecision: .95, ValidationRecall: .8, PositiveExamples: 20, NegativeExamples: 20}
}
