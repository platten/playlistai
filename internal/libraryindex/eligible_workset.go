package libraryindex

import (
	"context"
	"database/sql"
	"fmt"
)

// eligible_jobs is a disposable, indexed projection of durable jobs. Epoch zero
// supports unscoped claims; other epochs contain only the exact revisions frozen
// in their manifests. A readiness bit indexes blocked dependencies separately;
// settled jobs never enter it. Counts can include blocked work without rescanning
// unrelated epochs or already completed jobs.
//
// Triggers execute on the single writer in the same transaction as each durable
// transition. This covers release, retry, supersession, metadata completion and
// lease recovery without a second, fallible in-memory bookkeeping operation.
func rebuildEligibleWorkset(ctx context.Context, conn *sql.Conn) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS eligible_jobs (
 epoch_id INTEGER NOT NULL, kind TEXT NOT NULL, ready INTEGER NOT NULL, state TEXT NOT NULL,
 job_id INTEGER NOT NULL, file_id TEXT NOT NULL, lease_until TEXT,
 PRIMARY KEY(epoch_id,kind,ready,state,job_id)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS eligible_jobs_job ON eligible_jobs(job_id,epoch_id);
CREATE INDEX IF NOT EXISTS eligible_jobs_file ON eligible_jobs(file_id);
CREATE INDEX IF NOT EXISTS eligible_jobs_expiry ON eligible_jobs(epoch_id,kind,ready,state,lease_until,job_id);
CREATE INDEX IF NOT EXISTS scan_diff_job ON scan_diff_jobs(job_id,epoch_id);
DELETE FROM eligible_jobs;
`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, eligibleInsertSQL("1")); err != nil {
		return err
	}
	// Job IDs and file IDs are immutable in normal operation. Refresh both sides
	// on updates as well, so the projection remains correct for repair tooling.
	for _, trigger := range []struct{ name, event, predicate, remove string }{
		{"eligible_job_insert", "AFTER INSERT ON jobs", "j.file_id=NEW.file_id", "file_id=NEW.file_id"},
		{"eligible_job_update", "AFTER UPDATE OF state,source_revision,semantic_key,kind,file_id,lease_until ON jobs", "j.file_id IN (OLD.file_id,NEW.file_id)", "file_id IN (OLD.file_id,NEW.file_id)"},
		{"eligible_job_delete", "AFTER DELETE ON jobs", "j.file_id=OLD.file_id", "file_id=OLD.file_id"},
	} {
		statement := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %s %s BEGIN
DELETE FROM eligible_jobs WHERE %s;
%s
END;`, trigger.name, trigger.event, trigger.remove, eligibleInsertSQL(trigger.predicate))
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
CREATE TRIGGER IF NOT EXISTS eligible_diff_insert AFTER INSERT ON scan_diff_jobs BEGIN
 INSERT INTO eligible_jobs(epoch_id,kind,ready,state,job_id,file_id,lease_until)
 SELECT NEW.epoch_id,e.kind,e.ready,e.state,e.job_id,e.file_id,e.lease_until
 FROM eligible_jobs e JOIN jobs j ON j.id=e.job_id
 WHERE e.epoch_id=0 AND e.kind=NEW.kind AND e.job_id=NEW.job_id
 AND j.source_revision=NEW.source_revision AND j.semantic_key=NEW.semantic_key;
END;
CREATE TRIGGER IF NOT EXISTS eligible_diff_delete AFTER DELETE ON scan_diff_jobs BEGIN
 DELETE FROM eligible_jobs WHERE epoch_id=OLD.epoch_id AND kind=OLD.kind AND job_id=OLD.job_id;
END;
`); err != nil {
		return err
	}
	return tx.Commit()
}

func eligibleInsertSQL(predicate string) string {
	const ready = `(j.kind='metadata' OR EXISTS(SELECT 1 FROM jobs prerequisite
 WHERE prerequisite.file_id=j.file_id AND prerequisite.kind='metadata'
 AND prerequisite.state='completed' AND prerequisite.source_revision=j.source_revision))`
	return `INSERT INTO eligible_jobs(epoch_id,kind,ready,state,job_id,file_id,lease_until)
SELECT 0,j.kind,` + ready + `,j.state,j.id,j.file_id,j.lease_until FROM jobs j
WHERE (` + predicate + `) AND j.state IN ('pending','leased');
INSERT INTO eligible_jobs(epoch_id,kind,ready,state,job_id,file_id,lease_until)
SELECT d.epoch_id,j.kind,` + ready + `,j.state,j.id,j.file_id,j.lease_until
FROM jobs j JOIN scan_diff_jobs d ON d.job_id=j.id
WHERE (` + predicate + `) AND j.state IN ('pending','leased')
AND d.kind=j.kind AND d.source_revision=j.source_revision AND d.semantic_key=j.semantic_key;`
}
