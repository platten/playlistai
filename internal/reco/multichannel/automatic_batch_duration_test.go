package multichannel

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type automaticDurationMetadataCatalog struct {
	ports.Catalog
	metadata core.TrackMeta
}

func (c automaticDurationMetadataCatalog) Meta(id string) (core.TrackMeta, bool) {
	return c.metadata, id == c.metadata.Ref.ID
}

func TestAutomaticFallbackPreservesOnlyBoundFullDuration(t *testing.T) {
	for _, scenario := range []string{"row", "bare recording", "prefixed recording", "foreign duration", "preview", "invalid", "ambiguous"} {
		t.Run(scenario, func(t *testing.T) {
			ref := core.TrackRef{ID: "local:fixture", Artist: "Artist", Title: "Song", RecordingIdentity: "musicbrainz:recording"}
			duration := &core.RecordingDuration{Milliseconds: 576093, Source: "local:stream", RecordingID: ref.ID}
			switch scenario {
			case "bare recording":
				duration.RecordingID = "recording"
			case "prefixed recording":
				duration.RecordingID = "musicbrainz:recording"
			case "foreign duration":
				duration.RecordingID = "other"
			case "preview":
				duration.Source = "local:PREVIEW"
			case "invalid":
				duration.Milliseconds = 0
			}
			cat := automaticDurationMetadataCatalog{Catalog: testCatalog(), metadata: core.TrackMeta{Ref: ref, MusicBrainzRecording: "recording", FullRecordingDuration: duration}}
			intent := core.MusicIntent{}
			if scenario == "ambiguous" {
				intent.Knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{{Ref: ref, RecordingID: "other", IdentityStatus: core.ResolutionAmbiguous}}}
			}
			a := NewAutomatic(cat, nil, nil, DefaultConfig())
			b, err := a.prepareBatch(context.Background(), cat, []core.TrackRef{ref}, intent)
			if err != nil {
				t.Fatal(err)
			}
			got := b.recordings[ref.ID]
			want := scenario == "row" || scenario == "bare recording" || scenario == "prefixed recording"
			if got.FullRecordingDuration.Valid() != want {
				t.Fatalf("duration=%+v want=%v", got.FullRecordingDuration, want)
			}
			if scenario == "ambiguous" && got.IdentityStatus != core.ResolutionAmbiguous {
				t.Fatal("ambiguity cleared")
			}
			if want {
				if got.FullRecordingDuration == duration {
					t.Fatal("shared pointer")
				}
				got.FullRecordingDuration.Milliseconds = 1
				if duration.Milliseconds != 576093 {
					t.Fatal("source duration mutated")
				}
			}
		})
	}
}
