package llama

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// Options configure a llama-backed Parser.
type Options struct {
	// BinaryPath to llama-server. If empty, it is looked for next to the running
	// executable and then on PATH.
	BinaryPath string
	// ModelPath to the GGUF. Required.
	ModelPath string
	NCtx      int
	NThreads  int
	// StartTimeout for the server to become healthy (default 90s).
	StartTimeout time.Duration
	Logger       *slog.Logger
}

// Parser implements ports.IntentParser against a local llama-server.
type Parser struct {
	srv *Server
	cli *Client
	log *slog.Logger

	mu    sync.Mutex
	ready bool
}

// New resolves the binary, starts llama-server, waits for health, and returns a
// ready Parser. The caller must Close it.
func New(ctx context.Context, o Options) (*Parser, error) {
	if o.ModelPath == "" {
		return nil, errors.New("llama: model path is required")
	}
	if fi, err := os.Stat(o.ModelPath); err != nil || fi.IsDir() || fi.Size() == 0 {
		return nil, fmt.Errorf("llama: model not usable: %s", o.ModelPath)
	}

	bin, err := resolveBinary(o.BinaryPath)
	if err != nil {
		return nil, err
	}
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}

	timeout := o.StartTimeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	sctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	srv := newServer(ServerOptions{
		BinaryPath: bin, ModelPath: o.ModelPath,
		NCtx: o.NCtx, NThreads: o.NThreads, Logger: log,
	})
	if err := srv.Start(sctx); err != nil {
		return nil, err
	}

	p := &Parser{srv: srv, cli: NewClient(srv.BaseURL()), log: log, ready: true}
	return p, nil
}

// NewWithClient wires a Parser to an already-running server (tests, or a shared
// server). No process is managed.
func NewWithClient(cli *Client) *Parser {
	return &Parser{cli: cli, log: slog.Default(), ready: true}
}

// Info implements ports.IntentParser.
func (p *Parser) Info() ports.ParserInfo {
	p.mu.Lock()
	ready := p.ready
	p.mu.Unlock()
	return ports.ParserInfo{Name: "llama", Backend: "llama", Ready: ready}
}

// Parse implements ports.IntentParser. If the request fails and the managed
// server has died, it restarts once and retries.
func (p *Parser) Parse(ctx context.Context, in ports.IntentInput) (core.MusicIntent, error) {
	m, err := p.cli.Parse(ctx, in)
	if err == nil {
		return m, nil
	}

	if p.srv == nil || p.srv.Alive() {
		return core.MusicIntent{}, err
	}

	p.log.Warn("llama-server died; restarting", "err", err)
	rctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if rerr := p.srv.Restart(rctx); rerr != nil {
		p.setReady(false)
		return core.MusicIntent{}, fmt.Errorf("llama: restart failed: %w", rerr)
	}
	p.cli = NewClient(p.srv.BaseURL())
	return p.cli.Parse(ctx, in)
}

// Close stops the managed server, if any.
func (p *Parser) Close() error {
	p.setReady(false)
	if p.srv != nil {
		return p.srv.Stop()
	}
	return nil
}

func (p *Parser) setReady(v bool) {
	p.mu.Lock()
	p.ready = v
	p.mu.Unlock()
}

// resolveBinary finds llama-server: explicit path → next to the executable →
// PATH.
func resolveBinary(explicit string) (string, error) {
	name := "llama-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}

	if explicit != "" {
		if fi, err := os.Stat(explicit); err == nil && !fi.IsDir() {
			return explicit, nil
		}
		return "", fmt.Errorf("llama: llama-server not found at %s", explicit)
	}

	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), name)
		if fi, serr := os.Stat(cand); serr == nil && !fi.IsDir() {
			return cand, nil
		}
	}

	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("llama: %s not found (set ai.llama_server_path or put it on PATH)", name)
}

var _ ports.IntentParser = (*Parser)(nil)
