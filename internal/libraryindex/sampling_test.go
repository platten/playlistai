package libraryindex

import (
	"math"
	"testing"
)

func TestBalancedSamplingCentersAndUniqueCoverage(t *testing.T) {
	windows, err := SamplingWindows(100, ProfileBalanced)
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{7.5, 23.5, 39.5, 55.5, 71.5, 87.5}
	if len(windows) != len(want) {
		t.Fatalf("windows=%+v", windows)
	}
	for i := range want {
		if math.Abs(windows[i].Start-want[i]) > 1e-9 || math.Abs(windows[i].Duration-5) > 1e-9 || windows[i].Index != i {
			t.Fatalf("window[%d]=%+v want start %v", i, windows[i], want[i])
		}
	}
	short, err := SamplingWindows(12, ProfileBalanced)
	if err != nil {
		t.Fatal(err)
	}
	var covered float64
	for i, window := range short {
		if i > 0 && window.Start < short[i-1].Start+short[i-1].Duration-1e-9 {
			t.Fatalf("overlap counted twice: %+v", short)
		}
		covered += window.Duration
	}
	if covered > 12+1e-9 {
		t.Fatalf("coverage exceeds source duration: %v", covered)
	}
}

func TestShortSamplingUsesObservedAudioOnly(t *testing.T) {
	windows, err := SamplingWindows(1.25, ProfileDeep)
	if err != nil || len(windows) != 1 || windows[0].Duration != 1.25 {
		t.Fatalf("windows=%+v err=%v", windows, err)
	}
}
