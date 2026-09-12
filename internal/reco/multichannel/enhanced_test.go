package multichannel

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/ports"
)

func enhancedFixture(t *testing.T) (*fakes.Catalog, core.EnhancedAudioInput) {
	t.Helper()
	cat := fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "seed", Display: "Origin - Seed", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "a", Display: "First - A", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "b", Display: "Second - B", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "c", Display: "Third - C", Audio: []float32{1, 0}, Track: []float32{1, 0}},
	)
	input := core.EnhancedAudioInput{PolicyVersion: EnhancedPolicyVersion, CatalogVersion: cat.CatalogVersion(), DSPVersion: "dsp/v1", Model: core.AudioRepresentationIdentity{Model: "MERT", Revision: "rev", Preprocessing: "pcm", Runtime: "onnx", Dimension: 2, WeightsSHA256: "hash", Pooling: "mean"}, DSP: map[string]core.DSPAnalysis{}, Representations: map[string]core.AudioRepresentation{}}
	for _, id := range []string{"seed", "a", "b"} {
		meta, _ := cat.Meta(id)
		vector := []float32{1, 0}
		if id == "a" {
			vector = []float32{-1, 0}
		}
		input.Representations[id] = core.AudioRepresentation{TrackID: id, TrackKey: core.ProvisionalRecordingKey(meta.Ref), CatalogVersion: input.CatalogVersion, Model: input.Model, Pooled: vector}
		v := .3
		if id == "a" {
			v = 0
		}
		if id == "b" {
			v = .6
		}
		input.DSP[id] = core.DSPAnalysis{TrackID: id, TrackKey: core.ProvisionalRecordingKey(meta.Ref), CatalogVersion: input.CatalogVersion, Version: input.DSPVersion, Features: core.DSPFeatures{BassEnergyRatio: core.DSPValue{State: core.FeatureKnown, Value: &v}}}
	}
	return cat, input
}

type enhancedBudgetRetriever struct {
	cat    *fakes.Catalog
	budget *audio.EnhancedBudget
}

func (r *enhancedBudgetRetriever) Retrieve(ctx context.Context, _ ports.RetrievalRequest) ([]core.Candidate, error) {
	r.budget = audio.EnhancedBudgetFor(ctx)
	if r.budget == nil {
		return nil, fmt.Errorf("enhanced stage budget missing")
	}
	for i := range 23 {
		r.budget.Allow(fmt.Sprintf("earlier-stage-%d", i))
	}
	return candidatesForTracks(refs(r.cat, "a", "b", "c")), nil
}

func TestEnhancedStagesShareOneBudgetAndPrepareNegativeReferences(t *testing.T) {
	cat, input := enhancedFixture(t)
	retriever := &enhancedBudgetRetriever{cat: cat}
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
	engine.retriever = retriever
	providerCalls := 0
	engine.WithEnhancedAudioProvider(func(ctx context.Context, _ core.MusicIntent, _ core.TasteProfile, tracks []core.TrackRef) (*core.EnhancedAudioSnapshot, error) {
		providerCalls++
		budget := audio.EnhancedBudgetFor(ctx)
		if budget != retriever.budget || budget.Used() != 23 {
			t.Fatal("assembly reset earlier-stage budget")
		}
		admitted := 0
		negativePresent := false
		for _, track := range tracks {
			if budget.Allow(track.ID) {
				admitted++
			}
			negativePresent = negativePresent || track.ID == "a"
		}
		if admitted != 1 || budget.Used() != 24 {
			t.Fatalf("shared budget admitted %d", admitted)
		}
		if !negativePresent {
			t.Fatal("negative reference absent from cached acquisition list")
		}
		return core.NewEnhancedAudioSnapshot(input)
	})
	intent := testIntent(1)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	intent.References = append(intent.References, core.IntentReference{Kind: core.ReferenceTrack, TrackID: "a", Influence: core.InfluenceNegative})
	if _, err := engine.Build(context.Background(), intent); err != nil || providerCalls != 1 {
		t.Fatalf("build %v calls %d", err, providerCalls)
	}
	input.CatalogVersion = "incompatible"
	if _, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, EnhancedAudio: freezeEnhanced(t, input)}); err == nil {
		t.Fatal("direct client accepted incompatible catalog snapshot")
	}
}

func TestEnhancedRulesPromptReachesDSPRanking(t *testing.T) {
	cat, input := enhancedFixture(t)
	// Bass amount maps to band energy. Deep pitch alone now stays descriptive
	// rather than being incorrectly promoted to a bass-energy preference.
	for _, prompt := range []string{"Make a playlist with bass-heavy sound", "Make a percussive playlist with sharp attacks"} {
		intent, err := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
		if err != nil {
			t.Fatal(err)
		}
		intent.Controls.RecommendationMode = core.EnhancedHybrid
		for _, id := range []string{"a", "b"} {
			a := input.DSP[id]
			v := 0.0
			if id == "b" {
				v = 8
			}
			a.Features.OnsetRateHz = core.DSPValue{State: core.FeatureKnown, Value: &v}
			input.DSP[id] = a
		}
		got, err := NewRanker(cat, DefaultConfig()).Rank(context.Background(), candidatesForTracks(refs(cat, "a", "b")), ports.RankRequest{Intent: intent, EnhancedAudio: freezeEnhanced(t, input)})
		if err != nil || got[0].Track.ID != "b" || !got[0].Available.EnhancedDSP {
			t.Fatalf("prompt %s did not reach DSP: %v %+v", prompt, err, got)
		}
	}
	for _, prompt := range []string{"Make a playlist without deep bass", "Make a playlist with less deep bass", "Make a playlist, not deep bass"} {
		clauses := enhancedClauses(core.MusicIntent{OriginalDescription: prompt})
		if len(clauses) != 1 || !clauses[0].Negative {
			t.Fatalf("negation lost: %s %+v", prompt, clauses)
		}
	}
	for _, prompt := range []string{`Play "Deep Bass" by Someone`, "Play deep bassoon music"} {
		if clauses := enhancedClauses(core.MusicIntent{OriginalDescription: prompt}); len(clauses) != 0 {
			t.Fatalf("reference/word boundary false positive %s: %+v", prompt, clauses)
		}
	}
	for _, mode := range []core.Mode{core.ModeJourney, core.ModeSimilar} {
		intent := core.MusicIntent{Mode: mode, OriginalDescription: "Start with deep bass then end with bright sound", EssentialCriteria: []core.MusicalCriterion{{Kind: "texture", Value: "deep bass", Scope: "journey_start"}, {Kind: "texture", Value: "bright sound", Scope: "journey_end"}}}
		if clauses := enhancedClauses(intent); len(clauses) != 0 {
			t.Fatalf("stage texture leaked to playlist %+v", clauses)
		}
	}
}

func TestEnhancedInferredArtistRepresentativesKeepWeights(t *testing.T) {
	cat, input := enhancedFixture(t)
	intent := testIntent(2)
	intent.References = nil
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	intent.InferredAnchors = []core.InferredAnchor{{Suitability: core.AnchorSuitability{State: core.EvidenceMatch}, Reference: core.IntentReference{Kind: core.ReferenceArtist, Query: "Inferred fixture artist", Influence: core.InfluencePositive, Resolution: &core.ReferenceResolution{Selected: &core.ResolutionCandidate{Representatives: []core.WeightedTrack{{TrackID: "a", Weight: .25}, {TrackID: "b", Weight: .75}}}}}}}
	got, err := NewRanker(cat, DefaultConfig()).Rank(context.Background(), candidatesForTracks(refs(cat, "a", "b")), ports.RankRequest{Intent: intent, EnhancedAudio: freezeEnhanced(t, input)})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := findCandidate(got, "a")
	b, _ := findCandidate(got, "b")
	if a.Scores.EnhancedMERT != -.5 || b.Scores.EnhancedMERT != .5 {
		t.Fatalf("representative weights lost %v %v", a.Scores.EnhancedMERT, b.Scores.EnhancedMERT)
	}
}

func freezeEnhanced(t *testing.T, input core.EnhancedAudioInput) *core.EnhancedAudioSnapshot {
	t.Helper()
	s, err := core.NewEnhancedAudioSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEnhancedRankingSignedMissingNeutralAndLegacyParity(t *testing.T) {
	cat, input := enhancedFixture(t)
	candidates := candidatesForTracks(refs(cat, "a", "b", "c"))
	intent := testIntent(3)
	intent.Preferences.TextureDescriptions = []core.IntentPreference{{Value: "bass heavy", Influence: core.InfluencePositive}}
	ranker := NewRanker(cat, DefaultConfig())
	baseline, err := ranker.Rank(context.Background(), candidates, ports.RankRequest{Intent: intent})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := freezeEnhanced(t, input)
	for _, mode := range []core.RecommendationMode{core.AcousticBrainzFirst, core.CLAPFirst, core.DeejAIOnly} {
		intent.Controls.RecommendationMode = mode
		plain, _ := ranker.Rank(context.Background(), candidates, ports.RankRequest{Intent: intent})
		with, _ := ranker.Rank(context.Background(), candidates, ports.RankRequest{Intent: intent, EnhancedAudio: snapshot})
		if !reflect.DeepEqual(plain, with) {
			t.Fatalf("legacy %s changed", mode)
		}
	}
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	got, err := ranker.Rank(context.Background(), candidates, ports.RankRequest{Intent: intent, EnhancedAudio: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Track.ID != "b" || got[1].Track.ID != "c" || got[2].Track.ID != "a" {
		t.Fatalf("wrong ranking %+v", got)
	}
	if got[1].Available.EnhancedDSP || got[1].Available.EnhancedMERT {
		t.Fatal("unknown became measured")
	}
	for _, c := range got {
		base, _ := findCandidate(baseline, c.Track.ID)
		want := (base.Scores.Total + .15*c.Scores.EnhancedDSP + .15*c.Scores.EnhancedMERT) / 1.3
		if math.Abs(c.Scores.Total-want) > 1e-12 {
			t.Fatal("candidate-specific denominator")
		}
	}
	plain, _ := ranker.Rank(context.Background(), candidates, ports.RankRequest{Intent: intent})
	empty, _ := ranker.Rank(context.Background(), candidates, ports.RankRequest{Intent: intent, EnhancedAudio: freezeEnhanced(t, core.EnhancedAudioInput{})})
	if !reflect.DeepEqual(plain, empty) {
		t.Fatal("no-data fallback changed")
	}
}

func TestEnhancedSnapshotFrozenFingerprintAndIdentityMismatch(t *testing.T) {
	cat, input := enhancedFixture(t)
	snapshot := freezeEnhanced(t, input)
	original := snapshot.Fingerprint()
	input.Representations["a"].Pooled[0] = 1
	copy := snapshot.Input()
	copy.Representations["a"].Pooled[0] = 1
	if snapshot.Fingerprint() != original || snapshot.Input().Representations["a"].Pooled[0] != -1 {
		t.Fatal("mutable snapshot")
	}
	bytes, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var replay core.EnhancedAudioSnapshot
	if err = json.Unmarshal(bytes, &replay); err != nil || replay.Fingerprint() != original {
		t.Fatalf("replay %v", err)
	}
	input = snapshot.Input()
	for _, mutate := range []func(*core.EnhancedAudioInput){
		func(i *core.EnhancedAudioInput) { i.Model.Revision = "changed" },
		func(i *core.EnhancedAudioInput) { i.DSPVersion = "changed" },
		func(i *core.EnhancedAudioInput) { i.CatalogVersion = "changed" },
		func(i *core.EnhancedAudioInput) {
			a := i.Representations["a"]
			a.TrackKey = "different"
			i.Representations["a"] = a
		},
	} {
		changed := snapshot.Input()
		mutate(&changed)
		if freezeEnhanced(t, changed).Fingerprint() == original {
			t.Fatal("identity omitted from fingerprint")
		}
	}
	input.Model.Revision = "changed"
	meta, _ := cat.Meta("a")
	if _, ok := representation(input, meta.Ref); ok {
		t.Fatal("incompatible model accepted")
	}
	input = snapshot.Input()
	a := input.Representations["a"]
	a.TrackKey = "wrong"
	input.Representations["a"] = a
	if _, ok := representation(input, meta.Ref); ok {
		t.Fatal("wrong recording accepted")
	}
}

func TestEnhancedSequenceUsesSameObjectiveAndKeepsRequiredSpacing(t *testing.T) {
	cat, input := enhancedFixture(t)
	intent := testIntent(3)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	intent.Controls.TransitionSmoothness = 1
	intent.Constraints.NoRepeatArtistBackToBack = true
	request := ports.SequenceRequest{Intent: intent, Candidates: candidatesForTracks(refs(cat, "a", "b", "c")), ReferenceAnchors: refs(cat, "seed"), EnhancedAudio: freezeEnhanced(t, input)}
	s := NewSequencer(cat, DefaultConfig())
	got, err := s.Sequence(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tracks[0].ID != "b" {
		t.Fatalf("continuity not used: %+v", got.Tracks)
	}
	again, err := s.Sequence(context.Background(), request)
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatal("nondeterministic sequence")
	}
	request.Required = refs(cat, "seed")
	request.Intent.Count = 4
	got, err = s.Sequence(context.Background(), request)
	if err != nil || got.Tracks[0].ID != "seed" {
		t.Fatalf("required ordering: %v %+v", err, got)
	}
	local := *s
	local.enhancedInput = input
	items := []sequenceItem{{track: refs(cat, "a")[0]}, {track: refs(cat, "b")[0]}, {track: refs(cat, "c")[0]}}
	before := local.sequenceObjective(items, request)
	improved := local.improve(items, request)
	if local.sequenceObjective(improved, request) < before {
		t.Fatal("improvement regressed enhanced objective")
	}
}

func TestEnhancedDSPNeverInfersMoodOrUnknownValue(t *testing.T) {
	_, input := enhancedFixture(t)
	for _, clause := range []core.AudioClause{{Kind: "mood", Text: "dark"}, {Kind: "texture", Text: "energetic"}, {Kind: "texture", Text: "bass heavy", Scope: "stage:1"}} {
		if _, ok := enhancedDSP(input.DSP["a"], []core.AudioClause{clause}); ok {
			t.Fatalf("unsupported evidence %+v", clause)
		}
	}
	if score, ok := enhancedDSP(core.DSPAnalysis{}, []core.AudioClause{{Kind: "texture", Text: "bass heavy"}}); ok || score != 0 {
		t.Fatal("unknown coerced to zero measurement")
	}
}

func TestEnhancedProviderFreezesOnceAndReplayAvoidsAcquisition(t *testing.T) {
	cat, input := enhancedFixture(t)
	snapshot := freezeEnhanced(t, input)
	calls := 0
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithEnhancedAudioProvider(func(_ context.Context, _ core.MusicIntent, _ core.TasteProfile, tracks []core.TrackRef) (*core.EnhancedAudioSnapshot, error) {
		calls++
		if len(tracks) == 0 || tracks[0].ID != "seed" {
			t.Fatal("reference acquisition must precede candidates")
		}
		return snapshot, nil
	})
	intent := testIntent(2)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	result, err := engine.Build(context.Background(), intent)
	if err != nil || calls != 1 || result.EnhancedAudio == nil {
		t.Fatalf("provider calls=%d err=%v result=%+v", calls, err, result)
	}
	if engine.enhancedSnapshot != nil || engine.enhancedPrepared {
		t.Fatal("generation state escaped into shared engine")
	}
	replay, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, EnhancedAudio: result.EnhancedAudio})
	if err != nil || calls != 1 || !reflect.DeepEqual(result, replay) {
		t.Fatalf("replay acquired/changed evidence calls=%d err=%v", calls, err)
	}
	request := ports.RecommendationRequest{EnhancedAudio: snapshot}
	before := engine.assemblyKey(nil, intent, request, nil, nil, nil, 42)
	input.DSPVersion = "changed"
	request.EnhancedAudio = freezeEnhanced(t, input)
	if before == engine.assemblyKey(nil, intent, request, nil, nil, nil, 42) {
		t.Fatal("assembly reused incompatible DSP")
	}
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_artist", Value: "Second"}}
	filtered, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, EnhancedAudio: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	for _, track := range filtered.Tracks {
		if track.Artist == "Second" {
			t.Fatal("enhanced affinity bypassed hard artist exclusion")
		}
	}
}

func TestEnhancedNegativeReferencesAndBoundedWeights(t *testing.T) {
	cat, input := enhancedFixture(t)
	intent := testIntent(2)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	intent.References[0].Influence = core.InfluenceNegative
	input.PositiveCentroid = []float32{0, 1}
	input.NegativeCentroid = []float32{-1, 0} // explicit negative reference wins
	cfg := DefaultConfig()
	cfg.EnhancedMERTWeight = 100
	cfg.EnhancedDSPWeight = 100
	cfg.EnhancedTransitionWeight = 100
	ranker := NewRanker(cat, cfg)
	if ranker.cfg.EnhancedMERTWeight != .15 || ranker.cfg.EnhancedDSPWeight != .15 || ranker.cfg.EnhancedTransitionWeight != .15 {
		t.Fatal("unbounded policy")
	}
	got, err := ranker.Rank(context.Background(), candidatesForTracks(refs(cat, "a", "b")), ports.RankRequest{Intent: intent, EnhancedAudio: freezeEnhanced(t, input)})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := findCandidate(got, "a")
	b, _ := findCandidate(got, "b")
	if a.Scores.EnhancedMERT != 0 || b.Scores.EnhancedMERT != -1 {
		t.Fatalf("explicit negative lost: %v %v", a.Scores.EnhancedMERT, b.Scores.EnhancedMERT)
	}
}

func TestEnhancedPhraseAliasesAndTransientMissingness(t *testing.T) {
	_, input := enhancedFixture(t)
	known := func(v float64) core.DSPValue { return core.DSPValue{Value: &v, State: core.FeatureKnown} }
	a := input.DSP["b"]
	a.Features.SubbassEnergyRatio = known(.4)
	a.Features.OnsetRateHz = known(8)
	a.Features.PositiveSpectralFlux = known(1)
	a.Features.RMSWindowSpreadDB = known(15)
	a.Features.SpectralCentroidHz = known(500)
	for _, phrase := range []string{"deep bass", "more bass", "strong sub-bass", "lots of transients", "sharp attacks", "percussive", "big dynamic swings", "darker"} {
		clause := core.AudioClause{Kind: "texture", Text: phrase}
		if score, ok := enhancedDSP(a, []core.AudioClause{clause}); !ok || score != 1 {
			t.Fatalf("phrase %s: %v %v", phrase, score, ok)
		}
		clause.Negative = true
		if score, ok := enhancedDSP(a, []core.AudioClause{clause}); !ok || score != -1 {
			t.Fatalf("negative phrase %s: %v %v", phrase, score, ok)
		}
	}
	a.Features.PositiveSpectralFlux = core.DSPValue{}
	if score, ok := enhancedDSP(a, []core.AudioClause{{Kind: "texture", Text: "percussive"}}); !ok || score != .5 {
		t.Fatal("missing correlated measure removed denominator")
	}
}

func TestEnhancedStopPreservesEvidenceAndRejectsMalformedReplay(t *testing.T) {
	cat, input := enhancedFixture(t)
	snapshot := freezeEnhanced(t, input)
	stop := make(chan struct{})
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithEnhancedAudioProvider(func(ctx context.Context, _ core.MusicIntent, _ core.TasteProfile, _ []core.TrackRef) (*core.EnhancedAudioSnapshot, error) {
		close(stop)
		<-ctx.Done()
		return snapshot, ctx.Err()
	})
	intent := testIntent(2)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	if err := engine.prepareEnhanced(context.Background(), nil, intent, ports.RecommendationRequest{StopChecking: stop}, nil, nil, nil); err != nil || engine.enhancedSnapshot.Fingerprint() != snapshot.Fingerprint() {
		t.Fatalf("stop lost completed evidence: %v", err)
	}
	for _, raw := range []string{"null", " null ", "[]", "{\"representations\":2}"} {
		var s core.EnhancedAudioSnapshot
		if json.Unmarshal([]byte(raw), &s) == nil {
			t.Fatalf("malformed replay accepted: %s", raw)
		}
	}
}
