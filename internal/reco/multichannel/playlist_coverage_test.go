package multichannel

import (
	"context"
	"fmt"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func playlistCoverageFixture(genres []string, count int, labels [][]string) (*Orchestrator, core.MusicIntent, []core.Candidate) {
	var rows []fakes.CatalogTrack
	for i := range labels {
		rows = append(rows, fakes.CatalogTrack{ID: fmt.Sprint(i), Display: fmt.Sprintf("Artist %d - Song %d", i, i), Audio: []float32{1, 0}, Track: []float32{1, 0}})
	}
	cat := fakes.NewCatalog(2, rows...)
	o := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
	o.enhanced, o.bestAvailable = true, true
	intent := enhancedIntent(count)
	intent.References, intent.Seeds = nil, core.IntentSeeds{}
	intent.Knowledge = &core.KnowledgeSnapshot{}
	for _, genre := range genres {
		intent.EssentialCriteria = append(intent.EssentialCriteria, core.MusicalCriterion{Kind: "genre", Value: genre, Scope: "playlist", Strength: "essential", CoverageGroup: "mix"})
	}
	var candidates []core.Candidate
	for i, row := range rows {
		meta, _ := cat.Meta(row.ID)
		// These cited facts are independent fixture ground truth, not tags or
		// assertions about the musical quality of real recordings.
		intent.Knowledge.Tracks = append(intent.Knowledge.Tracks, citedGenreFixture(meta.Ref, labels[i]...))
		candidate := core.Candidate{Track: meta.Ref, Scores: core.CandidateScores{Total: 1 - float64(i)/1000}}
		candidates = append(candidates, candidate)
	}
	o.knowledge = intent.Knowledge
	return o, intent, candidates
}

func TestPlaylistCoverageSupportsCollectiveBaselineGenreLists(t *testing.T) {
	for _, genres := range [][]string{{"house", "techno", "uk garage"}, {"bossa nova", "samba", "brazilian jazz"}, {"bluegrass", "country", "americana"}} {
		t.Run(genres[0], func(t *testing.T) {
			var labels [][]string
			for i := range 10 {
				labels = append(labels, []string{genres[i%3]})
			}
			o, intent, candidates := playlistCoverageFixture(genres, 10, labels)
			accepted, report, err := o.filterEnhancedEssential(context.Background(), candidates, intent.EssentialCriteria)
			if err != nil || len(accepted) != 10 || len(playlistCoverageReasons(intent.EssentialCriteria, report.Matched)) != 0 {
				t.Fatalf("coverage rejected: %+v %v", report, err)
			}
			var tracks []core.TrackRef
			for _, candidate := range accepted {
				if tier, _ := o.enhancedTier(context.Background(), candidate, intent); tier != fitStrong {
					t.Fatal("independently supported genre became close")
				}
				tracks = append(tracks, candidate.Track)
			}
			if !o.qualityTargetMet(context.Background(), candidateAssembly{sequence: ports.SequenceResult{Tracks: tracks}}, intent) {
				t.Fatal("complete supported coverage missed quality target")
			}
			for i := range intent.EssentialCriteria {
				intent.EssentialCriteria[i].CoverageGroup = ""
			}
			if tier, _ := o.enhancedTier(context.Background(), candidates[0], intent); tier != fitClose {
				t.Fatal("legacy or explicit per-track conjunction relaxed")
			}
		})
	}
}

func TestPlaylistCoverageReservationAndShortCount(t *testing.T) {
	for _, test := range []struct {
		name        string
		labels      [][]string
		count, want int
		missing     bool
	}{
		{"rare genre", [][]string{{"house"}, {"house"}, {"house"}, {"techno"}, {"garage"}}, 3, 3, false},
		{"one genuine blend", [][]string{{"house"}, {"house", "techno", "garage"}}, 1, 1, false},
		{"short without blend", [][]string{{"house"}, {"techno"}, {"garage"}}, 2, 2, true},
		{"missing genre", [][]string{{"house"}, {"house"}, {"techno"}}, 3, 2, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			o, intent, candidates := playlistCoverageFixture([]string{"house", "techno", "garage"}, test.count, test.labels)
			reserved, _, err := o.reservePlaylistCoverage(context.Background(), candidates, nil, intent)
			if err != nil || len(reserved) != test.want {
				t.Fatalf("reserved=%v err=%v", candidateIDs(reserved), err)
			}
			_, report, _ := o.filterEnhancedEssential(context.Background(), reserved, intent.EssentialCriteria)
			if missing := len(playlistCoverageReasons(intent.EssentialCriteria, report.Matched)) > 0; missing != test.missing {
				t.Fatalf("coverage=%+v", report.Matched)
			}
		})
	}
}

func TestPlaylistCoverageKeepsOrdinaryAlternativesIndependentSetsAndCancellation(t *testing.T) {
	o, intent, candidates := playlistCoverageFixture([]string{"house", "techno", "garage"}, 2, [][]string{{"house"}, {"garage"}})
	intent.EssentialCriteria[0].Group, intent.EssentialCriteria[1].Group = "either", "either"
	_, report, _ := o.filterEnhancedEssential(context.Background(), candidates, intent.EssentialCriteria)
	if len(playlistCoverageReasons(intent.EssentialCriteria, report.Matched)) != 0 {
		t.Fatal("ordinary OR required both alternatives")
	}
	intent.EssentialCriteria[2].CoverageGroup = "second"
	if tier, _ := o.enhancedTier(context.Background(), candidates[0], intent); tier != fitClose {
		t.Fatal("distinct coverage sets collapsed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := o.reservePlaylistCoverage(ctx, candidates, nil, intent); err != context.Canceled {
		t.Fatalf("cancellation=%v", err)
	}
}

func TestPlaylistCoverageRespectsRequiredRecordingsDuplicatesAndJourney(t *testing.T) {
	o, intent, candidates := playlistCoverageFixture([]string{"house", "techno"}, 2, [][]string{{"house"}, {"techno"}})
	reserved, remaining, err := o.reservePlaylistCoverage(context.Background(), candidates, []core.TrackRef{candidates[0].Track}, intent)
	if err != nil || len(reserved) != 1 || reserved[0].Track.ID != "1" || len(remaining) != 0 {
		t.Fatalf("required recording not counted or reselected: %v %v %v", reserved, remaining, err)
	}
	intent.Mode = core.ModeJourney
	intent.EssentialCriteria = append(intent.EssentialCriteria, core.MusicalCriterion{Kind: "genre", Value: "house", Scope: "journey_start"}, core.MusicalCriterion{Kind: "genre", Value: "techno", Scope: "journey_end"})
	assembly := candidateAssembly{sequence: ports.SequenceResult{Tracks: []core.TrackRef{candidates[0].Track, candidates[1].Track}}}
	if !o.qualityTargetMet(context.Background(), assembly, intent) {
		t.Fatal("coverage flattened valid journey stages")
	}
	assembly.sequence.Tracks[0], assembly.sequence.Tracks[1] = assembly.sequence.Tracks[1], assembly.sequence.Tracks[0]
	if o.qualityTargetMet(context.Background(), assembly, intent) {
		t.Fatal("coverage bypassed journey order")
	}
	for i := range candidates {
		candidates[i].Track.RecordingIdentity = "musicbrainz:one-recording"
	}
	reserved, _, err = o.reservePlaylistCoverage(context.Background(), candidates, nil, intent)
	if err != nil || len(reserved) != 1 {
		t.Fatalf("coverage reserved duplicate recording: %v %v", reserved, err)
	}
}

func TestPlaylistCoverageRequiresIndependentEvidence(t *testing.T) {
	o, intent, candidates := playlistCoverageFixture([]string{"house", "techno"}, 2, [][]string{{"house"}, {"techno"}})
	o.knowledge.Tracks[1].Claims[0].Method = "community_tag"
	reserved, _, err := o.reservePlaylistCoverage(context.Background(), candidates, nil, intent)
	if err != nil || len(reserved) != 1 {
		t.Fatal("weak genre tag satisfied coverage")
	}
	_, report, _ := o.filterEnhancedEssential(context.Background(), candidates, intent.EssentialCriteria)
	reasons := playlistCoverageReasons(intent.EssentialCriteria, report.Matched)
	if len(reasons) != 1 || reasons[0].Criterion != "techno" {
		t.Fatalf("missing evidence hidden: %+v", reasons)
	}
}

func TestPlaylistCoverageGroundedSeedsAndVerifiedPolicy(t *testing.T) {
	o, intent, candidates := playlistCoverageFixture([]string{"house", "techno"}, 2, [][]string{{"house"}, {"techno"}})
	grounded, err := o.rankGroundedSeeds(context.Background(), candidates, intent, nil)
	if err != nil || len(grounded) != 2 {
		t.Fatalf("coverage seeds lost: %d %v", len(grounded), err)
	}
	o.bestAvailable = false
	verified, _, err := o.filterEssential(context.Background(), candidates, intent.EssentialCriteria)
	if err != nil || len(verified) != 2 {
		t.Fatalf("verified coverage ignored: %d %v", len(verified), err)
	}
	o.knowledge.Tracks[1].Claims[0].Method = "community_tag"
	verified, _, err = o.filterEssential(context.Background(), candidates, intent.EssentialCriteria)
	if err != nil || len(verified) != 1 {
		t.Fatalf("verified coverage accepted unknown: %d %v", len(verified), err)
	}
}

func TestPlaylistCoverageBuildReportsMissingGenresAndFreezesEvidence(t *testing.T) {
	for _, variant := range []string{"missing", "complete", "excluded"} {
		t.Run(variant, func(t *testing.T) {
			complete := variant != "missing"
			var labels [][]string
			for i := range enhancedMinimumComparisons {
				genre := "house"
				if complete && i == enhancedMinimumComparisons-2 {
					genre = "techno"
				}
				if complete && i == enhancedMinimumComparisons-1 {
					genre = "garage"
				}
				labels = append(labels, []string{genre})
			}
			o, intent, candidates := playlistCoverageFixture([]string{"house", "techno", "garage"}, 10, labels)
			if variant == "excluded" {
				intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_artist", Value: fmt.Sprintf("Artist %d", enhancedMinimumComparisons-1), Supported: true}}
			}
			o.WithCandidateSource(&fixtureDiscovery{})
			o.retriever = &metadataPriorityRetriever{poolRetriever{candidates: candidates, pageSize: enhancedChannelBatch}}
			result, err := o.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent})
			if err != nil || result.Search == nil || result.Search.Validate() != nil {
				t.Fatalf("build/snapshot: %v %+v", err, result.Outcome)
			}
			fulfilled := variant == "complete"
			if (result.Outcome.State == core.OutcomeFulfilled) != fulfilled || (result.Search.StopReason == "quality_target") != fulfilled {
				t.Fatalf("outcome=%+v stop=%s", result.Outcome, result.Search.StopReason)
			}
			missing := 0
			for _, reason := range result.Outcome.Reasons {
				if reason.Code == "playlist_genre_missing" {
					missing++
				}
			}
			wantMissing := map[string]int{"missing": 2, "complete": 0, "excluded": 1}[variant]
			if missing != wantMissing {
				t.Fatalf("missing reasons=%+v", result.Outcome.Reasons)
			}
			if len(result.Tracks) != 10 {
				t.Fatalf("tracks=%d", len(result.Tracks))
			}
			for _, track := range result.Tracks {
				if variant == "excluded" && track.ID == fmt.Sprint(enhancedMinimumComparisons-1) {
					t.Fatal("coverage reservation bypassed artist exclusion")
				}
			}
		})
	}
}
