package multichannel

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

type schedulingVerifier struct {
	enriched []string
	verified []string
	resolve  func(context.Context, core.TrackRef) (core.EnrichedTrack, error)
}

func (*schedulingVerifier) Name() string { return "offline scheduling fixture" }

func (v *schedulingVerifier) Enrich(ctx context.Context, refs []core.TrackRef, _ ports.Progress) ([]core.EnrichedTrack, error) {
	v.enriched = append(v.enriched, refs[0].ID)
	track := core.EnrichedTrack{Ref: refs[0], IdentityStatus: core.ResolutionUnresolved}
	if v.resolve != nil {
		var err error
		track, err = v.resolve(ctx, refs[0])
		return []core.EnrichedTrack{track}, err
	}
	return []core.EnrichedTrack{track}, ctx.Err()
}

func (v *schedulingVerifier) VerifyRecording(ctx context.Context, track core.EnrichedTrack, _ []core.MusicalCriterion) (core.EnrichedTrack, error) {
	v.verified = append(v.verified, track.Ref.ID)
	// Independent fixture ground truth, acquired only after identity succeeds.
	track.Claims = []core.RecordingClaim{{Kind: "genre", Value: "house", State: core.EvidenceMatch,
		Scope: "recording", EntityID: track.RecordingID, RecordingID: track.RecordingID,
		Source:  core.ContextSource{Provider: "fixture", URL: "https://label.example/recording", Revision: "fixture-v1"},
		Locator: "genre", Method: "linked_statement"}}
	return track, ctx.Err()
}

func verificationSchedulingFixture() (*Orchestrator, core.MusicIntent, *schedulingVerifier) {
	var rows []fakes.CatalogTrack
	recordings := map[string]string{}
	for i := range recordingVerificationLimit + 1 {
		for _, prefix := range []string{"unknown", "pinned"} {
			id := fmt.Sprintf("%s-%d", prefix, i)
			rows = append(rows, fakes.CatalogTrack{ID: id, Display: "Artist - " + id})
			if prefix == "pinned" {
				recordings[id] = fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)
			}
		}
	}
	base := fakes.NewCatalog(2, rows...)
	catalog := verifierIntegrationCatalog{Catalog: base, recordings: recordings}
	o := New(catalog, nil, base, DefaultConfig())
	o.enhanced, o.bestAvailable, o.requestContext = true, true, context.Background()
	v := &schedulingVerifier{}
	o.WithRecordingVerifier(v)
	intent := enhancedIntent(1)
	intent.References = nil
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "house", Scope: "playlist"}}
	return o, intent, v
}

func TestVerificationSchedulingRetainsLaterPinnedOpportunity(t *testing.T) {
	o, intent, v := verificationSchedulingFixture()
	for range 2 {
		for i := range recordingVerificationLimit {
			if err := o.verifyRecording(context.Background(), refs(o.cat, fmt.Sprintf("unknown-%d", i))[0], intent); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(v.enriched) != recordingVerificationLimit/2 || len(v.verified) != 0 {
		t.Fatalf("repeated/deferred identity misses acquired again or reached verifier: enrich=%d verify=%d", len(v.enriched), len(v.verified))
	}
	for i := range recordingVerificationLimit {
		if err := o.verifyRecording(context.Background(), refs(o.cat, fmt.Sprintf("pinned-%d", i))[0], intent); err != nil {
			t.Fatal(err)
		}
	}
	if len(o.verificationAttempted) != recordingVerificationLimit || len(v.verified) != recordingVerificationLimit/2 {
		t.Fatalf("ceiling or reserved opportunity lost: attempts=%d verified=%d", len(o.verificationAttempted), len(v.verified))
	}
	if !o.confirmedGenres(context.Background(), "pinned-0", intent, "") {
		t.Fatal("later pinned recording lost its independently supported genre")
	}
}

func TestVerificationSchedulingRequiredBypassesOnlyOptionalSubquota(t *testing.T) {
	o, intent, v := verificationSchedulingFixture()
	for i := range recordingVerificationLimit / 2 {
		if err := o.verifyRecording(context.Background(), refs(o.cat, fmt.Sprintf("unknown-%d", i))[0], intent); err != nil {
			t.Fatal(err)
		}
	}
	v.resolve = func(_ context.Context, ref core.TrackRef) (core.EnrichedTrack, error) {
		return citedGenreFixture(ref), nil
	}
	for i := recordingVerificationLimit / 2; i <= recordingVerificationLimit; i++ {
		if err := o.verifyRequiredRecording(context.Background(), refs(o.cat, fmt.Sprintf("unknown-%d", i))[0], intent); err != nil {
			t.Fatal(err)
		}
	}
	if len(o.verificationAttempted) != recordingVerificationLimit || len(v.enriched) != recordingVerificationLimit || len(v.verified) != recordingVerificationLimit/2 {
		t.Fatalf("required subquota bypass changed total bound: attempts=%d enrich=%d verify=%d", len(o.verificationAttempted), len(v.enriched), len(v.verified))
	}
}

func TestVerificationSchedulingDefersWithoutBlockingRequiredRetry(t *testing.T) {
	o, intent, v := verificationSchedulingFixture()
	o.optionalIdentityAttempts = recordingVerificationLimit / 2
	track := refs(o.cat, "unknown-0")[0]
	for range 3 {
		if err := o.verifyRecording(context.Background(), track, intent); err != nil {
			t.Fatal(err)
		}
	}
	if len(v.enriched) != 0 || len(o.verificationAttempted) != 0 {
		t.Fatal("deferral consumed an attempt or repeated resolution")
	}
	v.resolve = func(_ context.Context, ref core.TrackRef) (core.EnrichedTrack, error) {
		return citedGenreFixture(ref), nil
	}
	if err := o.verifyRequiredRecording(context.Background(), track, intent); err != nil || len(v.verified) != 1 {
		t.Fatalf("deferred optional candidate blocked required acquisition: %v verified=%d", err, len(v.verified))
	}
}

func TestVerificationSchedulingConflictsDoNotSpendAcquisitionSlots(t *testing.T) {
	o, intent, v := verificationSchedulingFixture()
	track := refs(o.cat, "pinned-0")[0]
	o.knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{{Ref: track, RecordingID: "conflicting-recording", IdentityStatus: core.ResolutionResolved}}}
	if err := o.verifyRecording(context.Background(), track, intent); err != nil {
		t.Fatal(err)
	}
	if len(o.verificationAttempted) != 0 || len(v.enriched) != 0 || len(v.verified) != 0 {
		t.Fatal("known identity conflict consumed acquisition or bypassed its gate")
	}
}

func TestVerificationSchedulingCancellationStopAndReplay(t *testing.T) {
	for _, mode := range []string{"canceled", "stopped", "replay", "stop during enrichment", "cancel during enrichment"} {
		t.Run(mode, func(t *testing.T) {
			o, intent, v := verificationSchedulingFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stop := make(chan struct{})
			o.verificationStop = stop
			switch mode {
			case "canceled":
				cancel()
			case "stopped":
				close(stop)
			case "replay":
				o.replayingEvidence = true
			case "stop during enrichment", "cancel during enrichment":
				v.resolve = func(child context.Context, _ core.TrackRef) (core.EnrichedTrack, error) {
					if mode == "cancel during enrichment" {
						cancel()
					} else {
						close(stop)
					}
					select {
					case <-child.Done():
						return core.EnrichedTrack{}, child.Err()
					case <-time.After(time.Second):
						t.Fatal("stop did not cancel optional identity lookup")
						return core.EnrichedTrack{}, nil
					}
				}
			}
			err := o.verifyRecording(ctx, refs(o.cat, "unknown-0")[0], intent)
			canceled := mode == "canceled" || mode == "cancel during enrichment"
			if canceled && !errors.Is(err, context.Canceled) || !canceled && err != nil {
				t.Fatal(err)
			}
			wantAttempts := 0
			if mode == "stop during enrichment" || mode == "cancel during enrichment" {
				wantAttempts = 1
			}
			if len(o.verificationAttempted) != wantAttempts || len(v.enriched) != wantAttempts || len(v.verified) != 0 {
				t.Fatal("inactive or unresolved operation dispatched recording verification")
			}
		})
	}
}

func TestVerificationSchedulingResetsForEachBuild(t *testing.T) {
	o, intent, candidates := playlistCoverageFixture([]string{"house"}, 1, [][]string{{"house"}})
	intent.Knowledge.Tracks[0].Claims = nil
	intent.Knowledge.Tracks[0].RecordingID = ""
	intent.Knowledge.Tracks[0].IdentityStatus = core.ResolutionUnresolved
	o.retriever = &poolRetriever{candidates: candidates, pageSize: 1}
	o.optionalIdentityAttempts = recordingVerificationLimit / 2
	o.verificationAttempted = map[string]bool{candidates[0].Track.ID: true}
	v := &schedulingVerifier{resolve: func(_ context.Context, ref core.TrackRef) (core.EnrichedTrack, error) {
		return citedGenreFixture(ref), nil
	}}
	o.WithRecordingVerifier(v)
	for i := range 2 {
		result, err := o.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent})
		if err != nil || len(result.Tracks) != 1 || len(v.enriched) != i+1 || len(v.verified) != i+1 {
			t.Fatalf("generation inherited acquisition state: tracks=%v enrich=%d verify=%d err=%v", result.IDs(), len(v.enriched), len(v.verified), err)
		}
	}
}
