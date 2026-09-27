package multichannel

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type durationVerifierFixture struct {
	calls, searches int
	update          func(context.Context, core.EnrichedTrack) (core.EnrichedTrack, error)
}

func (*durationVerifierFixture) Name() string { return "fixture" }
func (f *durationVerifierFixture) Enrich(_ context.Context, refs []core.TrackRef, _ ports.Progress) ([]core.EnrichedTrack, error) {
	f.searches++
	return nil, nil
}
func (f *durationVerifierFixture) VerifyRecording(ctx context.Context, t core.EnrichedTrack, _ []core.MusicalCriterion) (core.EnrichedTrack, error) {
	f.calls++
	if f.update != nil {
		return f.update(ctx, t)
	}
	return t, nil
}
func durationBatchFixture(count int) *automaticBatch {
	b := newAutomaticBatch()
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("local:duration:%d", i)
		ref := core.TrackRef{ID: id, Artist: "Artist", Title: "Song"}
		b.ids = append(b.ids, id)
		b.meta[id] = core.TrackMeta{Ref: ref}
		b.recordings[id] = core.EnrichedTrack{Ref: ref, RecordingID: fmt.Sprintf("recording-%d", i), IdentityStatus: core.ResolutionResolved, Matched: true, FullRecordingDuration: &core.RecordingDuration{Milliseconds: 576093, Source: "local:stream", RecordingID: id}}
	}
	return b
}
func TestAutomaticDurationConflictRejectsOnlySameIdentity(t *testing.T) {
	for _, different := range []string{"", "ref", "recording"} {
		t.Run("different-"+different, func(t *testing.T) {
			b := durationBatchFixture(1)
			id := b.ids[0]
			prior := automaticCopy(b.recordings[id])
			f := &durationVerifierFixture{update: func(_ context.Context, row core.EnrichedTrack) (core.EnrichedTrack, error) {
				row.IdentityStatus = core.ResolutionAmbiguous
				row.Claims = []core.RecordingClaim{{Kind: "genre", Value: "jazz"}}
				if different == "ref" {
					row.Ref.ID = "other"
				}
				if different == "recording" {
					row.RecordingID = "other"
				}
				return row, nil
			}}
			a := &AutomaticEngine{enricher: f}
			if err := a.acquireAutomaticEvidence(context.Background(), testCatalog(), b, core.MusicIntent{}, "", nil); err != nil {
				t.Fatal(err)
			}
			if f.calls != 1 {
				t.Fatal(f.calls)
			}
			if different != "" {
				if !reflect.DeepEqual(prior, b.recordings[id]) {
					t.Fatal("unrelated result changed identity")
				}
				return
			}
			got := b.recordings[id]
			if got.IdentityStatus != core.ResolutionAmbiguous || got.Matched || len(got.Claims) != 0 || automaticFacts(b, got.Ref, core.MusicIntent{}, "playlist") {
				t.Fatal("contradiction admitted", got)
			}
		})
	}
}
func TestAutomaticDurationMissingSkipsAndCancellationKeepsBaseline(t *testing.T) {
	b := durationBatchFixture(1)
	b.recordings[b.ids[0]] = core.EnrichedTrack{Ref: b.meta[b.ids[0]].Ref, RecordingID: "recording", IdentityStatus: core.ResolutionResolved}
	f := &durationVerifierFixture{}
	a := &AutomaticEngine{enricher: f}
	if err := a.acquireAutomaticEvidence(context.Background(), testCatalog(), b, core.MusicIntent{}, "", nil); err != nil || f.calls != 0 {
		t.Fatalf("missing duration err=%v calls=%d", err, f.calls)
	}
	b = durationBatchFixture(1)
	id := b.ids[0]
	prior := automaticCopy(b.recordings[id])
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.update = func(_ context.Context, row core.EnrichedTrack) (core.EnrichedTrack, error) {
		cancel()
		return row, context.Canceled
	}
	err := a.acquireAutomaticEvidence(ctx, testCatalog(), b, core.MusicIntent{}, "", nil)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(prior, b.recordings[id]) {
		t.Fatal("cancel changed baseline", err)
	}
}
func TestAutomaticDurationAndSearchShareAcquisitionLimit(t *testing.T) {
	b := durationBatchFixture(40)
	// Half of the first32 opportunities are ordinary missing-recording searches.
	for i, id := range b.ids {
		if i%2 == 0 {
			newID := fmt.Sprintf("remote:%d", i)
			m := b.meta[id]
			m.Ref.ID = newID
			delete(b.meta, id)
			delete(b.recordings, id)
			b.ids[i] = newID
			b.meta[newID] = m
			b.recordings[newID] = core.EnrichedTrack{Ref: m.Ref}
		}
	}
	f := &durationVerifierFixture{}
	a := &AutomaticEngine{enricher: f}
	if err := a.acquireAutomaticEvidence(context.Background(), testCatalog(), b, core.MusicIntent{}, "", nil); err != nil {
		t.Fatal(err)
	}
	if f.calls != 16 || f.searches != 16 {
		t.Fatalf("shared cap: verifies=%d searches=%d", f.calls, f.searches)
	}
}

func TestAutomaticDurationConflictingISRCMarksIdentityAmbiguous(t *testing.T) {
	b := durationBatchFixture(1)
	id := b.ids[0]
	m := b.meta[id]
	m.ISRC = "USAAA0100001"
	b.meta[id] = m
	prior := b.recordings[id]
	f := &durationVerifierFixture{update: func(_ context.Context, row core.EnrichedTrack) (core.EnrichedTrack, error) {
		row.ISRC = "USAAA0100002"
		return row, nil
	}}
	a := &AutomaticEngine{enricher: f}
	if err := a.acquireAutomaticEvidence(context.Background(), testCatalog(), b, core.MusicIntent{}, "", nil); err != nil {
		t.Fatal(err)
	}
	got := b.recordings[id]
	if got.Ref.ID != prior.Ref.ID || got.RecordingID != prior.RecordingID || got.IdentityStatus != core.ResolutionAmbiguous || got.Matched || len(got.Claims) != 0 || automaticFacts(b, got.Ref, core.MusicIntent{}, "playlist") {
		t.Fatal("same-recording ISRC contradiction admitted", got)
	}
}
