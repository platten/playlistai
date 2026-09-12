package nlu

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// This fake binary only exercises file/approval contracts. Native model health
// is a separate activation requirement and would reject this synthetic file.
func extractorFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, raw := range map[string]string{
		"model.onnx":  "synthetic test file; not a trained ONNX model",
		"vocab.txt":   "[PAD]\n[UNK]\n[CLS]\n[SEP]\n[MASK]\nAerosmith\n",
		"config.json": `{"model_type":"distilbert","dim":768}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	digest := func(name string) string {
		value, err := fileDigest(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	head := HeadManifest{Version: 1, ModelSHA256: digest("model.onnx"), TokenizerSHA256: digest("vocab.txt"), ConfigSHA256: digest("config.json"), Labels: []string{"O", "B-artist:similarity", "I-artist:similarity"}, OutputName: "logits", MaxTokens: 512}
	writeJSON(t, filepath.Join(dir, "nlu-head.json"), head)
	calibration := Calibration{Version: 1, ModelSHA256: head.ModelSHA256, TokenizerSHA256: head.TokenizerSHA256, ConfigSHA256: head.ConfigSHA256, HeadSHA256: digest("nlu-head.json"), Reviewed: true, Threshold: 0.95, ValidationExamples: 30}
	writeJSON(t, filepath.Join(dir, "calibration.json"), calibration)
	return dir
}

func TestInspectExtractorBindsEveryInterpretationInput(t *testing.T) {
	for _, name := range []string{"model.onnx", "config.json", "vocab.txt", "nlu-head.json", "calibration.json"} {
		t.Run(name, func(t *testing.T) {
			dir := extractorFixture(t)
			before, err := InspectExtractor(dir)
			if err != nil || before.Identity == "" || before.ModelSHA256 == "" {
				t.Fatalf("valid contract rejected: %+v %v", before, err)
			}
			file, err := os.OpenFile(filepath.Join(dir, name), os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = file.WriteString(" "); err != nil {
				t.Fatal(err)
			}
			if err = file.Close(); err != nil {
				t.Fatal(err)
			}
			after, err := InspectExtractor(dir)
			// Whitespace in the calibration JSON changes the pack identity but
			// not the approved model/head; all other byte changes invalidate it.
			if name == "calibration.json" {
				if err != nil || after.Identity == before.Identity {
					t.Fatalf("calibration change reused identity: %+v %v", after, err)
				}
			} else if err == nil {
				t.Fatalf("changed %s remained compatible", name)
			}
		})
	}
}

func TestImportExtractorCopiesOnlyApprovedAllowlistAndReusesIdentity(t *testing.T) {
	source := extractorFixture(t)
	if err := os.WriteFile(filepath.Join(source, "private-notes.txt"), []byte("must not copy"), 0600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	first, err := ImportExtractor(context.Background(), source, root)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(first.Directory) != filepath.Join(root, "extractors") {
		t.Fatalf("import escaped destination: %s", first.Directory)
	}
	files, err := os.ReadDir(first.Directory)
	if err != nil || len(files) != len(extractorFiles) {
		t.Fatalf("unexpected copied files: %v %v", files, err)
	}
	again, err := ImportExtractor(context.Background(), source, root)
	if err != nil || again != first {
		t.Fatalf("immutable import not reused: %+v %v", again, err)
	}
	if _, err := os.Stat(filepath.Join(first.Directory, "private-notes.txt")); !os.IsNotExist(err) {
		t.Fatal("unapproved source file copied")
	}
	if err := os.WriteFile(filepath.Join(first.Directory, "model.onnx"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportExtractor(context.Background(), source, root); err == nil {
		t.Fatal("corrupt immutable destination overwritten")
	}
	raw, err := os.ReadFile(filepath.Join(first.Directory, "model.onnx"))
	if err != nil || string(raw) != "tampered" {
		t.Fatal("existing destination changed despite rejection")
	}
}

func TestImportExtractorCancellationAndUnreviewedFilesDoNotCreatePack(t *testing.T) {
	source := extractorFixture(t)
	root := filepath.Join(t.TempDir(), "uncreated")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ImportExtractor(ctx, source, root); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("canceled import created directories")
	}
	writeJSON(t, filepath.Join(source, "calibration.json"), Calibration{Version: 1, Reviewed: false})
	if _, err := ImportExtractor(context.Background(), source, root); err == nil {
		t.Fatal("unreviewed model imported")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("unreviewed import created directories")
	}
}

func TestInspectRejectsNonregularAndOversizedMetadata(t *testing.T) {
	dir := extractorFixture(t)
	path := filepath.Join(dir, "nlu-head.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectExtractor(dir); err == nil {
		t.Fatal("directory treated as head file")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate((1 << 20) + 1); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectExtractor(dir); err == nil {
		t.Fatal("oversized metadata accepted")
	}
}

func TestExtractorCopyRejectsSourceChanges(t *testing.T) {
	source := extractorFixture(t)
	path := filepath.Join(source, "model.onnx")
	snapshot, err := inspectFile(context.Background(), path, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, snapshot.size), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyExtractorFile(context.Background(), path, filepath.Join(t.TempDir(), "model.onnx"), 1<<30, snapshot); err == nil {
		t.Fatal("changed source copied with obsolete identity")
	}
}

func TestConcurrentIdenticalImportsLeaveOneImmutablePack(t *testing.T) {
	source, root := extractorFixture(t), t.TempDir()
	type result struct {
		descriptor ExtractorDescriptor
		err        error
	}
	results := make(chan result, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Go(func() {
			descriptor, err := ImportExtractor(context.Background(), source, root)
			results <- result{descriptor, err}
		})
	}
	wait.Wait()
	close(results)
	var identity string
	for value := range results {
		if value.err != nil {
			t.Fatal(value.err)
		}
		if identity != "" && identity != value.descriptor.Identity {
			t.Fatal("identical pack identities differ")
		}
		identity = value.descriptor.Identity
	}
	entries, err := os.ReadDir(filepath.Join(root, "extractors"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("left staging directories: %v %v", entries, err)
	}
}
