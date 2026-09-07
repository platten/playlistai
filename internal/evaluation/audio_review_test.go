package evaluation

import (
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func audioReviewFixture() AudioReviewDataset {
	yes, no, first := true, false, int64(100)
	data := AudioReviewDataset{Version: 1, ID: "synthetic-control", Evidence: EvidenceSynthetic, PolicyVersion: "fixture", DevelopmentSet: "synthetic-only"}
	for _, split := range []string{"development", "heldout"} {
		for _, variant := range []string{"existing", "seed_verified", "candidate_verified"} {
			data.Runs = append(data.Runs, AudioReviewRun{CaseID: split, Prompt: "ambient electronica", Split: split, Variant: variant, Requested: 2, Outcome: core.OutcomePartial,
				Tracks:                  []AudioTrackReview{{ReviewedRecording: ReviewedRecording{RecordingID: split + "-recording", ArtistIDs: []string{split + "-artist"}}, MusicalFit: &yes, Violation: &no, PreviewCovered: true}, {ReviewedRecording: ReviewedRecording{RecordingID: split + "-unknown", ArtistIDs: []string{split + "-artist"}}}},
				FirstResultMilliseconds: &first, TotalMilliseconds: 200, PeakMemoryBytes: 1024, CacheHits: 1, NewAnalyses: 1})
		}
	}
	return data
}

func TestAudioReviewRatesPreserveUnknownsAndSyntheticLabel(t *testing.T) {
	report, err := EvaluateAudioReviews(audioReviewFixture())
	if err != nil {
		t.Fatal(err)
	}
	if report.HumanReviewed || len(report.Metrics) != 6 {
		t.Fatal("synthetic control was presented as human evidence")
	}
	for _, m := range report.Metrics {
		if m.Returned != 2 || m.FitJudged != 1 || *m.FitRate != 1 || *m.ViolationRate != 0 || *m.PreviewCoverage != .5 || *m.CacheReuse != .5 {
			t.Fatalf("unknown evidence changed denominator: %+v", m)
		}
	}
}

func TestAudioReviewRejectsArtistRecordingAndAblationLeakage(t *testing.T) {
	for _, mutate := range []func(*AudioReviewDataset){
		func(d *AudioReviewDataset) { d.Runs[3].Tracks[0].ArtistIDs = []string{"development-artist"} },
		func(d *AudioReviewDataset) { d.Runs[3].Tracks[0].RecordingID = "development-recording" },
		func(d *AudioReviewDataset) {
			d.Runs[3].References = []ReviewedRecording{{RecordingID: "development-recording", ArtistIDs: []string{"other"}}}
		},
		func(d *AudioReviewDataset) { d.Runs = d.Runs[:5] },
		func(d *AudioReviewDataset) { d.Runs[1].Prompt = "different request" },
		func(d *AudioReviewDataset) { d.Runs[1].FirstResultMilliseconds = nil },
	} {
		data := audioReviewFixture()
		mutate(&data)
		if _, err := EvaluateAudioReviews(data); err == nil {
			t.Fatal("invalid evaluation accepted")
		}
	}
}
