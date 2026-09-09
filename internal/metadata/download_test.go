package metadata

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownloadVerificationAndCancellation(t *testing.T) {
	const contents = "publisher fixture"
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte(contents)))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, contents)
	}))
	defer server.Close()
	dest := filepath.Join(t.TempDir(), "dump.gz")
	if err := downloadFile(context.Background(), server.URL, dest, sum); err != nil {
		t.Fatal(err)
	}
	if !verifiedFile(context.Background(), dest, sum) {
		t.Fatal("verified download not reusable")
	}
	for _, mode := range []string{"bad_hash", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			expected := fmt.Sprintf("%064d", 0)
			if mode == "cancel" {
				cancel()
				expected = sum
			}
			path := filepath.Join(t.TempDir(), "bad.gz")
			if err := downloadFile(ctx, server.URL, path, expected); err == nil {
				t.Fatal("invalid download published")
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil || len(entries) != 0 {
				t.Fatal("failed download not cleaned up", entries, err)
			}
		})
	}
}

func TestLatestAcrossPublishedYears(t *testing.T) {
	var calls []string
	get := func(_ context.Context, url string) ([]byte, error) {
		calls = append(calls, url)
		if strings.HasSuffix(url, "prefix=data%2F") {
			return []byte(`<a href="?prefix=data%2F2026%2F">2026</a><a href="?prefix=data%2F2025%2F">2025</a>`), nil
		}
		if strings.Contains(url, "2026") {
			return []byte("discogs_20260101_releases.xml.gz"), nil
		}
		var listing strings.Builder
		for _, kind := range []string{"artists.xml.gz", "labels.xml.gz", "masters.xml.gz", "releases.xml.gz", "CHECKSUM.txt"} {
			fmt.Fprintf(&listing, "discogs_20251201_%s\n", kind)
		}
		return []byte(listing.String()), nil
	}
	set, err := latest(context.Background(), 0, get)
	if err != nil || set.Date != "20251201" || len(calls) != 3 {
		t.Fatal(set, calls, err)
	}
	_, err = latest(context.Background(), 0, func(context.Context, string) ([]byte, error) { return nil, fmt.Errorf("offline") })
	if err == nil {
		t.Fatal("listing failure hidden")
	}
}

func TestParallelDownloadsReuseVerifiedFiles(t *testing.T) {
	const payload = "verified bulk fixture"
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte(payload)))
	var calls atomic.Int32
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Query().Get("download"), "CHECKSUM.txt") {
			for _, kind := range []string{"releases", "masters"} {
				_, _ = fmt.Fprintf(w, "%s discogs_20260901_%s.xml.gz\n", sum, kind)
			}
			return
		}
		calls.Add(1)
		arrived <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, payload)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dir := t.TempDir()
	done := make(chan error, 1)
	go func() { _, err := download(ctx, DumpSet{Date: "20260901"}, dir, server.URL+"/"); done <- err }()
	for range 2 {
		select {
		case <-arrived:
		case <-ctx.Done():
			t.Fatal("bulk transfers did not overlap")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	set, err := download(ctx, DumpSet{Date: "20260901"}, dir, server.URL+"/")
	if err != nil || len(set.Files) != 2 || calls.Load() != 2 {
		t.Fatal("verified files downloaded again", set, calls.Load(), err)
	}
}
