package libraryindex

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func TestDirectoryClaimsMergeOrderedPendingAndExpiredPrefixes(t *testing.T) {
	ctx := context.Background()
	state, epoch := newClaimTestState(t, 0)
	setClaimTestStates(t, state, `UPDATE directory_frontier SET state='completed'`)
	if err := state.write(ctx, true, func(conn *sql.Conn) error {
		for _, row := range []struct{ root, path, state, expiry string }{
			{"a", "a", "leased", "2000-01-01T00:00:00Z"},
			{"a", "b", "completed", ""},
			{"a", "c", "pending", ""},
			{"a", "d", "leased", "2999-01-01T00:00:00Z"},
			{"b", "a", "pending", ""},
			{"b", "b", "leased", "2000-01-01T00:00:00Z"},
		} {
			if _, err := conn.ExecContext(ctx, `INSERT INTO directory_frontier(epoch_id,root_id,relative_path,state,attempt,fence,lease_until) VALUES(?,?,?,?,3,'old',?)`, epoch, row.root, row.path, row.state, row.expiry); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, limit := range []int{2, 2, 2} {
		tasks, err := state.ClaimDirectories(ctx, epoch, limit, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range tasks {
			got = append(got, task.RootID+"/"+task.RelativePath)
			if task.Attempt != 4 || task.Fence == "old" || task.Fence == "" {
				t.Fatalf("claim did not establish fresh ownership: %+v", task)
			}
		}
	}
	if fmt.Sprint(got) != "[a/a a/c b/a b/b]" {
		t.Fatalf("claims=%v", got)
	}
}

func TestFinalizeBlockedScanDiffAudioPreservesChangedSemanticAndUnrelatedJobs(t *testing.T) {
	ctx := context.Background()
	state, epoch := newClaimTestState(t, 3)
	for _, invalid := range []int64{0, -1} {
		if err := state.FinalizeBlockedScanDiffAudio(ctx, invalid); err == nil {
			t.Fatalf("accepted invalid epoch %d", invalid)
		}
	}
	setClaimTestStates(t, state,
		`UPDATE jobs SET semantic_key='audio/v2' WHERE kind='audio' AND file_id=(SELECT id FROM files WHERE relative_path='track-000000.flac')`,
		`DELETE FROM scan_diff_jobs WHERE job_id=(SELECT id FROM jobs WHERE kind='audio' AND file_id=(SELECT id FROM files WHERE relative_path='track-000001.flac'))`)
	if err := state.FinalizeBlockedScanDiffAudio(ctx, epoch); err != nil {
		t.Fatal(err)
	}
	rows, err := state.reader.QueryContext(ctx, `SELECT f.relative_path,j.state,j.error_code FROM jobs j JOIN files f ON f.id=j.file_id WHERE j.kind='audio' ORDER BY f.relative_path`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var path, status, code string
		if err := rows.Scan(&path, &status, &code); err != nil {
			t.Fatal(err)
		}
		if path == "track-000002.flac" {
			if status != "failed" || code != "metadata_unavailable" {
				t.Fatalf("blocked scoped job: state=%s code=%s", status, code)
			}
		} else if status != "pending" || code != "" {
			t.Fatalf("out-of-scope %s was finalized: state=%s code=%s", path, status, code)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkClaimDirectoriesCompletedPrefix(b *testing.B) {
	for _, completed := range []int{1_000, 20_000} {
		b.Run(fmt.Sprint(completed), func(b *testing.B) {
			ctx := context.Background()
			state, epoch := newClaimTestState(b, 0)
			setClaimTestStates(b, state, `UPDATE directory_frontier SET state='completed'`)
			if err := state.write(ctx, true, func(conn *sql.Conn) error {
				tx, err := conn.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				defer func() { _ = tx.Rollback() }()
				insert, err := tx.PrepareContext(ctx, `INSERT INTO directory_frontier(epoch_id,root_id,relative_path,state) VALUES(?,'fixture',?,?)`)
				if err != nil {
					return err
				}
				defer insert.Close()
				for i := 0; i < completed+64; i++ {
					status := "completed"
					if i >= completed {
						status = "pending"
					}
					if _, err := insert.ExecContext(ctx, epoch, fmt.Sprintf("%06d", i), status); err != nil {
						return err
					}
				}
				return tx.Commit()
			}); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				setClaimTestStates(b, state, `UPDATE directory_frontier SET state='pending',fence='',lease_until=NULL WHERE state='leased'`)
				b.StartTimer()
				tasks, err := state.ClaimDirectories(ctx, epoch, 64, time.Minute)
				if err != nil || len(tasks) != 64 {
					b.Fatalf("claimed %d err=%v", len(tasks), err)
				}
			}
		})
	}
}
