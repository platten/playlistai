package localcatalog

import (
	"encoding/json"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestAnnotationsPreserveTagProvenanceAndCuratedPredictionScale(t *testing.T) {
	got := annotations(json.RawMessage(`{"genre":"R&B","artist":"AC/DC","mood_happy":"-78","BPM":"120","TBPM":"121","originaldate":"1979","date":"2005"}`))
	if len(got) != 7 {
		t.Fatalf("lost values: %+v", got)
	}
	counts := map[string]int{}
	for _, a := range got {
		counts[a.Kind]++
		if a.Origin != "embedded_tag" && a.Origin != "trusted_curated_tag" || a.SourceKey == "" {
			t.Fatalf("lost provenance: %+v", a)
		}
		if a.Kind == "acousticbrainz:mood_happy" && (a.Scale != "-100..100" || a.Value != "-78" || a.Origin != "trusted_curated_tag") {
			t.Fatal("lost signed curated mood scale")
		}
		if a.Kind == "artist_credit" && a.Value != "AC/DC" {
			t.Fatal("split literal artist name")
		}
	}
	if counts["tempo"] != 2 || counts["original_release_date"] != 1 || counts["edition_date"] != 1 {
		t.Fatalf("collapsed independent metadata: %v", counts)
	}
}

func TestAnnotationMatchingDoesNotInferGenreFromArtist(t *testing.T) {
	criterion := core.MusicalCriterion{Kind: "genre", Value: "rock"}
	if annotationMatches(core.MetadataAnnotation{Kind: "artist_credit", Value: "rock"}, criterion) {
		t.Fatal("artist credit became genre evidence")
	}
	if !annotationMatches(core.MetadataAnnotation{Kind: "style", Value: "rock"}, criterion) {
		t.Fatal("reviewed genre/style equivalent lost")
	}
}
