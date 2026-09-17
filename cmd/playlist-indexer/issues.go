package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/platten/playlistai/internal/libraryindex"
)

type issueRecorder struct {
	mu       sync.Mutex
	file     *os.File
	firstErr error
}

func openIssueRecorder(stateDir string) (*issueRecorder, error) {
	path := filepath.Join(stateDir, "issues.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &issueRecorder{file: file}, nil
}

func (r *issueRecorder) Record(issue libraryindex.ProcessingIssue) {
	if r == nil {
		return
	}
	if issue.Time.IsZero() {
		issue.Time = time.Now().UTC()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.firstErr != nil {
		return
	}
	raw, err := json.Marshal(issue)
	if err == nil {
		_, err = r.file.Write(append(raw, '\n'))
	}
	if err == nil {
		err = r.file.Sync()
	}
	if err != nil {
		r.firstErr = err
	}
}

func (r *issueRecorder) Err() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.firstErr
}

func (r *issueRecorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return r.firstErr
	}
	err := errors.Join(r.firstErr, r.file.Sync(), r.file.Close())
	r.file = nil
	return err
}
