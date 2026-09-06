package evaluation

import (
	"fmt"
	"os"
	"strings"
)

func WriteReportJSON(path string, report Report) error { return writeJSON(path, report) }

func WriteReportMarkdown(path string, report Report) error {
	var out strings.Builder
	fmt.Fprintf(&out, "# Recommendation Evaluation: %s\n\n", report.DatasetName)
	fmt.Fprintf(&out, "Evidence: `%s` · Catalog: `%s` · Parser: `%s` · K: %d\n\n", report.Evidence, report.CatalogVersion, report.ParserVersion, report.K)
	fmt.Fprintf(&out, "Temporal boundaries: train through `%s`; development through `%s`; later cases are held out.\n\n", report.TemporalSplit.TrainEnd.Format("2006-01-02T15:04:05Z07:00"), report.TemporalSplit.DevelopmentEnd.Format("2006-01-02T15:04:05Z07:00"))
	fmt.Fprintf(&out, "Intent field accuracy: %s. Resolution accuracy: %s.\n\n", fraction(report.Intent.Accuracy, report.Intent.CorrectFields, report.Intent.LabeledFields), fraction(report.Resolution.Accuracy, report.Resolution.Correct, report.Resolution.Cases))
	writeVariantTable(&out, "Development", report.Development)
	writeVariantTable(&out, "Held-out test", report.HeldOutTest)
	if report.SelectedParameters != nil {
		fmt.Fprintf(&out, "## Selected development parameters\n\n`%s` was selected before held-out evaluation. See the JSON report for exact values.\n\n", report.SelectedParameters.Name)
	} else {
		fmt.Fprint(&out, "## Recommended defaults\n\nNo evidence-backed retuning was possible; current defaults are retained, not newly recommended by quality evidence.\n\n")
	}
	if len(report.Limitations) > 0 {
		fmt.Fprint(&out, "## Limitations\n\n")
		for _, item := range report.Limitations {
			fmt.Fprintf(&out, "- %s\n", item)
		}
		fmt.Fprintln(&out)
	}
	return os.WriteFile(path, []byte(out.String()), 0o644)
}

func writeVariantTable(out *strings.Builder, title string, results []VariantResult) {
	fmt.Fprintf(out, "## %s\n\n| Variant | Cases | Recall@K | NDCG@K | Hard violations | Duplicates | Artist diversity | Transition | Total µs |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|\n", title)
	for _, item := range results {
		a := item.Aggregate
		fmt.Fprintf(out, "| %s | %d | %s | %s | %d | %d | %.3f | %s | %d |\n", item.Name, a.SuccessfulCases, estimate(item.Uncertainty["recallAtK"]), estimate(item.Uncertainty["ndcgAtK"]), a.HardConstraintViolations, a.RecordingDuplicates, a.ArtistDiversity, estimate(item.Uncertainty["transitionQuality"]), a.Latency.TotalMicros)
	}
	fmt.Fprintln(out)
}
func estimate(value Interval) string {
	if value.Cases == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.3f [%.3f, %.3f]", value.Mean, value.Low95, value.High95)
}
func fraction(value *float64, numerator, denominator int) string {
	if value == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.3f (%d/%d)", *value, numerator, denominator)
}
