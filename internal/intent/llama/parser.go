package llama

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// Options configure a llama-backed Parser.
type Options struct {
	// BinaryPath to a llama runtime (`llama-server`, or the unified `llama`
	// binary run as `llama serve`). Used only when Runtimes is empty; if that
	// is also blank, DetectRuntime looks in the usual places (next to the
	// app, ~/.local/bin, ~/.llama-app, PATH).
	BinaryPath string
	// Runtimes, when set, is an ordered list of candidate runtimes: New tries
	// each in turn and keeps the first that starts and reports healthy. This
	// is how "GPU build, fall back to CPU build" works.
	Runtimes []Runtime
	// ModelPath to the GGUF. Required.
	ModelPath string
	NCtx      int
	NThreads  int
	// GPULayers passed through to the runtime: >0 → pin that many layers to
	// the GPU; 0 → leave it to the build (a GPU build already offloads
	// everything); <0 → force CPU. The runtime labeled "cpu" always forces CPU.
	GPULayers int
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

	candidates := o.Runtimes
	if len(candidates) == 0 {
		rt := DetectRuntime(o.BinaryPath)
		if !rt.Available {
			return nil, errRuntimeMissing
		}
		candidates = []Runtime{rt.asRuntime()}
	}

	log := o.Logger
	if log == nil {
		log = slog.Default()
	}

	timeout := o.StartTimeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}

	var lastErr error
	for i, rt := range candidates {
		// GPU offload only makes sense for a GPU-capable build; force CPU
		// (-1) for the one explicitly labeled "cpu".
		ngl := o.GPULayers
		if rt.Label == "cpu" {
			ngl = -1
		}
		srv := newServer(ServerOptions{
			BinaryPath: rt.Path, Subcmd: rt.subcmd(), ModelPath: o.ModelPath,
			NCtx: o.NCtx, NThreads: o.NThreads, GPULayers: ngl, Logger: log,
		})
		sctx, cancel := context.WithTimeout(ctx, timeout)
		err := srv.Start(sctx)
		cancel()
		if err == nil {
			if i > 0 || rt.Label == "cpu" {
				log.Info("llama runtime selected", "path", rt.Path, "label", rt.Label)
			}
			return &Parser{srv: srv, cli: NewClient(srv.BaseURL()), log: log, ready: true}, nil
		}
		lastErr = err
		if i+1 < len(candidates) {
			log.Warn("llama runtime failed to start; trying the next one",
				"path", rt.Path, "label", rt.Label, "err", err)
		}
	}
	return nil, lastErr
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
	return ports.ParserInfo{Name: "llama", Backend: "llama", Version: "llama/v3", Ready: ready, ContractVersion: core.CurrentIntentVersion, Evidence: true}
}

// Parse implements ports.IntentParser. If the request fails and the managed
// server has died, it restarts once and retries.
func (p *Parser) Parse(ctx context.Context, in ports.IntentInput) (core.MusicIntent, error) {
	return p.parse(ctx, in, nil)
}

// ParseWithProgress is Parse with a progress reporter: while the model streams
// its output, prog is called under op "intent" with the running character
// count (total is -1 — indeterminate). onDelta is best-effort; a nil prog is
// fine.
func (p *Parser) ParseWithProgress(ctx context.Context, in ports.IntentInput, prog ports.Progress) (core.MusicIntent, error) {
	var onDelta func(int)
	if prog != nil {
		onDelta = func(chars int) { prog.Report(IntentProgressOp, int64(chars), -1, "understanding your request") }
	}
	return p.parse(ctx, in, onDelta)
}

// IntentProgressOp is the progress op label for a running intent parse.
const IntentProgressOp = "intent"

func (p *Parser) parse(ctx context.Context, in ports.IntentInput, onDelta func(int)) (core.MusicIntent, error) {
	m, err := p.cli.ParseWithProgress(ctx, in, onDelta)
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
	return p.cli.ParseWithProgress(ctx, in, onDelta)
}

// titleSystemPrompt steers Complete toward a terse playlist name.
const titleSystemPrompt = "You name music playlists. Given a listener's request, reply with ONLY a 2 to 5 word playlist title. " +
	"No quotation marks, no trailing punctuation, no explanation."

// Title asks the model for a short playlist name for prompt. It returns an
// empty string (no error) when the parser is not ready or the model gives
// nothing usable — callers fall back to a locally derived name.
func (p *Parser) Title(ctx context.Context, prompt string) string {
	p.mu.Lock()
	ready, cli := p.ready, p.cli
	p.mu.Unlock()
	if !ready || cli == nil {
		return ""
	}
	out, err := cli.Complete(ctx, titleSystemPrompt, prompt, 24)
	if err != nil {
		p.log.Warn("llama title generation failed", "err", err)
		return ""
	}
	return out
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

// RuntimeMemoryBytes returns the managed llama process's resident set when the
// host platform exposes it. It is intended for local evaluation, not ranking.
func (p *Parser) RuntimeMemoryBytes() int64 {
	if p.srv == nil {
		return 0
	}
	return p.srv.ResidentBytes()
}

var _ ports.IntentParser = (*Parser)(nil)
