package dataset

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestDownloadBoundsOversizedChunkedAndResumedResponses(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(fmt.Sprint(resume), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if resume {
					w.Header().Set("Content-Range", "bytes 2-7/8")
					w.WriteHeader(http.StatusPartialContent)
				}
				w.(http.Flusher).Flush() // no Content-Length: the stream itself must be bounded
				_, _ = w.Write(bytes.Repeat([]byte("x"), 65536))
			}))
			defer srv.Close()
			path := filepath.Join(t.TempDir(), "model")
			if resume {
				if err := os.WriteFile(path+".part", []byte("xx"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var maximum int64
			_, err := Download(context.Background(), srv.URL, path, 8, "", func(done, total int64) { maximum = max(maximum, done) })
			if err == nil || maximum > 8 {
				t.Fatalf("oversized response written: maximum=%d err=%v", maximum, err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("oversized model activated")
			}
		})
	}
}

func TestWrongResumeRangeLeavesPartialUntouched(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-3/6")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("cdef"))
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "model")
	if err := os.WriteFile(path+".part", []byte("ab"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Download(context.Background(), srv.URL, path, 6, "", nil); err == nil {
		t.Fatal("wrong offset accepted without a checksum")
	}
	raw, err := os.ReadFile(path + ".part")
	if err != nil || string(raw) != "ab" {
		t.Fatalf("valid partial corrupted: %q %v", raw, err)
	}
}

func TestDownloadRetriesPreserveResumeAndChecksum(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=2-" {
			t.Error("retry lost resume offset")
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Range", "bytes 2-5/6")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("cdef"))
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "model")
	if err := os.WriteFile(path+".part", []byte("ab"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Download(context.Background(), srv.URL, path, 6, sha256hex([]byte("abcdef")), nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "abcdef" || calls.Load() != 2 {
		t.Fatalf("retry/resume failed: %q calls=%d %v", raw, calls.Load(), err)
	}
}
