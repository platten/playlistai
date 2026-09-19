package libraryindex

import "testing"

func TestStageTraceBoundsAndChronologicalSnapshot(t *testing.T) {
	if NewStageTrace(0) != nil {
		t.Fatal("trace enabled by default")
	}
	trace := NewStageTrace(3)
	for i := 0; i < 10; i++ {
		trace.Ready(1)
	}
	events, dropped := trace.Snapshot()
	if len(events) != 3 || dropped != 7 {
		t.Fatalf("events=%d dropped=%d", len(events), dropped)
	}
	for i, event := range events {
		if event.ReadyWindows != i+8 || event.Stage != "ready_windows" {
			t.Fatalf("event=%+v", event)
		}
	}
	events[0].Stage = "mutated"
	again, _ := trace.Snapshot()
	if again[0].Stage != "ready_windows" {
		t.Fatal("snapshot aliases ring")
	}
}
