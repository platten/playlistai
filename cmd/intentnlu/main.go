// intentnlu prepares the native intent encoders and verifies independently
// generated tokenizer/embedding references. Python is offline tooling only.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/intent/nlu"
)

func main() { os.Exit(mainCode()) }

func mainCode() int {
	if len(os.Args) == 5 && os.Args[1] == "--nlu-worker" {
		if err := nlu.RunWorker(nlu.WorkerConfig{Kind: nlu.ModelKind(os.Args[2]), ModelDir: os.Args[3], RuntimeLibrary: os.Args[4]}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: intentnlu setup --root PATH | verify --kind minilm|distilbert --model-dir PATH --runtime PATH --reference-input FILE --output FILE")
	}
	switch args[0] {
	case "setup":
		return setup(ctx, args[1:], out, errOut)
	case "verify":
		return verify(ctx, args[1:], out, errOut)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

type progressUpdate struct {
	done, total int64
	note        string
}
type progress struct {
	updates  chan progressUpdate
	finished chan struct{}
}

func newProgress(output io.Writer) *progress {
	p := &progress{updates: make(chan progressUpdate, 1), finished: make(chan struct{})}
	go func() {
		defer close(p.finished)
		var last time.Time
		for update := range p.updates {
			if time.Since(last) < 2*time.Second && update.done != update.total {
				continue
			}
			last = time.Now()
			fmt.Fprintf(output, "%s (%d/%d bytes)\n", update.note, update.done, update.total)
		}
	}()
	return p
}

func (p *progress) Report(_ string, done, total int64, note string) {
	select {
	case p.updates <- progressUpdate{done, total, note}:
	default:
	}
}

func (p *progress) Close() { close(p.updates); <-p.finished }

func setup(ctx context.Context, args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	flags.SetOutput(errOut)
	root := flags.String("root", "", "Directory for verified native model assets")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *root == "" || flags.NArg() != 0 {
		return errors.New("setup requires --root and no positional arguments")
	}
	absolute, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	progress := newProgress(errOut)
	defer progress.Close()
	if err = nlu.InstallAssets(ctx, absolute, progress); err != nil {
		return err
	}
	dir := nlu.AssetDir(absolute)
	library, err := nlu.RuntimePath(dir)
	if err != nil {
		return err
	}
	w := &nlu.Worker{Config: nlu.WorkerConfig{Kind: nlu.MiniLM, ModelDir: filepath.Join(dir, "minilm"), RuntimeLibrary: library}, ExpectedModelSHA256: modelDigest(nlu.MiniLM)}
	defer w.Close()
	if err = w.Health(ctx); err != nil {
		return fmt.Errorf("native model health failed: %w", err)
	}
	return json.NewEncoder(out).Encode(struct {
		Installed    bool   `json:"installed"`
		Root         string `json:"root"`
		Identity     string `json:"identity"`
		MiniLMHealth bool   `json:"minilmHealth"`
		DistilBERT   string `json:"distilbert"`
	}{true, absolute, nlu.AssetsIdentity(), true, "base_encoder_inactive_without_reviewed_head"})
}

func modelDigest(kind nlu.ModelKind) string {
	for _, source := range nlu.Sources() {
		if source.Model == string(kind) && (source.Name == "model.onnx" || source.Name == "onnx/model.onnx") {
			return source.SHA256
		}
	}
	return ""
}

type parityCase struct {
	Text string `json:"text"`
	nlu.Encoding
	Embedding []float32 `json:"embedding,omitempty"`
	Millis    float64   `json:"millis,omitempty"`
}

type parityReport struct {
	Version              int           `json:"version"`
	Kind                 nlu.ModelKind `json:"kind"`
	Source               string        `json:"source"`
	OffsetUnit           string        `json:"offsetUnit"`
	MaxTokens            int           `json:"maxTokens"`
	Cases                []parityCase  `json:"cases"`
	NativeParity         bool          `json:"nativeParity"`
	SemanticCalibration  bool          `json:"semanticCalibration"`
	ReferenceSHA256      string        `json:"referenceSHA256,omitempty"`
	ModelSHA256          string        `json:"modelSHA256,omitempty"`
	LoadMillis           float64       `json:"loadMillis,omitempty"`
	TokenizerCases       int           `json:"tokenizerCases,omitempty"`
	EmbeddingCases       int           `json:"embeddingCases,omitempty"`
	MaximumAbsoluteError float64       `json:"maximumAbsoluteError,omitempty"`
	MinimumCosine        float64       `json:"minimumCosine,omitempty"`
}

func verify(ctx context.Context, args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	flags.SetOutput(errOut)
	kind := flags.String("kind", "", "minilm or distilbert")
	dir := flags.String("model-dir", "", "Directory containing config.json, vocab.txt and model.onnx")
	library := flags.String("runtime", "", "Absolute or relative path to the packaged ONNX Runtime library")
	input := flags.String("reference-input", "", "JSON reference produced by verify_intent_nlu_parity.py")
	output := flags.String("output", "", "JSON path for actual native outputs and parity summary")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if (*kind != string(nlu.MiniLM) && *kind != string(nlu.DistilBERT)) || *dir == "" || *library == "" || *input == "" || *output == "" || flags.NArg() != 0 {
		return errors.New("verify requires --kind minilm|distilbert, --model-dir, --runtime, --reference-input, --output and no positional arguments")
	}
	var err error
	for _, path := range []*string{dir, library, input, output} {
		*path, err = filepath.Abs(*path)
		if err != nil {
			return err
		}
	}
	if strings.ToLower(filepath.Ext(*output)) != ".json" {
		return errors.New("native parity output must be a .json file")
	}
	for _, protected := range []string{*input, *library, filepath.Join(*dir, "config.json"), filepath.Join(*dir, "vocab.txt"), filepath.Join(*dir, "model.onnx"), filepath.Join(*dir, "nlu-head.json"), filepath.Join(*dir, "calibration.json")} {
		if samePath(*output, protected) {
			return errors.New("native parity output would overwrite an input or model asset")
		}
	}
	reference, raw, err := readReference(*input, nlu.ModelKind(*kind))
	if err != nil {
		return err
	}
	tokenizer, err := nlu.LoadWordPiece(filepath.Join(*dir, "vocab.txt"), reference.Kind == nlu.MiniLM, reference.MaxTokens)
	if err != nil {
		return err
	}
	result := parityReport{Version: 1, Kind: reference.Kind, Source: *dir, OffsetUnit: "utf8-bytes", MaxTokens: reference.MaxTokens, NativeParity: true, MinimumCosine: 1}
	h := sha256.Sum256(raw)
	result.ReferenceSHA256 = hex.EncodeToString(h[:])
	modelFile, err := os.Open(filepath.Join(*dir, "model.onnx"))
	if err != nil {
		return err
	}
	modelHash := sha256.New()
	_, copyErr := io.Copy(modelHash, modelFile)
	closeErr := modelFile.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	result.ModelSHA256 = hex.EncodeToString(modelHash.Sum(nil))
	var worker *nlu.Worker
	if reference.Kind == nlu.MiniLM {
		worker = &nlu.Worker{Config: nlu.WorkerConfig{Kind: reference.Kind, ModelDir: *dir, RuntimeLibrary: *library}, ExpectedModelSHA256: result.ModelSHA256}
		defer worker.Close()
		started := time.Now()
		if err = worker.Health(ctx); err != nil {
			return err
		}
		result.LoadMillis = float64(time.Since(started).Microseconds()) / 1000
	}
	for _, expected := range reference.Cases {
		if err = ctx.Err(); err != nil {
			return err
		}
		started := time.Now()
		encoding, err := tokenizer.Encode(expected.Text)
		if err != nil {
			return err
		}
		row := parityCase{Text: expected.Text, Encoding: encoding}
		if !reflect.DeepEqual(encoding, expected.Encoding) {
			result.NativeParity = false
		}
		result.TokenizerCases++
		if worker != nil {
			row.Embedding, err = worker.EmbedText(ctx, expected.Text)
			if err != nil {
				return err
			}
			maximum, cosine, ok := compareVectors(row.Embedding, expected.Embedding)
			if !ok {
				result.NativeParity = false
			}
			result.MaximumAbsoluteError = math.Max(result.MaximumAbsoluteError, maximum)
			result.MinimumCosine = math.Min(result.MinimumCosine, cosine)
			result.EmbeddingCases++
		}
		row.Millis = float64(time.Since(started).Microseconds()) / 1000
		result.Cases = append(result.Cases, row)
	}
	if worker == nil {
		result.MinimumCosine = 0
	}
	if err = writeReport(*output, result); err != nil {
		return err
	}
	if err = json.NewEncoder(out).Encode(struct {
		Passed              bool   `json:"passed"`
		TokenizerCases      int    `json:"tokenizerCases"`
		EmbeddingCases      int    `json:"embeddingCases"`
		Output              string `json:"output"`
		SemanticCalibration bool   `json:"semanticCalibration"`
	}{result.NativeParity, result.TokenizerCases, result.EmbeddingCases, *output, false}); err != nil {
		return err
	}
	if !result.NativeParity {
		return errors.New("native/reference parity failed; actual outputs retained")
	}
	return nil
}

func readReference(path string, kind nlu.ModelKind) (parityReport, []byte, error) {
	var result parityReport
	f, err := os.Open(path)
	if err != nil {
		return result, nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if err != nil {
		return result, nil, err
	}
	if len(raw) > 8<<20 {
		return result, nil, errors.New("reference exceeds 8 MiB limit")
	}
	if err = json.Unmarshal(bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf}), &result); err != nil {
		return result, nil, err
	}
	limit := 256
	if kind == nlu.DistilBERT {
		limit = 512
	}
	if result.Version != 1 || result.Kind != kind || result.OffsetUnit != "utf8-bytes" || result.MaxTokens != limit || len(result.Cases) == 0 || len(result.Cases) > 512 {
		return result, nil, errors.New("incompatible reference contract")
	}
	for _, c := range result.Cases {
		if len(c.Text) > 16<<10 || len(c.IDs) < 2 || len(c.IDs) > limit || len(c.Tokens) != len(c.IDs) || len(c.AttentionMask) != len(c.IDs) || len(c.TypeIDs) != len(c.IDs) {
			return result, nil, errors.New("invalid reference case")
		}
		if kind == nlu.MiniLM && len(c.Embedding) != nlu.EmbeddingDimension {
			return result, nil, errors.New("MiniLM verification requires original-model reference embeddings")
		}
	}
	return result, raw, nil
}

func compareVectors(actual, expected []float32) (float64, float64, bool) {
	if len(actual) != nlu.EmbeddingDimension || len(expected) != len(actual) {
		return 1, 0, false
	}
	var maximum, dot, normA, normB float64
	for i, v := range actual {
		a, b := float64(v), float64(expected[i])
		if math.IsNaN(a) || math.IsNaN(b) || math.IsInf(a, 0) || math.IsInf(b, 0) {
			return 1, 0, false
		}
		maximum = math.Max(maximum, math.Abs(a-b))
		dot += a * b
		normA += a * a
		normB += b * b
	}
	if normA <= 0 || normB <= 0 {
		return 1, 0, false
	}
	cosine := dot / math.Sqrt(normA*normB)
	return maximum, cosine, maximum <= 0.001 && cosine >= 0.99999 && math.Abs(math.Sqrt(normA)-1) <= 0.0001
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func writeReport(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".nlu-parity-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(raw); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
