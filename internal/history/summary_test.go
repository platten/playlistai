package history

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestListSummariesPreservesOrderLimitAndOpaqueHistory(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	if rows, err := s.ListSummaries(ctx, 0); err != nil || len(rows) != 0 {
		t.Fatalf("empty summary list = %v %v", rows, err)
	}
	for _, id := range []string{"a", "z", "b"} {
		_, err := s.Save(ctx, Record{ID: id, CreatedAt: time.Unix(100, 0), Name: "Saved " + id, Prompt: "Original prompt", Notes: "notes", Mode: "journey", TrackCount: 4,
			IntentJSON: []byte("legacy opaque intent"), ResultJSON: []byte("malformed unused result")})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, limit := range []int{0, -1, 1, 2, 50} {
		full, err := s.List(ctx, limit)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.ListSummaries(ctx, limit)
		if err != nil || len(got) != len(full) {
			t.Fatalf("summary limits differ: %+v %v", got, err)
		}
		for i, record := range full {
			want := Summary{ID: record.ID, CreatedAt: record.CreatedAt, Name: record.Name, Prompt: record.Prompt, Notes: record.Notes, Mode: record.Mode, TrackCount: record.TrackCount}
			if !reflect.DeepEqual(got[i], want) {
				t.Fatalf("summary differs: %+v != %+v", got[i], want)
			}
		}
	}
	full, ok, err := s.Get(ctx, "a")
	if err != nil || !ok || string(full.IntentJSON) != "legacy opaque intent" || string(full.ResultJSON) != "malformed unused result" {
		t.Fatalf("listing changed stored payloads: %+v %v", full, err)
	}
}

func BenchmarkHistoryListingEvidence(b *testing.B) {
	s, err := Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	payload := []byte(`{"evidence":"` + strings.Repeat("x", 256*1024) + `"}`)
	for i := range 50 {
		if _, err := s.Save(ctx, Record{ID: fmt.Sprint(i), Name: "Synthetic playlist", ResultJSON: payload}); err != nil {
			b.Fatal(err)
		}
	}
	b.Run("full", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			rows, err := s.List(ctx, 50)
			if err != nil || len(rows) != 50 {
				b.Fatalf("full listing: count=%d %v", len(rows), err)
			}
		}
	})
	b.Run("summaries", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			rows, err := s.ListSummaries(ctx, 50)
			if err != nil || len(rows) != 50 {
				b.Fatalf("summary listing: count=%d %v", len(rows), err)
			}
		}
	})
}
