package history

import (
	"context"
	"testing"
	"time"
)

func TestSaveListGetDelete(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()

	a, err := s.Save(ctx, Record{Name: "Like Bonobo", Prompt: "something like bonobo", Mode: "similar", TrackCount: 12})
	if err != nil {
		t.Fatalf("Save a: %v", err)
	}
	if a.ID == "" || a.CreatedAt.IsZero() {
		t.Fatalf("Save must fill ID and CreatedAt: %+v", a)
	}

	// Force b to be newer so ordering is unambiguous.
	b, err := s.Save(ctx, Record{
		Name: "Justice → Kavinsky", Prompt: "justice into kavinsky", Mode: "journey", TrackCount: 30,
		CreatedAt:   a.CreatedAt.Add(time.Second),
		IntentJSON:  []byte(`{"mode":"journey"}`),
		RequestJSON: []byte(`{"count":30}`),
		TracksJSON:  []byte(`[{"id":"x"}]`),
	})
	if err != nil {
		t.Fatalf("Save b: %v", err)
	}

	list, err := s.List(ctx, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].ID != b.ID || list[1].ID != a.ID {
		t.Fatalf("List order wrong: %+v", list)
	}
	if string(list[0].IntentJSON) != `{"mode":"journey"}` || string(list[0].TracksJSON) != `[{"id":"x"}]` {
		t.Fatalf("blobs not round-tripped: %+v", list[0])
	}
	if string(list[1].IntentJSON) != "{}" || string(list[1].TracksJSON) != "[]" {
		t.Fatalf("empty blobs should default: %+v", list[1])
	}

	got, ok, err := s.Get(ctx, a.ID)
	if err != nil || !ok {
		t.Fatalf("Get a: ok=%v err=%v", ok, err)
	}
	if got.Prompt != "something like bonobo" || got.TrackCount != 12 {
		t.Fatalf("Get a mismatch: %+v", got)
	}

	if _, ok, _ := s.Get(ctx, "nope"); ok {
		t.Fatal("Get of unknown id should be ok=false")
	}

	if err := s.Delete(ctx, a.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, _ := s.Get(ctx, a.ID); ok {
		t.Fatal("record should be gone after Delete")
	}
	if err := s.Delete(ctx, "already-gone"); err != nil {
		t.Fatalf("Delete of missing id must not error: %v", err)
	}
}

func TestReopenPersists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	s1, err := Open(dir)
	if err != nil {
		t.Fatalf("Open 1: %v", err)
	}
	if _, err := s1.Save(context.Background(), Record{Name: "n", Prompt: "p"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	_ = s1.Close()

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open 2: %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	list, err := s2.List(context.Background(), 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("reopened store lost data: len=%d err=%v", len(list), err)
	}
}
