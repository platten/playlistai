package multichannel

import (
	"context"
	"errors"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func TestGenreAdmissionWaitsForCorroboration(t *testing.T) {
	o, intent, track := genreCorroborationFixture()
	candidate := core.Candidate{Track: track.Ref}
	// Retrieval must retain this lead until the bounded verifier can inspect it.
	before, _, err := o.filterEnhancedEssential(context.Background(), []core.Candidate{candidate}, intent.EssentialCriteria)
	if err != nil || len(before) != 1 {
		t.Fatalf("unknown lead removed before verification: %v %v", before, err)
	}
	if got, err := o.filterConfirmedOutput(context.Background(), before, intent); err != nil || len(got) != 0 {
		t.Fatalf("uncorroborated genre admitted to output: %v %v", got, err)
	}
	o.WithRecordingVerifier(recordingVerifierFunc(func(_ context.Context, got core.EnrichedTrack, _ []core.MusicalCriterion) (core.EnrichedTrack, error) {
		got.Claims = citedGenreFixture(got.Ref, "house").Claims
		return got, nil
	}))
	if err := o.verifyRecording(context.Background(), track.Ref, intent); err != nil {
		t.Fatal(err)
	}
	if got, err := o.filterConfirmedOutput(context.Background(), before, intent); err != nil || len(got) != 1 {
		t.Fatalf("completed evidence not admitted: %v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := o.filterConfirmedOutput(ctx, before, intent); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
}

func TestGenreAdmissionPreservesGroupingAndIndependentSets(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*core.MusicIntent)
		want int
	}{
		{"collective", func(*core.MusicIntent) {}, 3},
		{"strict per track blend", func(m *core.MusicIntent) {
			for i := range m.EssentialCriteria {
				m.EssentialCriteria[i].CoverageGroup = ""
			}
		}, 1},
		{"ordinary alternatives", func(m *core.MusicIntent) {
			for i := range m.EssentialCriteria {
				m.EssentialCriteria[i].CoverageGroup = ""
				m.EssentialCriteria[i].Group = "either"
			}
		}, 3},
		{"independent sets", func(m *core.MusicIntent) { m.EssentialCriteria[1].CoverageGroup = "second" }, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			o, intent, candidates := playlistCoverageFixture([]string{"house", "techno"}, 4, [][]string{{"house"}, {"techno"}, {"house", "techno"}, nil})
			test.edit(&intent)
			got, err := o.filterConfirmedOutput(context.Background(), candidates, intent)
			if err != nil || len(got) != test.want {
				t.Fatalf("ids=%v error=%v", candidateIDs(got), err)
			}
		})
	}
}

func TestGenreAdmissionKeepsSoftHintsAndOtherPolicies(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*core.MusicIntent)
		want bool
	}{
		{"defining", func(*core.MusicIntent) {}, false},
		{"soft criterion", func(m *core.MusicIntent) { m.EssentialCriteria[0].Strength = "preferred" }, true},
		{"soft preference", func(m *core.MusicIntent) {
			m.EssentialCriteria = nil
			m.Preferences.Genres = []core.IntentPreference{{Value: "house", Explicit: true, Strength: "preferred"}}
		}, true},
		{"explicit required preference", func(m *core.MusicIntent) {
			m.EssentialCriteria = nil
			m.Preferences.Styles = []core.IntentPreference{{Value: "house", Explicit: true, Strength: "required"}}
		}, false},
		{"legacy explicit preference", func(m *core.MusicIntent) {
			m.EssentialCriteria = nil
			m.Preferences.Genres = []core.IntentPreference{{Value: "house", Explicit: true}}
		}, false},
		{"artist and texture with inferred anchor", func(m *core.MusicIntent) {
			m.EssentialCriteria = []core.MusicalCriterion{{Kind: "texture", Value: "warm", Scope: "playlist"}}
			m.InferredAnchors = []core.InferredAnchor{{Reference: core.IntentReference{Kind: core.ReferenceArtist, Query: "Artist"}, Reason: "house retrieval lead"}}
			m.Knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{citedGenreFixture(core.TrackRef{ID: "tagged"}, "house")}}
		}, true},
		{"negative genre", func(m *core.MusicIntent) {
			m.EssentialCriteria = nil
			m.Preferences.Genres = []core.IntentPreference{{Value: "house", Explicit: true, Influence: core.InfluenceNegative, Strength: "required"}}
		}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			o, intent, track := genreCorroborationFixture()
			test.edit(&intent)
			if got := o.confirmedGenres(context.Background(), track.Ref.ID, intent, ""); got != test.want {
				t.Fatalf("admitted=%t want=%t", got, test.want)
			}
			o.enhanced = false
			if !o.confirmedGenres(context.Background(), track.Ref.ID, intent, "") {
				t.Fatal("other recommendation policy changed")
			}
		})
	}
}

func TestGenreAdmissionChecksAssignedJourneyAndRequiredWaypoints(t *testing.T) {
	o, intent, candidates := playlistCoverageFixture([]string{"house", "techno"}, 3, [][]string{{"house"}, {"techno"}, nil})
	intent.Mode = core.ModeJourney
	for i, scope := range []string{"journey_start", "journey_end"} {
		intent.EssentialCriteria[i].Scope, intent.EssentialCriteria[i].CoverageGroup = scope, ""
	}
	got, err := o.filterConfirmedOutput(context.Background(), candidates, intent)
	if err != nil || len(got) != 2 || !o.enhancedStageConstraints(context.Background(), "0", "journey_start", intent) || o.enhancedStageConstraints(context.Background(), "0", "journey_end", intent) {
		t.Fatalf("scoped admission flattened: %v %v", candidateIDs(got), err)
	}
	intent.Journey.Waypoints = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "1"}, {Kind: core.ReferenceTrack, TrackID: "0"}}
	reasons := o.requiredOutputReasons(context.Background(), []core.TrackRef{candidates[0].Track, candidates[1].Track}, intent)
	if len(reasons) != 2 || reasons[0].Code != "required_track_genre_unconfirmed" {
		t.Fatalf("wrong-stage required recordings bypassed gate: %+v", reasons)
	}
	intent.Journey.Waypoints[0], intent.Journey.Waypoints[1] = intent.Journey.Waypoints[1], intent.Journey.Waypoints[0]
	if reasons := o.requiredOutputReasons(context.Background(), []core.TrackRef{candidates[0].Track, candidates[1].Track}, intent); len(reasons) != 0 {
		t.Fatalf("supported journey rejected: %+v", reasons)
	}
}

func TestGenreAdmissionReturnsFewerTracksWithoutPadding(t *testing.T) {
	for _, required := range []bool{false, true} {
		t.Run(map[bool]string{false: "optional unknown omitted", true: "required unknown reported"}[required], func(t *testing.T) {
			o, intent, candidates := playlistCoverageFixture([]string{"house"}, 2, [][]string{{"house"}, {"house"}})
			intent.Knowledge.Tracks[1].Claims[0].Method = "community_tag"
			o.retriever = &poolRetriever{candidates: candidates, pageSize: 2}
			if required {
				intent.RequiredTracks = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: candidates[1].Track.ID, Influence: core.InfluencePositive}}
			}
			result, err := o.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent})
			if err != nil {
				t.Fatal(err)
			}
			if required {
				if len(result.Tracks) != 0 || result.Outcome.State != core.OutcomeNeedsClarification || result.Intent.Count != 2 || len(result.Intent.RequiredTracks) != 1 || len(result.Outcome.Reasons) == 0 || result.Outcome.Reasons[0].Code != "required_track_genre_unconfirmed" {
					t.Fatalf("required track silently dropped: %+v", result)
				}
			} else if len(result.Tracks) != 1 || result.Tracks[0].ID != candidates[0].Track.ID || result.Outcome.State != core.OutcomePartial {
				t.Fatalf("unknown padding or confirmed loss: ids=%v outcome=%+v", result.IDs(), result.Outcome)
			}
		})
	}
}

func TestGenreAdmissionDoesNotRequireEveryOtherFacetVerified(t *testing.T) {
	o, intent, candidates := playlistCoverageFixture([]string{"house"}, 1, [][]string{{"house"}, {"house"}})
	intent.References = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: candidates[0].Track.ID, Influence: core.InfluencePositive}}
	intent.EssentialCriteria = append(intent.EssentialCriteria, core.MusicalCriterion{Kind: "texture", Value: "warm", Scope: "playlist", Strength: "essential"})
	o.retriever = &poolRetriever{candidates: candidates, pageSize: 2}
	result, err := o.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent})
	if err != nil || len(result.Tracks) != 1 || result.Outcome.State != core.OutcomePartial || result.Intent.VerificationPolicy != core.BestAvailable {
		t.Fatalf("genre gate became global verified-only: ids=%v outcome=%+v err=%v", result.IDs(), result.Outcome, err)
	}
}

func TestGenreAdmissionRejectsConflictingEvidence(t *testing.T) {
	o, intent, candidates := playlistCoverageFixture([]string{"house"}, 1, [][]string{{"house"}})
	conflict := o.knowledge.Tracks[0].Claims[0]
	conflict.State = core.EvidenceMismatch
	conflict.Source.URL += "/conflicting-statement"
	o.knowledge.Tracks[0].Claims = append(o.knowledge.Tracks[0].Claims, conflict)
	if got, err := o.filterConfirmedOutput(context.Background(), candidates, intent); err != nil || len(got) != 0 {
		t.Fatalf("contradictory genre claims admitted: %+v %v", got, err)
	}
}

func TestGenreAdmissionVerifiesRequiredTrackBeforeDeciding(t *testing.T) {
	o, intent, candidates := playlistCoverageFixture([]string{"house"}, 1, [][]string{{"house"}})
	intent.Knowledge.Tracks[0].Claims[0].Method = "community_tag"
	intent.RequiredTracks = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: candidates[0].Track.ID, Influence: core.InfluencePositive}}
	o.retriever = &poolRetriever{candidates: candidates, pageSize: 1}
	calls := 0
	o.WithRecordingVerifier(recordingVerifierFunc(func(_ context.Context, got core.EnrichedTrack, _ []core.MusicalCriterion) (core.EnrichedTrack, error) {
		calls++
		got.Claims = citedGenreFixture(got.Ref, "house").Claims
		return got, nil
	}))
	result, err := o.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent})
	if err != nil || calls != 1 || len(result.Tracks) != 1 || result.Tracks[0].ID != candidates[0].Track.ID || result.Outcome.State != core.OutcomeFulfilled {
		t.Fatalf("required verification skipped: calls=%d ids=%v outcome=%+v error=%v", calls, result.IDs(), result.Outcome, err)
	}
}

func TestGenreAdmissionDoesNotPromoteInferredAnchorOrKnowledgeGenres(t *testing.T) {
	o, intent, candidates := playlistCoverageFixture([]string{"house"}, 1, [][]string{{"house"}, {"house"}})
	intent.EssentialCriteria = nil
	intent.OriginalDescription = "Music similar to Artist 0"
	intent.References = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: candidates[0].Track.ID, Influence: core.InfluencePositive}}
	intent.InferredAnchors = []core.InferredAnchor{{Reference: core.IntentReference{Kind: core.ReferenceTrack, TrackID: candidates[1].Track.ID}, Reason: "house retrieval lead"}}
	for i := range intent.Knowledge.Tracks {
		intent.Knowledge.Tracks[i].Claims[0].Method = "community_tag"
	}
	o.retriever = &poolRetriever{candidates: candidates, pageSize: 2}
	result, err := o.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent})
	if err != nil || len(result.Tracks) != 1 || len(genreAdmissionGroups(result.Intent)) != 0 {
		t.Fatalf("retrieval genre became user requirement: ids=%v criteria=%+v outcome=%+v err=%v", result.IDs(), result.Intent.EssentialCriteria, result.Outcome, err)
	}
}

func TestGenreAdmissionRequiredCollectiveCoverageRemainsCollective(t *testing.T) {
	o, intent, candidates := playlistCoverageFixture([]string{"house", "techno"}, 2, [][]string{{"house"}, {"techno"}})
	for i := range intent.EssentialCriteria {
		intent.EssentialCriteria[i].Strength = "required"
	}
	if got, err := o.filterConfirmedOutput(context.Background(), candidates, intent); err != nil || len(got) != 2 {
		t.Fatalf("strict collective group became per-track conjunction: ids=%v error=%v", candidateIDs(got), err)
	}
	o.retriever = &poolRetriever{candidates: candidates, pageSize: 2}
	result, err := o.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent})
	if err != nil || len(result.Tracks) != 2 || result.Outcome.State != core.OutcomeFulfilled {
		t.Fatalf("required collective genres not covered: ids=%v outcome=%+v error=%v", result.IDs(), result.Outcome, err)
	}
}

func TestGenreAdmissionStrictGroupKeepsNonstrictAlternative(t *testing.T) {
	o, intent, candidates := playlistCoverageFixture([]string{"house", "techno"}, 1, [][]string{{"techno"}})
	for i := range intent.EssentialCriteria {
		intent.EssentialCriteria[i].CoverageGroup, intent.EssentialCriteria[i].Group = "", "choice"
	}
	intent.EssentialCriteria[0].Strength = "required"
	intent.EssentialCriteria[1].Strength = "preferred"
	if !o.enhancedMetadataFallback(context.Background(), candidates[0], intent) {
		t.Fatal("fallback dropped a supported nonstrict OR alternative")
	}
	if got, err := o.filterConfirmedOutput(context.Background(), candidates, intent); err != nil || len(got) != 1 {
		t.Fatalf("strict group lost nonstrict OR alternative: %+v %v", got, err)
	}

	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "techno", Scope: "playlist", Strength: "essential", Group: "choice"}}
	intent.Preferences.VocalPreference = &core.IntentPreference{Value: "vocals", Influence: core.InfluenceNegative, Scope: "playlist", Strength: "required", Group: "choice"}
	if got, err := o.filterConfirmedOutput(context.Background(), candidates, intent); err != nil || len(got) != 1 || !o.enhancedMetadataFallback(context.Background(), candidates[0], intent) {
		t.Fatalf("mixed-kind/polarity alternative lost: %+v %v", got, err)
	}
	intent.Preferences.VocalPreference.Group = ""
	if got, err := o.filterConfirmedOutput(context.Background(), candidates, intent); err != nil || len(got) != 0 {
		t.Fatalf("independent strict absence bypassed: %+v %v", got, err)
	}
}
