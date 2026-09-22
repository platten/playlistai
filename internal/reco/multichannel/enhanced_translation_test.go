package multichannel

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func enhancedIntent(count int) core.MusicIntent {
	intent := testIntent(count)
	intent.VerificationPolicy = core.BestAvailable
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	return intent
}

func TestEnhancedDiversityPolicyBroadensAndSpacesUnlessExplicitlyRestricted(t *testing.T) {
	cat := diversityCatalog()
	intent := enhancedIntent(2)
	intent.Preferences.Genres = []core.IntentPreference{{Value: "rock", Influence: core.InfluencePositive}}
	intent.Controls.ArtistDiversity = 0
	input := []core.Candidate{selectionCandidate(cat, "a1", 1), selectionCandidate(cat, "a2", .99), selectionCandidate(cat, "b", .8)}
	selected, err := NewSelector(cat, DefaultConfig()).Select(context.Background(), input, ports.SelectionRequest{Intent: intent, Count: 2})
	if err != nil || candidateIDs(selected.Candidates) != "a1,b" {
		t.Fatalf("category diversity was not applied: %s %v", candidateIDs(selected.Candidates), err)
	}
	sequencer := NewSequencer(cat, DefaultConfig())
	result, err := sequencer.Sequence(context.Background(), ports.SequenceRequest{Intent: intent, Candidates: selected.Candidates})
	if err != nil || len(result.Tracks) != 2 {
		t.Fatalf("diverse Enhanced output was truncated: %+v %v", result, err)
	}
	intent.HardConstraints = []core.HardConstraint{{Kind: "require_artist", Value: "Artist A"}}
	if genreArtistDiversity(intent) {
		t.Fatal("explicit artist-only restriction did not opt out")
	}
}

func TestEnhancedStrongTierPrecedesCloseWithoutMovingJourneyEndpoints(t *testing.T) {
	cat := diversityCatalog()
	intent := enhancedIntent(3)
	intent.Controls.ArtistDiversity, intent.Controls.TransitionSmoothness = 1, 1
	input := []core.Candidate{selectionCandidate(cat, "a1", 1), selectionCandidate(cat, "a2", .9), selectionCandidate(cat, "b", .8)}
	input[0].FitTier, input[1].FitTier, input[2].FitTier = fitClose, fitClose, fitStrong
	for i := range input {
		input[i].Available.SemanticMatch = true
		input[i].Scores.SemanticMatch = input[i].Scores.Total
	}
	selected, err := NewSelector(cat, DefaultConfig()).Select(context.Background(), input, ports.SelectionRequest{Intent: intent, Count: 3})
	if err != nil || selected.Candidates[0].Track.ID != "b" {
		t.Fatalf("strong tier displaced: %+v %v", selected, err)
	}
	result, err := NewSequencer(cat, DefaultConfig()).Sequence(context.Background(), ports.SequenceRequest{Intent: intent, Candidates: selected.Candidates, ReferenceAnchors: refs(cat, "a1")})
	if err != nil || trackIDs(result.Tracks) != "a1,b,a2" {
		t.Fatalf("hard artist spacing was not preserved: %+v %v", result, err)
	}
}

func TestEnhancedCloseFloorCannotBeMetByUnrelatedBonuses(t *testing.T) {
	cat := diversityCatalog()
	intent := enhancedIntent(3)
	input := []core.Candidate{selectionCandidate(cat, "a1", .8), selectionCandidate(cat, "a2", 1), selectionCandidate(cat, "b", 1)}
	for i := range input {
		input[i].FitTier = fitClose
		input[i].Scores.EnhancedMERT = 1
		input[i].Available.EnhancedMERT = true
	}
	input[0].Scores.SemanticMatch, input[0].Available.SemanticMatch = .8, true
	input[1].Scores.SemanticMatch, input[1].Available.SemanticMatch = .01, true
	selected, err := NewSelector(cat, DefaultConfig()).Select(context.Background(), input, ports.SelectionRequest{Intent: intent, Count: 3})
	if err != nil || candidateIDs(selected.Candidates) != "a1" {
		t.Fatalf("weak/missing requested comparison was padded by unrelated bonuses: %+v %v", selected, err)
	}
}

func TestEnhancedExplicitEndpointsAreActualOutputAndCountedOnce(t *testing.T) {
	cat := testCatalog()
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
	intent := enhancedIntent(5)
	intent.Mode = core.ModeJourney
	intent.Start = &core.IntentReference{Kind: core.ReferenceArtist, Query: "Seed Artist", Influence: core.InfluencePositive}
	intent.Destination = &core.IntentReference{Kind: core.ReferenceArtist, Query: "Last Artist", Influence: core.InfluencePositive}
	first, err := engine.Build(context.Background(), intent)
	if err != nil || len(first.Tracks) != 5 || first.Tracks[0].ID != "seed" || first.Tracks[4].ID != "last" {
		t.Fatalf("endpoints/count: %+v %v", first, err)
	}
	again, err := engine.Build(context.Background(), first.Intent)
	if err != nil || !reflect.DeepEqual(first.Tracks, again.Tracks) {
		t.Fatalf("endpoint replay changed: %+v %v", again.Tracks, err)
	}
	intent.Mode = core.ModeSimilar
	outsideJourney, err := engine.Build(context.Background(), intent)
	if err != nil || len(outsideJourney.Tracks) != 5 || outsideJourney.Tracks[0].ID != "seed" || outsideJourney.Tracks[4].ID != "last" {
		t.Fatalf("explicit endpoint role depended on journey mode: %+v %v", outsideJourney, err)
	}
	intent.Start = nil
	ordinary, err := engine.Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	for _, track := range ordinary.Tracks {
		if track.ID == "seed" {
			t.Fatal("ordinary reference became required output")
		}
	}
	intent.Start = &core.IntentReference{Kind: core.ReferenceArtist, Query: "Absent Fixture Artist", Influence: core.InfluencePositive}
	unresolved, err := engine.Build(context.Background(), intent)
	if err != nil || unresolved.Outcome.State != core.OutcomeNeedsClarification || len(unresolved.Tracks) != 0 {
		t.Fatalf("unresolved start was guessed: %+v %v", unresolved, err)
	}
	intent.Start = &core.IntentReference{Kind: core.ReferenceArtist, Query: "Seed Artist", Influence: core.InfluencePositive}
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_artist", Value: "Seed Artist", Supported: true}}
	conflict, err := engine.Build(context.Background(), intent)
	if err == nil && (len(conflict.Tracks) != 0 || conflict.Outcome.State != core.OutcomeNeedsClarification) {
		t.Fatalf("excluded endpoint bypassed requirement: %+v", conflict)
	}
}

func TestEnhancedArtistEndpointWithoutPreviewRemainsExplicitCloseOutput(t *testing.T) {
	cat := testCatalog()
	service, _ := cachedAudioService(t, cat, "cooc")
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service })
	intent := enhancedIntent(1)
	intent.Mode = core.ModeJourney
	intent.Start = &core.IntentReference{Kind: core.ReferenceArtist, Query: "Seed Artist", Influence: core.InfluencePositive}
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "ambient", Scope: "journey_start"}}
	playlist, err := engine.Build(context.Background(), intent)
	if err != nil || len(playlist.Tracks) != 1 || playlist.Tracks[0].ID != "seed" || playlist.Outcome.State == core.OutcomeNeedsClarification {
		t.Fatalf("missing preview erased explicit artist endpoint: %+v %v", playlist, err)
	}
}

func TestEnhancedCategoryJourneyFixesActualFirstRecording(t *testing.T) {
	cat := journeyCatalog()
	intent := enhancedIntent(5)
	intent.Mode = core.ModeJourney
	intent.Start = &core.IntentReference{Kind: core.ReferenceTrack, TrackID: "start"}
	intent.Destination = &core.IntentReference{Kind: core.ReferenceTrack, TrackID: "end"}
	request := ports.SequenceRequest{Intent: intent, Required: refs(cat, "start", "end"), Candidates: []core.Candidate{sequencingCandidate(cat, "first", 1), sequencingCandidate(cat, "middle", 1), sequencingCandidate(cat, "second", 1)}, CategoryStages: []map[string]bool{{"start": true, "first": true, "middle": true}, {"middle": true, "second": true, "end": true}}}
	result, err := NewSequencer(cat, DefaultConfig()).Sequence(context.Background(), request)
	if err != nil || len(result.Tracks) != 5 || result.Tracks[0].ID != "start" || result.Tracks[4].ID != "end" {
		t.Fatalf("category endpoints: %+v %v", result, err)
	}
}

func TestEnhancedCloseCategoryNeedsRequestedEvidenceAndRequiredStaysStrict(t *testing.T) {
	cat := fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "audio", Display: "A - Strong", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "unknown", Display: "B - Close", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "cooc", Display: "C - Wrong", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "far", Display: "D - No evidence", Audio: []float32{1, 0}, Track: []float32{1, 0}})
	service, _ := cachedAudioService(t, cat, "audio", "unknown", "cooc")
	service.Policy = audio.Policy{}
	intent := enhancedIntent(3)
	intent.References = nil
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "electronic", Scope: "playlist", Strength: "essential"}}
	session, err := service.Begin(context.Background(), intent, cat.CatalogVersion(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for _, track := range refs(cat, "audio", "unknown", "cooc", "far") {
		if _, err := session.Check(context.Background(), track, false); err != nil {
			t.Fatal(err)
		}
	}
	features := &semanticFixture{info: core.FeatureStoreInfo{SupportedFacets: []string{"styles"}}, features: map[string]core.TrackFeatures{"audio": completeStyleFeature("audio", "electronic"), "cooc": completeStyleFeature("cooc", "rock")}}
	o := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, features, nil, DefaultConfig())
	o.enhanced, o.bestAvailable, o.audioSession = true, true, session
	got, _, err := o.filterEssential(context.Background(), candidatesForTracks(refs(cat, "audio", "unknown", "cooc", "far")), intent.EssentialCriteria)
	if err != nil || candidateIDs(got) != "audio,unknown" || got[0].FitTier != fitStrong || got[1].FitTier != fitClose {
		t.Fatalf("close/strict mismatch: %+v %v", got, err)
	}
	intent.EssentialCriteria[0].Strength = "required"
	got, _, err = o.filterEssential(context.Background(), candidatesForTracks(refs(cat, "audio", "unknown", "cooc", "far")), intent.EssentialCriteria)
	if err != nil || candidateIDs(got) != "audio" {
		t.Fatalf("required criterion relaxed: %+v %v", got, err)
	}
}

func TestEnhancedCriterionORGroupsPreserveAlternativesAndIndependentDemands(t *testing.T) {
	cat := testCatalog()
	features := &semanticFixture{info: core.FeatureStoreInfo{SupportedFacets: []string{"styles"}}, features: map[string]core.TrackFeatures{"audio": completeStyleFeature("audio", "classical"), "cooc": completeStyleFeature("cooc", "ambient"), "other": completeStyleFeature("other", "rock")}}
	o := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, features, nil, DefaultConfig())
	o.enhanced, o.bestAvailable = true, true
	criteria := []core.MusicalCriterion{{Kind: "genre", Value: "classical", Scope: "playlist", Group: "choice", Strength: "required"}, {Kind: "genre", Value: "ambient", Scope: "playlist", Group: "choice", Strength: "required"}}
	got, _, err := o.filterEssential(context.Background(), candidatesForTracks(refs(cat, "audio", "cooc", "other")), criteria)
	if err != nil || candidateIDs(got) != "audio,cooc" {
		t.Fatalf("OR treated as AND: %+v %v", got, err)
	}
	criteria = append(criteria, core.MusicalCriterion{Kind: "vocal", Value: "instrumental", Scope: "playlist", Strength: "required"})
	got, _, err = o.filterEssential(context.Background(), got, criteria)
	if err != nil || len(got) != 0 {
		t.Fatalf("independent required facet bypassed: %+v %v", got, err)
	}
}

func TestEnhancedInstrumentalRecordingTagIsVocalEvidenceButPreviewMismatchWins(t *testing.T) {
	cat := testCatalog()
	criterion := core.MusicalCriterion{Kind: "vocal", Value: "instrumental", Scope: "playlist", Strength: "required"}
	track := core.EnrichedTrack{
		Ref: refs(cat, "audio")[0], IdentityStatus: core.ResolutionResolved,
		GenreTags: []core.AttributedGenreTag{{Name: "instrumental", Votes: 1, Source: "musicbrainz", Facet: "genre"}},
	}
	o := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
	o.enhanced, o.bestAvailable = true, true
	o.knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{track}}
	if got := o.bestCriterion(context.Background(), "audio", criterion); got != core.EvidenceMatch {
		t.Fatalf("instrumental recording tag = %q, want match", got)
	}
	if got := o.bestCriterion(context.Background(), "audio", core.MusicalCriterion{Kind: "vocal", Value: "vocals", Scope: "playlist", Strength: "required"}); got != core.EvidenceMismatch {
		t.Fatalf("instrumental recording tag did not oppose vocals: %q", got)
	}
	clause := core.AudioClause{Kind: "vocal", Text: "instrumental", Scope: "playlist", Strict: true, Essential: true}
	preview := core.AudioAssessment{TrackID: "audio", Clauses: []core.AudioClauseAssessment{{Clause: clause, State: core.EvidenceMismatch}}}
	if got := o.clauseFitState(context.Background(), "audio", clause, preview); got != core.EvidenceMismatch {
		t.Fatalf("preview mismatch was hidden by metadata: %q", got)
	}
	intent := enhancedIntent(1)
	intent.Preferences.VocalPreference = &core.IntentPreference{Value: "instrumental", Influence: core.InfluencePositive, Strength: "required"}
	if !o.previewNeededForEnhanced(context.Background(), candidatesForTracks(refs(cat, "audio"))[0], intent) {
		t.Fatal("instrumental metadata bypassed preview vocal screening")
	}
	criteria := []core.MusicalCriterion{criterion, {Kind: "genre", Value: "ambient", Scope: "playlist", Strength: "essential"}}
	got, _, err := o.filterEssential(context.Background(), candidatesForTracks(refs(cat, "audio")), criteria)
	if err != nil || len(got) != 1 || !got[0].Available.LibraryMetadata || got[0].Scores.LibraryMetadata != .5 || got[0].FitTier != fitClose {
		t.Fatalf("partial recording-tag evidence was not carried to selection: %+v %v", got, err)
	}
	if score, available := enhancedRequestRelevance(got[0], intent); !available || score != .5 {
		t.Fatalf("recording-tag relevance = %v,%v", score, available)
	}
}

func TestEnhancedArchiveConflictDoesNotEraseFreshComparisonOrBecomeStrong(t *testing.T) {
	cat := testCatalog()
	intent := enhancedIntent(2)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "electronic", Scope: "playlist", Strength: "essential"}}
	metadata := acousticFixture("audio", .01)
	intent.Knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{metadata}}
	comparisons := acousticComparisons(metadata, audio.Clauses(intent))
	if !acousticCompatibleFor(intent, comparisons) || acousticCompatible(comparisons) {
		t.Fatal("Enhanced archive conflict policy leaked into legacy or remained a veto")
	}
	clause := audio.Clauses(intent)[0]
	preview := core.AudioAssessment{PolicyVersion: audio.SimilarityPolicyVersion + "+" + audio.QueryPolicyVersion, Clauses: []core.AudioClauseAssessment{{Clause: clause, Score: .8, ScoreAvailable: true, State: core.EvidenceUnknown}}}
	ranked, err := NewRanker(cat, DefaultConfig()).Rank(context.Background(), candidatesForTracks(refs(cat, "audio")), ports.RankRequest{Intent: intent, PreviewAssessments: map[string]core.AudioAssessment{"audio": preview}})
	if err != nil || ranked[0].Scores.SemanticMatch != .8 {
		t.Fatalf("archive erased fresh preview: %+v %v", ranked, err)
	}
	intent.EssentialCriteria[0].Strength = "required"
	if acousticCompatibleFor(intent, acousticComparisons(metadata, audio.Clauses(intent))) {
		t.Fatal("strict archive screening relaxed")
	}
}

func TestEnhancedDictionaryDSPDoesNotConfuseDeepPitchWithBassAmount(t *testing.T) {
	intent := enhancedIntent(2)
	intent.Translation = &core.IntentTranslation{}
	intent.OriginalDescription = "deep bass"
	intent.Preferences.TextureDescriptions = []core.IntentPreference{{Value: "deep bass", ConceptID: "texture.deep-bass", Influence: core.InfluencePositive}}
	if clauses := enhancedClauses(intent); len(clauses) != 0 {
		t.Fatalf("deep pitch became measured bass loudness: %+v", clauses)
	}
	intent.Preferences.TextureDescriptions = []core.IntentPreference{{Value: "dark", ConceptID: "texture.dark", Influence: core.InfluencePositive}}
	clauses := enhancedClauses(intent)
	if len(clauses) != 1 || clauses[0].Text != "bright" || !clauses[0].Negative {
		t.Fatalf("signed DSP mapping lost: %+v", clauses)
	}
	if acousticClass("genre", "hip-hop", "genre_tzanetakis") != "hip" || acousticClass("texture", "bright", "timbre") != "bright" || acousticClass("genre", "drum and bass", "genre_electronic") != "" {
		t.Fatal("exact alias or applicability mapping incorrect")
	}
}

func TestEnhancedRefreshIsCacheOnlyAndPreservesReplayAndStop(t *testing.T) {
	cat, input := enhancedFixture(t)
	o := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
	acquired, refreshed := 0, 0
	o.WithEnhancedAudioProvider(func(context.Context, core.MusicIntent, core.TasteProfile, []core.TrackRef) (*core.EnhancedAudioSnapshot, error) {
		acquired++
		return core.NewEnhancedAudioSnapshot(input)
	})
	o.WithEnhancedAudioRefreshProvider(func(_ context.Context, _ core.MusicIntent, _ core.TasteProfile, tracks []core.TrackRef, previous *core.EnhancedAudioSnapshot) (*core.EnhancedAudioSnapshot, error) {
		refreshed++
		if previous == nil || previous.Fingerprint() != o.enhancedSnapshot.Fingerprint() {
			t.Fatal("refresh lost initial request evidence")
		}
		found := false
		for _, track := range tracks {
			found = found || track.ID == "c"
		}
		if !found {
			t.Fatal("refill evidence not included")
		}
		return core.NewEnhancedAudioSnapshot(input)
	})
	intent := enhancedIntent(2)
	request := ports.RecommendationRequest{Intent: intent}
	if err := o.prepareEnhanced(context.Background(), candidatesForTracks(refs(cat, "a")), intent, request, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := o.prepareEnhanced(context.Background(), candidatesForTracks(refs(cat, "a", "c")), intent, request, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if acquired != 1 || refreshed != 1 {
		t.Fatalf("acquisition restarted: %d/%d", acquired, refreshed)
	}
	before := o.enhancedSnapshot.Fingerprint()
	stop := make(chan struct{})
	close(stop)
	request.StopChecking = stop
	if err := o.prepareEnhanced(context.Background(), nil, intent, request, nil, nil, nil); err != nil || o.enhancedSnapshot.Fingerprint() != before || refreshed != 1 {
		t.Fatal("stop discarded evidence or started refresh")
	}
	request.EnhancedAudio = o.enhancedSnapshot
	if err := o.prepareEnhanced(context.Background(), nil, intent, request, nil, nil, nil); err != nil || acquired != 1 || refreshed != 1 {
		t.Fatal("saved replay triggered acquisition")
	}
}

func TestEnhancedMetadataFallbackDoesNotBypassMissingStrictVocalEvidence(t *testing.T) {
	cat := testCatalog()
	features := &semanticFixture{info: core.FeatureStoreInfo{SupportedFacets: []string{"styles"}}, features: map[string]core.TrackFeatures{"audio": completeStyleFeature("audio", "classical")}}
	o := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, features, nil, DefaultConfig())
	o.enhanced, o.bestAvailable = true, true
	intent := enhancedIntent(2)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "classical", Scope: "playlist"}}
	candidate := candidatesForTracks(refs(cat, "audio"))[0]
	if !o.enhancedMetadataFallback(context.Background(), candidate, intent) {
		t.Fatal("affirmative metadata could not provide a close fallback")
	}
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_vocals"}}
	if o.enhancedMetadataFallback(context.Background(), candidate, intent) {
		t.Fatal("metadata genre bypassed strict no-vocals evidence")
	}
	tier, detail := o.enhancedTier(context.Background(), candidate, intent)
	if tier != fitClose || !strings.Contains(detail, "vocals") {
		t.Fatalf("missing vocal evidence hidden: %s %s", tier, detail)
	}
}

func TestEnhancedScopedVocalExclusionCannotLeakIntoWrongJourneyStage(t *testing.T) {
	cat := testCatalog()
	f := completeStyleFeature("audio", "rock")
	f.VocalEvidence = f.Styles[0]
	f.VocalEvidence.Value = "vocal"
	features := &semanticFixture{info: core.FeatureStoreInfo{SupportedFacets: []string{"styles", "vocal_evidence"}}, features: map[string]core.TrackFeatures{"audio": f}}
	o := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, features, nil, DefaultConfig())
	o.enhanced, o.bestAvailable = true, true
	intent := enhancedIntent(2)
	intent.Mode = core.ModeJourney
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "rock", Scope: "journey_start"}, {Kind: "genre", Value: "rock", Scope: "journey_end"}}
	intent.Preferences.VocalPreference = &core.IntentPreference{Value: "vocals", Influence: core.InfluenceNegative, Scope: "journey_start", Strength: "required"}
	candidate := candidatesForTracks(refs(cat, "audio"))[0]
	if !o.enhancedMetadataFallback(context.Background(), candidate, intent) {
		t.Fatal("a possible end-stage candidate was globally excluded")
	}
	start, _, err := o.filterJourneyStage(context.Background(), []core.Candidate{candidate}, intent.EssentialCriteria[0], intent)
	if err != nil || len(start) != 0 {
		t.Fatalf("known vocals entered forbidden start: %+v %v", start, err)
	}
	end, _, err := o.filterJourneyStage(context.Background(), []core.Candidate{candidate}, intent.EssentialCriteria[1], intent)
	if err != nil || len(end) != 1 {
		t.Fatalf("permitted end stage lost: %+v %v", end, err)
	}
	intent.Preferences.VocalPreference = nil
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_vocal", Value: "harsh vocals"}}
	if o.enhancedMetadataFallback(context.Background(), candidate, intent) {
		t.Fatal("generic vocal label falsely proved absence of harsh vocals")
	}
	if reasons := o.unsupportedStrictReasons(intent); len(reasons) != 1 {
		t.Fatalf("unsupported strict classifier not surfaced: %+v", reasons)
	}
}

func TestEnhancedGroupedTierUsesAnyAllowedAlternativeAndCategoryAliasesStayDirectional(t *testing.T) {
	cat := testCatalog()
	features := &semanticFixture{info: core.FeatureStoreInfo{SupportedFacets: []string{"styles"}}, features: map[string]core.TrackFeatures{"audio": completeStyleFeature("audio", "classical")}}
	o := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, features, nil, DefaultConfig())
	o.enhanced, o.bestAvailable = true, true
	intent := enhancedIntent(1)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "classical", Scope: "playlist", Group: "choice"}, {Kind: "genre", Value: "ambient", Scope: "playlist", Group: "choice"}}
	if tier, detail := o.enhancedTier(context.Background(), candidatesForTracks(refs(cat, "audio"))[0], intent); tier != fitStrong {
		t.Fatalf("allowed OR alternative mislabeled: %s %s", tier, detail)
	}
	if !enhancedCategoryMatches("electronic", "electronica", core.GenreGraph{}) || enhancedCategoryMatches("electronica", "electronic", core.GenreGraph{}) || !enhancedCategoryMatches("hip-hop", "hip hop", core.GenreGraph{}) {
		t.Fatal("exact aliases and parent direction conflated")
	}
	intent.Mode = core.ModeJourney
	intent.EssentialCriteria[0].Scope, intent.EssentialCriteria[1].Scope = "journey_start", "journey_start"
	intent.EssentialCriteria = append(intent.EssentialCriteria, core.MusicalCriterion{Kind: "genre", Value: "rock", Scope: "journey_end"})
	if stages := journeyStageCriteria(intent); len(stages) != 2 {
		t.Fatalf("OR alternatives became separate sequential stages: %+v", stages)
	}
}

func TestEnhancedJourneyReservationUsesDirectRequestEvidenceAtUnchangedFloor(t *testing.T) {
	cat := testCatalog()
	features := &semanticFixture{info: core.FeatureStoreInfo{SupportedFacets: []string{"styles"}}, features: map[string]core.TrackFeatures{
		"audio": completeStyleFeature("audio", "rock"),
		"cooc":  completeStyleFeature("cooc", "classical"),
	}}
	o := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, features, nil, DefaultConfig())
	o.enhanced, o.bestAvailable = true, true
	intent := enhancedIntent(2)
	intent.Mode = core.ModeJourney
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "rock", Scope: "journey_start"}, {Kind: "genre", Value: "classical", Scope: "journey_end"}}
	candidates := candidatesForTracks(refs(cat, "audio", "cooc"))
	for i := range candidates {
		candidates[i].FitTier = fitClose
		candidates[i].Scores.Total = .01
		candidates[i].Scores.SemanticMatch = .01 - float64(i)*.002
		candidates[i].Available.SemanticMatch = true
	}
	reserved, _, reasons, err := o.reserveJourneyStages(context.Background(), candidates, nil, intent)
	if err != nil || len(reserved) != 2 || len(reasons) != 0 {
		t.Fatalf("direct evidence did not preserve journey stages: reserved=%+v reasons=%+v error=%v", reserved, reasons, err)
	}
}

func TestEnhancedJourneyReservationAllowsOverlappingPositiveStageComparisons(t *testing.T) {
	cat := testCatalog()
	service, _ := cachedAudioService(t, cat, "audio", "cooc", "last")
	service.Policy = audio.Policy{}
	intent := enhancedIntent(3)
	intent.Mode = core.ModeJourney
	intent.EssentialCriteria = []core.MusicalCriterion{
		{Kind: "genre", Value: "ambient", Scope: "journey_start"},
		{Kind: "genre", Value: "rock", Scope: "journey_via"},
		{Kind: "genre", Value: "melodic", Scope: "journey_end"},
	}
	session, err := service.Begin(context.Background(), intent, cat.CatalogVersion(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for _, track := range refs(cat, "audio", "cooc", "last") {
		if _, err := session.Check(context.Background(), track, false); err != nil {
			t.Fatal(err)
		}
	}
	o := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
	o.enhanced, o.bestAvailable, o.audioSession = true, true, session
	candidates := candidatesForTracks(refs(cat, "audio", "cooc", "last"))
	for i := range candidates {
		candidates[i].FitTier = fitClose
		candidates[i].Scores.Total = .01
		candidates[i].Scores.SemanticMatch = .001 * float64(len(candidates)-i)
		candidates[i].Available.SemanticMatch = true
	}
	reserved, _, reasons, err := o.reserveJourneyStages(context.Background(), candidates, nil, intent)
	if err != nil || len(reserved) != 3 || len(reasons) != 0 {
		t.Fatalf("overlapping direct comparisons did not cover journey: reserved=%+v reasons=%+v error=%v", reserved, reasons, err)
	}
}

func TestEnhancedPreviewNeededOnlyForIncompleteCatalogEvidence(t *testing.T) {
	cat := testCatalog()
	features := &semanticFixture{info: core.FeatureStoreInfo{SupportedFacets: []string{"styles"}}, features: map[string]core.TrackFeatures{"audio": completeStyleFeature("audio", "classical")}}
	o := NewWithSemantic(cat, fakes.NewSimilarityEngine(cat), cat, features, nil, DefaultConfig())
	o.enhanced, o.bestAvailable = true, true
	candidate := candidatesForTracks(refs(cat, "audio"))[0]
	intent := enhancedIntent(1)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "classical", Scope: "playlist"}}
	if o.previewNeededForEnhanced(context.Background(), candidate, intent) {
		t.Fatal("fully grounded category requested a redundant preview")
	}
	intent.Preferences.TextureDescriptions = []core.IntentPreference{{Value: "sparkle", Influence: core.InfluencePositive}}
	if !o.previewNeededForEnhanced(context.Background(), candidate, intent) {
		t.Fatal("unknown texture bypassed preview evidence")
	}
	intent.Preferences.TextureDescriptions = nil
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_vocals"}}
	if !o.previewNeededForEnhanced(context.Background(), candidate, intent) {
		t.Fatal("strict vocal exclusion bypassed preview evidence")
	}
}

type compoundSupportFixture struct {
	ports.Catalog
	trackID string
}

func (c compoundSupportFixture) CompoundGenreSupport(_ context.Context, id string, criterion core.MusicalCriterion) bool {
	return id == c.trackID && criterion.Kind == "genre" && criterion.Value == "ambient electronica"
}

func TestEnhancedCompoundGenreSupportStaysCloseAndCannotBypassStrictClause(t *testing.T) {
	base := testCatalog()
	cat := compoundSupportFixture{Catalog: base, trackID: "audio"}
	o := New(cat, fakes.NewSimilarityEngine(base), base, DefaultConfig())
	o.enhanced, o.bestAvailable = true, true
	candidate := candidatesForTracks(refs(base, "audio"))[0]
	intent := enhancedIntent(1)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "ambient electronica"}}
	intent.Preferences.TextureDescriptions = []core.IntentPreference{{Value: "gentle pulse", Influence: core.InfluencePositive}}
	if !o.enhancedMetadataFallback(context.Background(), candidate, intent) || o.previewNeededForEnhanced(context.Background(), candidate, intent) {
		t.Fatal("sourced compound category did not retain a bounded close suggestion")
	}
	if tier, detail := o.enhancedTier(context.Background(), candidate, intent); tier != fitClose || !strings.Contains(detail, "gentle pulse") {
		t.Fatalf("unsupported texture was hidden: %s %s", tier, detail)
	}
	filtered, _, err := o.filterEnhancedEssential(context.Background(), []core.Candidate{candidate}, intent.EssentialCriteria)
	if err != nil || len(filtered) != 1 {
		t.Fatalf("compound genre candidate was filtered: %+v %v", filtered, err)
	}
	if relevance, available := enhancedRequestRelevance(filtered[0], intent); !available || relevance != .75 {
		t.Fatalf("compound genre request relevance = %v,%v, want .75,true", relevance, available)
	}
	selected, err := NewSelector(base, DefaultConfig()).Select(context.Background(), filtered, ports.SelectionRequest{Intent: intent, Count: 1})
	if err != nil || len(selected.Candidates) != 1 {
		t.Fatalf("compound genre support did not survive final selection: %+v %v", selected, err)
	}
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_vocals"}}
	if o.enhancedMetadataFallback(context.Background(), candidate, intent) || !o.previewNeededForEnhanced(context.Background(), candidate, intent) {
		t.Fatal("partial compound support bypassed strict vocal screening")
	}
}
