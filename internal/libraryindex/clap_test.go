package libraryindex

import "testing"

func TestCLAPWindowsUseTwoDistributedTenSecondExcerpts(t *testing.T) {
	tests := []struct {
		duration float64
		want     []SampleWindow
	}{
		{8, []SampleWindow{{Index: 0, Duration: 8}}},
		{15, []SampleWindow{{Index: 0, Duration: 7.5}, {Index: 1, Start: 7.5, Duration: 7.5}}},
		{25, []SampleWindow{{Index: 0, Duration: 10}, {Index: 1, Start: 15, Duration: 10}}},
		{60, []SampleWindow{{Index: 0, Start: 15, Duration: 10}, {Index: 1, Start: 35, Duration: 10}}},
	}
	for _, test := range tests {
		got, err := CLAPWindows(test.duration)
		if err != nil || len(got) != len(test.want) {
			t.Fatalf("duration %v: windows=%v err=%v", test.duration, got, err)
		}
		for i := range got {
			if got[i] != test.want[i] {
				t.Fatalf("duration %v window %d = %+v, want %+v", test.duration, i, got[i], test.want[i])
			}
			if got[i].Duration > 10 {
				t.Fatalf("duration %v exceeds CLAP input", got[i].Duration)
			}
			if i > 0 && got[i].Start < got[i-1].Start+got[i-1].Duration {
				t.Fatalf("windows overlap: %v", got)
			}
		}
	}
}

func TestCLAPWindowsRejectInvalidDuration(t *testing.T) {
	if _, err := CLAPWindows(0); err == nil {
		t.Fatal("expected invalid duration")
	}
}
