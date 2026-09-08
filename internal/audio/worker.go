package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/process"
)

const WorkerProtocol = 1

type WorkerRequest struct {
	Protocol int
	Audio    []float32
	Text     string
	Health   bool
}
type WorkerResponse struct {
	Protocol int
	Vector   []float32
	Error    string
	Model    core.AudioModelIdentity
}

// Frames are bounded and cleared on both sides. No payloads go to argv, files,
// environment variables, logs, or a network listener.
func WriteFrame(w io.Writer, value any) error {
	var buffer bytes.Buffer
	defer func() { clear(buffer.Bytes()) }()
	if err := gob.NewEncoder(&buffer).Encode(value); err != nil {
		return err
	}
	if buffer.Len() > 16<<20 {
		return fmt.Errorf("audio worker: oversized frame")
	}
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(buffer.Len())) //nolint:gosec // bounded above
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(buffer.Bytes())
	return err
}
func ReadFrame(r io.Reader, value any, limit uint32) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	length := binary.LittleEndian.Uint32(header[:])
	if length == 0 || length > limit {
		return fmt.Errorf("audio worker: invalid frame size")
	}
	data := make([]byte, length)
	defer clear(data)
	if _, err := io.ReadFull(r, data); err != nil {
		return err
	}
	return gob.NewDecoder(bytes.NewReader(data)).Decode(value)
}

// Worker serializes CPU inference and isolates decoder/runtime native failures
// in a managed child. Cancellation kills and reaps it before releasing buffers.
type Worker struct {
	Executable string
	BundleDir  string
	Model      core.AudioModelIdentity
	mu         sync.Mutex
	closed     bool
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     io.ReadCloser
}

func (w *Worker) Identity() core.AudioModelIdentity { return w.Model }
func (w *Worker) EmbedAudio(ctx context.Context, pcm []float32) ([]float32, error) {
	return w.call(ctx, WorkerRequest{Protocol: WorkerProtocol, Audio: pcm})
}
func (w *Worker) EmbedText(ctx context.Context, text string) ([]float32, error) {
	if len(text) > 4096 {
		return nil, fmt.Errorf("audio: clause exceeds tokenizer input limit")
	}
	return w.call(ctx, WorkerRequest{Protocol: WorkerProtocol, Text: text})
}
func (w *Worker) Health(ctx context.Context) error {
	_, err := w.call(ctx, WorkerRequest{Protocol: WorkerProtocol, Health: true})
	return err
}

func (w *Worker) call(ctx context.Context, request WorkerRequest) ([]float32, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil, fmt.Errorf("audio: worker is closed")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
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
			flag = "--audio-worker"
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
		response WorkerResponse
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
		if result.err != nil || result.response.Error != "" || result.response.Protocol != WorkerProtocol || result.response.Model != w.Model {
			w.stopLocked()
			return nil, fmt.Errorf("audio: worker failed or model is incompatible")
		}
		if !request.Health && !validVector(result.response.Vector, w.Model.Dimension) {
			w.stopLocked()
			return nil, fmt.Errorf("audio: invalid embedding")
		}
		return result.response.Vector, nil
	}
}
func (w *Worker) stopLocked() {
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
func (w *Worker) Unload() { w.mu.Lock(); defer w.mu.Unlock(); w.stopLocked() }

// Close retires the worker permanently, including references held by a
// superseded generation during model replacement or application shutdown.
func (w *Worker) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	w.stopLocked()
	return nil
}
