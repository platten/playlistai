package dataset

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestCanceledWarmDatasetOperations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "present")
	if err := os.WriteFile(path, []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for name, run := range map[string]func() error{
		"fetch":   func() error { return Fetch(ctx, dir, &Manifest{Files: []File{{Name: "present", Size: 2}}}, nil) },
		"archive": func() error { return DownloadArchive(ctx, "invalid", path, 2, "", nil) },
		"unpack":  func() error { return Unpack(ctx, "invalid", dir, nil) },
		"resume":  func() error { _, err := Download(ctx, "invalid", path, 2, "", nil); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}

type cancelingReader struct{ cancel context.CancelFunc }

func (r cancelingReader) Read(p []byte) (int, error) { r.cancel(); copy(p, "data"); return 4, io.EOF }

func TestContextReaderPreservesCancellationAtEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := io.Copy(io.Discard, contextReader{ctx, cancelingReader{cancel}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("last read hid cancellation: %v", err)
	}
}
