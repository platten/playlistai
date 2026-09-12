package multichannel

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/ports"
)

type durationCatalog struct {
	*fakes.Catalog
	durations map[string]*core.RecordingDuration
}

func (c *durationCatalog) Meta(id string) (core.TrackMeta, bool) {
	meta, ok := c.Catalog.Meta(id)
	meta.FullRecordingDuration = c.durations[id]
	return meta, ok
}

func durationFixture(count int, milliseconds int64) (*durationCatalog, *Orchestrator, core.MusicIntent) {
	tracks := []fakes.CatalogTrack{{ID: "seed", Display: "Seed Artist - Origin", Audio: []float32{1, 0}, Track: []float32{1, 0}}}
	durations := map[string]*core.RecordingDuration{"seed": {Milliseconds: milliseconds, Source: "test/full-recording", RecordingID: "recording-seed"}}
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("track-%02d", i)
		tracks = append(tracks, fakes.CatalogTrack{ID: id, Display: fmt.Sprintf("Artist %02d - Title %02d", i, i), Audio: []float32{1, 0}, Track: []float32{1, 0}})
		durations[id] = &core.RecordingDuration{Milliseconds: milliseconds, Source: "test/full-recording", RecordingID: "recording-" + id}
	}
	cat := &durationCatalog{Catalog: fakes.NewCatalog(2, tracks...), durations: durations}
	engine := New(cat, fakes.NewSimilarityEngine(cat.Catalog), cat, DefaultConfig())
	intent := testIntent(20)
	intent.DurationSeconds = 4500
	source := lexicon.Extract("One hour and fifteen minutes of music.")
	intent.Translation = &source
	return cat, engine, intent.Normalized()
}

func TestDurationOnlySelectsEighteenTracksInsteadOfDefaultTwenty(t *testing.T) {
	_, engine, intent := durationFixture(20, 250000)
	playlist, err := engine.Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if len(playlist.Tracks) != 18 || playlist.Duration == nil || playlist.Duration.KnownMilliseconds != 4500000 || playlist.Duration.State != core.EvidenceMatch || playlist.Outcome.State != core.OutcomeFulfilled {
		t.Fatalf("duration target was not honored: count=%d duration=%+v outcome=%+v", len(playlist.Tracks), playlist.Duration, playlist.Outcome)
	}
	if playlist.Intent.Count != 20 || playlist.Intent.Controls.TotalTrackCount != 20 || playlist.Intent.HasExplicitTrackCount() {
		t.Fatal("duration fitting rewrote the saved default into an explicit count")
	}
	for _, notice := range playlist.Notices {
		if notice.Code == "eligible_tracks_exhausted" || notice.Code == "duration_unsupported" {
			t.Fatalf("fulfilled duration received a count/unknown warning: %+v", notice)
		}
	}
	again, err := engine.Build(context.Background(), playlist.Intent)
	if err != nil || !reflect.DeepEqual(playlist.Tracks, again.Tracks) {
		t.Fatal("duration selection is not deterministic", err)
	}
	encoded, err := json.Marshal(playlist)
	if err != nil {
		t.Fatal(err)
	}
	var restored core.Playlist
	if err := json.Unmarshal(encoded, &restored); err != nil || !reflect.DeepEqual(playlist.Duration, restored.Duration) || restored.Intent.Normalized().DurationTolerance() != 60 {
		t.Fatal("saved duration evidence or tolerance changed", err)
	}
}

func TestDurationPreservesExplicitCountWhenBothCannotFit(t *testing.T) {
	_, engine, intent := durationFixture(20, 250000)
	intent.TrackCountExplicit = true
	playlist, err := engine.Build(context.Background(), intent)
	if err != nil || len(playlist.Tracks) != 20 || playlist.Outcome.State != core.OutcomePartial || playlist.Duration.State != core.EvidenceMismatch {
		t.Fatalf("explicit count was silently relaxed: %d %+v %v", len(playlist.Tracks), playlist.Outcome, err)
	}
	if playlist.Duration.KnownMilliseconds != 5000000 || playlist.Duration.ToleranceSeconds != 60 {
		t.Fatalf("duration evidence was fabricated: %+v", playlist.Duration)
	}
}

func TestDurationContinuationWorksWhenRequiredTracksFillDefaultCount(t *testing.T) {
	for _, mandatory := range []int{20, 21} {
		t.Run(fmt.Sprint(mandatory), func(t *testing.T) {
			_, engine, intent := durationFixture(35, 150000)
			engine.WithCandidateSource(&fixtureDiscovery{}) // configured discovery, no external stream
			for i := 0; i < mandatory; i++ {
				intent.RequiredTracks = append(intent.RequiredTracks, core.IntentReference{Kind: core.ReferenceTrack, TrackID: fmt.Sprintf("track-%02d", i), Influence: core.InfluencePositive})
			}
			playlist, err := engine.Build(context.Background(), intent)
			if err != nil || len(playlist.Tracks) != 30 || playlist.Duration == nil || playlist.Duration.State != core.EvidenceMatch {
				t.Fatalf("duration continuation stopped at default count: %d %+v %v", len(playlist.Tracks), playlist.Outcome, err)
			}
			seen := map[string]bool{}
			for _, track := range playlist.Tracks {
				seen[track.ID] = true
			}
			for _, required := range intent.RequiredTracks {
				if !seen[required.TrackID] {
					t.Fatalf("required recording removed: %s", required.TrackID)
				}
			}
			if playlist.Intent.Count != 20 || playlist.Intent.HasExplicitTrackCount() {
				t.Fatal("working retrieval batch changed saved count semantics")
			}
		})
	}
}

func TestDurationToleranceUsesExactMillisecondsAndInclusiveBounds(t *testing.T) {
	for _, tc := range []struct {
		ms    int64
		state core.EvidenceState
	}{{4440000, core.EvidenceMatch}, {4560000, core.EvidenceMatch}, {4439999, core.EvidenceMismatch}, {4560001, core.EvidenceMismatch}} {
		cat, engine, intent := durationFixture(1, tc.ms)
		got := engine.assessDuration(refs(cat, "track-00"), intent)
		if got.State != tc.state {
			t.Fatalf("%dms: state=%s want %s", tc.ms, got.State, tc.state)
		}
	}
}

func TestUnknownOrSparseDurationEvidencePreservesMusicalResult(t *testing.T) {
	for _, known := range []int{0, 1, 5} {
		t.Run(fmt.Sprint(known), func(t *testing.T) {
			cat, engine, intent := durationFixture(20, 250000)
			for i := known; i < 20; i++ {
				delete(cat.durations, fmt.Sprintf("track-%02d", i))
			}
			playlist, err := engine.Build(context.Background(), intent)
			if err != nil || len(playlist.Tracks) != 20 || playlist.Duration.State != core.EvidenceUnknown || playlist.Outcome.State != core.OutcomePartial {
				t.Fatalf("missing metadata collapsed musical result: %d %+v %v", len(playlist.Tracks), playlist.Duration, err)
			}
			if len(playlist.Duration.UnknownTrackIDs) != 20-known {
				t.Fatalf("missing duration was treated as known: %+v", playlist.Duration)
			}
		})
	}
}

func TestDurationKeepsRequiredEndpointsExclusionsAndRecordingDeduplication(t *testing.T) {
	cat, engine, intent := durationFixture(25, 250000)
	start := core.IntentReference{Kind: core.ReferenceTrack, TrackID: "seed", Influence: core.InfluencePositive}
	end := core.IntentReference{Kind: core.ReferenceTrack, TrackID: "track-24", Influence: core.InfluencePositive}
	intent.Mode, intent.Start, intent.Destination = core.ModeJourney, &start, &end
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_artist", Value: "Artist 04", Supported: true}}
	playlist, err := engine.Build(context.Background(), intent)
	if err != nil || len(playlist.Tracks) != 18 || playlist.Duration.State != core.EvidenceMatch {
		t.Fatalf("duration journey failed: %d %+v %v", len(playlist.Tracks), playlist.Outcome, err)
	}
	if playlist.Tracks[0].ID != "seed" || playlist.Tracks[len(playlist.Tracks)-1].ID != "track-24" {
		t.Fatal("duration fitting changed endpoints")
	}
	seen := map[string]bool{}
	for _, track := range playlist.Tracks {
		key := core.ProvisionalRecordingKey(track)
		if track.ID == "track-04" || seen[key] {
			t.Fatal("exclusion or recording uniqueness was lost")
		}
		seen[key] = true
	}
	// A cached complete assembly must still reject a changed duration target.
	engine.assemblyCache = &completedAssembly{}
	candidates := candidatesForTracks(refs(cat, "track-00", "track-01"))
	intent = testIntent(2)
	intent.DurationSeconds, intent.TrackCountExplicit = 500, true
	a, err := engine.assembleCandidates(context.Background(), candidates, intent, ports.RecommendationRequest{Intent: intent}, nil, nil, nil, 42)
	if err != nil || !a.complete(2) {
		t.Fatal("valid duration assembly rejected", err)
	}
	intent.DurationSeconds = 700
	b, err := engine.assembleCandidates(context.Background(), candidates, intent, ports.RecommendationRequest{Intent: intent}, nil, nil, nil, 42)
	if err != nil || b.complete(2) {
		t.Fatal("stale duration assembly reused", err)
	}
}

func TestDurationRejectsPreviewAndAmbiguousIdentityEvidence(t *testing.T) {
	cat, engine, intent := durationFixture(1, 250000)
	delete(cat.durations, "track-00")
	seconds := 4500.0
	track := core.EnrichedTrack{Ref: refs(cat, "track-00")[0], Matched: true, IdentityStatus: core.ResolutionResolved, RecordingID: "recording-track-00", Acoustic: &core.AcousticCharacteristics{Low: &core.AcousticMeasurements{AnalyzedSeconds: &seconds}}}
	engine.knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{track}}
	if got := engine.assessDuration([]core.TrackRef{track.Ref}, intent); got.State != core.EvidenceUnknown {
		t.Fatal("analyzed audio length became full-recording duration")
	}
	track.FullRecordingDuration = &core.RecordingDuration{Milliseconds: 4500000, Source: "musicbrainz", RecordingID: "different-recording"}
	engine.knowledge.Tracks[0] = track
	if got := engine.assessDuration([]core.TrackRef{track.Ref}, intent); got.State != core.EvidenceUnknown {
		t.Fatal("wrong recording identity supplied duration")
	}
	track.FullRecordingDuration.RecordingID = track.RecordingID
	track.IdentityStatus = core.ResolutionAmbiguous
	engine.knowledge.Tracks[0] = track
	if got := engine.assessDuration([]core.TrackRef{track.Ref}, intent); got.State != core.EvidenceUnknown {
		t.Fatal("ambiguous recording supplied duration")
	}
}
