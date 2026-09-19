package audio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/process"
)

const (
	MERTWorkerProtocol        = 1
	MERTWorkerResponseTimeout = 30 * time.Second
	MERTCUDAHealthTimeout     = 2 * time.Minute
)

type MERTWorkerRequest struct {
	Protocol int
	Audio    []float32
	Health   bool
}
type MERTWorkerResponse struct {
	Protocol int
	Vector   []float32
	Error    string
	Model    core.AudioRepresentationIdentity
	Timings  MERTWorkerTimings
}

type MERTWorkerTimings struct {
	Preprocessing time.Duration `json:"preprocessing"`
	Execution     time.Duration `json:"execution"`
}

// Worker serializes CPU inference and isolates decoder/runtime native failures
// in a managed child. Cancellation kills and reaps it before releasing buffers.
type MERTWorker struct {
	Executable string
	BundleDir  string
	Model      core.AudioRepresentationIdentity
	// Device is "cpu" or "cuda[:index]". CUDA requires a CUDA-capable,
	// parity-validated runtime bundle; it never silently falls back to a CPU-only
	// runtime.
	Device string
	// InferenceThreads is the explicit native intra-operation budget. Zero
	// retains the conservative legacy default of two threads.
	InferenceThreads int
	// ResponseTimeout bounds a silent native request. Zero uses the production
	// 30-second watchdog. It is configurable for deterministic process tests.
	ResponseTimeout time.Duration
	diagnostics     MERTWorkerDiagnostics
	startedOnce     bool
	mu              sync.Mutex
	closed          bool
	cmd             *exec.Cmd
	stdin           io.WriteCloser
	stdout          io.ReadCloser
	stderr          *boundedWorkerStderr
}

type boundedWorkerStderr struct {
	mu   sync.Mutex
	data []byte
}

func (w *boundedWorkerStderr) Write(value []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := (16 << 10) - len(w.data)
	if remaining > 0 {
		w.data = append(w.data, value[:min(len(value), remaining)]...)
	}
	return len(value), nil
}

func (w *boundedWorkerStderr) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.TrimSpace(string(w.data))
}

func (w *MERTWorker) Identity() core.AudioRepresentationIdentity { return w.Model }
func (w *MERTWorker) EffectiveDevice() string {
	if w.Device != "" {
		return w.Device
	}
	if strings.HasSuffix(w.Model.Runtime, "/cuda") {
		return "cuda:0"
	}
	return "cpu"
}
func (w *MERTWorker) EmbedAudio(ctx context.Context, pcm []float32) ([]float32, error) {
	vector, _, err := w.EmbedAudioWithTimings(ctx, pcm)
	return vector, err
}
func (w *MERTWorker) EmbedAudioWithTimings(ctx context.Context, pcm []float32) ([]float32, MERTWorkerTimings, error) {
	if len(pcm) < 400 || len(pcm) > MERTSegmentSamples {
		return nil, MERTWorkerTimings{}, fmt.Errorf("audio: invalid MERT segment length")
	}
	return w.call(ctx, MERTWorkerRequest{Protocol: MERTWorkerProtocol, Audio: pcm})
}
func (w *MERTWorker) Health(ctx context.Context) error {
	_, _, err := w.call(ctx, MERTWorkerRequest{Protocol: MERTWorkerProtocol, Health: true})
	return err
}

func (w *MERTWorker) call(ctx context.Context, request MERTWorkerRequest) (vector []float32, timings MERTWorkerTimings, callErr error) {
	for !w.mu.TryLock() {
		select {
		case <-ctx.Done():
			return nil, MERTWorkerTimings{}, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	defer w.mu.Unlock()
	started := time.Now()
	restarting := false
	defer func() {
		if errors.Is(callErr, ErrNativeWorker) {
			w.diagnostics.NativeFailures++
		}
		if request.Health {
			w.diagnostics.HealthChecks++
			w.diagnostics.HealthDuration += time.Since(started)
		}
		if restarting {
			w.diagnostics.RestartDuration += time.Since(started)
		}
	}()
	if w.closed {
		return nil, MERTWorkerTimings{}, fmt.Errorf("audio: worker is closed")
	}
	parent := ctx
	timeout := w.ResponseTimeout
	if timeout <= 0 {
		timeout = MERTWorkerResponseTimeout
		if request.Health {
			if _, cuda := MERTCUDADeviceIndex(w.EffectiveDevice()); cuda {
				timeout = MERTCUDAHealthTimeout
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, MERTWorkerTimings{}, err
	}
	if w.cmd == nil {
		executable, flag := w.Executable, "--mert-worker"
		if executable == "" {
			var err error
			executable, err = os.Executable()
			if err != nil {
				return nil, MERTWorkerTimings{}, err
			}
		}
		cmd := exec.Command(executable, flag, w.BundleDir) //nolint:gosec // verified managed bundle or app's own isolated worker
		process.Owned(cmd)
		threads := w.InferenceThreads
		if threads <= 0 {
			threads = 2
		}
		cmd.Env = append(os.Environ(), fmt.Sprintf("PLAYLISTAI_MERT_INTRA_THREADS=%d", threads))
		cmd.Env = append(cmd.Env, "PLAYLISTAI_MERT_DEVICE="+w.EffectiveDevice())
		// ONNX Runtime loads execution-provider libraries after the main shared
		// library. Keep verified app-local provider libraries discoverable in the
		// isolated worker without modifying the parent process environment.
		if w.BundleDir != "" {
			cmd.Env = prependMERTLibraryPath(cmd.Env, w.BundleDir)
		}
		workerStderr := &boundedWorkerStderr{}
		cmd.Stderr = workerStderr
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, MERTWorkerTimings{}, err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			_ = stdin.Close()
			return nil, MERTWorkerTimings{}, err
		}
		if err := cmd.Start(); err != nil {
			_ = stdin.Close()
			_ = stdout.Close()
			return nil, MERTWorkerTimings{}, fmt.Errorf("%w: failed to start", ErrNativeWorker)
		}
		restarting = w.startedOnce
		if restarting {
			w.diagnostics.Restarts++
		}
		w.startedOnce = true
		w.cmd, w.stdin, w.stdout, w.stderr = cmd, stdin, stdout, workerStderr
	}
	type answer struct {
		response MERTWorkerResponse
		err      error
	}
	done := make(chan answer, 1)
	go func() {
		var a answer
		a.err = WriteMERTRequest(w.stdin, request)
		if a.err == nil {
			a.err = ReadFrame(w.stdout, &a.response, 1<<20)
		}
		done <- a
	}()
	select {
	case <-ctx.Done():
		w.stopLocked()
		<-done
		if parent.Err() != nil {
			return nil, MERTWorkerTimings{}, parent.Err()
		}
		return nil, MERTWorkerTimings{}, fmt.Errorf("%w: no response for %s: %w", ErrNativeWorker, timeout, context.DeadlineExceeded)
	case result := <-done:
		if result.err != nil || result.response.Error != "" || result.response.Protocol != MERTWorkerProtocol || result.response.Model != w.Model {
			detail := ""
			if w.stderr != nil {
				detail = w.stderr.String()
			}
			w.stopLocked()
			if detail != "" {
				return nil, MERTWorkerTimings{}, fmt.Errorf("%w: process exited or returned an incompatible response: %s", ErrNativeWorker, detail)
			}
			return nil, MERTWorkerTimings{}, fmt.Errorf("%w: process exited or returned an incompatible response", ErrNativeWorker)
		}
		if !request.Health && (w.Model.Dimension != MERTDimension || !MERTParity(result.response.Vector, result.response.Vector)) {
			w.stopLocked()
			return nil, MERTWorkerTimings{}, fmt.Errorf("%w: invalid embedding", ErrNativeWorker)
		}
		return result.response.Vector, result.response.Timings, nil
	}
}
func (w *MERTWorker) stopLocked() {
	if w.cmd != nil {
		_ = w.cmd.Process.Kill()
		_ = w.stdin.Close()
		_ = w.stdout.Close()
		_ = w.cmd.Wait()
		w.cmd = nil
		w.stderr = nil
	}
}

// Unload releases native memory when analysis is disabled. A later enabled
// request can start this same validated worker again.
func (w *MERTWorker) Unload() { w.mu.Lock(); defer w.mu.Unlock(); w.stopLocked() }

func (w *MERTWorker) ResidentBytes() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cmd == nil || w.cmd.Process == nil {
		return 0
	}
	return processResidentBytes(w.cmd.Process.Pid)
}

// Close retires the worker permanently, including references held by a
// superseded generation during model replacement or application shutdown.
func (w *MERTWorker) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	w.stopLocked()
	return nil
}

// MERTWorkerDiagnostics separates process replacements and fixture validation
// from failed native inference. Durations can overlap (a health check may restart).
type MERTWorkerDiagnostics struct {
	NativeFailures  int64
	Restarts        int64
	HealthChecks    int64
	HealthDuration  time.Duration
	RestartDuration time.Duration
}

func (w *MERTWorker) Diagnostics() MERTWorkerDiagnostics {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.diagnostics
}
