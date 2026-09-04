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

// ProgressOp is the op label used in progress reports.
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
		n, err := downloadFile(ctx, m.fileURL(f), target, f, func(fileDone int64) {
			p.Report(ProgressOp, done+fileDone, total, f.Name)
		})
		done += n
		if err != nil {
			return fmt.Errorf("fetch %s: %w", f.Name, err)
		}
	}

	p.Report(ProgressOp, total, total, "ready")
	return nil
}

// downloadFile fetches url into target (via target+".part"), resuming from any
// existing part file. Returns the total bytes of the file on success.
func downloadFile(ctx context.Context, url, target string, f File, onProgress func(int64)) (int64, error) {
	part := target + partSuffix

	h := sha256.New()
	var have int64
	if fi, err := os.Stat(part); err == nil && fi.Size() > 0 && fi.Size() < f.Size {
		// Feed the existing prefix through the hash so we can verify at the end.
		existing, err := os.Open(part) //nolint:gosec // path derived from dir + manifest name
		if err != nil {
			return 0, err
		}
		have, err = io.Copy(h, existing)
		existing.Close()
		if err != nil {
			return 0, err
		}
	} else if err == nil && fi.Size() >= f.Size {
		_ = os.Remove(part) // stale / oversized — start over
		have = 0
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

	var out *os.File
	switch resp.StatusCode {
	case http.StatusPartialContent:
		out, err = os.OpenFile(part, os.O_WRONLY|os.O_APPEND, 0o644) //nolint:gosec
	case http.StatusOK:
		// Server ignored the range; restart cleanly.
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

	written, err := copyHashed(out, h, resp.Body, have, onProgress)
	total := have + written
	if err != nil {
		return total, err
	}

	if f.Size > 0 && total != f.Size {
		return total, fmt.Errorf("size %d, expected %d", total, f.Size)
	}
	if sum := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(sum, f.SHA256) {
		_ = os.Remove(part)
		return total, fmt.Errorf("sha256 %s, expected %s", sum, f.SHA256)
	}

	if err := out.Sync(); err != nil {
		return total, err
	}
	out.Close()
	if err := os.Rename(part, target); err != nil {
		return total, err
	}
	return total, nil
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
