package audio

import (
	"context"
	"os"
	"testing"
)

func TestMERTRecoveryValidatesEachQuarantinedSession(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pool := NewMERTWorkerPool(&MERTWorker{Executable: exe, BundleDir: "test:mert-healthy", Model: mertTestModel()}, 2)
	defer pool.Close()
	failed := pool.workers[1]
	failed.BundleDir = "test:mert-crash"
	pool.Quarantine(failed)
	if err := pool.ValidateAll(context.Background()); err == nil {
		t.Fatal("healthy sibling cleared outage")
	}
	if !pool.Quarantined(failed) || pool.Quarantined(pool.workers[0]) {
		t.Fatal("quarantine did not follow session identity")
	}
	failed.BundleDir = "test:mert-healthy"
	if err := pool.ValidateAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if pool.Quarantined(failed) {
		t.Fatal("successful validation did not clear quarantine")
	}
	stats := pool.Diagnostics()
	if stats.NativeFailures != 1 || stats.Restarts != 1 || stats.HealthChecks != 4 || stats.HealthDuration <= 0 || stats.RestartDuration <= 0 {
		t.Fatalf("diagnostics=%+v", stats)
	}
}
