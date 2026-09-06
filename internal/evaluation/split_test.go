package evaluation

import (
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
)

func TestTemporalSplitAndProfileEventsPreventLeakage(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := make([]RecommendationCase, 5)
	for i := range cases {
		cases[i] = RecommendationCase{ID: string(rune('a' + i)), OccurredAt: base.Add(time.Duration(i) * 24 * time.Hour)}
	}
	split, err := TemporalSplitCases(cases)
	if err != nil {
		t.Fatal(err)
	}
	if split.Assignments["a"] != SplitTrain || split.Assignments["d"] != SplitDevelopment || split.Assignments["e"] != SplitTest {
		t.Fatalf("assignments=%v", split.Assignments)
	}
	records := []InteractionRecord{
		{ListenerID: "listener", Event: eventAt("train", base.Add(2*time.Hour))},
		{ListenerID: "listener", Event: eventAt("dev", base.Add(2*24*time.Hour+12*time.Hour))},
		{ListenerID: "listener", Event: eventAt("future", base.Add(5*24*time.Hour))},
		{ListenerID: "other", Event: eventAt("other", base)}}
	dev := ProfileEvents(records, "listener", cases[3].OccurredAt, SplitDevelopment, split)
	if len(dev) != 1 || dev[0].Event.ID != "train" {
		t.Fatalf("dev profile leaked: %+v", dev)
	}
	test := ProfileEvents(records, "listener", cases[4].OccurredAt, SplitTest, split)
	if len(test) != 2 {
		t.Fatalf("test profile should use pre-test train+dev only: %+v", test)
	}
}

func eventAt(id string, at time.Time) core.FeedbackEvent {
	return core.FeedbackEvent{Version: 1, ID: id, OccurredAt: at, Type: core.FeedbackLike, Scope: core.FeedbackScopeDurable, TrackID: "track"}
}
