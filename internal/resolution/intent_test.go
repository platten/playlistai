package resolution

import (
	"errors"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

type testResolver struct {
	calls  int
	result core.ReferenceResolution
}

func (r *testResolver) CatalogVersion() string { return "catalog-v2" }
func (r *testResolver) ResolveReference(core.IntentReference) core.ReferenceResolution {
	r.calls++
	return r.result
}

func TestApplyResolvesAllReferenceRoles(t *testing.T) {
	r := &testResolver{result: core.ReferenceResolution{Status: core.ResolutionUnresolved, CatalogVersion: "catalog-v2"}}
	ref := core.IntentReference{Kind: core.ReferenceArtist, Query: "Unknown", Influence: core.InfluencePositive}
	intent := core.MusicIntent{Version: 8, References: []core.IntentReference{ref}, RequiredTracks: []core.IntentReference{{Kind: core.ReferenceTrack, Query: "Required"}}, Journey: core.JourneyPlan{Waypoints: []core.IntentReference{ref}}, InferredAnchors: []core.InferredAnchor{{Reference: ref, Role: "starting-point"}}}
	got, issues := Apply(r, intent)
	if r.calls != 4 || len(issues) != 4 {
		t.Fatalf("calls=%d issues=%+v", r.calls, issues)
	}
	if !issues[2].Required || !issues[3].Inferred || issues[3].Role != "starting-point" {
		t.Fatalf("lost role: %+v", issues)
	}
	if got.References[0].Resolution == nil || got.RequiredTracks[0].Resolution == nil || got.Journey.Waypoints[0].Resolution == nil || got.InferredAnchors[0].Reference.Resolution == nil {
		t.Fatal("missing resolution")
	}
	if intent.References[0].Resolution != nil {
		t.Fatal("mutated original intent")
	}
}

func TestResolutionCacheAndRepresentative(t *testing.T) {
	selected := &core.ResolutionCandidate{Representatives: []core.WeightedTrack{{TrackID: "real-track", Weight: 1}}}
	for _, tc := range []struct {
		name, version string
		kind          core.ReferenceKind
		status        core.ResolutionStatus
		selected      *core.ResolutionCandidate
		calls         int
		id            string
		issue         bool
	}{
		{"cached", "catalog-v2", core.ReferenceArtist, core.ResolutionResolved, selected, 0, "real-track", false},
		{"stale", "old", core.ReferenceArtist, core.ResolutionResolved, selected, 1, "fresh-track", false},
		{"missing selection", "catalog-v2", core.ReferenceArtist, core.ResolutionResolved, nil, 1, "fresh-track", false},
		{"unresolved", "catalog-v2", core.ReferenceArtist, core.ResolutionUnresolved, nil, 1, "fresh-track", false},
		{"album cached failure", "catalog-v2", core.ReferenceAlbum, core.ResolutionUnresolved, nil, 0, "", true},
		{"empty representatives", "catalog-v2", core.ReferenceArtist, core.ResolutionResolved, &core.ResolutionCandidate{}, 0, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &testResolver{result: core.ReferenceResolution{Status: core.ResolutionResolved, Selected: &core.ResolutionCandidate{Representatives: []core.WeightedTrack{{TrackID: "fresh-track"}}}}}
			refs, issues := applyList(r, []core.IntentReference{{Kind: tc.kind, TrackID: "stale-id", Resolution: &core.ReferenceResolution{CatalogVersion: tc.version, Status: tc.status, Selected: tc.selected}}}, false, nil)
			if r.calls != tc.calls || refs[0].TrackID != tc.id || (len(issues) > 0) != tc.issue {
				t.Fatalf("calls=%d refs=%+v issues=%+v", r.calls, refs, issues)
			}
		})
	}
}

func TestBlockingErrors(t *testing.T) {
	for _, tc := range []struct {
		issue    Issue
		want     error
		contains string
	}{
		{Issue{Status: core.ResolutionAmbiguous, Query: "  Electronic  ", Alternatives: []core.ResolutionCandidate{{Artist: "A"}, {Artist: "B", Title: "Song"}}}, core.ErrAmbiguousReference, `"B - Song"`},
		{Issue{Status: core.ResolutionAmbiguous, Kind: core.ReferenceArtist}, core.ErrAmbiguousReference, `"artist"`},
		{Issue{Status: core.ResolutionUnresolved, Required: true, Query: "Song"}, core.ErrRequiredTrackConflict, "Song"},
		{Issue{Status: core.ResolutionAmbiguous, Inferred: true}, nil, ""},
		{Issue{Status: core.ResolutionUnresolved}, nil, ""},
	} {
		err := BlockingError([]Issue{tc.issue})
		if !errors.Is(err, tc.want) || (err != nil && !strings.Contains(err.Error(), tc.contains)) {
			t.Fatalf("%+v: %v", tc.issue, err)
		}
	}
	if err := BlockingError(nil); err != nil {
		t.Fatal(err)
	}
}
