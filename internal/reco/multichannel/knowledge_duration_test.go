package multichannel

import (
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestKnowledgeMergesOnlySameRecordingDuration(t *testing.T) {
	for _, defect := range []string{"", "ref", "recording", "isrc", "durationBinding", "preview", "ambiguous"} {
		t.Run("defect-"+defect, func(t *testing.T) {
			base := core.EnrichedTrack{Ref: core.TrackRef{ID: "local:file"}, RecordingID: "recording", ISRC: "USAAA0100001", IdentityStatus: core.ResolutionResolved}
			local := base
			local.FullRecordingDuration = &core.RecordingDuration{Milliseconds: 576093, Source: "local:stream", RecordingID: "local:file"}
			switch defect {
			case "ref":
				local.Ref.ID = "other"
			case "recording":
				local.RecordingID = "other"
			case "isrc":
				local.ISRC = "USAAA0100002"
			case "durationBinding":
				local.FullRecordingDuration.RecordingID = "other"
			case "preview":
				local.FullRecordingDuration.Source = "local:PREVIEW"
			case "ambiguous":
				base.IdentityStatus = core.ResolutionAmbiguous
			}
			got := mergeRecordingMetadata(base, local)
			want := defect == "" || defect == "ambiguous"
			if got.FullRecordingDuration.Valid() != want {
				t.Fatalf("duration=%+v", got.FullRecordingDuration)
			}
			if got.IdentityStatus != base.IdentityStatus {
				t.Fatal("ambiguity changed")
			}
			if want {
				if got.FullRecordingDuration == local.FullRecordingDuration {
					t.Fatal("shared pointer")
				}
				got.FullRecordingDuration.Milliseconds = 1
				if local.FullRecordingDuration.Milliseconds != 576093 {
					t.Fatal("mutated source")
				}
			}
		})
	}
}
