package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"

	"github.com/platten/playlistai/internal/modelpack"
)

const manifestName = "manifest.json"
const maxHostedBytes int64 = 3_000_000_000 // discoveryasset.MaxDownloadBytes

type manifest struct {
	SchemaVersion int        `json:"schemaVersion"`
	Format        string     `json:"format"`
	Compression   string     `json:"compression"`
	PartSize      int64      `json:"partSize"`
	Source        fileDigest `json:"source"`
	Payload       fileDigest `json:"payload"`
	Parts         []part     `json:"parts"`
}

type fileDigest struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type part struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func execute(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	flags := flag.NewFlagSet("paipack-split", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var output, partSizeText, compression string
	var hosted bool
	flags.StringVar(&output, "out", "", "output directory (default: <input>.parts)")
	flags.StringVar(&partSizeText, "part-size", "", "maximum part size (default: 1900MiB, or 190MB with --hosted)")
	flags.StringVar(&compression, "compression", "none", "transport compression: none or zstd")
	flags.BoolVar(&hosted, "hosted", false, "create a desktop-compatible multipart tar.zst release for HTTPS hosting")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "Usage: paipack-split [options] input.paipack")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return 2, err
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return 2, errors.New("paipack-split: exactly one input pack is required")
	}
	input := flags.Arg(0)
	if !strings.EqualFold(filepath.Ext(input), ".paipack") {
		return 2, errors.New("paipack-split: input must have a .paipack extension")
	}
	if partSizeText == "" {
		partSizeText = "1900MiB"
		if hosted {
			partSizeText = "190MB"
		}
	}
	partSize, err := parseByteSize(partSizeText)
	if err != nil {
		return 2, fmt.Errorf("paipack-split: --part-size: %w", err)
	}
	compression = strings.ToLower(strings.TrimSpace(compression))
	if compression != "none" && compression != "zstd" {
		return 2, errors.New("paipack-split: --compression must be none or zstd")
	}
	if hosted && compression != "none" {
		return 2, errors.New("paipack-split: --hosted manages tar.zst compression; omit --compression")
	}
	if output == "" {
		output = input + ".parts"
	}
	if hosted {
		if partSize < 1024 || partSize >= modelpack.MaxPartBytes {
			return 2, fmt.Errorf("paipack-split: hosted part size must be between 1024 and %d bytes (exclusive)", modelpack.MaxPartBytes)
		}
		report, err := splitHosted(ctx, input, output, partSize)
		if err != nil {
			return 1, err
		}
		_, err = fmt.Fprintf(stdout, "Packaged %s into %d hosted part(s) in %s\nManifest: %s\n", input, len(report.Parts), output, filepath.Join(output, manifestName))
		if err != nil {
			return 1, err
		}
		return 0, nil
	}
	report, err := splitPack(ctx, input, output, partSize, compression)
	if err != nil {
		return 1, err
	}
	_, err = fmt.Fprintf(stdout, "Split %s into %d part(s) in %s\nManifest: %s\n", input, len(report.Parts), output, filepath.Join(output, manifestName))
	if err != nil {
		return 1, err
	}
	return 0, nil
}

func splitHosted(ctx context.Context, input, output string, partSize int64) (modelpack.Manifest, error) {
	info, err := os.Stat(input)
	if err != nil {
		return modelpack.Manifest{}, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxHostedBytes {
		return modelpack.Manifest{}, errors.New("paipack-split: hosted paipack must be a regular file no larger than 3 GB")
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return modelpack.Manifest{}, err
	}
	if _, err := os.Lstat(output); err == nil {
		return modelpack.Manifest{}, fmt.Errorf("paipack-split: output already exists: %s", output)
	} else if !errors.Is(err, os.ErrNotExist) {
		return modelpack.Manifest{}, err
	}
	parent := filepath.Dir(output)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return modelpack.Manifest{}, err
	}
	temporary, err := os.MkdirTemp(parent, ".paipack-hosted-*")
	if err != nil {
		return modelpack.Manifest{}, err
	}
	defer os.RemoveAll(temporary)
	name := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
	report, err := modelpack.PackageFile(ctx, name, input, temporary, partSize)
	if err != nil {
		return modelpack.Manifest{}, err
	}
	manifestInfo, err := os.Stat(filepath.Join(temporary, manifestName))
	if err != nil {
		return modelpack.Manifest{}, err
	}
	transportBytes := manifestInfo.Size()
	for _, part := range report.Parts {
		transportBytes += part.Size
	}
	if transportBytes > maxHostedBytes {
		return modelpack.Manifest{}, errors.New("paipack-split: hosted bundle exceeds the 3 GB desktop download limit")
	}
	if err := os.Rename(temporary, output); err != nil {
		return modelpack.Manifest{}, fmt.Errorf("paipack-split: publish hosted output: %w", err)
	}
	if err := syncParentDirectory(parent); err != nil {
		return modelpack.Manifest{}, err
	}
	return report, nil
}

func splitPack(ctx context.Context, input, output string, partSize int64, compression string) (manifest, error) {
	if err := ctx.Err(); err != nil {
		return manifest{}, err
	}
	source, err := os.Open(input) //nolint:gosec // user-selected local pack
	if err != nil {
		return manifest{}, fmt.Errorf("paipack-split: open input: %w", err)
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return manifest{}, err
	}
	if !info.Mode().IsRegular() {
		return manifest{}, errors.New("paipack-split: input is not a regular file")
	}
	if info.Size() == 0 {
		return manifest{}, errors.New("paipack-split: input is empty")
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return manifest{}, err
	}
	if _, err := os.Lstat(output); err == nil {
		return manifest{}, fmt.Errorf("paipack-split: output already exists: %s", output)
	} else if !errors.Is(err, os.ErrNotExist) {
		return manifest{}, err
	}
	parent := filepath.Dir(output)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return manifest{}, err
	}
	temporary, err := os.MkdirTemp(parent, ".paipack-split-*")
	if err != nil {
		return manifest{}, err
	}
	defer os.RemoveAll(temporary)

	payloadName := filepath.Base(input)
	if compression == "zstd" {
		payloadName += ".zst"
	}
	parts := &partWriter{ctx: ctx, directory: temporary, prefix: payloadName, limit: partSize}
	payloadHash := sha256.New()
	destination := io.MultiWriter(parts, payloadHash)
	sourceHash := sha256.New()
	reader := io.TeeReader(&contextReader{ctx: ctx, reader: source}, sourceHash)

	var copied int64
	if compression == "zstd" {
		encoder, createErr := zstd.NewWriter(destination,
			zstd.WithEncoderLevel(zstd.SpeedBetterCompression),
			zstd.WithEncoderConcurrency(2),
			zstd.WithWindowSize(8<<20),
		)
		if createErr != nil {
			return manifest{}, createErr
		}
		copied, err = io.Copy(encoder, reader)
		closeErr := encoder.Close()
		err = errors.Join(err, closeErr)
	} else {
		copied, err = io.Copy(destination, reader)
	}
	err = errors.Join(err, parts.Close())
	if err != nil {
		return manifest{}, fmt.Errorf("paipack-split: write parts: %w", err)
	}
	if copied != info.Size() {
		return manifest{}, fmt.Errorf("paipack-split: input size changed while reading: expected %d bytes, read %d", info.Size(), copied)
	}
	report := manifest{
		SchemaVersion: 1,
		Format:        "playlist-ai-paipack-parts",
		Compression:   compression,
		PartSize:      partSize,
		Source: fileDigest{
			Name: filepath.Base(input), Size: info.Size(), SHA256: hex.EncodeToString(sourceHash.Sum(nil)),
		},
		Payload: fileDigest{
			Name: payloadName, Size: parts.total, SHA256: hex.EncodeToString(payloadHash.Sum(nil)),
		},
		Parts: parts.parts,
	}
	if err := writeManifest(temporary, report); err != nil {
		return manifest{}, err
	}
	if err := os.Rename(temporary, output); err != nil {
		return manifest{}, fmt.Errorf("paipack-split: publish output: %w", err)
	}
	if err := syncParentDirectory(parent); err != nil {
		return manifest{}, err
	}
	return report, nil
}

func writeManifest(directory string, report manifest) error {
	file, err := os.OpenFile(filepath.Join(directory, manifestName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644) //nolint:gosec // private temporary output directory
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	writeErr := encoder.Encode(report)
	return errors.Join(writeErr, file.Sync(), file.Close())
}

type partWriter struct {
	ctx       context.Context
	directory string
	prefix    string
	limit     int64
	current   *os.File
	hash      hash.Hash
	size      int64
	total     int64
	parts     []part
}

func (w *partWriter) Write(contents []byte) (int, error) {
	written := 0
	for len(contents) > 0 {
		if err := w.ctx.Err(); err != nil {
			return written, err
		}
		if w.current == nil {
			if err := w.open(); err != nil {
				return written, err
			}
		}
		remaining := w.limit - w.size
		amount := int64(len(contents))
		if amount > remaining {
			amount = remaining
		}
		chunk := contents[:int(amount)]
		n, err := w.current.Write(chunk)
		if n > 0 {
			_, _ = w.hash.Write(chunk[:n])
			w.size += int64(n)
			w.total += int64(n)
			written += n
			contents = contents[n:]
		}
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
		if w.size == w.limit {
			if err := w.closePart(); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

func (w *partWriter) open() error {
	name := fmt.Sprintf("%s.part%012d", w.prefix, len(w.parts))
	file, err := os.OpenFile(filepath.Join(w.directory, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644) //nolint:gosec // generated name below a private directory
	if err != nil {
		return err
	}
	w.current, w.hash, w.size = file, sha256.New(), 0
	return nil
}

func (w *partWriter) closePart() error {
	if w.current == nil {
		return nil
	}
	name := filepath.Base(w.current.Name())
	err := errors.Join(w.current.Sync(), w.current.Close())
	if err == nil {
		w.parts = append(w.parts, part{Index: len(w.parts), Name: name, Size: w.size, SHA256: hex.EncodeToString(w.hash.Sum(nil))})
	}
	w.current, w.hash, w.size = nil, nil, 0
	return err
}

func (w *partWriter) Close() error {
	return w.closePart()
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(contents []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(contents)
}

func parseByteSize(value string) (int64, error) {
	trimmed := strings.TrimSpace(value)
	upper := strings.ToUpper(trimmed)
	multipliers := []struct {
		suffix string
		value  int64
	}{
		{"GIB", 1 << 30}, {"MIB", 1 << 20}, {"KIB", 1 << 10},
		{"GB", 1_000_000_000}, {"MB", 1_000_000}, {"KB", 1_000}, {"B", 1},
	}
	multiplier, number := int64(1), upper
	for _, candidate := range multipliers {
		if strings.HasSuffix(upper, candidate.suffix) {
			multiplier = candidate.value
			number = strings.TrimSpace(trimmed[:len(trimmed)-len(candidate.suffix)])
			break
		}
	}
	amount, err := strconv.ParseInt(number, 10, 64)
	if err != nil || amount <= 0 || amount > (1<<63-1)/multiplier {
		return 0, fmt.Errorf("invalid positive byte size %q", value)
	}
	return amount * multiplier, nil
}
