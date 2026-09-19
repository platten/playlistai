package libraryindex

import (
	"sync"
	"time"

	"github.com/platten/playlistai/internal/audio"
)

// StageTrace is an opt-in bounded ring of path-free scheduler observations.
// It never stores PCM, source paths, metadata, or embeddings.
type StageTrace struct {
	mu                 sync.Mutex
	events             []StageTraceEvent
	limit, next, ready int
	dropped            uint64
	finished           map[*audio.MERTWorker]time.Time
}

type StageTraceEvent struct {
	Time         time.Time     `json:"time"`
	Stage        string        `json:"stage"`
	Duration     time.Duration `json:"duration"`
	Count        int           `json:"count,omitempty"`
	ReadyWindows int           `json:"readyWindows"`
	MemoryBytes  int64         `json:"memoryBytes,omitempty"`
	PCMBytes     int64         `json:"pcmBytes,omitempty"`
}

func NewStageTrace(limit int) *StageTrace {
	if limit <= 0 {
		return nil
	}
	return &StageTrace{limit: min(limit, 100000), finished: make(map[*audio.MERTWorker]time.Time)}
}

func (t *StageTrace) recordLocked(event StageTraceEvent) {
	event.ReadyWindows = t.ready
	if len(t.events) < t.limit {
		t.events = append(t.events, event)
		return
	}
	t.events[t.next] = event
	t.next = (t.next + 1) % t.limit
	t.dropped++
}

func (t *StageTrace) Ready(delta int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ready += delta
	t.recordLocked(StageTraceEvent{Time: time.Now().UTC(), Stage: "ready_windows"})
}

func (t *StageTrace) Dispatch(worker *audio.MERTWorker) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	if previous, ok := t.finished[worker]; ok {
		t.recordLocked(StageTraceEvent{Time: now.UTC(), Stage: "worker_idle_gap", Duration: now.Sub(previous)})
	}
}

func (t *StageTrace) Finished(worker *audio.MERTWorker) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.finished[worker] = time.Now()
	t.mu.Unlock()
}

func (a *Analyzer) trace(stage string, started time.Time, count int) {
	if a.Trace == nil {
		return
	}
	used, _ := a.Admission.Usage()
	a.Trace.mu.Lock()
	defer a.Trace.mu.Unlock()
	a.Trace.recordLocked(StageTraceEvent{Time: time.Now().UTC(), Stage: stage, Duration: time.Since(started), Count: count, MemoryBytes: used.Memory, PCMBytes: used.PCMBytes})
}

func (t *StageTrace) Snapshot() ([]StageTraceEvent, uint64) {
	if t == nil {
		return nil, 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	events := make([]StageTraceEvent, 0, len(t.events))
	events = append(events, t.events[t.next:]...)
	events = append(events, t.events[:t.next]...)
	return events, t.dropped
}
