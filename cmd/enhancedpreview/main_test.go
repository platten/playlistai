package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestAuthorizationAndBudgetBeforeIO(t *testing.T) {
	for _, args := range [][]string{{"--bundle", "missing", "--catalog", "missing", "--track-ids", "a", "--output", "missing"}, {"--authorized", "--bundle", "missing", "--catalog", "missing", "--track-ids", "a", "--output", "missing", "--limit", "25"}} {
		if err := run(args, &bytes.Buffer{}); err == nil {
			t.Fatal("invalid acquisition accepted")
		}
	}
	ids, err := validateArgs(true, "bundle", "catalog", " a,b,a ", "out", 2, 0)
	if err != nil || !reflect.DeepEqual(ids, []string{"a", "b"}) {
		t.Fatal(ids, err)
	}
	if _, err = validateArgs(true, "b", "c", "a,b,c", "o", 2, 0); err == nil {
		t.Fatal("budget bypass")
	}
}
func TestCohortWritesOnlyDerivedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cohort.json")
	data := cohort{Version: 1, Name: "test", Tracks: []cohortTrack{{ID: "a", RecordingID: "recording", MERT: []float64{1, 0}}}}
	if err := writeJSON(path, data); err != nil {
		t.Fatal(err)
	}
	data.Name = "updated"
	if err := writeJSON(path, data); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(raw, []byte("updated")) {
		t.Fatal(err)
	}
	if _, err = os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("staged artifact retained")
	}
}

type fakeResolver struct{}

func (fakeResolver) ResolveAudioPreview(context.Context, core.TrackRef, core.EnrichedTrack) (core.ResolvedAudioPreview, error) {
	return core.ResolvedAudioPreview{Identity: core.PreviewIdentity{Status: core.ResolutionAmbiguous, Provider: "deezer"}, URL: "https://example.invalid/private-preview"}, nil
}
func TestCaptureStoresIdentityWithoutPreviewURL(t *testing.T) {
	r := identityResolver{resolver: fakeResolver{}}
	_, err := r.ResolveAudioPreview(context.Background(), core.TrackRef{}, core.EnrichedTrack{})
	if err != nil || r.last.Status != core.ResolutionAmbiguous {
		t.Fatal(err)
	}
}
