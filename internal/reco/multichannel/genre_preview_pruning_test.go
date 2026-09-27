package multichannel

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// Exhausting verification must not trigger unusable preview work or hide later
// candidates that already have independent recording evidence.
func TestGenrePreviewPruningAfterVerificationBudget(t *testing.T) {
	for _, lateSupported := range []bool{false, true} {
		t.Run(fmt.Sprint("late-supported=", lateSupported), func(t *testing.T) {
			labels := make([][]string, 41)
			if lateSupported {
				labels[40] = []string{"house"}
			}
			o, intent, candidates := playlistCoverageFixture([]string{"house"}, 10, labels)
			calls := 0
			o.WithRecordingVerifier(recordingVerifierFunc(func(_ context.Context, tr core.EnrichedTrack, _ []core.MusicalCriterion) (core.EnrichedTrack, error) {
				calls++
				return tr, nil
			}))
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			for _, c := range candidates[:32] {
				if err := o.verifyRecording(ctx, c.Track, intent); err != nil {
					t.Fatal(err)
				}
			}
			if calls != 32 || len(o.verificationAttempted) != 32 {
				t.Fatalf("quota not demonstrably exhausted: calls=%d ledger=%d", calls, len(o.verificationAttempted))
			}
			store, err := audio.OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			resolver := &noPreviewFetch{}
			service := &audio.Service{Resolver: resolver, Analyzer: &audioFixtureEncoder{}, Store: store, Authorized: true, ParityValidated: true}
			session, err := service.BeginWithBudget(ctx, intent, "genre-exhaustion-fixture", nil, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			if session.Calibrated() {
				t.Fatal("fixture unexpectedly calibrated")
			}
			o.audioSession = session
			remaining := candidates[32:40]
			if lateSupported {
				remaining = candidates[32:]
			}
			o.retriever = &poolRetriever{candidates: remaining, pageSize: 32}
			got, _, err := o.collectIteratively(ctx, nil, nil, intent, ports.RecommendationRequest{Intent: intent}, newEligibility(intent, nil, nil), nil, nil, nil, 17)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if lateSupported {
				want = 1
			}
			if len(got) != want || calls != 32 || resolver.calls != 0 {
				t.Fatalf("tracks=%d want=%d verify=%d preview=%d", len(got), want, calls, resolver.calls)
			}
			if lateSupported && got[0].Track.ID != "40" {
				t.Fatal("lost independently supported later recording")
			}
			t.Logf("verifier=%d quota=%d subsequent_preview_resolutions=%d newAnalyses=%d returned=%d stop=%s", calls, len(o.verificationAttempted), resolver.calls, session.Snapshot().NewAnalyses, len(got), o.search.StopReason)
		})
	}
}

type genrePruningResolver struct {
	mu  sync.Mutex
	ids []string
}

func (r *genrePruningResolver) ResolveAudioPreview(_ context.Context, ref core.TrackRef, _ core.EnrichedTrack) (core.ResolvedAudioPreview, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, ref.ID)
	return core.ResolvedAudioPreview{}, nil
}

func genrePruningSession(t *testing.T, intent core.MusicIntent, parallelism int, calibrated bool) (*audio.Session, *audio.Service, *genrePruningResolver) {
	t.Helper()
	store, err := audio.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	resolver := &genrePruningResolver{}
	service := &audio.Service{Resolver: resolver, Analyzer: &parallelTestAudioAnalyzer{AudioAnalyzer: &audioFixtureEncoder{}, parallelism: parallelism}, Store: store, Authorized: true, ParityValidated: true}
	if calibrated {
		service.Policy = audio.Policy{Version: "synthetic/v1", DevelopmentSet: "control-flow-only", MinimumPositive: .6, MaximumNegative: .2}
	}
	session, err := service.BeginWithBudget(context.Background(), intent, "genre-pruning-fixture", nil, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Close)
	return session, service, resolver
}

func TestGenrePreviewPruningSequentialAndParallelKeepsSupportedContenders(t *testing.T) {
	for _, parallelism := range []int{1, 3} {
		t.Run(fmt.Sprint(parallelism), func(t *testing.T) {
			o, intent, candidates := playlistCoverageFixture([]string{"house"}, 10, [][]string{{"house"}, nil, {"house"}, nil, nil})
			// Supported genres still deserve optional texture/ranking comparisons.
			intent.Preferences.TextureDescriptions = []core.IntentPreference{{Value: "warm", Strength: "preferred", Explicit: true}}
			session, _, resolver := genrePruningSession(t, intent, parallelism, false)
			o.audioSession = session
			o.retriever = &poolRetriever{candidates: candidates, pageSize: len(candidates)}
			got, _, err := o.collectIteratively(context.Background(), nil, nil, intent, ports.RecommendationRequest{Intent: intent}, newEligibility(intent, nil, nil), nil, nil, nil, 17)
			slices.Sort(resolver.ids)
			if err != nil || len(got) != 2 || !slices.Equal(resolver.ids, []string{"0", "2"}) {
				t.Fatalf("supported contenders or batch pruning changed: tracks=%s preview=%v err=%v", candidateIDs(got), resolver.ids, err)
			}
			for _, c := range o.search.Candidates {
				if c.Track.ID != "0" && c.Track.ID != "2" && !slices.Contains(c.DecisionReasons, "requested_genre_unconfirmed") {
					t.Fatalf("omitted candidate lost its reason: %+v", c)
				}
			}
		})
	}
}

func TestGenrePreviewPruningLeavesUnclearAlternativesAndOtherModes(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Orchestrator, *core.MusicIntent)
		want bool
	}{
		{"ordinary unknown", func(*Orchestrator, *core.MusicIntent) {}, true},
		{"soft only", func(_ *Orchestrator, i *core.MusicIntent) { i.EssentialCriteria[0].Strength = "preferred" }, false},
		{"journey", func(_ *Orchestrator, i *core.MusicIntent) { i.Mode = core.ModeJourney }, false},
		{"scoped genre", func(_ *Orchestrator, i *core.MusicIntent) { i.EssentialCriteria[0].Scope = "journey_start" }, false},
		{"mixed OR", func(_ *Orchestrator, i *core.MusicIntent) {
			i.EssentialCriteria[0].Group = "either"
			i.EssentialCriteria = append(i.EssentialCriteria, core.MusicalCriterion{Kind: "texture", Value: "warm", Scope: "playlist", Group: "either", Strength: "preferred"})
		}, false},
		{"instrumental genre", func(_ *Orchestrator, i *core.MusicIntent) { i.EssentialCriteria[0].Value = "instrumental" }, false},
		{"no vocals style", func(_ *Orchestrator, i *core.MusicIntent) {
			i.EssentialCriteria[0].Kind, i.EssentialCriteria[0].Value = "style", "no vocals"
		}, false},
		{"vocal screening", func(_ *Orchestrator, i *core.MusicIntent) {
			i.Preferences.VocalPreference = &core.IntentPreference{Value: "instrumental", Strength: "required", Explicit: true}
		}, false},
		{"local decisive evidence", func(o *Orchestrator, _ *core.MusicIntent) {
			o.knowledge.Tracks[0] = citedGenreFixture(o.knowledge.Tracks[0].Ref, "house")
		}, false},
		{"Deej-AI", func(o *Orchestrator, i *core.MusicIntent) {
			o.enhanced = false
			i.Controls.RecommendationMode = core.DeejAIOnly
		}, false},
		{"AcousticBrainz", func(o *Orchestrator, i *core.MusicIntent) {
			o.enhanced = false
			i.Controls.RecommendationMode = core.AcousticBrainzFirst
		}, false},
		{"CLAP", func(o *Orchestrator, i *core.MusicIntent) {
			o.enhanced = false
			i.Controls.RecommendationMode = core.CLAPFirst
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, intent, track := genreCorroborationFixture()
			tc.edit(o, &intent)
			o.audioSession, _, _ = genrePruningSession(t, intent, 1, false)
			if got := o.previewCannotConfirmGenre(context.Background(), track.Ref.ID, intent); got != tc.want {
				t.Fatalf("pruned=%t want=%t", got, tc.want)
			}
		})
	}
}

func TestGenrePreviewPruningKeepsCollectiveAndORCoverage(t *testing.T) {
	for _, ordinaryOR := range []bool{false, true} {
		o, intent, candidates := playlistCoverageFixture([]string{"house", "techno"}, 3, [][]string{{"house"}, {"techno"}, nil})
		if ordinaryOR {
			for i := range intent.EssentialCriteria {
				intent.EssentialCriteria[i].CoverageGroup, intent.EssentialCriteria[i].Group = "", "either"
			}
		}
		o.audioSession, _, _ = genrePruningSession(t, intent, 1, false)
		for i, candidate := range candidates {
			if got := o.previewCannotConfirmGenre(context.Background(), candidate.Track.ID, intent); got != (i == 2) {
				t.Fatalf("OR=%t id=%s pruned=%t", ordinaryOR, candidate.Track.ID, got)
			}
		}
		intent.EssentialCriteria[1].CoverageGroup, intent.EssentialCriteria[1].Group = "separate", ""
		if !o.previewCannotConfirmGenre(context.Background(), candidates[0].Track.ID, intent) {
			t.Fatal("one supported genre erased an independent unmet obligation")
		}
	}
}

func TestGenrePreviewPruningKeepsCalibratedCacheAndCompletedEvidence(t *testing.T) {
	o, intent, track := genreCorroborationFixture()
	session, service, resolver := genrePruningSession(t, intent, 1, true)
	o.audioSession = session
	if o.previewCannotConfirmGenre(context.Background(), track.Ref.ID, intent) {
		t.Fatal("calibrated evidence acquisition skipped")
	}
	record := core.AudioAnalysis{TrackID: track.Ref.ID, CatalogVersion: "genre-pruning-fixture", TrackKey: core.ProvisionalRecordingKey(track.Ref), Model: service.Analyzer.Identity(), Identity: core.PreviewIdentity{PolicyVersion: core.PreviewIdentityPolicyVersion, Provider: "deezer", ProviderID: track.Ref.ID, Status: core.ResolutionResolved}, AudioSHA256: strings.Repeat("0", 64), Segments: []core.AudioSegment{{StartSeconds: 0, EndSeconds: 10, Embedding: []float32{1, 0}}}}
	record.ID = audio.Fingerprint(record)
	if err := service.Store.Put(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Check(context.Background(), track.Ref, false); err != nil || !o.confirmedGenres(context.Background(), track.Ref.ID, intent, "") || len(resolver.ids) != 0 {
		t.Fatalf("calibrated cached genre evidence lost: %v", err)
	}
	// Keep a completed request assessment even if no new categorical analysis
	// is available. This controlled fixture changes only future acquisition.
	service.Policy = audio.Policy{}
	if o.previewCannotConfirmGenre(context.Background(), track.Ref.ID, intent) {
		t.Fatal("completed decisive preview evidence was discarded")
	}
}

func TestGenrePreviewPruningRunsAfterVerification(t *testing.T) {
	o, intent, track := genreCorroborationFixture()
	o.audioSession, _, _ = genrePruningSession(t, intent, 1, false)
	calls := 0
	o.WithRecordingVerifier(recordingVerifierFunc(func(_ context.Context, tr core.EnrichedTrack, _ []core.MusicalCriterion) (core.EnrichedTrack, error) {
		calls++
		return citedGenreFixture(tr.Ref, "house"), nil
	}))
	_, _, keep, err := o.prepareIterativeCandidate(context.Background(), core.Candidate{Track: track.Ref}, intent, newEligibility(intent, nil, nil), nil)
	if err != nil || !keep || calls != 1 || !o.confirmedGenres(context.Background(), track.Ref.ID, intent, "") {
		t.Fatalf("candidate pruned before its verifier opportunity: keep=%t calls=%d err=%v", keep, calls, err)
	}
}

func TestGenrePreviewPruningBuildPreservesRequiredAndAnchorAnalysis(t *testing.T) {
	for _, role := range []string{"optional", "required", "anchor"} {
		t.Run(role, func(t *testing.T) {
			o, intent, candidates := playlistCoverageFixture([]string{"house"}, 1, [][]string{nil})
			_, service, resolver := genrePruningSession(t, intent, 1, false)
			o.WithAudioProvider(func() *audio.Service { return service })
			o.retriever = &poolRetriever{candidates: candidates, pageSize: 1}
			ref := core.IntentReference{Kind: core.ReferenceTrack, TrackID: candidates[0].Track.ID, Query: candidates[0].Track.Display(), Influence: core.InfluencePositive}
			switch role {
			case "required":
				intent.RequiredTracks = []core.IntentReference{ref}
			case "anchor":
				intent.InferredAnchors = []core.InferredAnchor{{Reference: ref, Role: "retrieval"}}
			}
			result, err := o.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent})
			want := 0
			if role != "optional" {
				want = 1
			}
			if err != nil || len(result.Tracks) != 0 || len(resolver.ids) != want {
				t.Fatalf("role=%s tracks=%v previews=%v err=%v", role, result.IDs(), resolver.ids, err)
			}
			if role == "required" && (result.Outcome.State != core.OutcomeNeedsClarification || len(result.Intent.RequiredTracks) != 1) {
				t.Fatal("required-track accounting disappeared")
			}
		})
	}
}

func TestGenrePreviewPruningPreservesStopAndCancel(t *testing.T) {
	for _, stopOnly := range []bool{false, true} {
		o, intent, candidates := playlistCoverageFixture([]string{"house"}, 1, [][]string{nil})
		session, _, resolver := genrePruningSession(t, intent, 1, false)
		o.audioSession = session
		o.retriever = &poolRetriever{candidates: candidates, pageSize: 1}
		ctx, cancel := context.WithCancel(context.Background())
		stop := make(chan struct{})
		if stopOnly {
			close(stop)
		} else {
			cancel()
		}
		_, _, err := o.collectIteratively(ctx, nil, nil, intent, ports.RecommendationRequest{Intent: intent, StopChecking: stop}, newEligibility(intent, nil, nil), nil, nil, nil, 17)
		cancel()
		if !stopOnly && !errors.Is(err, context.Canceled) || stopOnly && err != nil || len(resolver.ids) != 0 {
			t.Fatalf("stop=%t err=%v previews=%v", stopOnly, err, resolver.ids)
		}
	}
}
