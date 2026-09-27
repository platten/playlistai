package multichannel

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

// This is a deterministic derived-data fixture. No provider, native model or store.
func TestMERTSearchRepresentationLocationPreservesRanking(t *testing.T) {
	vec := func(c float64) []float32 { return []float32{float32(c), float32(math.Sqrt(1 - c*c))} }
	cat := fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "seed", Display: "Reference - Song", Audio: vec(1), Track: vec(1)},
		fakes.CatalogTrack{ID: "dense", Display: "Evaluated - Candidate", Audio: vec(.9661348633), Track: vec(.7597677658)},
		fakes.CatalogTrack{ID: "packed", Display: "Packed - Candidate"},
	)
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, VerificationPolicy: core.BestAvailable, Mode: core.ModeSimilar, Count: 1,
		References: []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "seed", Influence: core.InfluencePositive}},
		Controls:   core.IntentControls{RecommendationMode: core.EnhancedHybrid, TotalTrackCount: 1, AudioWeight: .5, CooccurrenceWeight: .5, ArtistDiversity: .7},
	}.Normalized()
	model := core.AudioRepresentationIdentity{Model: "MERT", Revision: "rev", Preprocessing: "pcm", Runtime: "onnx", Dimension: 2, WeightsSHA256: "hash", Pooling: "mean"}
	rep := func(id string, c float64) core.AudioRepresentation {
		m, _ := cat.Meta(id)
		return core.AudioRepresentation{ID: id + "-representation", TrackID: id, TrackKey: core.ProvisionalRecordingKey(m.Ref), CatalogVersion: cat.CatalogVersion(), Model: model, Pooled: vec(c)}
	}
	seed, raw, scored := rep("seed", 1), rep("packed", .8887467445466016), rep("dense", .8525581799111228)
	seedMeta, _ := cat.Meta("seed")
	rawScore, _ := enhancedCosine(seed.Pooled, raw.Pooled, 2)
	search := &core.MERTSimilaritySearch{Recorded: true, CatalogVersion: cat.CatalogVersion(), Model: model, SearchableTracks: 3,
		Queries: []core.MERTSimilarityQuery{{GroupID: "reference", Track: seedMeta.Ref, Weight: 1, RepresentationID: seed.ID}},
		Hits:    []core.MERTSimilarityHit{{GroupID: "reference", QueryTrackID: "seed", TrackID: "packed", Rank: 1, Score: rawScore, QueryWeight: 1, Representation: raw}},
	}
	input := core.EnhancedAudioInput{PolicyVersion: EnhancedPolicyVersion, CatalogVersion: cat.CatalogVersion(), Model: model, Representations: map[string]core.AudioRepresentation{"seed": seed, "dense": scored}, MERTSearch: search}
	o := New(cat, nil, cat, DefaultConfig())
	candidates := o.mertCandidates(search)
	if len(candidates) != 1 {
		t.Fatal("exact frozen MERT hit did not establish candidate")
	}
	denseMeta, _ := cat.Meta("dense")
	candidates = append(candidates, core.Candidate{Track: denseMeta.Ref})
	for i := range candidates {
		candidates[i].FitTier = fitClose
		candidates[i].MusicalFit = core.EvidenceUnknown
	}
	type observation struct {
		relevance, requestFit float64
		available             bool
		selected              string
	}
	evaluate := func(label string) observation {
		snapshot, err := core.NewEnhancedAudioSnapshot(input)
		if err != nil {
			t.Fatal(err)
		}
		ranked, err := NewRanker(cat, DefaultConfig()).Rank(context.Background(), candidates, ports.RankRequest{Intent: intent, EnhancedAudio: snapshot})
		if err != nil {
			t.Fatal(err)
		}
		var observed observation
		for _, c := range ranked {
			relevance, _ := candidateRequestFit(c, intent)
			t.Logf("%s id=%s requestFit=%g available=%t combinedMERT=%g available=%t selectorRelevance=%g total=%g", label, c.Track.ID, c.Scores.RequestFit, c.Available.RequestFit, c.Scores.CombinedMERT, c.Available.CombinedMERT, relevance, c.Scores.Total)
			if c.Track.ID == "packed" {
				observed.relevance, observed.requestFit, observed.available = relevance, c.Scores.RequestFit, c.Available.RequestFit
			}
		}
		selected, err := NewSelector(cat, DefaultConfig()).Select(context.Background(), ranked, ports.SelectionRequest{Intent: intent, Count: 1})
		if err != nil || len(selected.Candidates) != 1 {
			t.Fatalf("selection=%+v error=%v", selected, err)
		}
		observed.selected = selected.Candidates[0].Track.ID
		t.Logf("%s selected=%s", label, observed.selected)
		return observed
	}
	before := evaluate("search-hit-only")
	// No new observation: expose the already saved, identity-checked hit vector to
	// the scorer using its other snapshot map. This must not change fit scales.
	input.Representations["packed"] = raw
	after := evaluate("same-evidence-hydrated")
	for _, observed := range []observation{before, after} {
		if !observed.available || math.IsNaN(observed.requestFit) || math.IsInf(observed.requestFit, 0) {
			t.Fatalf("saved MERT evidence must produce an available finite request fit: %+v", observed)
		}
		if math.IsNaN(observed.relevance) || math.IsInf(observed.relevance, 0) || math.Abs(observed.relevance-observed.requestFit) > 1e-9 {
			t.Fatalf("selector relevance must use normalized request fit: %+v", observed)
		}
		if observed.selected != "dense" {
			t.Fatalf("completed evaluated candidate must win on the shared fit scale: %+v", observed)
		}
	}
	if math.Abs(before.relevance-after.relevance) > 1e-6 || before.selected != after.selected {
		t.Fatalf("same frozen MERT evidence changed ranking: before=%+v after=%+v", before, after)
	}
}

func searchRepresentationFixture(t *testing.T) (*fakes.Catalog, core.EnhancedAudioInput, core.TrackRef) {
	t.Helper()
	cat, input := enhancedFixture(t)
	meta, _ := cat.Meta("a")
	rep := input.Representations["a"]
	rep.ID = "saved-hit"
	delete(input.Representations, "a")
	seed, _ := cat.Meta("seed")
	input.MERTSearch = &core.MERTSimilaritySearch{Recorded: true, CatalogVersion: input.CatalogVersion, Model: input.Model,
		Queries: []core.MERTSimilarityQuery{{GroupID: "reference", Track: seed.Ref, Weight: 1, RepresentationID: "saved-reference"}},
		Hits:    []core.MERTSimilarityHit{{GroupID: "reference", QueryTrackID: "seed", TrackID: "a", Rank: 1, Score: -1, QueryWeight: 1, Representation: rep}},
	}
	return cat, input, meta.Ref
}

func TestMERTSearchRepresentationRejectsIncompatibleEvidence(t *testing.T) {
	tests := []struct {
		name   string
		change func(*core.EnhancedAudioInput)
	}{
		{"unrecorded search", func(i *core.EnhancedAudioInput) { i.MERTSearch.Recorded = false }},
		{"search catalog", func(i *core.EnhancedAudioInput) { i.MERTSearch.CatalogVersion = "other" }},
		{"search model", func(i *core.EnhancedAudioInput) { i.MERTSearch.Model.Revision = "other" }},
		{"invalid policy", func(i *core.EnhancedAudioInput) { i.PolicyVersion = "other" }},
		{"missing query", func(i *core.EnhancedAudioInput) { i.MERTSearch.Queries = nil }},
		{"query group", func(i *core.EnhancedAudioInput) { i.MERTSearch.Queries[0].GroupID = "other" }},
		{"query identity", func(i *core.EnhancedAudioInput) { i.MERTSearch.Queries[0].Track.ID = "other" }},
		{"query receipt", func(i *core.EnhancedAudioInput) { i.MERTSearch.Queries[0].RepresentationID = "" }},
		{"query weight", func(i *core.EnhancedAudioInput) { i.MERTSearch.Queries[0].Weight = 0 }},
		{"query nonfinite weight", func(i *core.EnhancedAudioInput) { i.MERTSearch.Queries[0].Weight = math.NaN() }},
		{"wrong hit", func(i *core.EnhancedAudioInput) { i.MERTSearch.Hits[0].TrackID = "other" }},
		{"missing rank", func(i *core.EnhancedAudioInput) { i.MERTSearch.Hits[0].Rank = 0 }},
		{"invalid score", func(i *core.EnhancedAudioInput) { i.MERTSearch.Hits[0].Score = 1.1 }},
		{"nonfinite score", func(i *core.EnhancedAudioInput) { i.MERTSearch.Hits[0].Score = math.Inf(1) }},
		{"missing representation receipt", func(i *core.EnhancedAudioInput) { i.MERTSearch.Hits[0].Representation.ID = "" }},
		{"representation track", func(i *core.EnhancedAudioInput) { i.MERTSearch.Hits[0].Representation.TrackID = "other" }},
		{"recording identity", func(i *core.EnhancedAudioInput) { i.MERTSearch.Hits[0].Representation.TrackKey = "different-recording" }},
		{"representation catalog", func(i *core.EnhancedAudioInput) { i.MERTSearch.Hits[0].Representation.CatalogVersion = "other" }},
		{"representation model", func(i *core.EnhancedAudioInput) { i.MERTSearch.Hits[0].Representation.Model.Revision = "other" }},
		{"wrong dimension", func(i *core.EnhancedAudioInput) { i.MERTSearch.Hits[0].Representation.Pooled = []float32{1} }},
		{"zero vector", func(i *core.EnhancedAudioInput) { i.MERTSearch.Hits[0].Representation.Pooled = []float32{0, 0} }},
		{"nonfinite vector", func(i *core.EnhancedAudioInput) {
			i.MERTSearch.Hits[0].Representation.Pooled = []float32{float32(math.NaN()), 1}
		}},
		{"conflicting receipts", func(i *core.EnhancedAudioInput) {
			other := i.MERTSearch.Hits[0]
			other.Representation.ID = "another-observation"
			i.MERTSearch.Hits = append(i.MERTSearch.Hits, other)
		}},
		{"conflicting vectors", func(i *core.EnhancedAudioInput) {
			other := i.MERTSearch.Hits[0]
			other.Representation.Pooled = []float32{1, 0}
			i.MERTSearch.Hits = append(i.MERTSearch.Hits, other)
		}},
		{"conflicting coverage", func(i *core.EnhancedAudioInput) {
			other := i.MERTSearch.Hits[0]
			other.Representation.Coverage = core.PreviewCoverage{Available: true, CoveredSeconds: 30}
			i.MERTSearch.Hits = append(i.MERTSearch.Hits, other)
		}},
		{"conflicting observation identity", func(i *core.EnhancedAudioInput) {
			other := i.MERTSearch.Hits[0]
			other.Representation.AudioSHA256 = "different-preview"
			i.MERTSearch.Hits = append(i.MERTSearch.Hits, other)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, input, track := searchRepresentationFixture(t)
			test.change(&input)
			if _, ok := representation(input, track); ok {
				t.Fatal("incompatible evidence became a representation")
			}
		})
	}
}

type mertObservationCatalog struct {
	libraryEvidenceFixture
	observation core.AudioObservation
}

func (c mertObservationCatalog) LibraryObservation(ctx context.Context, _, _ string) (core.AudioObservation, error) {
	return c.observation, ctx.Err()
}

func TestMERTSearchObservationLocationPreservesCoverageAndFingerprintChoice(t *testing.T) {
	for _, test := range []struct {
		name              string
		packedSeconds     float64
		packedFingerprint string
		invalidMap        bool
		invalidHit        bool
		want              float64
	}{
		{name: "longer search observation", packedSeconds: 5, want: -1},
		{name: "longer packed observation", packedSeconds: 60, want: 1},
		{name: "equal coverage search fingerprint", packedSeconds: 30, packedFingerprint: "z", want: -1},
		{name: "equal coverage packed fingerprint", packedSeconds: 30, packedFingerprint: "0", want: 1},
		{name: "invalid map cannot supply coverage", packedSeconds: 60, invalidMap: true, want: 1},
		{name: "invalid hit cannot supply observation", packedSeconds: 5, invalidHit: true, want: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			base, input, track := searchRepresentationFixture(t)
			rep := input.MERTSearch.Hits[0].Representation
			rep.Coverage = core.PreviewCoverage{Available: true, CoveredSeconds: 30, EndSeconds: 30, Source: "deezer"}
			if test.invalidHit {
				rep.TrackKey = "wrong-recording"
			}
			input.MERTSearch.Hits[0].Representation = rep
			if test.invalidMap {
				invalid := rep
				invalid.TrackKey = "wrong-recording"
				invalid.Coverage.CoveredSeconds = 120
				input.Representations[track.ID] = invalid
			}
			cat := mertObservationCatalog{
				libraryEvidenceFixture: libraryEvidenceFixture{Catalog: base, vectors: map[string]core.LibraryVector{
					"seed":   {Source: core.LibraryEvidenceSource{SpaceID: "pack-space"}, Values: []float32{1, 0}},
					track.ID: {Source: core.LibraryEvidenceSource{SpaceID: "pack-space"}, Values: []float32{1, 0}},
				}},
				observation: core.AudioObservation{Coverage: core.PreviewCoverage{Available: true, CoveredSeconds: test.packedSeconds}, Fingerprint: test.packedFingerprint},
			}
			cfg := DefaultConfig()
			cfg.LibraryEvidenceEnabled = true
			evaluate := func() core.Candidate {
				t.Helper()
				snapshot := freezeEnhanced(t, input)
				ranked, err := NewRanker(cat, cfg).Rank(context.Background(), []core.Candidate{{Track: track}}, ports.RankRequest{Intent: enhancedIntent(1), EnhancedAudio: snapshot})
				if err != nil || len(ranked) != 1 {
					t.Fatalf("rank=%+v error=%v", ranked, err)
				}
				got := ranked[0]
				if !got.Available.LibraryMERT || !got.Available.CombinedMERT || !got.Available.RequestFit || got.Available.EnhancedMERT == test.invalidHit {
					t.Fatalf("unexpected observation availability: %+v", got.Available)
				}
				if got.Scores.CombinedMERT != test.want || math.IsNaN(got.Scores.RequestFit) || math.IsInf(got.Scores.RequestFit, 0) {
					t.Fatalf("wrong observation choice: scores=%+v want combinedMERT=%g", got.Scores, test.want)
				}
				if !test.invalidHit {
					selected, ok := selectedRepresentation(snapshot.Input(), track)
					if !ok || selected.Coverage != rep.Coverage || audio.Fingerprint(selected) != audio.Fingerprint(rep) {
						t.Fatal("vector and observation metadata did not come from the exact saved hit")
					}
				}
				return got
			}
			searchOnly := evaluate()
			input.Representations[track.ID] = rep
			hydrated := evaluate()
			if !reflect.DeepEqual(searchOnly.Scores, hydrated.Scores) || searchOnly.Available != hydrated.Available {
				t.Fatalf("same observation changed full ranking across snapshot locations: search=%+v hydrated=%+v", searchOnly, hydrated)
			}
		})
	}
}

func TestMERTSearchRepresentationIsImmutableAndPreservesExistingObservation(t *testing.T) {
	_, input, track := searchRepresentationFixture(t)
	input.MERTSearch.Hits = append(input.MERTSearch.Hits, input.MERTSearch.Hits[0])
	frozen := freezeEnhanced(t, input)
	before := frozen.Fingerprint()
	if got, ok := representation(frozen.Input(), track); !ok || !reflect.DeepEqual(got, []float32{-1, 0}) {
		t.Fatalf("same repeated observation unavailable: %v %t", got, ok)
	}
	if freezeEnhanced(t, input).Fingerprint() != before || frozen.Fingerprint() != before {
		t.Fatal("lookup mutated snapshot")
	}
	// The already selected snapshot observation keeps priority over a search hit.
	selected := input.MERTSearch.Hits[0].Representation
	selected.ID, selected.Pooled = "selected-observation", []float32{0, 1}
	input.Representations[track.ID] = selected
	if got, ok := representation(input, track); !ok || !reflect.DeepEqual(got, selected.Pooled) {
		t.Fatalf("search replaced the selected observation: %v %t", got, ok)
	}
}

func TestEnhancedMissingRequestFitDoesNotUseRawRetrievalScale(t *testing.T) {
	intent := enhancedIntent(1)
	intent.References = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "seed", Influence: core.InfluencePositive}}
	candidate := core.Candidate{Sources: []core.RetrievalEvidence{{Channel: ChannelMERTAudio, QueryID: "reference:seed", Score: .95}}}
	if _, ok := enhancedRequestRelevance(candidate, intent); !ok {
		t.Fatal("reference admission evidence was lost")
	}
	if score, ok := candidateRequestFit(candidate, intent); ok || score != 0 {
		t.Fatalf("unknown fit bypassed completed scale: %g %t", score, ok)
	}
	candidate.Available.RequestFit, candidate.Scores.RequestFit = true, .12
	if score, ok := candidateRequestFit(candidate, intent); !ok || score != .12 {
		t.Fatalf("completed fit was replaced: %g %t", score, ok)
	}
}

func TestMERTSearchScoringKeepsNegativeReferencesModesAndCancellation(t *testing.T) {
	cat, input, track := searchRepresentationFixture(t)
	// Positive and negative anchors have the same vector: no positive net MERT.
	input.MERTSearch.Hits[0].Representation.Pooled = []float32{1, 0}
	input.MERTSearch.Hits[0].Score = 1
	seed := input.Representations["seed"]
	seed.TrackID = "b"
	meta, _ := cat.Meta("b")
	seed.TrackKey = core.ProvisionalRecordingKey(meta.Ref)
	input.Representations["b"] = seed
	intent := enhancedIntent(1)
	intent.References = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "seed", Influence: core.InfluencePositive}, {Kind: core.ReferenceTrack, TrackID: "b", Influence: core.InfluenceNegative}}
	candidates := []core.Candidate{{Track: track}}
	ranked, err := NewRanker(cat, DefaultConfig()).Rank(context.Background(), candidates, ports.RankRequest{Intent: intent, EnhancedAudio: freezeEnhanced(t, input)})
	if err != nil || !ranked[0].Available.CombinedMERT || math.Abs(ranked[0].Scores.CombinedMERT) > 1e-9 {
		t.Fatalf("negative reference ignored: %+v %v", ranked, err)
	}
	for _, mode := range []core.RecommendationMode{core.AcousticBrainzFirst, core.CLAPFirst, core.DeejAIOnly} {
		intent.Controls.RecommendationMode = mode
		with := ports.RankRequest{Intent: intent, EnhancedAudio: freezeEnhanced(t, input)}
		without := with
		without.EnhancedAudio = nil
		left, leftErr := NewRanker(cat, DefaultConfig()).Rank(context.Background(), candidates, with)
		right, rightErr := NewRanker(cat, DefaultConfig()).Rank(context.Background(), candidates, without)
		if leftErr != nil || rightErr != nil || !reflect.DeepEqual(left, right) {
			t.Fatalf("%s changed: %+v / %+v errors=%v/%v", mode, left, right, leftErr, rightErr)
		}
	}
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewRanker(cat, DefaultConfig()).Rank(ctx, candidates, ports.RankRequest{Intent: intent, EnhancedAudio: freezeEnhanced(t, input)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}
