package multichannel

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func estimatedObservations(c *automaticCatalogFixture, intent core.MusicIntent, values map[string][]float64) {
	execution := intent
	execution.Controls.RecommendationMode = core.EnhancedHybrid
	clauses := audio.Clauses(execution)
	for id, scores := range values {
		assessment := core.AudioAssessment{TrackID: id, AnalysisID: "prepared:" + id, ModelFingerprint: "fixture-clap/v1", LibraryCoverage: &core.LibraryCLAPCoverage{CoveredSeconds: 30}}
		for i, score := range scores {
			assessment.Clauses = append(assessment.Clauses, core.AudioClauseAssessment{Clause: clauses[i], Score: score, ScoreAvailable: true, State: core.EvidenceUnknown})
		}
		c.observations[id] = assessment
	}
}

func TestAutomaticOrdinaryDescriptionRanksCompletePhraseWithoutClaimingFulfillment(t *testing.T) {
	c, intent, pool := automaticFixture(t, 2)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "instrumentation", Value: "soft piano", Scope: "playlist", Strength: "essential"}}
	estimatedObservations(c, intent, map[string][]float64{"0": {.2}, "1": {.8}, "2": {.6}, "3": {.1}})
	result, err := NewAutomatic(c, nil, pool, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || len(result.Tracks) != 2 || result.Outcome.State != core.OutcomePartial {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, track := range result.Tracks {
		if track.ID != "1" && track.ID != "2" {
			t.Fatalf("literal score ranking lost: %+v", result.Tracks)
		}
	}
	if result.Search.StopReason != "prepared_obligations_met" || result.Search.Validate() != nil {
		t.Fatalf("prepared stop/replay lost: %+v", result.Search)
	}
	for _, fit := range result.FitAssessments {
		clause := fit.Assessment.Clauses[0]
		if clause.Clause.Text != "soft piano" || clause.State == core.AutomaticStrong || !clause.EstimateAvailable {
			t.Fatal(clause)
		}
	}
	// The same evidence cannot satisfy explicit must-haves or ambiguous legacy strengths.
	for _, strength := range []string{"required", ""} {
		strict := intent
		strict.EssentialCriteria = automaticCopy(intent.EssentialCriteria)
		strict.EssentialCriteria[0].Strength = strength
		estimatedObservations(c, strict, map[string][]float64{"0": {.2}, "1": {.8}, "2": {.6}, "3": {.1}})
		got, err := NewAutomatic(c, nil, pool, DefaultConfig()).Build(context.Background(), strict)
		if err != nil || len(got.Tracks) != 0 {
			t.Fatalf("strength %q bypassed strict admission: %+v %v", strength, got, err)
		}
	}
}

func TestAutomaticDescriptiveJourneyRetainsDifferentStageMembership(t *testing.T) {
	c, intent, pool := automaticFixture(t, 4)
	intent.Mode = core.ModeJourney
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "instrumentation", Value: "soft piano", Scope: "journey_start", Strength: "essential"}, {Kind: "instrumentation", Value: "pounding drums", Scope: "journey_end", Strength: "essential"}}
	estimatedObservations(c, intent, map[string][]float64{"0": {.9, .1}, "1": {.8, .2}, "2": {.2, .8}, "3": {.1, .9}})
	result, err := NewAutomatic(c, nil, pool, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || len(result.Tracks) != 4 {
		t.Fatalf("journey=%+v err=%v", result, err)
	}
	for i, track := range result.Tracks {
		if (i < 2) != (track.ID == "0" || track.ID == "1") {
			t.Fatalf("journey flattened: %+v", result.Tracks)
		}
	}
	for _, reason := range result.Outcome.Reasons {
		if reason.Code == "journey_incomplete" {
			t.Fatal(reason)
		}
	}
}

func TestAutomaticCachedConflictsPrecedeSufficiencyAndInference(t *testing.T) {
	b := durationBatchFixture(3)
	calls := 0
	verifier := &automaticCachedVerifier{cached: func(_ context.Context, row core.EnrichedTrack) (core.EnrichedTrack, error) {
		calls++
		if row.Ref.ID == b.ids[2] {
			row.IdentityStatus = core.ResolutionAmbiguous
			row.Matched = false
		}
		return row, nil
	}}
	engine := &AutomaticEngine{enricher: verifier, audioProvider: func() *audio.Service { t.Fatal("sufficient pool started inference"); return nil }}
	err := engine.acquireAutomaticEvidence(context.Background(), testCatalog(), b, core.MusicIntent{}, "catalog", nil, func() bool {
		if calls != 3 || b.recordings[b.ids[2]].IdentityStatus != core.ResolutionAmbiguous {
			t.Fatal("sufficiency preceded cached identity checks")
		}
		return true
	})
	if err != nil || verifier.calls != 0 {
		t.Fatal(err, verifier.calls)
	}
}

func TestAutomaticEstimatedReferenceStillHonorsArtistOnly(t *testing.T) {
	b := newAutomaticBatch()
	seed := core.TrackRef{ID: "seed", Artist: "Seed", Title: "Song"}
	other := core.TrackRef{ID: "other", Artist: "Neighbor", Title: "Other"}
	b.meta[seed.ID] = core.TrackMeta{Ref: seed}
	b.meta[other.ID] = core.TrackMeta{Ref: other}
	b.recordings[seed.ID] = core.EnrichedTrack{Ref: seed, Matched: true, IdentityStatus: core.ResolutionResolved, ArtistIDs: []string{automaticReferenceArtist}}
	b.recordings[other.ID] = core.EnrichedTrack{Ref: other, Matched: true, IdentityStatus: core.ResolutionResolved, ArtistIDs: []string{automaticNeighborArtist}}
	g := automaticReferenceGate{batch: b, own: map[string]string{}, anchors: []automaticReferenceAnchor{{estimated: true, artistRequest: true, artist: automaticReferenceArtist, tracks: []core.WeightedTrack{{TrackID: seed.ID, Weight: 1}}}}, graph: map[string][]core.RetrievalEvidence{other.ID: {{Channel: "musicgraph_neighbor", QueryID: automaticReferenceArtist + ":" + automaticNeighborArtist, Rank: 1, Score: .1}}}}
	if ok, _ := g.match(core.Candidate{Track: other}); !ok {
		t.Fatal("ordinary discovery still needs unavailable calibration")
	}
	g.anchors[0].estimated = false
	if ok, _ := g.match(core.Candidate{Track: other}); ok {
		t.Fatal("legacy reference was silently reinterpreted")
	}
	intent := core.MusicIntent{References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Seed", Strength: "required", Influence: core.InfluencePositive, Resolution: &core.ReferenceResolution{Status: core.ResolutionResolved, Selected: &core.ResolutionCandidate{Kind: core.ReferenceArtist, EntityID: automaticReferenceArtist}}}}, HardConstraints: []core.HardConstraint{{Kind: "require_artist", Value: "Seed"}}}
	if automaticFacts(b, other, intent, "playlist") {
		t.Fatal("artist-only admitted another artist")
	}
	// Reference audio is bound to a catalog recording; it does not require an
	// artist UUID when the request asks for estimated discovery.
	g.anchors[0].estimated, g.anchors[0].artist = true, ""
	g.graph = nil
	b.clap[seed.ID] = core.LibraryVector{Source: core.LibraryEvidenceSource{SpaceID: "clap/v1"}, Values: []float32{1, 0}}
	b.clap[other.ID] = b.clap[seed.ID]
	if ok, _ := g.match(core.Candidate{Track: other}); !ok {
		t.Fatal("catalog reference audio incorrectly required artist UUID")
	}
	g.anchors[0].artist = automaticOtherArtist
	if ok, _ := g.match(core.Candidate{Track: other}); ok {
		t.Fatal("audio from a conflicting seed artist admitted discovery")
	}
}

func TestAutomaticUnsupportedStrictDescriptionSkipsUnhelpfulAudio(t *testing.T) {
	b := durationBatchFixture(1)
	intent := core.MusicIntent{EssentialCriteria: []core.MusicalCriterion{{Kind: "texture", Value: "spacious reverberation", Scope: "playlist", Strength: "required"}}}
	if automaticAudioCanHelp(b, b.ids[0], intent) {
		t.Fatal("uncalibrated acquisition cannot resolve strict texture")
	}
	calibration := core.AudioSimilarityCalibration{Version: "test/v1", ModelFingerprint: "clap/v1", Kind: "reference", Criterion: "reference_similarity", MinimumScore: .7, DevelopmentSet: "dev", ValidationSet: "validation", ValidationPrecision: .95, ValidationRecall: .8, PositiveExamples: 20, NegativeExamples: 20}
	b.calibrations = []core.AudioSimilarityCalibration{calibration}
	if automaticAudioCanHelp(b, b.ids[0], intent) {
		t.Fatal("irrelevant calibration started unhelpful inference")
	}
	b.calibrations[0].Kind, b.calibrations[0].Criterion = "texture", "spacious reverberation"
	if automaticAudioCanHelp(b, b.ids[0], intent, "other-model") {
		t.Fatal("incompatible calibration started unhelpful inference")
	}
	if !automaticAudioCanHelp(b, b.ids[0], intent, "clap/v1") {
		t.Fatal("matching literal calibration could resolve requirement")
	}
	intent.EssentialCriteria[0].Strength = "essential"
	if !automaticAudioCanHelp(b, b.ids[0], intent) {
		t.Fatal("ordinary descriptions need estimated audio ranking")
	}
}

func TestAutomaticReadyProbeDoesNotPublishSuggestions(t *testing.T) {
	c, intent, pool := automaticFixture(t, 1)
	automaticSupport(c, intent, "0", "house")
	calls := 0
	got, err := NewAutomatic(c, nil, pool, DefaultConfig()).BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, OnSuggested: func(core.TrackRef) { calls++ }})
	if err != nil || calls != 1 || len(got.Tracks) != 1 {
		t.Fatal(calls, len(got.Tracks), err)
	}
}

func TestAutomaticClassifierScoresRemainOneUncalibratedFamily(t *testing.T) {
	b := newAutomaticBatch()
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	model := core.MusicClassifierIdentity{Model: "discogs-effnet", Revision: "1", WeightsSHA256: hash, MetadataSHA256: hash}
	prediction := core.MusicClassifierEvidence{Version: core.MusicClassifierEvidenceVersion, Encoder: model, Preprocessing: "essentia/v1", Runtime: "fixture", AudioSHA256: hash, Source: "fixture", SourceID: "track", License: "CC0", Coverage: core.LibraryCLAPCoverage{CoveredSeconds: 8, Segments: []core.LibraryAudioInterval{{StartSeconds: 0, EndSeconds: 8}}}, Heads: []core.MusicClassifierHead{{Kind: "instrumentation", Model: model, Classes: []string{"piano"}, Scores: []float32{1}}}}
	b.classifiers["track"] = []core.MusicClassifierEvidence{prediction, prediction}
	clause := core.AudioClause{Kind: "instrumentation", Text: "piano", Scope: "playlist", Essential: true, Strength: "required", Strict: true}
	fit := automaticClauseFit(b, "track", clause, .3)
	if fit.State != core.AutomaticUnknown || len(fit.Signals) != 1 || !fit.Signals[0].ScoreAvailable {
		t.Fatalf("classifier manufactured confidence: %+v", fit)
	}
	if automaticScopeFits(core.AutomaticFitAssessment{Clauses: []core.AutomaticClauseFit{fit}}, "playlist") {
		t.Fatal("uncalibrated classifier satisfied must-have")
	}
	clause.Text = "soft piano"
	if got := automaticClauseFit(b, "track", clause, .3); len(got.Signals) != 0 {
		t.Fatal("classifier broadened qualified phrase", got)
	}
}

func TestAutomaticRanksAndRetrievalKeepIncompatibleSpacesSeparate(t *testing.T) {
	b := newAutomaticBatch()
	var candidates []core.Candidate
	for i, item := range []struct {
		id, space string
		score     float64
	}{{"a", "mert-a", .9}, {"b", "mert-b", .2}, {"c", "mert-a", .8}} {
		b.mert[item.id] = core.LibraryVector{Source: core.LibraryEvidenceSource{SpaceID: item.space}}
		candidate := core.Candidate{Track: core.TrackRef{ID: item.id}, Sources: []core.RetrievalEvidence{{Channel: "library_mert", QueryID: "same-recording", Rank: i + 1, LibrarySource: &core.LibraryEvidenceSource{SpaceID: item.space}}}}
		candidate.Scores.LibraryMERT, candidate.Available.LibraryMERT = item.score, true
		candidates = append(candidates, candidate)
	}
	ranks := automaticReferenceRanks(candidates, b)
	if ranks["a"] != ranks["b"] || ranks["c"] >= ranks["a"] {
		t.Fatalf("raw scores compared across spaces: %v", ranks)
	}
	duplicate := automaticCopy(candidates[0])
	duplicate.Sources[0].LibrarySource.SpaceID = "another-space"
	pooled := automaticBalancedPool([][]core.Candidate{candidates[:1], {duplicate}}, 3, 42)
	if len(pooled) != 1 || len(pooled[0].Sources) != 2 {
		t.Fatalf("incompatible evidence source collapsed: %+v", pooled)
	}
}

func TestAutomaticAcquisitionDoesNotInventAbsenceCalibrationOrFlattenStrictStages(t *testing.T) {
	b := newAutomaticBatch()
	b.calibrations = []core.AudioSimilarityCalibration{{Version: "test/v1", ModelFingerprint: "clap/v1", Kind: "texture", Criterion: "reverberation", MinimumScore: .7, DevelopmentSet: "dev", ValidationSet: "validation", ValidationPrecision: .95, ValidationRecall: .8, PositiveExamples: 20, NegativeExamples: 20}}
	exclusion := core.MusicIntent{HardConstraints: []core.HardConstraint{{Kind: "exclude_texture", Value: "reverberation"}}}
	if automaticAudioCanHelp(b, "recording", exclusion, "clap/v1") {
		t.Fatal("positive similarity calibration certified absence")
	}
	journey := core.MusicIntent{Mode: core.ModeJourney, EssentialCriteria: []core.MusicalCriterion{{Kind: "texture", Value: "spacious reverberation", Scope: "journey_start", Strength: "required"}, {Kind: "instrumentation", Value: "pounding drums", Scope: "journey_end", Strength: "required"}}}
	if automaticAudioCanHelp(b, "recording", journey, "clap/v1") {
		t.Fatal("unsupported strict stages started inference")
	}
	journey.EssentialCriteria[1].Strength = "essential"
	if !automaticAudioCanHelp(b, "recording", journey, "clap/v1") {
		t.Fatal("ordinary remaining stage lost inference")
	}
}
