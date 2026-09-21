package localcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
)

type recordingMetadataBase struct {
	testBase
	meta core.TrackMeta
}

func (b recordingMetadataBase) Meta(id string) (core.TrackMeta, bool) {
	return b.meta, id == b.meta.Ref.ID
}

func TestNestedMetadataMergesSparseAliasesAndRetainsConflicts(t *testing.T) {
	const recording = "12345678-1234-1234-1234-123456789abc"
	base := recordingMetadataBase{meta: core.TrackMeta{Ref: core.TrackRef{ID: "outside", Artist: "Artist", Title: "Recording", RecordingIdentity: "musicbrainz:" + recording}, MusicBrainzRecording: recording}}
	ctx := context.Background()
	intent := core.MusicIntent{EssentialCriteria: []core.MusicalCriterion{{Kind: "instrumentation", Value: "piano"}, {Kind: "genre", Value: "ambient"}}}
	for _, conflict := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("conflict=%v/reverse=%v", conflict, reverse), func(t *testing.T) {
				tags := []string{`{"originaldate":"1994","fbpm":"120.25","mood_happy":"50","genre":"ambient"}`, `{"instrument":"piano"}`}
				if conflict {
					tags[1] = `{"instrument":"piano","originaldate":"2004","fbpm":"140","mood_happy":"-50"}`
					tags = append(tags, `{"originaldate":"1994","fbpm":"120.25","mood_happy":"50"}`)
				}
				if reverse {
					for i, j := 0, len(tags)-1; i < j; i, j = i+1, j-1 {
						tags[i], tags[j] = tags[j], tags[i]
					}
				}
				var composite *CompositeCatalog
				var ids []string
				for i, raw := range tags {
					local, manager := openTestCatalog(t, []librarypack.Track{{ID: "copy", Artist: "Artist", Title: "Recording", MusicBrainzRecording: recording, RawTags: json.RawMessage(raw)}}, nil)
					t.Cleanup(func() { local.Close(); manager.Close() })
					local.prefix = fmt.Sprintf("local:source%d:", i)
					ids = append(ids, local.NamespacedID("copy"))
					if composite == nil {
						composite = &CompositeCatalog{base: base, local: local, mode: ModeCombined}
					} else {
						composite = &CompositeCatalog{base: composite, local: local, mode: ModeCombined}
					}
				}
				ids = append(ids, "outside")
				for _, id := range ids {
					metadata, ok, err := composite.LibraryRecordingMetadata(ctx, id)
					if err != nil || !ok {
						t.Fatalf("%s metadata: %v %v", id, ok, err)
					}
					features, ok := composite.LibraryTrackFeatures(ctx, id)
					if !ok {
						t.Fatalf("%s features missing", id)
					}
					if conflict {
						if metadata.OriginalReleaseDate != "" || features.OriginalYear != nil || features.TempoBPM != nil {
							t.Fatalf("%s conflict resurrected: %+v %+v", id, metadata, features)
						}
						if _, known := features.Descriptors["acousticbrainz:mood_happy"]; known {
							t.Fatal("descriptor conflict resurrected")
						}
					} else if metadata.OriginalReleaseDate != "1994" || features.TempoBPM == nil || *features.TempoBPM != 120.25 {
						t.Fatalf("%s sparse alias hid facts: %+v %+v", id, metadata, features)
					}
					score, ok := composite.LibraryPreferenceScore(ctx, id, intent, "playlist")
					if !ok || score != 1 {
						t.Fatalf("%s complementary evidence lost: %g %v", id, score, ok)
					}
				}
			})
		}
	}
}

func TestNestedMetadataDoesNotJoinNamesOrCrossLibraryOnly(t *testing.T) {
	const recording = "12345678-1234-1234-1234-123456789abc"
	shared, sharedManager := openTestCatalog(t, []librarypack.Track{{ID: "shared", Artist: "Artist", Title: "Recording", MusicBrainzRecording: recording, RawTags: json.RawMessage(`{"originaldate":"1994"}`)}}, nil)
	defer sharedManager.Close()
	defer shared.Close()
	shared.prefix = "pack:shared:"
	inner := &CompositeCatalog{base: testBase{}, local: shared, mode: ModeCombined}
	personal, personalManager := openTestCatalog(t, []librarypack.Track{{ID: "unidentified", Artist: "Artist", Title: "Recording"}, {ID: "identified", Artist: "Artist", Title: "Recording", MusicBrainzRecording: recording}}, nil)
	defer personalManager.Close()
	defer personal.Close()
	outer := &CompositeCatalog{base: inner, local: personal, mode: ModeCombined}
	m, _, err := outer.LibraryRecordingMetadata(context.Background(), personal.NamespacedID("unidentified"))
	if err != nil || m.OriginalReleaseDate != "" {
		t.Fatal("name-only join borrowed date", m, err)
	}
	outer.mode = ModeLibraryOnly
	m, _, err = outer.LibraryRecordingMetadata(context.Background(), personal.NamespacedID("identified"))
	if err != nil || m.OriginalReleaseDate != "" {
		t.Fatal("library-only reached outside evidence", m, err)
	}
}

func TestPackedRecordingMetadataAndUnknownPreference(t *testing.T) {
	local, manager := openTestCatalog(t, []librarypack.Track{
		{ID: "known", Artist: "Artist", Title: "Piano", RawTags: json.RawMessage(`{"instrument":"piano; cello","originaldate":"1994","date":"2024","fbpm":"120.5","key":"Db minor"}`)},
		{ID: "unknown", Artist: "Artist", Title: "Unknown", RawTags: json.RawMessage(`{"comment":"piano","date":"2024"}`)},
	}, nil)
	defer manager.Close()
	defer local.Close()
	c := &CompositeCatalog{base: testBase{}, local: local, mode: ModeCombined}
	ctx := context.Background()
	m, ok, err := c.LibraryRecordingMetadata(ctx, local.NamespacedID("known"))
	if err != nil || !ok || m.OriginalReleaseDate != "1994" || m.ReleaseEditionDate != "2024" || m.Year != 2024 {
		t.Fatalf("metadata %+v %v %v", m, ok, err)
	}
	f, ok := c.LibraryTrackFeatures(ctx, local.NamespacedID("known"))
	if !ok || f.TempoBPM == nil || *f.TempoBPM != 120.5 || f.Key != "c# minor" {
		t.Fatalf("typed features %+v", f)
	}
	intent := core.MusicIntent{EssentialCriteria: []core.MusicalCriterion{{Kind: "instrumentation", Value: "piano"}, {Kind: "genre", Value: "ambient"}}}
	score, available := c.LibraryPreferenceScore(ctx, local.NamespacedID("known"), intent, "playlist")
	if !available || score != .5 {
		t.Fatalf("sparse denominator: %v %v", score, available)
	}
	if _, available := c.LibraryPreferenceScore(ctx, local.NamespacedID("unknown"), intent, "playlist"); available {
		t.Fatal("administrative or absent evidence scored")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := c.LibraryRecordingMetadata(ctx, local.NamespacedID("known")); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestCompoundGenreSupportRequiresTwoSourcedTagsOnSameRecording(t *testing.T) {
	local, manager := openTestCatalog(t, []librarypack.Track{
		{ID: "both", Artist: "A", Title: "One", RawTags: json.RawMessage(`{"AB:GENRE":"Ambient; Electronic"}`)},
		{ID: "ambient", Artist: "B", Title: "Two", RawTags: json.RawMessage(`{"AB:GENRE":"Ambient"}`)},
		{ID: "electronic", Artist: "C", Title: "Three", RawTags: json.RawMessage(`{"AB:GENRE":"Electronic"}`)},
		{ID: "title", Artist: "D", Title: "Ambient electronic", RawTags: json.RawMessage(`{"comment":"ambient electronic"}`)},
	}, nil)
	defer manager.Close()
	defer local.Close()
	catalog := &CompositeCatalog{base: testBase{}, local: local, mode: ModeCombined}
	criterion := core.MusicalCriterion{Kind: "genre", Value: "ambient electronica"}
	intent := core.MusicIntent{EssentialCriteria: []core.MusicalCriterion{criterion}}
	ctx := context.Background()
	for _, id := range []string{"ambient", "electronic", "title"} {
		if catalog.CompoundGenreSupport(ctx, local.NamespacedID(id), criterion) {
			t.Fatalf("unsupported %q admitted", id)
		}
	}
	id := local.NamespacedID("both")
	if !catalog.CompoundGenreSupport(ctx, id, criterion) {
		t.Fatal("same-recording reviewed tags lost")
	}
	if got := catalog.CriterionEvidence(ctx, id, criterion); got == core.EvidenceMatch {
		t.Fatal("partial support was promoted to categorical proof")
	}
	if score, ok := catalog.LibraryPreferenceScore(ctx, id, intent, "playlist"); !ok || score != .75 {
		t.Fatalf("partial score=%g available=%v", score, ok)
	}
}
