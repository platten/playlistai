package libraryindex

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func BenchmarkObserveFiles(b *testing.B) {
	const chunkSize = 256
	semantic := map[string]string{"metadata": "probe/v1", "audio": "audio/v1"}

	b.Run("individual", func(b *testing.B) {
		state, root, epoch := newObservationBenchmarkState(b)
		b.ResetTimer()
		started := time.Now()
		for index := 0; index < b.N; index++ {
			if _, err := state.ObserveFile(context.Background(), epoch, SourceFile{
				RootID: root.ID, RelativePath: fmt.Sprintf("individual-%09d.flac", index), Size: int64(index + 1), MTimeNS: 1, Extension: ".flac",
			}, semantic); err != nil {
				b.Fatal(err)
			}
		}
		elapsed := time.Since(started)
		b.StopTimer()
		b.ReportMetric(float64(elapsed.Nanoseconds())/float64(max(1, b.N)), "ns/file")
	})

	b.Run("directory_chunk", func(b *testing.B) {
		state, root, epoch := newObservationBenchmarkState(b)
		b.ResetTimer()
		started := time.Now()
		for iteration := 0; iteration < b.N; iteration++ {
			files := make([]SourceFile, chunkSize)
			for offset := range files {
				index := iteration*chunkSize + offset
				files[offset] = SourceFile{
					RootID: root.ID, RelativePath: fmt.Sprintf("chunk-%09d.flac", index), Size: int64(index + 1), MTimeNS: 1, Extension: ".flac",
				}
			}
			if _, err := state.ObserveFiles(context.Background(), epoch, files, semantic); err != nil {
				b.Fatal(err)
			}
		}
		elapsed := time.Since(started)
		b.StopTimer()
		b.ReportMetric(float64(elapsed.Nanoseconds())/float64(max(1, b.N*chunkSize)), "ns/file")
	})
}

func newObservationBenchmarkState(b *testing.B) (*State, Root, int64) {
	b.Helper()
	ctx := context.Background()
	state, err := OpenState(ctx, b.TempDir(), "observe-files-benchmark", 1)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := state.Close(); err != nil {
			b.Error(err)
		}
	})
	root, err := state.EnsureRoot(ctx, b.TempDir(), "music")
	if err != nil {
		b.Fatal(err)
	}
	epoch, err := state.BeginEpoch(ctx, []Root{root})
	if err != nil {
		b.Fatal(err)
	}
	return state, root, epoch
}

// BenchmarkClaimScanDiffJobsLateInRun measures a claim after most of a large
// manifest has finished; its cost should follow the remaining work.
func BenchmarkClaimScanDiffJobsLateInRun(b *testing.B) {
	const files = 20_000
	state, epoch := newClaimTestState(b, files)
	setClaimTestStates(b, state,
		`UPDATE jobs SET state='completed' WHERE kind='metadata'`,
		fmt.Sprintf(`UPDATE jobs SET state='completed' WHERE id IN (SELECT id FROM jobs WHERE kind='audio' ORDER BY id LIMIT %d)`, files*9/10))
	ctx := context.Background()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		b.StopTimer()
		setClaimTestStates(b, state, `UPDATE jobs SET state='pending',fence='',lease_until=NULL WHERE kind='audio' AND state='leased'`)
		b.StartTimer()
		jobs, err := state.ClaimScanDiffJobs(ctx, epoch, "audio", 64, time.Minute)
		if err != nil || len(jobs) != 64 {
			b.Fatalf("claimed %d err=%v", len(jobs), err)
		}
	}
}

// BenchmarkEligibleSelection isolates selection from durable lease commits.
// Synthetic fixtures exercise three distributions without real library access.
func BenchmarkEligibleSelection(b *testing.B) {
	for _, scenario := range []string{"appended_root", "metadata_blocked", "mostly_completed"} {
		for _, files := range []int{1_000, 20_000} {
			b.Run(fmt.Sprintf("%s/%d", scenario, files), func(b *testing.B) {
				ctx := context.Background()
				state, epoch := newClaimTestState(b, files)
				kind := "audio"
				switch scenario {
				case "appended_root":
					root, err := state.EnsureRoot(ctx, b.TempDir(), "appended")
					if err != nil {
						b.Fatal(err)
					}
					epoch, err = state.BeginEpoch(ctx, []Root{root})
					if err != nil {
						b.Fatal(err)
					}
					semantic := map[string]string{"metadata": "probe/v1", "audio": "audio/v1"}
					var appended []SourceFile
					for i := 0; i < 64; i++ {
						appended = append(appended, SourceFile{RootID: root.ID, RelativePath: fmt.Sprintf("appended-%d.flac", i), Size: 1, MTimeNS: 1, Extension: ".flac"})
					}
					if _, err := state.ObserveFiles(ctx, epoch, appended, semantic); err != nil {
						b.Fatal(err)
					}
					if _, err := state.WriteScanManifest(ctx, epoch, semantic); err != nil {
						b.Fatal(err)
					}
					kind = "metadata"
				case "metadata_blocked":
					setClaimTestStates(b, state, `UPDATE jobs SET state='completed' WHERE kind='metadata' AND file_id IN (SELECT file_id FROM jobs WHERE kind='metadata' ORDER BY id DESC LIMIT 64)`)
				case "mostly_completed":
					setClaimTestStates(b, state, `UPDATE jobs SET state='completed' WHERE kind='metadata'`, `UPDATE jobs SET state='completed' WHERE kind='audio' AND id NOT IN (SELECT id FROM jobs WHERE kind='audio' ORDER BY id DESC LIMIT 64)`)
				}
				now := time.Now().UTC()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					jobs, err := state.claimCandidates(ctx, kind, epoch, now, 64)
					if err != nil || len(jobs) != 64 {
						b.Fatalf("selected %d err=%v", len(jobs), err)
					}
				}
			})
		}
	}
}

// BenchmarkClaimSelectionAppendedComparison retains the reviewed global-pending
// query as a baseline. Both selectors read identical metadata jobs from the same
// temporary database; the baseline omits its empty expired-lease query.
func BenchmarkClaimSelectionAppendedComparison(b *testing.B) {
	state, epoch := newClaimTestState(b, 20_000)
	setClaimTestStates(b, state, `DELETE FROM scan_diff_jobs WHERE job_id NOT IN (SELECT id FROM jobs WHERE kind='metadata' ORDER BY id DESC LIMIT 64)`)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, selector := range []string{"global_pending", "eligible_workset"} {
		b.Run(selector, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if selector == "eligible_workset" {
					jobs, err := state.claimCandidates(ctx, "metadata", epoch, now, 64)
					if err != nil || len(jobs) != 64 {
						b.Fatalf("selected %d err=%v", len(jobs), err)
					}
					continue
				}
				rows, err := state.reader.QueryContext(ctx, `SELECT j.id,j.kind,j.file_id,j.source_revision,j.semantic_key,j.attempt,j.retry_count
					FROM jobs j INDEXED BY jobs_claim_order WHERE j.kind='metadata' AND j.state='pending'
					AND EXISTS(SELECT 1 FROM scan_diff_jobs d WHERE d.epoch_id=? AND d.job_id=j.id AND d.kind=j.kind
					AND d.source_revision=j.source_revision AND d.semantic_key=j.semantic_key)
					ORDER BY j.id LIMIT 64`, epoch)
				if err != nil {
					b.Fatal(err)
				}
				count := 0
				for rows.Next() {
					var job Job
					if err := rows.Scan(&job.ID, &job.Kind, &job.FileID, &job.SourceRevision, &job.SemanticKey, &job.Attempt, &job.RetryCount); err != nil {
						b.Fatal(err)
					}
					count++
				}
				if err := rows.Err(); err != nil {
					b.Fatal(err)
				}
				if err := rows.Close(); err != nil {
					b.Fatal(err)
				}
				if count != 64 {
					b.Fatalf("selected %d", count)
				}
			}
		})
	}
}
