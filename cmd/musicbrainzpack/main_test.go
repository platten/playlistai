package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ulikunitz/xz"
)

func TestRerunReusesCompletedIndexOffline(t *testing.T) {
	root := t.TempDir()
	artist := filepath.Join(root, "artist.tar.xz")
	recording := filepath.Join(root, "recording.tar.xz")
	writeTestArchive(t, artist, "artist", `{"id":"a","name":"Artist"}`)
	writeTestArchive(t, recording, "recording", `{"id":"r","title":"Track","artist-credit":[{"artist":{"id":"a","name":"Artist"}}]}`)
	var stdout, stderr bytes.Buffer
	args := []string{"-work-dir", root, "-bundle-dir", filepath.Join(root, "first"), "-artist-archive", artist, "-recording-archive", recording, "-snapshot", "20260912-001001"}
	if err := runWithOutput(context.Background(), args, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(root, "musicbrainz.sqlite")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	empty := filepath.Join(root, "second")
	if err := os.Mkdir(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	args = []string{"-work-dir", root, "-bundle-dir", empty, "-source", ":invalid"}
	if err := runWithOutput(context.Background(), args, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(stdout.Bytes()) || !strings.Contains(stderr.String(), "Reusing completed") || strings.Contains(stderr.String(), "Process artists") {
		t.Fatalf("stdout=%s stderr=%s", &stdout, &stderr)
	}
	after, err := os.ReadFile(index)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("index changed: %v", err)
	}
	args[3] = filepath.Join(root, "third")
	err = runWithOutput(context.Background(), append(args, "-snapshot", "20260913-001001"), &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "-replace-index") {
		t.Fatalf("snapshot mismatch: %v", err)
	}
	err = runWithOutput(context.Background(), append(args, "-replace-index"), &stdout, &stderr)
	if err == nil {
		t.Fatal("replace must attempt a fresh download")
	}
}

func TestPreflightPreservesExistingFiles(t *testing.T) {
	for _, occupiedBundle := range []bool{false, true} {
		t.Run(map[bool]string{true: "bundle", false: "invalid index"}[occupiedBundle], func(t *testing.T) {
			root := t.TempDir()
			bundle := filepath.Join(root, "bundle")
			path := filepath.Join(root, "musicbrainz.sqlite")
			want := "-replace-index"
			if occupiedBundle {
				if err := os.Mkdir(bundle, 0o700); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(bundle, "keep")
				want = "not empty"
			}
			if err := os.WriteFile(path, []byte("preserve"), 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			err := runWithOutput(context.Background(), []string{"-work-dir", root, "-bundle-dir", bundle, "-source", ":invalid"}, &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("%v", err)
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected progress: %s", &stderr)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "preserve" {
				t.Fatalf("file changed: %v", err)
			}
		})
	}
}

func TestProgressConcurrentUpdatesAndPendingStages(t *testing.T) {
	var out bytes.Buffer
	p := newProgressDisplay(&out)
	p.Add("pending", "Never started", "bytes")
	var wg sync.WaitGroup
	for _, key := range []string{"artists", "recordings"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := int64(0); i <= 100; i++ {
				p.Update(key, key, i, 100, i)
			}
		}()
	}
	wg.Wait()
	p.Close()
	p.Close()
	if strings.Contains(out.String(), "Never started") || strings.Count(out.String(), "100%") != 2 {
		t.Fatal(out.String())
	}
}

func writeTestArchive(t *testing.T, path, entity, row string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z, err := xz.NewWriter(f)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(z)
	data := []byte(row + "\n")
	if err := tw.WriteHeader(&tar.Header{Name: "mbdump/" + entity, Mode: 0o600, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
	for _, close := range []func() error{tw.Close, z.Close, f.Close} {
		if err := close(); err != nil {
			t.Fatal(err)
		}
	}
}
