package nlu

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/process"
)

const WorkerProtocol = 1

type WorkerRequest struct {
	Protocol int
	Text     string
	Health   bool
}

type WorkerResponse struct {
	Protocol    int
	Kind        ModelKind
	ModelSHA256 string
	Embedding   []float32
	Result      Result
	Error       string
}

type nativeModel interface {
	Infer(Encoding) ([]float32, error)
	Close() error
}

var ErrNativeUnavailable = errors.New("nlu: native ONNX Runtime build is unavailable")

// RunWorker is an entry point for the app's --nlu-worker child. Text travels
// through bounded pipes, never arguments, logs, environment or a network port.
// The process is isolated from the audio workers' global ONNX environments.
func RunWorker(config WorkerConfig) error {
	settings, err := readSettings(config)
	if err != nil {
		return err
	}
	var model nativeModel
	if settings.abstention == "" {
		model, err = loadNativeModel(config, settings)
		if err != nil {
			return err
		}
		defer func() { _ = model.Close() }()
	}
	return serveWorker(os.Stdin, os.Stdout, config.Kind, settings, model)
}

func serveWorker(input io.Reader, output io.Writer, kind ModelKind, settings modelSettings, model nativeModel) error {
	for {
		var request WorkerRequest
		if err := audio.ReadFrame(input, &request, 1<<20); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if request.Protocol != WorkerProtocol {
			return fmt.Errorf("nlu: protocol mismatch")
		}
		response := WorkerResponse{Protocol: WorkerProtocol, Kind: kind, ModelSHA256: settings.digest}
		text := request.Text
		if request.Health {
			text = "quiet music"
		}
		if len(text) > 16<<10 {
			response.Error = "input_limit"
		} else if settings.abstention != "" {
			response.Result = Result{Abstained: true, Reason: settings.abstention}
		} else {
			encoding, err := settings.tokenizer.Encode(text)
			if err == nil {
				var values []float32
				values, err = model.Infer(encoding)
				if err == nil {
					if kind == MiniLM {
						response.Embedding, err = MeanPool(values, encoding.AttentionMask, EmbeddingDimension)
					} else {
						response.Result, err = DecodeProposals(text, encoding, values, *settings.head, *settings.calibration)
					}
				}
				clear(values)
			}
			if err != nil {
				response.Error = "inference_unavailable"
			}
		}
		if err := audio.WriteFrame(output, response); err != nil {
			return err
		}
		clear(response.Embedding)
	}
}

// Worker serializes bounded CPU inference. Cancellation kills and reaps the
// owned process before a later request can start a fresh isolated worker.
type Worker struct {
	Executable string
	Config     WorkerConfig
	// ExpectedModelSHA256, when provided by verified setup, prevents accepting
	// responses after a model directory was replaced under an existing worker.
	ExpectedModelSHA256 string
	mu                  sync.Mutex
	closed              bool
	cmd                 *exec.Cmd
	stdin               io.WriteCloser
	stdout              io.ReadCloser
}

func (w *Worker) EmbedText(ctx context.Context, text string) ([]float32, error) {
	if w.Config.Kind != MiniLM {
		return nil, fmt.Errorf("nlu: model does not provide sentence embeddings")
	}
	response, err := w.call(ctx, WorkerRequest{Protocol: WorkerProtocol, Text: text})
	return response.Embedding, err
}

func (w *Worker) Propose(ctx context.Context, text string) (Result, error) {
	if w.Config.Kind != DistilBERT {
		return Result{}, fmt.Errorf("nlu: model does not provide intent proposals")
	}
	response, err := w.call(ctx, WorkerRequest{Protocol: WorkerProtocol, Text: text})
	return response.Result, err
}

func (w *Worker) Health(ctx context.Context) error {
	response, err := w.call(ctx, WorkerRequest{Protocol: WorkerProtocol, Health: true})
	if err == nil && w.Config.Kind == DistilBERT && response.Result.Abstained && response.Result.Reason != "no_confident_proposals" {
		return fmt.Errorf("nlu: trained extractor health unavailable")
	}
	return err
}

func (w *Worker) call(ctx context.Context, request WorkerRequest) (WorkerResponse, error) {
	if len(request.Text) > 16<<10 {
		return WorkerResponse{}, ErrInputTooLong
	}
	if err := ctx.Err(); err != nil {
		return WorkerResponse{}, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return WorkerResponse{}, fmt.Errorf("nlu: worker closed")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return WorkerResponse{}, err
	}
	if w.cmd == nil {
		executable := w.Executable
		if executable == "" {
			var err error
			executable, err = os.Executable()
			if err != nil {
				return WorkerResponse{}, err
			}
		}
		cmd := exec.Command(executable, "--nlu-worker", string(w.Config.Kind), w.Config.ModelDir, w.Config.RuntimeLibrary) //nolint:gosec // own executable and verified local model/runtime paths
		process.Background(cmd)
		cmd.Stderr = io.Discard
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return WorkerResponse{}, err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			_ = stdin.Close()
			return WorkerResponse{}, err
		}
		if err = cmd.Start(); err != nil {
			_ = stdin.Close()
			_ = stdout.Close()
			return WorkerResponse{}, fmt.Errorf("nlu: worker failed to start")
		}
		w.cmd, w.stdin, w.stdout = cmd, stdin, stdout
	}
	type answer struct {
		response WorkerResponse
		err      error
	}
	done := make(chan answer, 1)
	stdin, stdout := w.stdin, w.stdout
	go func() {
		var result answer
		result.err = audio.WriteFrame(stdin, request)
		if result.err == nil {
			result.err = audio.ReadFrame(stdout, &result.response, 1<<20)
		}
		done <- result
	}()
	select {
	case <-ctx.Done():
		w.stopLocked()
		<-done
		return WorkerResponse{}, ctx.Err()
	case result := <-done:
		if result.err != nil || result.response.Error != "" || result.response.Protocol != WorkerProtocol || result.response.Kind != w.Config.Kind || (w.ExpectedModelSHA256 != "" && result.response.ModelSHA256 != w.ExpectedModelSHA256) {
			w.stopLocked()
			return WorkerResponse{}, fmt.Errorf("nlu: worker failed or model is incompatible")
		}
		if w.Config.Kind == MiniLM && !validEmbedding(result.response.Embedding) {
			w.stopLocked()
			return WorkerResponse{}, fmt.Errorf("nlu: invalid sentence embedding")
		}
		if w.Config.Kind == DistilBERT && !request.Health {
			if err := validateProposals(request.Text, result.response.Result); err != nil {
				w.stopLocked()
				return WorkerResponse{}, err
			}
		}
		return result.response, nil
	}
}

func validEmbedding(vector []float32) bool {
	if len(vector) != EmbeddingDimension {
		return false
	}
	var sum float64
	for _, v := range vector {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return false
		}
		sum += float64(v) * float64(v)
	}
	return math.Abs(sum-1) < 0.001
}

func validateProposals(text string, result Result) error {
	if result.Abstained && len(result.Proposals) > 0 {
		return fmt.Errorf("nlu: inconsistent abstention")
	}
	if !result.Abstained && len(result.Proposals) == 0 {
		return fmt.Errorf("nlu: empty extraction must abstain")
	}
	for _, p := range result.Proposals {
		if p.Start < 0 || p.End <= p.Start || p.End > len(text) || !utf8.ValidString(text[:p.Start]) || !utf8.ValidString(text[:p.End]) || p.Text != text[p.Start:p.End] || p.Score < 0.5 || p.Score > 1 || math.IsNaN(p.Score) || len(p.Label) == 0 || len(p.Label) > 100 {
			return fmt.Errorf("nlu: invalid source proposal")
		}
	}
	return nil
}

func (w *Worker) stopLocked() {
	if w.cmd != nil {
		_ = w.cmd.Process.Kill()
		_ = w.stdin.Close()
		_ = w.stdout.Close()
		_ = w.cmd.Wait()
		w.cmd, w.stdin, w.stdout = nil, nil, nil
	}
}

func (w *Worker) Unload() { w.mu.Lock(); defer w.mu.Unlock(); w.stopLocked() }

func (w *Worker) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	w.stopLocked()
	return nil
}
