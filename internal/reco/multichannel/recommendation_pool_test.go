package multichannel

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

type poolRetriever struct {
	candidates []core.Candidate
	calls      []ports.RetrievalRequest
	pageSize   int
}

func (r *poolRetriever) Retrieve(_ context.Context, request ports.RetrievalRequest) ([]core.Candidate, error) {
	copyRequest := request
	copyRequest.AttemptedIDs = make(map[string]struct{}, len(request.AttemptedIDs))
	for id := range request.AttemptedIDs {
		copyRequest.AttemptedIDs[id] = struct{}{}
	}
	r.calls = append(r.calls, copyRequest)
	var out []core.Candidate
	for _, candidate := range r.candidates {
		if _, seen := request.AttemptedIDs[candidate.Track.ID]; !seen {
			out = append(out, candidate)
			if r.pageSize > 0 && len(out) == r.pageSize {
				break
			}
		}
	}
	return out, nil
}

// Synthetic cached embeddings exercise the orchestration, not musical quality.
func recommendationPoolFixture(t *testing.T, size, mismatches int, artists ...string) (*fakes.Catalog, *audio.Service, *poolRetriever) {
	t.Helper()
	tracks := []fakes.CatalogTrack{{ID: "seed", Display: "Seed - Origin", Audio: []float32{1, 0}, Track: []float32{1, 0}}}
	var ids []string
	for i := range size {
		id := fmt.Sprintf("p%03d", i)
		ids = append(ids, id)
		artist := id
		if i < len(artists) {
			artist = artists[i]
		}
		tracks = append(tracks, fakes.CatalogTrack{ID: id, Display: artist + " - Song " + id, Audio: []float32{1, 0}, Track: []float32{1, 0}})
	}
	cat := fakes.NewCatalog(2, tracks...)
	service, _ := cachedAudioService(t, cat, "seed")
	for i, id := range ids {
		meta, _ := cat.Meta(id)
		vector := []float32{1, 0}
		if i < mismatches {
			vector = []float32{0, 1}
		}
		record := core.AudioAnalysis{TrackID: id, CatalogVersion: cat.CatalogVersion(), TrackKey: core.ProvisionalRecordingKey(meta.Ref), Model: service.Analyzer.Identity(), Identity: core.PreviewIdentity{Provider: "deezer", ProviderID: id, Status: core.ResolutionResolved}, AudioSHA256: strings.Repeat("0", 64), Segments: []core.AudioSegment{{StartSeconds: 0, EndSeconds: 10, Embedding: vector}}}
		record.ID = audio.Fingerprint(record)
		if err := service.Store.Put(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	return cat, service, &poolRetriever{candidates: candidatesForTracks(refs(cat, ids...))}
}

func poolIntent(count int) core.MusicIntent {
	intent := testIntent(count)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Value: "electronic", Scope: "playlist"}}
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_style", Value: "rock"}}
	return intent
}

func TestRecommendationPoolPreparesDoubleCountAndStopsAtFinalCount(t *testing.T) {
	for _, mode := range []core.RecommendationMode{core.AcousticBrainzFirst, core.CLAPFirst} {
		for _, iterative := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/iterative=%v", mode, iterative), func(t *testing.T) {
				cat, service, retriever := recommendationPoolFixture(t, 40, 0)
				engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service })
				if iterative {
					engine.WithCandidateSource(&fixtureDiscovery{})
				}
				engine.retriever = retriever
				intent := poolIntent(10)
				intent.Controls.RecommendationMode = mode
				checked, prepared := 0, 0
				request := ports.RecommendationRequest{Intent: intent, OnChecked: func(core.TrackRef) { checked++ }, Progress: ports.ProgressFunc(func(_ string, done, total int64, note string) {
					if strings.HasPrefix(note, "Prepared recommendation shortlist") {
						prepared++
						if checked != 0 || done != 20 || total != 20 {
							t.Fatalf("shortlist must precede checks: checked=%d pool=%d/%d", checked, done, total)
						}
					}
				})}
				got, err := engine.BuildRecommendation(context.Background(), request)
				if err != nil || len(got.Tracks) != 10 || got.Outcome.State != core.OutcomeFulfilled || checked != 10 || prepared != 1 || len(retriever.calls) != 1 || len(got.AudioEvidence.Assessments) != 10 {
					t.Fatalf("tracks=%d checked=%d pools=%d retrievals=%d outcome=%+v err=%v", len(got.Tracks), checked, prepared, len(retriever.calls), got.Outcome, err)
				}
				if got.Intent.Count != 10 || got.Intent.Controls.TotalTrackCount != 10 || got.Intent.Seed != "42" || retriever.calls[0].Intent.Count != 10 {
					t.Fatal("temporary pool size leaked into generation intent/history")
				}
				again, err := engine.Build(context.Background(), got.Intent)
				if err != nil || !reflect.DeepEqual(got.Tracks, again.Tracks) {
					t.Fatal("fixed-seed replay changed tracks", err)
				}
			})
		}
	}
}

func TestRecommendationPoolRefillsAfterRejectedShortlist(t *testing.T) {
	cat, service, retriever := recommendationPoolFixture(t, 40, 20)
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(&fixtureDiscovery{}).WithAudioProvider(func() *audio.Service { return service })
	engine.retriever = retriever
	got, err := engine.Build(context.Background(), poolIntent(10))
	if err != nil || len(got.Tracks) != 10 || len(retriever.calls) != 2 || len(retriever.calls[1].AttemptedIDs) != 20 || len(got.AudioEvidence.Assessments) != 30 {
		t.Fatalf("tracks=%d retrievals=%d err=%v", len(got.Tracks), len(retriever.calls), err)
	}
	for _, track := range got.Tracks {
		if track.ID < "p020" {
			t.Fatalf("mismatching/excluded recording survived: %s", track.ID)
		}
	}
}

func TestRecommendationPoolHonorsArchivedOppositionAndExhaustion(t *testing.T) {
	for _, mode := range []core.RecommendationMode{core.AcousticBrainzFirst, core.CLAPFirst} {
		t.Run(string(mode), func(t *testing.T) {
			cat, service, retriever := recommendationPoolFixture(t, 8, 2)
			engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(&fixtureDiscovery{}).WithAudioProvider(func() *audio.Service { return service })
			engine.retriever = retriever
			intent := poolIntent(10)
			intent.Controls.RecommendationMode = mode
			intent.Knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{acousticFixture("p002", .01)}}
			got, err := engine.Build(context.Background(), intent)
			if err != nil || len(got.Tracks) != 5 || got.Outcome.State != core.OutcomePartial {
				t.Fatalf("tracks=%d outcome=%+v err=%v", len(got.Tracks), got.Outcome, err)
			}
			for _, track := range got.Tracks {
				if track.ID <= "p002" {
					t.Fatalf("AcousticBrainz/CLAP opposition bypassed: %s", track.ID)
				}
			}
		})
	}
}

func TestRecommendationPoolCountsRequiredJourneyWaypointsOnce(t *testing.T) {
	cat, service, retriever := recommendationPoolFixture(t, 30, 0)
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(&fixtureDiscovery{}).WithAudioProvider(func() *audio.Service { return service })
	engine.retriever = retriever
	intent := poolIntent(10)
	intent.Mode = core.ModeJourney
	intent.RequiredTracks = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "p000"}, {Kind: core.ReferenceTrack, TrackID: "p029"}}
	intent.Journey.Waypoints = intent.RequiredTracks
	prepared := 0
	got, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, Progress: ports.ProgressFunc(func(_ string, done, total int64, note string) {
		if strings.HasPrefix(note, "Prepared recommendation shortlist") {
			prepared++
			if done != 20 || total != 20 {
				t.Fatalf("required tracks not included in 2N: %d/%d", done, total)
			}
		}
	})})
	if err != nil || len(got.Tracks) != 10 || prepared != 1 || got.Tracks[0].ID != "p000" || got.Tracks[9].ID != "p029" || len(got.AudioEvidence.Assessments) != 10 {
		t.Fatalf("journey=%+v pools=%d err=%v", got.Tracks, prepared, err)
	}
	seen := map[string]bool{}
	for _, track := range got.Tracks {
		key := core.ProvisionalRecordingKey(track)
		if seen[key] {
			t.Fatal("repeated required recording")
		}
		seen[key] = true
	}
	intent.Controls.TotalTrackCount = 2
	retriever.calls = nil
	got, err = engine.Build(context.Background(), intent)
	if err != nil || len(got.Tracks) != 2 || len(retriever.calls) != 0 || recommendationPoolSize(2, 2) != 0 {
		t.Fatalf("unnecessary pool for required-only output: tracks=%d calls=%d err=%v", len(got.Tracks), len(retriever.calls), err)
	}
}

func TestRecommendationPoolAdvancesEntirelyExcludedPage(t *testing.T) {
	cat, service, retriever := recommendationPoolFixture(t, 8, 0)
	retriever.pageSize = 4
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(&fixtureDiscovery{}).WithAudioProvider(func() *audio.Service { return service })
	engine.retriever = retriever
	intent := poolIntent(4)
	for _, artist := range []string{"p000", "p001", "p002", "p003"} {
		intent.HardConstraints = append(intent.HardConstraints, core.HardConstraint{Kind: "exclude_artist", Value: artist})
	}
	got, err := engine.Build(context.Background(), intent)
	if err != nil || len(got.Tracks) != 4 || len(retriever.calls) != 3 {
		t.Fatalf("excluded page treated as exhaustion: tracks=%d calls=%d err=%v", len(got.Tracks), len(retriever.calls), err)
	}
	for _, track := range got.Tracks {
		if track.ID < "p004" {
			t.Fatal("hard artist exclusion bypassed")
		}
	}
}

func TestRecommendationPoolExpandsRealRetrievalForLargeCount(t *testing.T) {
	cat, _, _ := recommendationPoolFixture(t, 240, 0)
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(&fixtureDiscovery{})
	intent := testIntent(100) // no descriptors: test actual retrieval, not audio I/O
	prepared := 0
	got, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, Progress: ports.ProgressFunc(func(_ string, done, total int64, note string) {
		if strings.HasPrefix(note, "Prepared recommendation shortlist") {
			prepared++
			if done != 200 || total != 200 {
				t.Fatalf("overlapping channels did not supply 2N: %d/%d", done, total)
			}
		}
	})})
	if err != nil || len(got.Tracks) != 100 || prepared != 1 {
		t.Fatalf("tracks=%d pools=%d err=%v", len(got.Tracks), prepared, err)
	}
	original := engine.retriever.(*Retriever)
	if original.cfg != DefaultConfig() {
		t.Fatal("request expanded shared retrieval budgets")
	}
	bounded := *original
	bounded.cfg.MaxCandidates = 12
	if expanded := bounded.withRecommendationPool(200); expanded.cfg.SeedAudioBudget > 32 || expanded.cfg.MaxCandidates != 12 {
		t.Fatal("configured retrieval cap ignored")
	}
}

func TestRecommendationPoolPreservesDiversityBeforeEarlyStop(t *testing.T) {
	var artists []string
	for _, artist := range []string{"A", "B", "C"} {
		for range 10 {
			artists = append(artists, artist)
		}
	}
	cat, service, retriever := recommendationPoolFixture(t, 30, 0, artists...)
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(&fixtureDiscovery{}).WithAudioProvider(func() *audio.Service { return service })
	engine.retriever = retriever
	intent := poolIntent(10)
	intent.Controls.ArtistDiversity = 1
	got, err := engine.Build(context.Background(), intent)
	if err != nil || len(got.Tracks) != 10 || len(retriever.calls) != 1 || len(got.AudioEvidence.Assessments) != 10 {
		t.Fatalf("tracks=%d calls=%d err=%v", len(got.Tracks), len(retriever.calls), err)
	}
	counts := map[string]int{}
	for i, track := range got.Tracks {
		counts[track.Artist]++
		if i > 0 && track.Artist == got.Tracks[i-1].Artist {
			t.Fatal("diversity alternatives ignored: consecutive artist")
		}
	}
	if len(counts) != 3 || counts["A"] > 4 || counts["B"] > 4 || counts["C"] > 4 {
		t.Fatalf("early stop crowded out artist alternatives: %v", counts)
	}
}

func TestRecommendationShortlistDoesNotEnforceProvisionalFloor(t *testing.T) {
	cat := testCatalog()
	selector := NewSelector(cat, DefaultConfig())
	candidates := candidatesForTracks(refs(cat, "audio", "cooc", "last"))
	for i := range candidates {
		candidates[i].Scores.Total = -3 - float64(i)
	}
	request := ports.SelectionRequest{Intent: testIntent(2), Count: 3}
	shortlist, err := selector.shortlist(context.Background(), candidates, request)
	if err != nil || len(shortlist.Candidates) != 3 {
		t.Fatalf("unassessed candidates discarded: %d %v", len(shortlist.Candidates), err)
	}
	final, err := selector.Select(context.Background(), candidates, request)
	if err != nil || len(final.Candidates) != 0 {
		t.Fatal("provisional ordering disabled final relevance floor", err)
	}
}

func TestRecommendationPoolKeepsSearchingForLaterJourneyStage(t *testing.T) {
	cat, service, retriever := recommendationPoolFixture(t, 6, 4)
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(&fixtureDiscovery{}).WithAudioProvider(func() *audio.Service { return service })
	engine.retriever = retriever
	intent := testIntent(2)
	intent.Mode = core.ModeJourney
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "style", Value: "rock", Scope: "journey_start"}, {Kind: "style", Value: "electronic", Scope: "journey_end"}}
	got, err := engine.Build(context.Background(), intent)
	if err != nil || len(got.Tracks) != 2 || got.Outcome.State != core.OutcomeFulfilled || len(retriever.calls) != 3 || len(got.AudioEvidence.Assessments) != 5 {
		t.Fatalf("incomplete journey stopped early: tracks=%v calls=%d outcome=%+v err=%v", got.Tracks, len(retriever.calls), got.Outcome, err)
	}
	if got.Tracks[0].ID >= "p004" || got.Tracks[1].ID < "p004" {
		t.Fatalf("journey order lost: %v", got.Tracks)
	}
}

func TestRecommendationPoolTopsUpPartiallyExcludedPageBeforeAnalysis(t *testing.T) {
	cat, service, retriever := recommendationPoolFixture(t, 40, 0)
	retriever.pageSize = 20
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(&fixtureDiscovery{}).WithAudioProvider(func() *audio.Service { return service })
	engine.retriever = retriever
	intent := poolIntent(10)
	for i := range 12 {
		intent.HardConstraints = append(intent.HardConstraints, core.HardConstraint{Kind: "exclude_artist", Value: fmt.Sprintf("p%03d", i)})
	}
	prepared, checked := 0, 0
	got, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, OnChecked: func(core.TrackRef) { checked++ }, Progress: ports.ProgressFunc(func(_ string, done, total int64, note string) {
		if strings.HasPrefix(note, "Prepared recommendation shortlist") {
			prepared++
			if checked != 0 || done != 20 || total != 20 || len(retriever.calls) != 2 {
				t.Fatalf("partially excluded page not topped up: checked=%d pool=%d/%d queries=%d", checked, done, total, len(retriever.calls))
			}
		}
	})})
	if err != nil || len(got.Tracks) != 10 || prepared != 1 || checked != 10 {
		t.Fatalf("tracks=%d pools=%d checked=%d err=%v", len(got.Tracks), prepared, checked, err)
	}
	for _, track := range got.Tracks {
		if track.ID < "p012" {
			t.Fatal("top-up bypassed hard exclusion")
		}
	}
}

func TestRecommendationPoolRefillRetainsOnlyAcceptedContinuationContext(t *testing.T) {
	cat, service, retriever := recommendationPoolFixture(t, 6, 3)
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithCandidateSource(&fixtureDiscovery{}).WithAudioProvider(func() *audio.Service { return service })
	engine.retriever = retriever
	intent := poolIntent(2)
	got, err := engine.Build(context.Background(), intent)
	if err != nil || len(got.Tracks) != 2 || len(retriever.calls) != 3 {
		t.Fatalf("tracks=%d retrievals=%d err=%v", len(got.Tracks), len(retriever.calls), err)
	}
	for _, request := range retriever.calls[1:] {
		if len(request.RecentSelections) != 1 || request.RecentSelections[0].ID != "p003" || request.Intent.References[0].TrackID != "seed" || request.Seed != 42 {
			t.Fatalf("refill lost anchor/accepted continuation or used rejected tracks: %+v", request)
		}
	}
}
