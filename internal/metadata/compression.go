package metadata

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/klauspost/compress/zstd"
)

type CompressionResult struct {
	Codec        string `json:"codec"`
	InputBytes   int64  `json:"inputBytes"`
	Bytes        int64  `json:"bytes"`
	Milliseconds int64  `json:"milliseconds"`
}
type countOutput struct{ n int64 }

func (c *countOutput) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

// CompareCompression measures the actual runtime index without creating extra
// archives. It makes no claim of globally optimal compression across codecs.
func CompareCompression(ctx context.Context, path string) ([]CompressionResult, error) {
	var results []CompressionResult
	for _, codec := range []string{"gzip-9", "zstd-default", "zstd-best", "zstd-best-64MiB", "zstd-best-128MiB"} {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		out := &countOutput{}
		start := time.Now()
		var writer io.WriteCloser
		if codec == "gzip-9" {
			writer, err = gzip.NewWriterLevel(out, gzip.BestCompression)
		} else {
			level := zstd.SpeedDefault
			if codec != "zstd-default" {
				level = zstd.SpeedBestCompression
			}
			window := 16 << 20
			if codec == "zstd-best-64MiB" {
				window = 64 << 20
			}
			if codec == "zstd-best-128MiB" {
				window = 128 << 20
			}
			writer, err = zstd.NewWriter(out, zstd.WithEncoderLevel(level), zstd.WithEncoderConcurrency(2), zstd.WithWindowSize(window))
		}
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		n, copyErr := io.Copy(writer, contextReader{ctx, f})
		err = errors.Join(copyErr, writer.Close(), f.Close())
		if err != nil {
			return nil, err
		}
		results = append(results, CompressionResult{Codec: codec, InputBytes: n, Bytes: out.n, Milliseconds: time.Since(start).Milliseconds()})
	}
	return results, nil
}
