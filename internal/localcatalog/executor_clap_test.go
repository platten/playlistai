package localcatalog

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestExecutorMergesMetadataMERTAndCLAPDeterministically(t *testing.T) {
	composite, _ := openSemanticCatalog(t, true)
	catalog := composite.local
	executor, err := NewExecutor(catalog, 2)
	if err != nil {
		t.Fatal(err)
	}
	seed := catalog.NamespacedID("pooled")
	query := Query{
		Metadata: &MetadataQuery{Text: "Music", Limit: 10},
		MERT:     &NeighborQuery{SeedID: seed, Limit: 10},
		CLAP:     &NeighborQuery{SeedID: seed, Limit: 10},
	}
	first, err := executor.Query(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	channels := map[string]bool{}
	for _, candidate := range first.Candidates {
		for _, evidence := range candidate.Evidence {
			channels[evidence.Channel] = true
		}
	}
	for _, channel := range []string{MetadataChannel, MERTChannel, CLAPChannel} {
		if !channels[channel] {
			t.Fatalf("channel %s lost from all-channel execution: %+v", channel, first)
		}
	}
	second, err := executor.Query(context.Background(), query)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("nondeterministic channel merge: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := executor.Query(ctx, query); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled all-channel query=%v", err)
	}
}
