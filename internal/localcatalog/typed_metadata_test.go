package localcatalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
)

func TestTypedMetadataPreservesZeroPrecisionAndConflicts(t *testing.T) {
	got := NormalizeMusicalMetadata(annotations(json.RawMessage(`{"bpm":"120","fbpm":"120.25","mood_happy":"0","mood_arousal":"-63","originaldate":"1994-02-01","date":"2024","key":"D flat minor","tkey":"C#min"}`)))
	if got.BPM == nil || *got.BPM != 120.25 || got.OriginalYear == nil || *got.OriginalYear != 1994 || got.EditionYear == nil || *got.EditionYear != 2024 || got.Key != "c# minor" {
		t.Fatalf("lost typed values: %+v", got)
	}
	if n, ok := got.Descriptors["acousticbrainz:mood_happy"]; !ok || n != 0 {
		t.Fatal("zero became missing")
	}
	if got.Descriptors["acousticbrainz:mood_arousal"] != -63 {
		t.Fatal("signed descriptor lost")
	}
	got = NormalizeMusicalMetadata(annotations(json.RawMessage(`{"bpm":"120","fbpm":"141.2","originaldate":"1994","originalyear":"2004","key":"C","tkey":"D","mood_happy":"NaN","mood_sad":"101"}`)))
	if got.BPM != nil || got.OriginalYear != nil || got.Key != "" || !got.Conflicts["tempo"] || !got.Conflicts["original_release_date"] || len(got.Descriptors) != 0 {
		t.Fatalf("conflicts or invalid values accepted: %+v", got)
	}
}

func TestMusicalTagListsAndSearchExcludeAdministrativeValues(t *testing.T) {
	raw := json.RawMessage(`{"instrument":"piano; violoncello", "genre":"ambient; classical", "artist":"AC/DC; literal", "comment":"secretpiano", "encoder":"ambientencoder"}`)
	a := annotations(raw)
	for _, criterion := range []core.MusicalCriterion{{Kind: "instrumentation", Value: "cello"}, {Kind: "instrumentation", Value: "piano"}, {Kind: "genre", Value: "ambient"}} {
		matched := false
		for _, item := range a {
			matched = matched || annotationMatches(item, criterion)
		}
		if !matched {
			t.Fatalf("list/alias not recognized: %+v in %+v", criterion, a)
		}
	}
	for _, item := range a {
		if item.Kind == "artist_credit" && item.Value != "AC/DC; literal" {
			t.Fatal("identity split")
		}
	}
	c, manager := openTestCatalog(t, []librarypack.Track{{ID: "tagged", Artist: "Artist", Title: "Track", RawTags: raw}}, nil)
	defer manager.Close()
	defer c.Close()
	for _, text := range []string{"secretpiano", "ambientencoder"} {
		hits, err := c.Search(context.Background(), MetadataQuery{Text: text, Limit: 5})
		if err != nil || len(hits) != 0 {
			t.Fatalf("administrative value searchable %s: %+v %v", text, hits, err)
		}
	}
	criterion := core.MusicalCriterion{Kind: "instrumentation", Value: "cello"}
	hits, err := c.Search(context.Background(), MetadataQuery{Text: "cello", Criterion: &criterion, Limit: 5})
	if err != nil || len(hits) != 1 {
		t.Fatalf("canonical alias failed lexical gate: %+v %v", hits, err)
	}
	if got := c.CriterionEvidence(context.Background(), hits[0].Track.ID, criterion); got != core.EvidenceMatch {
		t.Fatal(got)
	}
	criterion.Value = "violin"
	if got := c.CriterionEvidence(context.Background(), hits[0].Track.ID, criterion); got != core.EvidenceUnknown {
		t.Fatalf("absence proved mismatch: %s", got)
	}
}

func TestTypedSearchUsesReviewedParentMembership(t *testing.T) {
	c, manager := openTestCatalog(t, []librarypack.Track{{ID: "child", Artist: "Artist", Title: "Track", RawTags: json.RawMessage(`{"genre":"synthpop"}`)}}, nil)
	defer manager.Close()
	defer c.Close()
	criterion := core.MusicalCriterion{Kind: "genre", Value: "pop"}
	hits, err := c.Search(context.Background(), MetadataQuery{Text: "pop", Criterion: &criterion, Limit: 5})
	if err != nil || len(hits) != 1 {
		t.Fatalf("parent posting unavailable: %+v %v", hits, err)
	}
}

func TestOpenBuildsMissingDerivativeAndRejectsCorruptManifest(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "corrupt"}[corrupt], func(t *testing.T) {
			root := t.TempDir()
			pack := filepath.Join(root, "test.paipack")
			writeTestPack(t, pack, "one", testTracks())
			manager, err := librarypack.OpenManager(context.Background(), filepath.Join(root, "manager"), librarypack.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			staged, err := manager.Stage(context.Background(), pack)
			if err != nil {
				t.Fatal(err)
			}
			if corrupt {
				indexDir := filepath.Join(staged.Generation().Directory(), derivedIndexDir)
				if err := os.MkdirAll(indexDir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(indexDir, "manifest.json"), []byte(`{"version":0}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := manager.Activate(context.Background(), staged); err != nil {
				t.Fatal(err)
			}
			lease, err := manager.Pin()
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := Open(lease, Options{SourceID: "test"})
			if corrupt {
				if err == nil {
					catalog.Close()
					t.Fatal("corrupt derivative rebuilt")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer catalog.Close()
			hits, err := catalog.Search(context.Background(), MetadataQuery{Text: "Halo", Limit: 5})
			if err != nil || len(hits) != 1 {
				t.Fatalf("upgraded index unavailable: %+v %v", hits, err)
			}
		})
	}
}

func TestDSPStagePreferencesDoNotLeakAcrossJourney(t *testing.T) {
	intent := core.MusicIntent{Preferences: core.SemanticPreferences{TextureDescriptions: []core.IntentPreference{
		{ConceptID: "texture.bass-heavy", Scope: "journey_start", Influence: core.InfluencePositive},
		{ConceptID: "texture.bass-heavy", Scope: "journey_end", Influence: core.InfluenceNegative},
	}}}
	if len(reviewedDSPPreferences(intent)) != 0 {
		t.Fatal("stage preference leaked globally")
	}
	start := reviewedDSPPreferencesForScope(intent, "journey_start")
	end := reviewedDSPPreferencesForScope(intent, "journey_end")
	if len(start) != 1 || len(end) != 1 || start[0].direction != -end[0].direction {
		t.Fatalf("stage targeting lost: %+v %+v", start, end)
	}
}
