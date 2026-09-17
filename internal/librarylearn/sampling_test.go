package librarylearn

import (
	"reflect"
	"slices"
	"testing"
)

func TestDiverseSampleStableAndDeduplicated(t *testing.T) {
	items := []SampleItem{
		{ID: "a1", ArtistID: "a", GroupID: "same"},
		{ID: "a2", ArtistID: "a", GroupID: "a2"},
		{ID: "b1", ArtistID: "b", GroupID: "b1"},
		{ID: "c1", ArtistID: "c", GroupID: "c1"},
		{ID: "duplicate-master", ArtistID: "z", GroupID: "same"},
	}
	want := DiverseSample(items, 4, 42)
	for range 5 {
		slices.Reverse(items)
		if got := DiverseSample(items, 4, 42); !reflect.DeepEqual(got, want) {
			t.Fatalf("sample changed with input order: %v != %v", got, want)
		}
	}
	if len(want) != 4 {
		t.Fatalf("sample size=%d: %v", len(want), want)
	}
	seenGroup := 0
	for _, id := range want {
		if id == "a1" || id == "duplicate-master" {
			seenGroup++
		}
	}
	if seenGroup != 1 {
		t.Fatalf("probable recording group represented %d times: %v", seenGroup, want)
	}
}
