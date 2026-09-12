package audio

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/process"
)

const MERTWorkerProtocol = 1

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
}

// Worker serializes CPU inference and isolates decoder/runtime native failures
// in a managed child. Cancellation kills and reaps it before releasing buffers.
type MERTWorker struct {
	Executable string
	BundleDir  string
	Model      core.AudioRepresentationIdentity
	mu         sync.Mutex
	closed     bool
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     io.ReadCloser
}

func (w *MERTWorker) Identity() core.AudioRepresentationIdentity { return w.Model }
func (w *MERTWorker) EmbedAudio(ctx context.Context, pcm []float32) ([]float32, error) {
	if len(pcm) < 400 || len(pcm) > MERTSegmentSamples {
		return nil, fmt.Errorf("audio: invalid MERT segment length")
	}
	return w.call(ctx, MERTWorkerRequest{Protocol: MERTWorkerProtocol, Audio: pcm})
}
func (w *MERTWorker) Health(ctx context.Context) error {
	_, err := w.call(ctx, MERTWorkerRequest{Protocol: MERTWorkerProtocol, Health: true})
	return err
}

func (w *MERTWorker) call(ctx context.Context, request MERTWorkerRequest) ([]float32, error) {
	for !w.mu.TryLock() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	defer w.mu.Unlock()
	if w.closed {
		return nil, fmt.Errorf("audio: worker is closed")
	}
	ctx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if w.cmd == nil {
		executable, flag := w.Executable, "--bundle"
		if executable == "" {
			var err error
			executable, err = os.Executable()
			if err != nil {
				return nil, err
			}
			flag = "--mert-worker"
		}
		cmd := exec.Command(executable, flag, w.BundleDir) //nolint:gosec // verified managed bundle or app's own isolated worker
		process.Background(cmd)
		cmd.Stderr = io.Discard
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			_ = stdin.Close()
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			_ = stdin.Close()
			_ = stdout.Close()
			return nil, fmt.Errorf("audio: worker failed to start")
		}
		w.cmd, w.stdin, w.stdout = cmd, stdin, stdout
	}
	type answer struct {
		response MERTWorkerResponse
		err      error
	}
	done := make(chan answer, 1)
	go func() {
		var a answer
		a.err = WriteFrame(w.stdin, request)
		if a.err == nil {
			a.err = ReadFrame(w.stdout, &a.response, 1<<20)
		}
		done <- a
	}()
	select {
	case <-ctx.Done():
		w.stopLocked()
		<-done
		return nil, ctx.Err()
	case result := <-done:
		if result.err != nil || result.response.Error != "" || result.response.Protocol != MERTWorkerProtocol || result.response.Model != w.Model {
			w.stopLocked()
			return nil, fmt.Errorf("audio: worker failed or model is incompatible")
		}
		if !request.Health && (w.Model.Dimension != MERTDimension || !MERTParity(result.response.Vector, result.response.Vector)) {
			w.stopLocked()
			return nil, fmt.Errorf("audio: invalid embedding")
		}
		return result.response.Vector, nil
	}
}
func (w *MERTWorker) stopLocked() {
	if w.cmd != nil {
		_ = w.cmd.Process.Kill()
		_ = w.stdin.Close()
		_ = w.stdout.Close()
		_ = w.cmd.Wait()
		w.cmd = nil
	}
}

// Unload releases native memory when analysis is disabled. A later enabled
// request can start this same validated worker again.
func (w *MERTWorker) Unload() { w.mu.Lock(); defer w.mu.Unlock(); w.stopLocked() }

// Close retires the worker permanently, including references held by a
// superseded generation during model replacement or application shutdown.
func (w *MERTWorker) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	w.stopLocked()
	return nil
}
