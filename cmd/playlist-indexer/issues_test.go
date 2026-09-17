package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/platten/playlistai/internal/libraryindex"
)

func TestIssueRecorderPersistsConcurrentIssues(t *testing.T) {
	dir := t.TempDir()
	recorder, err := openIssueRecorder(dir)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			recorder.Record(libraryindex.NewProcessingIssue("metadata", "archive", "Artist/Track.flac", "corrupt_media", os.ErrInvalid, false))
		}()
	}
	workers.Wait()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join(dir, "issues.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		var issue libraryindex.ProcessingIssue
		if err := json.Unmarshal(scanner.Bytes(), &issue); err != nil {
			t.Fatal(err)
		}
		if issue.RootAlias != "archive" || issue.Path != "Artist/Track.flac" || issue.Code != "corrupt_media" {
			t.Fatalf("issue=%+v", issue)
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 8 {
		t.Fatalf("issue count=%d", count)
	}
}
