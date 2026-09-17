package audio

import "testing"

func TestMERTWorkerPoolPreservesExplicitNativeThreadBudget(t *testing.T) {
	primary := &MERTWorker{InferenceThreads: 3}
	pool := NewMERTWorkerPool(primary, 2)
	defer pool.Close()
	if len(pool.workers) != 2 || pool.workers[0].InferenceThreads != 3 || pool.workers[1].InferenceThreads != 3 {
		t.Fatalf("thread budget was not copied to independent workers: %+v", pool.workers)
	}
}
