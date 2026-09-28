package audio

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/platten/playlistai/internal/process"
)

const DiscogsWorkerProtocol = "discogs-effnet-worker/v1"

type DiscogsWorkerRequest struct {
	Protocol string    `json:"protocol"`
	Health   bool      `json:"health,omitempty"`
	Audio    []float32 `json:"audio,omitempty"` // observed 16 kHz mono PCM
}
type DiscogsWorkerResponse struct {
	Protocol string      `json:"protocol"`
	Scores   [][]float32 `json:"scores,omitempty"` // style, instrument, vocal, mood
	Error    string      `json:"error,omitempty"`
}

// DiscogsWorker isolates ONNX Runtime from the GUI and reuses loaded graphs.
type DiscogsWorker struct {
	ModelDir, RuntimeDir, Executable string
	mu                               sync.Mutex
	cmd                              *exec.Cmd
	stdin                            io.WriteCloser
	stdout                           io.ReadCloser
	closed                           bool
}

func (w *DiscogsWorker) Health(ctx context.Context) error {
	_, err := w.call(ctx, nil, true)
	return err
}
func (w *DiscogsWorker) Classify(ctx context.Context, pcm []float32) ([][]float32, error) {
	if len(pcm) < 3*discogsRate || len(pcm) > 60*discogsRate {
		return nil, fmt.Errorf("audio: invalid Discogs interval")
	}
	return w.call(ctx, pcm, false)
}
func (w *DiscogsWorker) call(ctx context.Context, pcm []float32, health bool) ([][]float32, error) {
	for !w.mu.TryLock() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	defer w.mu.Unlock()
	if w.closed {
		return nil, fmt.Errorf("audio: Discogs worker is closed")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if w.cmd == nil {
		executable := w.Executable
		if executable == "" {
			var err error
			executable, err = os.Executable()
			if err != nil {
				return nil, err
			}
		}
		cmd := exec.Command(executable, "--discogs-worker", w.ModelDir, w.RuntimeDir) //nolint:gosec // verified model/runtime directories and app executable
		process.Owned(cmd)
		cmd.Env = prependMERTLibraryPath(os.Environ(), w.RuntimeDir)
		cmd.Stderr = &boundedWorkerStderr{}
		in, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			_ = in.Close()
			return nil, err
		}
		if err = cmd.Start(); err != nil {
			_ = in.Close()
			_ = out.Close()
			return nil, fmt.Errorf("%w: Discogs worker start failed", ErrNativeWorker)
		}
		w.cmd, w.stdin, w.stdout = cmd, in, out
	}
	type result struct {
		response DiscogsWorkerResponse
		err      error
	}
	done := make(chan result, 1)
	go func() {
		var r result
		r.err = WriteFrame(w.stdin, DiscogsWorkerRequest{Protocol: DiscogsWorkerProtocol, Audio: pcm, Health: health})
		if r.err == nil {
			r.err = ReadFrame(w.stdout, &r.response, 1<<20)
		}
		done <- r
	}()
	select {
	case <-ctx.Done():
		w.stopLocked()
		<-done
		return nil, ctx.Err()
	case r := <-done:
		if r.err != nil || r.response.Protocol != DiscogsWorkerProtocol || r.response.Error != "" {
			w.stopLocked()
			return nil, fmt.Errorf("%w: Discogs inference failed", ErrNativeWorker)
		}
		if health {
			return nil, nil
		}
		for i, count := range []int{400, 40, 2, 2} {
			if len(r.response.Scores) != 4 || len(r.response.Scores[i]) != count {
				w.stopLocked()
				return nil, fmt.Errorf("%w: invalid Discogs output shape", ErrNativeWorker)
			}
		}
		return r.response.Scores, nil
	}
}
func (w *DiscogsWorker) stopLocked() {
	if w.cmd == nil {
		return
	}
	_ = w.cmd.Process.Kill()
	_ = w.stdin.Close()
	_ = w.stdout.Close()
	_ = w.cmd.Wait()
	w.cmd, w.stdin, w.stdout = nil, nil, nil
}
func (w *DiscogsWorker) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopLocked()
	w.closed = true
	return nil
}
