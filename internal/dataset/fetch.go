package dataset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/platten/playlistai/internal/ports"
)

// ProgressOp is the op label used by Fetch's progress reports.
const ProgressOp = "catalog"

const partSuffix = ".part"

// Status reports whether every file in the manifest is already present in dir
// with the right size and checksum, and lists the ones that are not.
func Status(dir string, m *Manifest) (complete bool, missing []string) {
	for _, f := range m.Files {
		if verifyFile(filepath.Join(dir, f.Name), f.Size, f.SHA256) != nil {
			missing = append(missing, f.Name)
		}
	}
	return len(missing) == 0, missing
}

// Fetch downloads any missing/incomplete manifest files into dir, resuming
// partial downloads via HTTP range requests and verifying SHA-256 before an
// atomic rename. Progress is reported in bytes across all files under ProgressOp.
// Files already present and valid are skipped.
func Fetch(ctx context.Context, dir string, m *Manifest, p ports.Progress) error {
	if p == nil {
		p = ports.NopProgress{}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	total := m.TotalBytes()
	var done int64

	for _, f := range m.Files {
		target := filepath.Join(dir, f.Name)

		if verifyFile(target, f.Size, f.SHA256) == nil {
			done += f.Size
			p.Report(ProgressOp, done, total, "have "+f.Name)
			continue
		}

		p.Report(ProgressOp, done, total, "downloading "+f.Name)
		base := done
		n, err := Download(ctx, m.fileURL(f), target, f.Size, f.SHA256, func(fileDone, _ int64) {
			p.Report(ProgressOp, base+fileDone, total, f.Name)
		})
		done = base + n
		if err != nil {
			return fmt.Errorf("fetch %s: %w", f.Name, err)
		}
	}

	p.Report(ProgressOp, total, total, "ready")
	return nil
}

// Download fetches url into target (via target+".part"), resuming from any
// existing part file via an HTTP range request. size and sha256hex are optional
// integrity checks — pass 0 / "" to skip. onProgress, when non-nil, is called
// with (bytesDone, expectedTotal); expectedTotal is size, or -1 when unknown.
// Returns the total size of the file on success.
func Download(ctx context.Context, url, target string, size int64, sha256hex string, onProgress func(done, total int64)) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, err
	}
	part := target + partSuffix
	verify := sha256hex != ""

	h := sha256.New()
	var have int64
	if fi, err := os.Stat(part); err == nil && fi.Size() > 0 && (size == 0 || fi.Size() < size) {
		existing, oerr := os.Open(part) //nolint:gosec // path derived from a validated target dir
		if oerr != nil {
			return 0, oerr
		}
		have, oerr = io.Copy(h, existing)
		existing.Close()
		if oerr != nil {
			return 0, oerr
		}
	} else if err == nil && size > 0 && fi.Size() >= size {
		_ = os.Remove(part) // stale / oversized — start over
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	total := size
	if total == 0 {
		total = -1
	}

	var out *os.File
	switch resp.StatusCode {
	case http.StatusPartialContent:
		out, err = os.OpenFile(part, os.O_WRONLY|os.O_APPEND, 0o644) //nolint:gosec
	case http.StatusOK:
		have = 0
		h.Reset()
		out, err = os.Create(part) //nolint:gosec
	default:
		return have, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	if err != nil {
		return have, err
	}
	defer out.Close()

	written, err := copyHashed(out, h, resp.Body, have, func(done int64) {
		if onProgress != nil {
			onProgress(done, total)
		}
	})
	got := have + written
	if err != nil {
		return got, err
	}

	if size > 0 && got != size {
		return got, fmt.Errorf("size %d, expected %d", got, size)
	}
	if verify {
		if sum := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(sum, sha256hex) {
			_ = os.Remove(part)
			return got, fmt.Errorf("sha256 %s, expected %s", sum, sha256hex)
		}
	}

	if err := out.Sync(); err != nil {
		return got, err
	}
	out.Close()
	if err := os.Rename(part, target); err != nil {
		return got, err
	}
	return got, nil
}

func copyHashed(dst io.Writer, h hash.Hash, src io.Reader, startAt int64, onProgress func(int64)) (int64, error) {
	buf := make([]byte, 128<<10)
	var written int64
	w := io.MultiWriter(dst, h)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return written, werr
			}
			written += int64(n)
			if onProgress != nil {
				onProgress(startAt + written)
			}
		}
		if errors.Is(err, io.EOF) {
			return written, nil
		}
		if err != nil {
			return written, err
		}
	}
}

func verifyFile(path string, size int64, wantHex string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if size > 0 && fi.Size() != size {
		return fmt.Errorf("size %d, expected %d", fi.Size(), size)
	}
	if wantHex == "" {
		return nil
	}
	fp, err := os.Open(path) //nolint:gosec
	if err != nil {
		return err
	}
	defer fp.Close()
	h := sha256.New()
	if _, err := io.Copy(h, fp); err != nil {
		return err
	}
	if sum := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(sum, wantHex) {
		return fmt.Errorf("sha256 mismatch")
	}
	return nil
}
