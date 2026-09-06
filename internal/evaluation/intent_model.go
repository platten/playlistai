package evaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

const IntentModelReportVersion = 1

type IntentModelIdentity struct {
	ID            string `json:"id"`
	Path          string `json:"path"`
	ArtifactBytes int64  `json:"artifactBytes"`
	SHA256        string `json:"sha256"`
	Runtime       string `json:"runtime"`
}

type IntentModelRun struct {
	CaseID        string           `json:"caseId"`
	Tags          []string         `json:"tags,omitempty"`
	Attempt       int              `json:"attempt"`
	SchemaValid   bool             `json:"schemaValid"`
	LabeledFields int              `json:"labeledFields"`
	CorrectFields int              `json:"correctFields"`
	Exact         bool             `json:"exact"`
	LatencyMillis int64            `json:"latencyMillis"`
	Output        core.MusicIntent `json:"output"`
	Error         string           `json:"error,omitempty"`
}

type IntentModelAggregate struct {
	Runs                int     `json:"runs"`
	SchemaValidRuns     int     `json:"schemaValidRuns"`
	ExactRuns           int     `json:"exactRuns"`
	LabeledFields       int     `json:"labeledFields"`
	CorrectFields       int     `json:"correctFields"`
	SchemaValidity      float64 `json:"schemaValidity"`
	ExactCaseAccuracy   float64 `json:"exactCaseAccuracy"`
	FieldAccuracy       float64 `json:"fieldAccuracy"`
	MedianLatencyMillis int64   `json:"medianLatencyMillis"`
	P95LatencyMillis    int64   `json:"p95LatencyMillis"`
	PeakResidentBytes   int64   `json:"peakResidentBytes"`
}

type IntentModelReport struct {
	Version         int                  `json:"version"`
	DatasetName     string               `json:"datasetName"`
	GeneratedAt     time.Time            `json:"generatedAt"`
	ParserVersion   string               `json:"parserVersion"`
	ContractVersion int                  `json:"contractVersion"`
	Model           IntentModelIdentity  `json:"model"`
	Cases           []IntentModelRun     `json:"cases"`
	Aggregate       IntentModelAggregate `json:"aggregate"`
}

type memoryReporter interface {
	RuntimeMemoryBytes() int64
}

// EvaluateIntentModel applies the real grammar-constrained parser to labeled
// intent cases. A parse failure counts every labeled field as incorrect.
func EvaluateIntentModel(ctx context.Context, parser ports.IntentParser, dataset Dataset, identity IntentModelIdentity, repeat int) (IntentModelReport, error) {
	if parser == nil {
		return IntentModelReport{}, fmt.Errorf("intent evaluation: parser is required")
	}
	if repeat <= 0 {
		repeat = 1
	}
	info := parser.Info()
	report := IntentModelReport{Version: IntentModelReportVersion, DatasetName: dataset.Name, GeneratedAt: time.Now().UTC(), ParserVersion: info.Version, ContractVersion: info.ContractVersion, Model: identity, Cases: []IntentModelRun{}}
	latencies := make([]int64, 0, len(dataset.IntentCases)*repeat)
	for attempt := 1; attempt <= repeat; attempt++ {
		for _, item := range dataset.IntentCases {
			if err := ctx.Err(); err != nil {
				return IntentModelReport{}, err
			}
			started := time.Now()
			intent, err := parser.Parse(ctx, intentInput(item))
			elapsed := time.Since(started).Milliseconds()
			run := IntentModelRun{CaseID: item.ID, Tags: append([]string(nil), item.Tags...), Attempt: attempt, LatencyMillis: elapsed, LabeledFields: intentLabelCount(item.Expected)}
			latencies = append(latencies, elapsed)
			if err != nil {
				run.Error = err.Error()
			} else {
				run.SchemaValid = true
				run.Output = intent.Normalized()
				checks := intentChecks(run.Output, item.Expected)
				for _, correct := range checks {
					if correct {
						run.CorrectFields++
					}
				}
				run.Exact = run.CorrectFields == run.LabeledFields
			}
			report.Cases = append(report.Cases, run)
			report.Aggregate.Runs++
			report.Aggregate.LabeledFields += run.LabeledFields
			report.Aggregate.CorrectFields += run.CorrectFields
			if run.SchemaValid {
				report.Aggregate.SchemaValidRuns++
			}
			if run.Exact {
				report.Aggregate.ExactRuns++
			}
			if measured, ok := parser.(memoryReporter); ok {
				report.Aggregate.PeakResidentBytes = max(report.Aggregate.PeakResidentBytes, measured.RuntimeMemoryBytes())
			}
		}
	}
	if report.Aggregate.Runs > 0 {
		report.Aggregate.SchemaValidity = float64(report.Aggregate.SchemaValidRuns) / float64(report.Aggregate.Runs)
		report.Aggregate.ExactCaseAccuracy = float64(report.Aggregate.ExactRuns) / float64(report.Aggregate.Runs)
	}
	if report.Aggregate.LabeledFields > 0 {
		report.Aggregate.FieldAccuracy = float64(report.Aggregate.CorrectFields) / float64(report.Aggregate.LabeledFields)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	if len(latencies) > 0 {
		report.Aggregate.MedianLatencyMillis = percentileMillis(latencies, .5)
		report.Aggregate.P95LatencyMillis = percentileMillis(latencies, .95)
	}
	return report, nil
}

func percentileMillis(sorted []int64, percentile float64) int64 {
	index := int(float64(len(sorted)-1)*percentile + .5)
	return sorted[min(len(sorted)-1, max(0, index))]
}

func WriteIntentModelReportJSON(path string, report IntentModelReport) error {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

func WriteIntentModelReportMarkdown(path string, report IntentModelReport) error {
	a := report.Aggregate
	text := fmt.Sprintf("# Intent Model Evaluation: %s\n\nModel: `%s`  \nArtifact: `%d` bytes, SHA-256 `%s`  \nRuntime: `%s`  \nParser/schema: `%s` / `v%d`\n\n| Runs | Schema-valid | Exact cases | Field accuracy | Median | P95 | Peak RSS |\n|---:|---:|---:|---:|---:|---:|---:|\n| %d | %.3f | %.3f | %.3f | %d ms | %d ms | %d bytes |\n\n", report.DatasetName, report.Model.ID, report.Model.ArtifactBytes, report.Model.SHA256, report.Model.Runtime, report.ParserVersion, report.ContractVersion, a.Runs, a.SchemaValidity, a.ExactCaseAccuracy, a.FieldAccuracy, a.MedianLatencyMillis, a.P95LatencyMillis, a.PeakResidentBytes)
	text += "| Case | Attempt | Schema | Correct fields | Exact | Latency | Error |\n|---|---:|---:|---:|---:|---:|---|\n"
	for _, run := range report.Cases {
		errText := strings.NewReplacer("|", "\\|", "\n", " ").Replace(run.Error)
		text += fmt.Sprintf("| %s | %d | %t | %d/%d | %t | %d ms | %s |\n", run.CaseID, run.Attempt, run.SchemaValid, run.CorrectFields, run.LabeledFields, run.Exact, run.LatencyMillis, errText)
	}
	text += "\n"
	return os.WriteFile(path, []byte(text), 0o644)
}
