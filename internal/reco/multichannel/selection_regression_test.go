package multichannel

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

func TestEnhancedLocalPoolDiversifiesBeforeFirstN(t *testing.T) {
	_, intent := localPriorityFixture()
	intent.Count, intent.Controls.TotalTrackCount, intent.Controls.ArtistDiversity = 4, 4, 1
	var rows []fakes.CatalogTrack
	var ids []string
	for i := range 12 {
		artist := "Concentrated"
		if i >= 8 {
			artist = fmt.Sprintf("Alternative %d", i)
		}
		id := fmt.Sprintf("pack:fixture:%02d", i)
		rows = append(rows, fakes.CatalogTrack{ID: id, Display: artist + " - " + id})
		ids = append(ids, id)
	}
	cat := metadataPriorityCatalog{fakes.NewCatalog(2, rows...)}
	run := func(intent core.MusicIntent) core.Playlist {
		t.Helper()
		r := &metadataPriorityRetriever{poolRetriever{candidates: candidatesForTracks(refs(cat, ids...))}}
		s := &metadataPriorityStream{block: true}
		o := New(cat, fakes.NewSimilarityEngine(cat.Catalog), cat, DefaultConfig()).WithCandidateSource(s)
		o.retriever = r
		pl, err := o.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent})
		if err != nil || len(pl.Tracks) != 4 || pl.Outcome.State != core.OutcomeFulfilled || s.pulls != 0 {
			t.Fatalf("result=%+v pulls=%d err=%v", pl, s.pulls, err)
		}
		artists := map[string]bool{}
		for _, track := range pl.Tracks {
			artists[track.Artist] = true
		}
		if len(artists) != 4 {
			t.Fatalf("first-N acceptance discarded diverse alternatives: %v", pl.Tracks)
		}
		return pl
	}
	fresh := run(intent)
	replayed := run(fresh.Intent)
	if !reflect.DeepEqual(fresh.Tracks, replayed.Tracks) {
		t.Fatalf("saved generation changed: %v != %v", fresh.Tracks, replayed.Tracks)
	}
}

func TestEnhancedProviderPoolUsesDiversityShortlist(t *testing.T) {
	_, intent := localPriorityFixture()
	intent.Count, intent.Controls.TotalTrackCount, intent.Controls.ArtistDiversity = 3, 3, 1
	cat := metadataPriorityCatalog{fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "a1", Display: "A - First"},
		fakes.CatalogTrack{ID: "a2", Display: "A - Second"},
		fakes.CatalogTrack{ID: "a3", Display: "A - Third"},
		fakes.CatalogTrack{ID: "b", Display: "B - Song"},
		fakes.CatalogTrack{ID: "c", Display: "C - Song"},
		fakes.CatalogTrack{ID: "d", Display: "D - Song"},
	)}
	s := &fixtureDiscovery{tracks: refs(cat, "a1", "a2", "a3", "b", "c", "d")}
	o := New(cat, fakes.NewSimilarityEngine(cat.Catalog), cat, DefaultConfig()).WithCandidateSource(s)
	o.retriever = &poolRetriever{}
	pl, err := o.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent})
	if err != nil || len(pl.Tracks) != 3 || s.pulls != 6 {
		t.Fatalf("tracks=%v pulls=%d err=%v", pl.Tracks, s.pulls, err)
	}
	artists := map[string]bool{}
	for _, track := range pl.Tracks {
		artists[track.Artist] = true
	}
	if len(artists) != 3 {
		t.Fatalf("provider first N monopolized output: %v", pl.Tracks)
	}
}

func TestEnhancedAllFailingInitialPoolHandsOffAfterBoundedChecks(t *testing.T) {
	// The raw retrieval union can be much larger than the analysis shortlist.
	// Every cached preview deliberately fails; no network/model work is used.
	cat, service, retriever := recommendationPoolFixture(t, DefaultConfig().MaxCandidates, DefaultConfig().MaxCandidates)
	intent := poolIntent(3)
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	stop := make(chan struct{})
	session, err := service.BeginWithBudget(context.Background(), intent, cat.CatalogVersion(), stop, iterativeBudget)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	wantChecks := recommendationPoolSize(intent.Count, 0)
	stream := &metadataPriorityStream{before: func() {
		if got := len(session.Snapshot().Assessments); got != wantChecks {
			t.Errorf("initial raw union consumed %d audio checks before provider handoff; want bounded %d", got, wantChecks)
		}
		close(stop)
	}, block: true}
	o := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig())
	o.enhanced, o.bestAvailable, o.audioSession = true, true, session
	o.retriever = retriever
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got, _, err := o.collectIteratively(ctx, retriever.candidates, stream, intent, ports.RecommendationRequest{Intent: intent, StopChecking: stop}, newEligibility(intent, nil, nil), nil, nil, nil, 42)
	if err != nil || len(got) != 0 || stream.pulls != 1 || len(session.Snapshot().Assessments) != wantChecks {
		t.Fatalf("unbounded failing queue: accepted=%d checks=%d pulls=%d err=%v", len(got), len(session.Snapshot().Assessments), stream.pulls, err)
	}
	if len(retriever.calls) != 0 {
		t.Fatal("stop at bounded provider handoff unexpectedly refilled candidates")
	}
}

func TestIncludeOtherArtistsReservesEligibleOutputAndReportsMissing(t *testing.T) {
	for _, other := range []bool{true, false} {
		t.Run(fmt.Sprint(other), func(t *testing.T) {
			_, intent := localPriorityFixture()
			intent.Count, intent.Controls.TotalTrackCount = 2, 2
			intent.Constraints.ExcludeSeedArtists = false
			intent.References = []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Radiohead", Influence: core.InfluencePositive}}
			intent.HardConstraints = []core.HardConstraint{{Kind: core.HardConstraintIncludeOtherArtists, Value: "true"}}
			cat := metadataPriorityCatalog{fakes.NewCatalog(2,
				fakes.CatalogTrack{ID: "a0", Display: "Radiohead - Reference"},
				fakes.CatalogTrack{ID: "a1", Display: "Radiohead - First"},
				fakes.CatalogTrack{ID: "a2", Display: "Radiohead - Second"},
				fakes.CatalogTrack{ID: "b", Display: "Portishead - Song"},
			)}
			intent.References[0].Resolution = &core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: cat.CatalogVersion(), Selected: &core.ResolutionCandidate{Kind: core.ReferenceArtist, Artist: "Radiohead", EntityID: "radiohead", Representatives: []core.WeightedTrack{{TrackID: "a0", Weight: 1}}}}
			ids := []string{"a1", "a2"}
			if other {
				ids = append(ids, "b")
			}
			o := New(cat, fakes.NewSimilarityEngine(cat.Catalog), cat, DefaultConfig()).WithCandidateSource(&metadataPriorityStream{})
			o.retriever = &metadataPriorityRetriever{poolRetriever{candidates: candidatesForTracks(refs(cat, ids...))}}
			pl, err := o.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent})
			if err != nil || len(pl.Tracks) != 2 {
				t.Fatalf("tracks=%v outcome=%+v err=%v", pl.Tracks, pl.Outcome, err)
			}
			if other {
				if pl.Outcome.State != core.OutcomeFulfilled || !includesOtherArtists(cat, pl.Intent, pl.Tracks) {
					t.Fatalf("explicit requirement bypassed: %+v", pl)
				}
			} else {
				if pl.Outcome.State != core.OutcomePartial {
					t.Fatalf("count falsely implies fulfillment: %+v", pl.Outcome)
				}
				found := false
				for _, reason := range pl.Outcome.Reasons {
					found = found || reason.Code == "other_artists_missing"
				}
				if !found {
					t.Fatalf("missing honest explanation: %+v", pl.Outcome)
				}
			}
		})
	}
}

func TestPerformerDiversityUsesKnownCreditsAndGroundedAliases(t *testing.T) {
	intent := testIntent(2)
	intent.Controls.RecommendationMode, intent.Controls.ArtistDiversity = core.EnhancedHybrid, 1
	intent.References = []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Alias", Resolution: &core.ReferenceResolution{Status: core.ResolutionResolved, Selected: &core.ResolutionCandidate{Artist: "Classemotion", Evidence: []core.ResolutionEvidence{{Match: "alias", MatchedText: "Alias"}}}}}}
	cat := fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "solo", Display: "Classemotion - One"},
		fakes.CatalogTrack{ID: "joint", Display: "Classemotion & Angela Mosley - Two"},
		fakes.CatalogTrack{ID: "alias", Display: "Alias - Three"},
		fakes.CatalogTrack{ID: "other", Display: "Other - Four"},
	)
	candidates := []core.Candidate{selectionCandidate(cat, "solo", 1), selectionCandidate(cat, "joint", .99), selectionCandidate(cat, "alias", .99), selectionCandidate(cat, "other", .95)}
	selected, err := NewSelector(cat, DefaultConfig()).Select(context.Background(), candidates, ports.SelectionRequest{Intent: intent, Count: 2})
	if err != nil || candidateIDs(selected.Candidates) != "solo,other" {
		t.Fatalf("shared performers escaped concentration: %v err=%v", selected, err)
	}
	keys := newPerformerKeys(intent, []core.TrackRef{{Artist: "Earth, Wind & Fire"}})
	if got := keys.keys("Earth, Wind & Fire"); len(got) != 1 {
		t.Fatalf("invented band members: %v", got)
	}
}

func TestReciprocalFusionDuplicateQueryIsNotIndependentEvidence(t *testing.T) {
	base := []core.RetrievalEvidence{{Channel: "metadata", QueryID: "genre", Rank: 1, QueryWeight: 1}, {Channel: "seed_audio", QueryID: "reference-2", Rank: 2, QueryWeight: 1}}
	want := reciprocalRankFusion(base, 60)
	duplicate := append(append([]core.RetrievalEvidence(nil), base...), base...)
	duplicate = append(duplicate, core.RetrievalEvidence{Channel: "metadata", QueryID: "genre", Rank: 20, QueryWeight: 1})
	if got := reciprocalRankFusion(duplicate, 60); got != want {
		t.Fatalf("duplicate files boosted query evidence: %.12f != %.12f", got, want)
	}
	a := core.RetrievalEvidence{Channel: "library_audio", QueryID: "reference", Rank: 1, QueryWeight: 1, LibrarySource: &core.LibraryEvidenceSource{PackID: "pack", SpaceID: "mert-v1", Generation: "one", Scope: "full"}}
	copyInAnotherPack := a
	copyInAnotherPack.LibrarySource = &core.LibraryEvidenceSource{PackID: "other-pack", SpaceID: "mert-v1", Generation: "other-generation", Scope: "full"}
	if got := reciprocalRankFusion([]core.RetrievalEvidence{a, copyInAnotherPack}, 60); got != 1.0/61 {
		t.Fatalf("repeated pack evidence bought extra votes: %v", got)
	}
	b := a
	b.LibrarySource = &core.LibraryEvidenceSource{PackID: "pack", SpaceID: "mert-v2", Generation: "two", Scope: "full"}
	if got := reciprocalRankFusion([]core.RetrievalEvidence{a, a, b}, 60); got != 2.0/61 {
		t.Fatalf("independent representation contracts collapsed: %v", got)
	}
}

func TestOtherArtistsAndArtistOnlyConflictNeedsClarification(t *testing.T) {
	cat := artistRequestCatalog()
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, VerificationPolicy: core.BestAvailable, Mode: core.ModeSimilar, Count: 2,
		References:      []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Artist 0", Influence: core.InfluencePositive}},
		HardConstraints: []core.HardConstraint{{Kind: "require_artist", Value: "Artist 0"}, {Kind: core.HardConstraintIncludeOtherArtists, Value: "true"}},
		Controls:        core.IntentControls{TotalTrackCount: 2, RecommendationMode: core.EnhancedHybrid}}
	pl, err := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || pl.Outcome.State != core.OutcomeNeedsClarification || len(pl.Tracks) != 0 {
		t.Fatalf("contradiction silently relaxed: outcome=%+v tracks=%v err=%v", pl.Outcome, pl.Tracks, err)
	}
}

func TestIndependentReferencesReceiveRetrievalOpportunities(t *testing.T) {
	cat := fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "reference-a", Display: "A - Reference", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "reference-b", Display: "B - Reference", Audio: []float32{0, 1}, Track: []float32{0, 1}},
		fakes.CatalogTrack{ID: "neighbor-a", Display: "C - Similar A", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "neighbor-b", Display: "D - Similar B", Audio: []float32{0, 1}, Track: []float32{0, 1}},
	)
	intent := testIntent(2)
	intent.References = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "reference-a", Influence: core.InfluencePositive}, {Kind: core.ReferenceTrack, TrackID: "reference-b", Influence: core.InfluencePositive}}
	intent.Controls.Discovery = 0
	cfg := DefaultConfig()
	cfg.SeedAudioBudget, cfg.SeedCooccurrenceBudget = 1, 1
	pool, err := NewRetriever(cat, fakes.NewSimilarityEngine(cat), cfg).Retrieve(context.Background(), ports.RetrievalRequest{Intent: intent})
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"a", "b"} {
		found := false
		for _, candidate := range pool {
			if candidate.Track.ID != "neighbor-"+suffix {
				continue
			}
			for _, source := range candidate.Sources {
				found = found || source.Channel == ChannelSeedAudio && strings.HasPrefix(source.QueryID, "reference-"+suffix+":")
			}
		}
		if !found {
			t.Fatalf("reference %s received no independent retrieval opportunity: %+v", suffix, pool)
		}
	}
	if len(intent.RequiredTracks) != 0 {
		t.Fatal("retrieval references became mandatory output tracks")
	}
}
