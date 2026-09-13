package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/assist"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/intent/nlu"
	"github.com/platten/playlistai/internal/musicconcepts"
	"github.com/platten/playlistai/internal/ports"
)

type intentAssistState struct {
	mu                sync.Mutex
	opMu              sync.Mutex
	enabled           bool
	installed         bool
	worker            *nlu.Worker
	mapper            *assist.Mapper
	extractor         *nlu.Worker
	extractorIdentity string
}

type IntentAssistStatus struct {
	Installed          bool   `json:"installed"`
	Enabled            bool   `json:"enabled"`
	DownloadBytes      int64  `json:"downloadBytes"`
	Detail             string `json:"detail"`
	ExtractorInstalled bool   `json:"extractorInstalled"`
	UnsupportedReason  string `json:"unsupportedReason,omitempty"`
}

func (c *Container) intentAssetRoot() string { return filepath.Join(c.cfg.DataDir, "intent-nlu") }

func (c *Container) wireIntentAssist() {
	s := &c.intentAssist
	if nlu.PlatformSupportError() != nil {
		return
	}
	s.enabled = config.LoadPrefs(c.cfg.DataDir).IntentAssistEnabled
	s.installed = nlu.AssetsReady(c.intentAssetRoot())
	if dir := config.LoadPrefs(c.cfg.DataDir).IntentExtractorDir; dir != "" && s.installed {
		if d, err := nlu.InspectExtractor(dir); err == nil {
			rt, _ := nlu.RuntimePath(nlu.AssetDir(c.intentAssetRoot()))
			s.extractor = &nlu.Worker{ExpectedModelSHA256: d.ModelSHA256, Config: nlu.WorkerConfig{Kind: nlu.DistilBERT, ModelDir: d.Directory, RuntimeLibrary: rt}}
			s.extractorIdentity = d.Identity
		}
	}
	c.RegisterCloser(func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.worker != nil {
			_ = s.worker.Close()
		}
		if s.extractor != nil {
			return s.extractor.Close()
		}
		return nil
	})
}

func (c *Container) GetIntentAssistStatus() IntentAssistStatus {
	if err := nlu.PlatformSupportError(); err != nil {
		return IntentAssistStatus{UnsupportedReason: err.Error(), Detail: err.Error()}
	}
	s := &c.intentAssist
	s.mu.Lock()
	defer s.mu.Unlock()
	detail := "MiniLM dictionary suggestions are experimental and optional. DistilBERT's base encoder is prepared for training; extraction stays inactive until a reviewed, calibrated task model is available."
	if s.extractor != nil {
		detail = "MiniLM dictionary mapping and the imported DistilBERT extractor provide optional suggestions to your local LLM. Explicit source facts remain authoritative."
	}
	return IntentAssistStatus{Installed: s.installed, Enabled: s.enabled, DownloadBytes: nlu.SetupBytes(), Detail: detail, ExtractorInstalled: s.extractor != nil}
}

func (c *Container) InstallIntentModels(ctx context.Context, p ports.Progress) error {
	return c.installIntentModels(ctx, "", p)
}

// InstallIntentModelPack consumes a segmented model manifest without enabling
// experimental suggestions or a trained extractor.
func (c *Container) InstallIntentModelPack(ctx context.Context, source string, p ports.Progress) error {
	if strings.TrimSpace(source) == "" {
		return fmt.Errorf("model pack source is required")
	}
	return c.installIntentModels(ctx, source, p)
}

func (c *Container) installIntentModels(ctx context.Context, source string, p ports.Progress) error {
	ctx, release := c.OperationContext(ctx)
	defer release()
	s := &c.intentAssist
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	var installErr error
	if source != "" {
		dir, cleanup, err := c.prepareModelPack(ctx, source, "intent-models", p)
		if err != nil {
			return err
		}
		defer cleanup()
		installErr = nlu.ImportAssets(ctx, c.intentAssetRoot(), dir, p)
	} else {
		installErr = nlu.InstallAssets(ctx, c.intentAssetRoot(), p)
	}
	if installErr != nil {
		return installErr
	}
	dir := nlu.AssetDir(c.intentAssetRoot())
	rt, err := nlu.RuntimePath(dir)
	if err != nil {
		return err
	}
	w := &nlu.Worker{ExpectedModelSHA256: nlu.ModelSHA256(nlu.MiniLM), Config: nlu.WorkerConfig{Kind: nlu.MiniLM, ModelDir: filepath.Join(dir, "minilm"), RuntimeLibrary: rt}}
	defer func() { _ = w.Close() }()
	if err = w.Health(ctx); err != nil {
		return fmt.Errorf("native intent model health check failed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.installed = true
	s.mu.Unlock()
	return nil
}

func (c *Container) SetIntentAssistEnabled(enabled bool) error {
	if enabled {
		if err := nlu.PlatformSupportError(); err != nil {
			return err
		}
	}
	s := &c.intentAssist
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	installed := s.installed
	s.mu.Unlock()
	if enabled && !installed {
		return fmt.Errorf("download the intent models before enabling dictionary suggestions")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("application is closed")
	}
	prefs, err := config.LoadPrefsChecked(c.cfg.DataDir)
	if err != nil {
		return err
	}
	prefs.IntentAssistEnabled = enabled
	if err := prefs.Save(c.cfg.DataDir); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = enabled
	if !enabled && s.worker != nil {
		_ = s.worker.Close()
		s.worker, s.mapper = nil, nil
	}
	if !enabled && s.extractor != nil {
		s.extractor.Unload()
	}
	return nil
}

func (c *Container) intentAssistIdentity() string {
	s := &c.intentAssist
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("%t/%t/%s/%s/%s/%s/%s", s.enabled, s.installed, nlu.AssetsIdentity(), assist.Version, musicconcepts.Version, lexicon.Version, s.extractorIdentity)
}

func (c *Container) intentSuggestions(ctx context.Context, prompt string, source *core.IntentTranslation) []core.IntentProposal {
	s := &c.intentAssist
	s.mu.Lock()
	if !s.enabled || !s.installed {
		s.mu.Unlock()
		return nil
	}
	if s.mapper == nil {
		dir := nlu.AssetDir(c.intentAssetRoot())
		rt, err := nlu.RuntimePath(dir)
		if err != nil {
			s.mu.Unlock()
			return nil
		}
		s.worker = &nlu.Worker{ExpectedModelSHA256: nlu.ModelSHA256(nlu.MiniLM), Config: nlu.WorkerConfig{Kind: nlu.MiniLM, ModelDir: filepath.Join(dir, "minilm"), RuntimeLibrary: rt}}
		s.mapper = &assist.Mapper{Embedder: s.worker, Identity: nlu.AssetsIdentity() + "/" + assist.Version}
	}
	m := s.mapper
	extractor, extractorID := s.extractor, s.extractorIdentity
	s.mu.Unlock()
	// Advisory processing has a bounded budget; its failure preserves the parser.
	bounded, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	proposals, err := m.ProposeWithSource(bounded, prompt, source)
	if err != nil {
		proposals = nil
	}
	if extractor != nil && bounded.Err() == nil {
		if result, err := extractor.Propose(bounded, prompt); err == nil && !result.Abstained {
			for _, p := range result.Proposals {
				kind, role, _ := strings.Cut(p.Label, ":")
				if p.Start < 0 || p.End > len(prompt) || p.End <= p.Start || prompt[p.Start:p.End] != p.Text {
					continue
				}
				proposals = append(proposals, core.IntentProposal{Origin: "distilbert", Kind: kind, Role: role, Value: p.Text, Source: core.SourceEvidence{Text: p.Text, Start: p.Start, End: p.End}, Model: extractorID, Score: p.Score, Advisory: true})
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled || s.mapper != m || s.extractor != extractor {
		return nil
	}
	return proposals
}

// InstallIntentExtractor imports an explicitly chosen, reviewed native task pack.
// It never activates the untrained encoder downloaded during ordinary setup.
func (c *Container) InstallIntentExtractor(ctx context.Context, directory string) error {
	ctx, release := c.OperationContext(ctx)
	defer release()
	s := &c.intentAssist
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	installed := s.installed
	s.mu.Unlock()
	if !installed {
		return fmt.Errorf("prepare the intent model runtime first")
	}
	d, err := nlu.ImportExtractor(ctx, directory, c.intentAssetRoot())
	if err != nil {
		return err
	}
	rt, err := nlu.RuntimePath(nlu.AssetDir(c.intentAssetRoot()))
	if err != nil {
		return err
	}
	w := &nlu.Worker{ExpectedModelSHA256: d.ModelSHA256, Config: nlu.WorkerConfig{Kind: nlu.DistilBERT, ModelDir: d.Directory, RuntimeLibrary: rt}}
	committed := false
	defer func() {
		if !committed {
			_ = w.Close()
		}
	}()
	if err := w.Health(ctx); err != nil {
		return err
	}
	if result, err := w.Propose(ctx, "music like Aerosmith"); err != nil {
		return err
	} else if result.Reason == "trained_head_unavailable" {
		return fmt.Errorf("trained extraction head unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("application is closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	prefs, err := config.LoadPrefsChecked(c.cfg.DataDir)
	if err != nil {
		return err
	}
	prefs.IntentExtractorDir = d.Directory
	if err := prefs.Save(c.cfg.DataDir); err != nil {
		return err
	}
	s.mu.Lock()
	old := s.extractor
	s.extractor, s.extractorIdentity = w, d.Identity
	s.mu.Unlock()
	committed = true
	if old != nil {
		_ = old.Close()
	}
	return nil
}
