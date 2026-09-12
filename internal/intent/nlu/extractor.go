package nlu

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ExtractorDescriptor describes a reviewed task artifact, not a claim of
// measured playlist quality. Activation separately requires native health and
// an explicit application preference; importing never changes that preference.
type ExtractorDescriptor struct {
	Directory   string `json:"directory"`
	Identity    string `json:"identity"`
	ModelSHA256 string `json:"modelSHA256"`
}

var extractorFiles = [...]struct {
	name  string
	limit int64
}{
	{"model.onnx", 1 << 30},
	{"config.json", 1 << 20},
	{"vocab.txt", 8 << 20},
	{"nlu-head.json", 1 << 20},
	{"calibration.json", 1 << 20},
}

type extractorFile struct {
	digest string
	size   int64
}
type extractorInspection struct {
	descriptor  ExtractorDescriptor
	files       map[string]extractorFile
	fingerprint string
}

// InspectExtractor reads only five bounded regular files and validates the
// model, tokenizer, configuration, labels and reviewed calibration as a unit.
// An upstream encoder or unreviewed calibration candidate is never importable.
func InspectExtractor(dir string) (ExtractorDescriptor, error) {
	inspection, err := inspectExtractor(context.Background(), dir)
	return inspection.descriptor, err
}

func inspectExtractor(ctx context.Context, dir string) (extractorInspection, error) {
	var result extractorInspection
	if dir == "" {
		return result, errors.New("nlu: extractor directory required")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return result, err
	}
	st, err := os.Lstat(absolute)
	if err != nil || !st.IsDir() {
		return result, errors.New("nlu: extractor must be a regular directory")
	}
	result.files = make(map[string]extractorFile, len(extractorFiles))
	fingerprint := sha256.New()
	_, _ = io.WriteString(fingerprint, "distilbert-token-head/v1\n")
	for _, file := range extractorFiles {
		snapshot, err := inspectFile(ctx, filepath.Join(absolute, file.name), file.limit)
		if err != nil {
			return result, fmt.Errorf("nlu: extractor %s: %w", file.name, err)
		}
		result.files[file.name] = snapshot
		_, _ = fmt.Fprintf(fingerprint, "%s:%d:%s\n", file.name, snapshot.size, snapshot.digest)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	settings, err := readModelSettings(absolute, DistilBERT)
	if err != nil {
		return result, err
	}
	if settings.abstention != "" || settings.head == nil || settings.calibration == nil {
		return result, fmt.Errorf("nlu: extractor is inactive: %s", settings.abstention)
	}
	if settings.digest != result.files["model.onnx"].digest || settings.head.ConfigSHA256 != result.files["config.json"].digest || settings.head.TokenizerSHA256 != result.files["vocab.txt"].digest || settings.calibration.HeadSHA256 != result.files["nlu-head.json"].digest {
		return result, errors.New("nlu: extractor changed during inspection")
	}
	// Metadata may have been replaced after the initial snapshot; reject that
	// instead of recording an identity for another head or approval file.
	for _, file := range extractorFiles[1:] {
		current, err := inspectFile(ctx, filepath.Join(absolute, file.name), file.limit)
		if err != nil {
			return result, err
		}
		if current != result.files[file.name] {
			return result, errors.New("nlu: extractor changed during inspection")
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result.fingerprint = hex.EncodeToString(fingerprint.Sum(nil))
	result.descriptor = ExtractorDescriptor{Directory: absolute, Identity: "distilbert-token-head/v1/" + result.fingerprint, ModelSHA256: settings.digest}
	return result, nil
}

func inspectFile(ctx context.Context, path string, limit int64) (extractorFile, error) {
	var result extractorFile
	f, st, err := openRegular(path, limit)
	if err != nil {
		return result, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(contextReader{ctx, f}, limit+1))
	if err != nil {
		return result, err
	}
	if n != st.Size() || n > limit {
		return result, errors.New("file changed or exceeds size limit")
	}
	return extractorFile{digest: hex.EncodeToString(h.Sum(nil)), size: n}, nil
}

func openRegular(path string, limit int64) (*os.File, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() || before.Size() < 1 || before.Size() > limit {
		return nil, nil, errors.New("bounded regular file required")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	after, err := f.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() != before.Size() {
		_ = f.Close()
		return nil, nil, errors.New("file changed while opening")
	}
	return f, after, nil
}

type contextReader struct {
	context context.Context
	reader  io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.context.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

// ImportExtractor copies an approved pack into root/extractors/<content hash>.
// Extra source files are ignored, existing immutable packs are never replaced,
// and cancellation/failure only removes the five files in its own staging dir.
func ImportExtractor(ctx context.Context, source, root string) (ExtractorDescriptor, error) {
	if err := ctx.Err(); err != nil {
		return ExtractorDescriptor{}, err
	}
	if root == "" {
		return ExtractorDescriptor{}, errors.New("nlu: extractor import root required")
	}
	original, err := inspectExtractor(ctx, source)
	if err != nil {
		return ExtractorDescriptor{}, err
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return ExtractorDescriptor{}, err
	}
	packs := filepath.Join(absolute, "extractors")
	destination := filepath.Join(packs, original.fingerprint)
	if _, err = os.Lstat(destination); err == nil {
		existing, err := inspectExtractor(ctx, destination)
		if err != nil {
			return ExtractorDescriptor{}, err
		}
		if existing.descriptor.Identity != original.descriptor.Identity {
			return ExtractorDescriptor{}, errors.New("nlu: existing immutable extractor is incompatible")
		}
		return existing.descriptor, nil
	} else if !os.IsNotExist(err) {
		return ExtractorDescriptor{}, err
	}
	if err := ctx.Err(); err != nil {
		return ExtractorDescriptor{}, err
	}
	if err = os.MkdirAll(packs, 0700); err != nil {
		return ExtractorDescriptor{}, err
	}
	staging, err := os.MkdirTemp(packs, ".extractor-")
	if err != nil {
		return ExtractorDescriptor{}, err
	}
	defer func() {
		for _, file := range extractorFiles {
			_ = os.Remove(filepath.Join(staging, file.name))
		}
		_ = os.Remove(staging)
	}()
	for _, file := range extractorFiles {
		if err = copyExtractorFile(ctx, filepath.Join(original.descriptor.Directory, file.name), filepath.Join(staging, file.name), file.limit, original.files[file.name]); err != nil {
			return ExtractorDescriptor{}, err
		}
	}
	validated, err := inspectExtractor(ctx, staging)
	if err != nil {
		return ExtractorDescriptor{}, err
	}
	if validated.descriptor.Identity != original.descriptor.Identity {
		return ExtractorDescriptor{}, errors.New("nlu: extractor copy failed identity validation")
	}
	if err = ctx.Err(); err != nil {
		return ExtractorDescriptor{}, err
	}
	if err = os.Rename(staging, destination); err != nil {
		// Another process may have imported this exact immutable pack first.
		existing, inspectErr := inspectExtractor(ctx, destination)
		if inspectErr == nil && existing.descriptor.Identity == original.descriptor.Identity {
			return existing.descriptor, nil
		}
		return ExtractorDescriptor{}, err
	}
	validated.descriptor.Directory = destination
	return validated.descriptor, nil
}

func copyExtractorFile(ctx context.Context, source, target string, limit int64, expected extractorFile) error {
	input, st, err := openRegular(source, limit)
	if err != nil {
		return err
	}
	defer input.Close()
	if st.Size() != expected.size {
		return errors.New("nlu: extractor source changed before copy")
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, copyErr := io.Copy(output, io.TeeReader(io.LimitReader(contextReader{ctx, input}, expected.size+1), h))
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n != expected.size || hex.EncodeToString(h.Sum(nil)) != expected.digest {
		return errors.New("nlu: extractor source changed during copy")
	}
	return nil
}
