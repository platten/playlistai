package multichannel

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func contextIntent() core.MusicIntent {
	intent := testIntent(2)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	intent.References = []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Seed Artist", Influence: core.InfluencePositive,
		Resolution: &core.ReferenceResolution{Status: core.ResolutionResolved, Selected: &core.ResolutionCandidate{
			EntityID: "artist:seed artist", Artist: "Seed Artist", Representatives: []core.WeightedTrack{{TrackID: "seed", Weight: 1}},
		}},
	}}
	intent.Knowledge = &core.KnowledgeSnapshot{ContextPlans: []core.ContextSeedPlan{{
		ReferenceKind: core.ReferenceArtist, Query: "Seed Artist", EntityKey: "artist:seed artist", Scope: "playlist",
		Seeds:   []core.WeightedTrack{{TrackID: "other", Weight: 1}},
		Profile: core.ContextProfile{ID: "profile-revision", Kind: "artist", Genres: []string{"rock"}, ExtractorVersion: core.ContextProfileVersion},
	}}}
	return intent
}

func TestContextualSeedsRefineExplicitReferenceWithoutRewritingIt(t *testing.T) {
	t.Parallel()
	cat := testCatalog()
	intent := contextIntent()
	before, _ := json.Marshal(intent)
	positive := positiveReferenceVectors(cat, intent)
	if len(positive) != 1 || len(positive[0].reps) != 1 || positive[0].reps[0].id != "other" {
		t.Fatalf("context was ignored in the presence of an explicit artist: %+v", positive)
	}
	// Context cannot replace an actual requested output recording.
	required := resolvedRequiredTracks(cat, intent.References)
	if len(required) != 1 || required[0].ID != "seed" {
		t.Fatalf("context changed required endpoint identity: %+v", required)
	}
	after, _ := json.Marshal(intent)
	if string(before) != string(after) {
		t.Fatal("context selection mutated the saved intent")
	}
	var replay core.MusicIntent
	if err := json.Unmarshal(before, &replay); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(positive, positiveReferenceVectors(cat, replay.Normalized())) {
		t.Fatal("context snapshot failed history JSON round trip")
	}
}

func TestContextualSeedChangesRetrievedNeighborhood(t *testing.T) {
	t.Parallel()
	cat := fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "seed", Display: "Seed Artist - Later Sound", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "other", Display: "Seed Artist - Early Sound", Audio: []float32{0, 1}, Track: []float32{0, 1}},
		fakes.CatalogTrack{ID: "early-neighbor", Display: "Neighbor - Early Style", Audio: []float32{.01, .99}, Track: []float32{.01, .99}},
		fakes.CatalogTrack{ID: "later-neighbor", Display: "Neighbor - Later Style", Audio: []float32{.99, .01}, Track: []float32{.99, .01}},
	)
	cfg := DefaultConfig()
	cfg.SeedAudioBudget, cfg.SeedCooccurrenceBudget, cfg.ExplorationBudget = 1, 1, 0
	retriever := NewRetriever(cat, fakes.NewSimilarityEngine(cat), cfg)
	intent := contextIntent()
	// Excluding an artist from output must not erase its explicitly requested
	// sound reference or force retrieval back to the artist's global medoid.
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_artist", Value: "Seed Artist", Supported: true}}
	contextual, err := retriever.Retrieve(context.Background(), ports.RetrievalRequest{Intent: intent})
	if err != nil || len(contextual) != 1 || contextual[0].Track.ID != "early-neighbor" {
		t.Fatalf("scoped seed did not reach its neighborhood: %+v, %v", contextual, err)
	}
	eligibility := newEligibility(intent.Normalized(), resolvedReferenceTracks(cat, intent), nil)
	if _, excludedByID := eligibility.excludedIDs["seed"]; excludedByID {
		t.Fatal("original medoid must test the artist exclusion, not contextual reference-ID exclusion")
	}
	eligible, err := eligibility.filter(context.Background(), append(contextual, candidateFor(cat, "seed", 1)), false)
	if err != nil || len(eligible) != 1 || eligible[0].Track.ID != "early-neighbor" {
		t.Fatalf("output exclusion was lost while retaining the requested reference: %+v, %v", eligible, err)
	}
	intent.Knowledge = nil
	baseline, err := retriever.Retrieve(context.Background(), ports.RetrievalRequest{Intent: intent})
	if err != nil || len(baseline) != 1 || baseline[0].Track.ID != "later-neighbor" {
		t.Fatalf("baseline control unexpectedly used scoped context: %+v, %v", baseline, err)
	}
}

func TestContextualSeedsRejectInapplicablePlans(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(*core.MusicIntent){
		"CLAP first policy":         func(m *core.MusicIntent) { m.Controls.RecommendationMode = core.CLAPFirst },
		"wrong identity":            func(m *core.MusicIntent) { m.Knowledge.ContextPlans[0].EntityKey = "artist:someone else" },
		"wrong scope":               func(m *core.MusicIntent) { m.Knowledge.ContextPlans[0].Scope = "journey_end" },
		"old policy":                func(m *core.MusicIntent) { m.Knowledge.ContextPlans[0].Profile.ExtractorVersion = "obsolete" },
		"missing catalog recording": func(m *core.MusicIntent) { m.Knowledge.ContextPlans[0].Seeds[0].TrackID = "absent" },
		"zero weight":               func(m *core.MusicIntent) { m.Knowledge.ContextPlans[0].Seeds[0].Weight = 0 },
		"explicit recording": func(m *core.MusicIntent) {
			m.References[0].Kind = core.ReferenceTrack
			m.Knowledge.ContextPlans[0].ReferenceKind = core.ReferenceTrack
		},
	} {
		t.Run(name, func(t *testing.T) {
			intent := contextIntent()
			change(&intent)
			got := positiveReferenceVectors(testCatalog(), intent)
			if len(got) != 1 || len(got[0].reps) != 1 || got[0].reps[0].id != "seed" {
				t.Fatalf("inapplicable context replaced the original reference: %+v", got)
			}
		})
	}
}

func TestContextualSeedsRespectJourneyRoleAndRecordingDuplicates(t *testing.T) {
	t.Parallel()
	intent := contextIntent()
	start := intent.References[0]
	intent.Start = &start
	plan := &intent.Knowledge.ContextPlans[0]
	plan.Scope = "journey_start"
	plan.Seeds = []core.WeightedTrack{{TrackID: "audio", Weight: 2}, {TrackID: "audio-copy", Weight: 3}, {TrackID: "other", Weight: 2}}
	got := positiveReferenceVectors(testCatalog(), intent)
	if len(got) != 1 || len(got[0].reps) != 2 || got[0].reps[0].weight != .5 || got[0].reps[1].weight != .5 {
		t.Fatalf("start scope or recording deduplication lost: %+v", got)
	}
	intent.References[0].Influence = core.InfluenceNegative
	negative := negativeReferenceVectors(testCatalog(), intent)
	if len(negative) != 1 || negative[0].reps[0].id != "seed" {
		t.Fatalf("context changed explicit negative comparison: %+v", negative)
	}
}

type contextSearcher struct {
	queries []string
	err     error
}

func (*contextSearcher) Info() core.FeatureStoreInfo { return core.FeatureStoreInfo{} }
func (s *contextSearcher) Search(_ context.Context, text string, _ int, _ map[string]struct{}) ([]core.SemanticHit, error) {
	s.queries = append(s.queries, text)
	return []core.SemanticHit{{TrackID: "audio", Score: .8}}, s.err
}

func TestMusicContextQueriesAreBoundedAndNeverBecomeRecordingEvidence(t *testing.T) {
	t.Parallel()
	intent := contextIntent()
	intent.Preferences.Genres = []core.IntentPreference{{Value: "metal", Influence: core.InfluenceNegative}}
	intent.Knowledge.ContextPlans[0].Profile.Genres = []string{"metal", "rock", "ambient", "jazz", "soul", "blues"}
	intent.Knowledge.ContextPlans[0].Profile.Description = "This biography is not a musical query."
	before, _ := json.Marshal(intent)
	searcher := &contextSearcher{}
	cat := testCatalog()
	r := NewSemanticRetriever(cat, fakes.NewSimilarityEngine(cat), searcher, DefaultConfig())
	union := map[string]*core.Candidate{}
	if err := r.retrieveMusicContext(context.Background(), intent, union, nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(searcher.queries, []string{"rock", "ambient", "jazz", "soul"}) {
		t.Fatalf("context used excluded, biography or unbounded queries: %v", searcher.queries)
	}
	candidate := union["audio"]
	if candidate == nil || !hasChannel(*candidate, ChannelMusicContext) || candidate.Available.SemanticMatch || candidate.MusicalFit == core.EvidenceMatch {
		t.Fatalf("retrieval hint became a verified musical comparison: %+v", candidate)
	}
	o := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
	o.knowledge = intent.Knowledge
	if got := o.bestCriterion(context.Background(), "audio", core.MusicalCriterion{Kind: "genre", Value: "rock", Scope: "playlist"}); got == core.EvidenceMatch {
		t.Fatal("artist description certified a recording genre")
	}
	after, _ := json.Marshal(intent)
	if string(before) != string(after) {
		t.Fatal("retrieval context changed requested criteria")
	}
}

func TestMusicContextRetrievalRemainsOptionalAndHonorsCancellation(t *testing.T) {
	t.Parallel()
	cat := testCatalog()
	searcher := &contextSearcher{err: core.ErrUnavailable}
	r := NewSemanticRetriever(cat, fakes.NewSimilarityEngine(cat), searcher, DefaultConfig())
	intent := contextIntent()
	if _, err := r.Retrieve(context.Background(), ports.RetrievalRequest{Intent: intent}); err != nil {
		t.Fatalf("optional context outage prevented seeded retrieval: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.retrieveMusicContext(ctx, intent, map[string]*core.Candidate{}, nil); err != context.Canceled {
		t.Fatalf("cancellation lost: %v", err)
	}
	searcher.queries = nil
	intent.Destination = &intent.References[0]
	intent.Knowledge.ContextPlans[0].Scope = "journey_end"
	if err := r.retrieveMusicContext(context.Background(), intent, map[string]*core.Candidate{}, nil); err != nil || len(searcher.queries) != 0 {
		t.Fatalf("destination context contaminated starting retrieval: queries=%v err=%v", searcher.queries, err)
	}
}

func TestMusicContextSoundDescriptorsRespectUserNegatives(t *testing.T) {
	t.Parallel()
	intent := contextIntent()
	intent.Knowledge.ContextPlans[0].Profile.Characteristics = []string{"aggressive", "warm", "melancholic"}
	intent.Preferences.Moods = []core.IntentPreference{{Value: "aggressive", Influence: core.InfluenceNegative}}
	searcher := &contextSearcher{}
	cat := testCatalog()
	r := NewSemanticRetriever(cat, fakes.NewSimilarityEngine(cat), searcher, DefaultConfig())
	if err := r.retrieveMusicContext(context.Background(), intent, map[string]*core.Candidate{}, nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(searcher.queries, []string{"rock", "warm", "melancholic"}) {
		t.Fatalf("source traits overrode a user exclusion: %v", searcher.queries)
	}
}

func TestMusicContextNegativesKeepJourneyScope(t *testing.T) {
	t.Parallel()
	for _, scope := range []string{"journey_start", "journey_end", "playlist"} {
		t.Run(scope, func(t *testing.T) {
			intent := contextIntent()
			start := intent.References[0]
			intent.Start = &start
			intent.Knowledge.ContextPlans[0].Scope = "journey_start"
			intent.Knowledge.ContextPlans[0].Profile.Characteristics = []string{"aggressive"}
			intent.Preferences.Genres = []core.IntentPreference{{Value: "rock", Influence: core.InfluenceNegative, Scope: scope}}
			intent.Preferences.Moods = []core.IntentPreference{{Value: "aggressive", Influence: core.InfluenceNegative, Scope: scope}}
			searcher := &contextSearcher{}
			cat := testCatalog()
			r := NewSemanticRetriever(cat, fakes.NewSimilarityEngine(cat), searcher, DefaultConfig())
			if err := r.retrieveMusicContext(context.Background(), intent, map[string]*core.Candidate{}, nil); err != nil {
				t.Fatal(err)
			}
			if scope == "journey_end" {
				if !reflect.DeepEqual(searcher.queries, []string{"rock", "aggressive"}) {
					t.Fatalf("ending-only exclusion suppressed the requested start: %v", searcher.queries)
				}
			} else if len(searcher.queries) != 0 {
				t.Fatalf("applicable exclusion ignored: %v", searcher.queries)
			}
		})
	}
	intent := contextIntent()
	intent.Knowledge.ContextPlans[0] = core.ContextSeedPlan{Query: "rock", Scope: "journey_start", Profile: core.ContextProfile{Kind: "genre", ExtractorVersion: core.ContextProfileVersion}}
	intent.Preferences.Genres = []core.IntentPreference{{Value: "rock", Influence: core.InfluencePositive, Scope: "journey_end"}}
	if activeContextPlan(intent, intent.Knowledge.ContextPlans[0]) {
		t.Fatal("ending genre activated an unrelated starting context")
	}
}
