package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
)

func automaticEngineFixture() (AutomaticCorpus, AutomaticFeatures, AutomaticEngineOptions) {
	c := automaticFixtureCorpus()
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Seed: core.NewRNGSeed(17), Controls: core.IntentControls{RecommendationMode: core.Automatic, TotalTrackCount: 10}, EssentialCriteria: []core.MusicalCriterion{{Kind: "genre", Value: "house", Scope: "playlist", Strength: "essential"}}}
	intent = intent.Normalized()
	execution := intent
	execution.Controls.RecommendationMode = core.EnhancedHybrid
	query := AutomaticFeatureQuery{Request: AutomaticFeatureRequest{Facet: "genre_dortmund", Value: "electronic", Intent: intent}}
	for _, clause := range audio.Clauses(execution) {
		query.Queries = append(query.Queries, core.AudioClauseVector{Clause: clause, Values: []float32{1, 0}})
	}
	f := AutomaticFeatures{Version: AutomaticFeaturesVersion, CorpusSHA256: AutomaticCorpusSHA256(c), Split: "development", DecoderID: "fixture-decoder", Sampling: "two-excerpts/v1", MetadataSourceSHA256: automaticSHA([]byte("independent-uploader-tags")), MetadataSourceURL: "https://example.test/uploader-tags", Model: core.AudioModelIdentity{Model: "fixture", Revision: "fixture", Runtime: "fixture", Preprocessing: "fixture", Dimension: 2, Weights: automaticSHA([]byte("pairedfixture"))}, Queries: []AutomaticFeatureQuery{query}}
	for i, track := range c.Tracks[:300] {
		vector := []float32{0, 1}
		var annotations []core.MetadataAnnotation
		if i < 150 {
			vector = []float32{1, 0}
			annotations = []core.MetadataAnnotation{{Kind: "genre", Value: "house", Origin: "uploader", SourceKey: "genre"}}
		}
		// Independent synthetic fixture setup, never a measured musical result.
		f.Tracks = append(f.Tracks, AutomaticFeatureTrack{ID: track.ID, ArtistID: track.ArtistID, AudioSHA256: track.LowAudioSHA256, DurationSeconds: 100, CoveredSeconds: 20, Pooled: vector, Segments: []core.AudioSegment{{StartSeconds: 20, EndSeconds: 30, Embedding: vector}, {StartSeconds: 60, EndSeconds: 70, Embedding: vector}}, Annotations: annotations})
	}
	o := AutomaticEngineOptions{ProducerSourceSHA256: automaticSHA([]byte("producer")), PolicySHA256: automaticSHA([]byte("policy")), Seed: core.NewRNGSeed(17)}
	return c, f, o
}

func TestAutomaticProductionEngineAblationsAndLabelSeparation(t *testing.T) {
	c, f, options := automaticEngineFixture()
	before, _ := json.Marshal(f)
	m, err := RunAutomaticFeatures(context.Background(), c, f, options)
	if err != nil || len(m.Runs) != 3 {
		t.Fatal(len(m.Runs), err)
	}
	for _, run := range m.Runs {
		if len(run.CandidateIDs) != 300 || len(run.Retrieved) != 300 || run.Seed != "17" || run.CacheCondition != "warm_installed" || run.TimingScope != "prepared_engine" || run.Milliseconds == nil || *run.Milliseconds < 1 || run.FactualViolations == nil || *run.FactualViolations != 0 {
			t.Fatalf("lost fixed-pool/identity/timing contract: %+v", run)
		}
		want := 0
		if run.Variant == "combined" {
			want = 10
			if len(run.StrongAdmissions) != 150 {
				t.Fatal("production agreement assessment was bypassed", len(run.StrongAdmissions))
			}
		}
		if len(run.Output) != want {
			t.Fatal(run.Variant, len(run.Output), want)
		}
	}
	after, _ := json.Marshal(f)
	if string(before) != string(after) {
		t.Fatal("feature source mutated")
	}
	// Target changes cannot change any recording input, retrieval, or admission.
	for i := range c.Tracks {
		c.Tracks[i].Labels["genre_dortmund"] = "independent-new-target"
	}
	f.CorpusSHA256 = AutomaticCorpusSHA256(c)
	slices.Reverse(f.Tracks)
	other, err := RunAutomaticFeatures(context.Background(), c, f, options)
	if err != nil {
		t.Fatal(err)
	}
	for i := range m.Runs {
		if !reflect.DeepEqual(m.Runs[i].Output, other.Runs[i].Output) || !reflect.DeepEqual(m.Runs[i].StrongAdmissions, other.Runs[i].StrongAdmissions) || !reflect.DeepEqual(m.Runs[i].Retrieved, other.Runs[i].Retrieved) {
			t.Fatal("labels or input order influenced the engine")
		}
	}
	report, err := EvaluateAutomatic(c, other)
	if err != nil || report.State == "pass" {
		t.Fatal("prepared engine run falsely passed whole-application gate", report.State, err)
	}
}

func TestAutomaticFeatureAdapterRejectsInvalidEvidence(t *testing.T) {
	for _, name := range []string{"identity", "checksum", "coverage", "query", "labels-as-tags", "failed-audio", "heldout"} {
		t.Run(name, func(t *testing.T) {
			c, f, options := automaticEngineFixture()
			switch name {
			case "identity":
				f.Tracks[0].ArtistID = "artist_999"
			case "checksum":
				f.Tracks[0].AudioSHA256 = c.Tracks[0].AudioSHA256
			case "coverage":
				f.Tracks[0].Segments[1].StartSeconds = 25
			case "query":
				f.Queries[0].Queries[0].Clause.Negative = true
			case "labels-as-tags":
				f.MetadataSourceSHA256 = c.AnnotationsSHA256
			case "failed-audio":
				f.Tracks[0].Error = "decode failed"
			case "heldout":
				f.Split = "heldout"
			}
			if _, err := RunAutomaticFeatures(context.Background(), c, f, options); err == nil {
				t.Fatal("invalid feature artifact accepted")
			}
		})
	}
}

func TestAutomaticFeatureMissingAcquisitionAndCancellation(t *testing.T) {
	c, f, options := automaticEngineFixture()
	f.Tracks[0].Error, f.Tracks[0].Pooled, f.Tracks[0].Segments, f.Tracks[0].CoveredSeconds = "download unavailable", nil, nil, 0
	m, err := RunAutomaticFeatures(context.Background(), c, f, options)
	if err != nil || len(m.Runs[2].CandidateIDs) != 300 || len(m.Runs[2].StrongAdmissions) != 149 {
		t.Fatal("failed acquisition disappeared from denominator or became support", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = RunAutomaticFeatures(ctx, c, f, options); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, request := range AutomaticFeatureRequests() {
		count := 10
		if request.Facet == "voice_instrumental" && request.Value == "instrumental" {
			count = 60
		}
		if request.Intent.Count != count {
			t.Fatal("changed fixed task denominator", request.Facet, request.Value, request.Intent.Count)
		}
		if err := request.Intent.Validate(); err != nil {
			t.Fatal(request.Facet, request.Value, err)
		}
	}
}

func TestAutomaticTaskBankKeepsPublishedGenreLabels(t *testing.T) {
	// Exact label spellings from music-classification-annotations-raw.tsv;
	// acoustic query text is deliberately a separate, readable representation.
	want := []string{"alternative", "blues", "electronic", "folk_country", "funk_soul_rnb", "jazz", "pop", "rap_hiphop", "rock"}
	var got []string
	for _, request := range AutomaticFeatureRequests() {
		if request.Facet == "genre_dortmund" {
			got = append(got, request.Value)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
}
