package llama

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server manages a llama runtime child process bound to loopback.
type Server struct {
	bin     string
	subcmd  []string // e.g. ["serve"] for the unified `llama` binary
	model   string
	nCtx    int
	threads int
	ngl     int
	device  string
	log     *slog.Logger

	mu   sync.Mutex
	cmd  *exec.Cmd
	port int
	done chan struct{} // closed when the process exits
}

// ServerOptions configures a Server.
type ServerOptions struct {
	BinaryPath string   // path to llama-server or the unified llama binary
	Subcmd     []string // prepended to the args (["serve"] for unified llama)
	ModelPath  string   // path to the GGUF
	NCtx       int      // context size (0 → 4096)
	NThreads   int      // 0 → let the runtime decide
	GPULayers  int      // >0 that many; 0 offload all; <0 force CPU
	Device     string   // optional llama.cpp device ID, such as CUDA0
	Logger     *slog.Logger
}

func newServer(o ServerOptions) *Server {
	nctx := o.NCtx
	if nctx <= 0 {
		nctx = 4096
	}
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		bin: o.BinaryPath, subcmd: o.Subcmd, model: o.ModelPath,
		nCtx: nctx, threads: o.NThreads, ngl: o.GPULayers, device: o.Device, log: log,
	}
}

// Start launches the process and blocks until /health is green or ctx is done.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.cmd != nil {
		s.mu.Unlock()
		return nil
	}

	port, err := freePort()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	args := append([]string{}, s.subcmd...)
	args = append(args,
		"--model", s.model,
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--ctx-size", strconv.Itoa(s.nCtx),
	)
	if s.threads > 0 {
		args = append(args, "--threads", strconv.Itoa(s.threads))
	}
	if s.device != "" && s.ngl >= 0 {
		args = append(args, "--device", s.device)
	}
	// GPU offload. 0 (the default) passes nothing — a GPU build of llama.cpp
	// already offloads every layer by default, and a CPU build has nowhere to
	// offload. >0 pins the layer count; <0 forces CPU (n-gpu-layers 0).
	switch {
	case s.ngl > 0:
		args = append(args, "--n-gpu-layers", strconv.Itoa(s.ngl))
	case s.ngl < 0:
		args = append(args, "--n-gpu-layers", "0")
	}

	cmd := exec.Command(s.bin, args...) //nolint:gosec // bin/model come from validated config
	cmd.Stdout = logWriter{s.log, "llama-server"}
	cmd.Stderr = logWriter{s.log, "llama-server"}
	if err := cmd.Start(); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("start llama runtime: %w", err)
	}

	done := make(chan struct{})
	s.cmd, s.port, s.done = cmd, port, done
	s.mu.Unlock()

	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	return s.waitHealthy(ctx)
}

func (s *Server) waitHealthy(ctx context.Context) error {
	cli := NewClient(s.BaseURL())
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = s.Stop()
			return fmt.Errorf("llama-server did not become healthy: %w", ctx.Err())
		case <-s.done:
			return errors.New("llama-server exited during startup")
		case <-tick.C:
			hctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			ok := cli.Healthy(hctx)
			cancel()
			if ok {
				s.log.Info("llama-server healthy", "port", s.port)
				return nil
			}
		}
	}
}

// Alive reports whether the process is still running.
func (s *Server) Alive() bool {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done == nil {
		return false
	}
	select {
	case <-done:
		return false
	default:
		return true
	}
}

// ResidentBytes reports the managed runtime's current resident set on Linux.
// Other platforms return zero because there is no portable process-RSS API.
func (s *Server) ResidentBytes() int64 {
	if runtime.GOOS != "linux" {
		return 0
	}
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(cmd.Process.Pid) + "/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "VmRSS:" && fields[2] == "kB" {
			value, err := strconv.ParseInt(fields[1], 10, 64)
			if err == nil {
				return value * 1024
			}
		}
	}
	return 0
}

// BaseURL is the loopback URL of the server.
func (s *Server) BaseURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return "http://127.0.0.1:" + strconv.Itoa(s.port)
}

// Restart stops and starts the process.
func (s *Server) Restart(ctx context.Context) error {
	_ = s.Stop()
	s.mu.Lock()
	s.cmd, s.done = nil, nil
	s.mu.Unlock()
	return s.Start(ctx)
}

// Stop signals the process and waits, then kills it if it lingers.
func (s *Server) Stop() error {
	s.mu.Lock()
	cmd, done := s.cmd, s.done
	s.cmd, s.done = nil, nil
	s.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return nil
	}
	_ = cmd.Process.Signal(interruptSignal())
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
	return nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

type logWriter struct {
	log *slog.Logger
	tag string
}

func (w logWriter) Write(p []byte) (int, error) {
	w.log.Debug(w.tag, "line", string(p))
	return len(p), nil
}
