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
	prefs, prefsErr := config.LoadPrefsChecked(c.cfg.DataDir)
	s.installed = nlu.AssetsReady(c.intentAssetRoot())
	validExtractor := false
	if dir := prefs.IntentExtractorDir; dir != "" {
		if d, err := nlu.InspectExtractor(dir); err == nil {
			validExtractor = true
			if nlu.RuntimeReady(c.intentAssetRoot()) {
				rt, _ := nlu.RuntimePath(nlu.AssetDir(c.intentAssetRoot()))
				s.extractor = &nlu.Worker{ExpectedModelSHA256: d.ModelSHA256, Config: nlu.WorkerConfig{Kind: nlu.DistilBERT, ModelDir: d.Directory, RuntimeLibrary: rt}}
				s.extractorIdentity = d.Identity
			}
		}
	}
	s.enabled = migratedExtractorEnabled(prefs, validExtractor)
	if prefsErr == nil && prefs.IntentExtractorEnabled == nil {
		prefs.IntentExtractorEnabled = &s.enabled
		prefs.IntentAssistEnabled = false
		if err := prefs.Save(c.cfg.DataDir); err != nil && c.log != nil {
			c.log.Warn("could not save intent extractor preference migration")
		}
	}
	c.RegisterCloser(func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
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
	detail := "DistilBERT base assets support a separately imported reviewed extractor. Base weights alone do not interpret prompts."
	if s.extractor != nil {
		detail = "The reviewed DistilBERT extractor can provide optional source-span suggestions to your local LLM. Explicit source facts remain authoritative."
	}
	// Direct pinned downloads include the platform runtime archive. Verified
	// local files are reused, so the actual remaining download can be smaller.
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
	if err := nlu.CheckPackagedRuntime(); err != nil {
		return err
	}
	if source != "" {
		dir, cleanup, err := c.prepareModelPack(ctx, source, "intent-models", p)
		if err != nil {
			return err
		}
		defer cleanup()
		installErr = nlu.ImportAssets(ctx, c.intentAssetRoot(), dir, p)
	} else {
		// No DistilBERT-only hosted pack has been published. Fetch only the
		// retained pinned sources; never fall back to the retired combined pack.
		installErr = nlu.InstallAssets(ctx, c.intentAssetRoot(), p)
	}
	if installErr != nil {
		return installErr
	}
	if !nlu.AssetsReady(c.intentAssetRoot()) {
		return fmt.Errorf("DistilBERT assets or native runtime failed verification")
	}
	// Base encoder readiness is deliberately separate from the inference health
	// required when importing a reviewed, calibrated task extractor.
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.installed = true
	s.mu.Unlock()
	// Runtime repair restores an already selected reviewed extractor without
	// requiring an application restart or changing its enable preference.
	prefs, err := config.LoadPrefsChecked(c.cfg.DataDir)
	if err == nil && prefs.IntentExtractorDir != "" {
		if d, inspectErr := nlu.InspectExtractor(prefs.IntentExtractorDir); inspectErr == nil {
			rt, _ := nlu.RuntimePath(nlu.AssetDir(c.intentAssetRoot()))
			s.mu.Lock()
			if s.extractor == nil {
				s.extractor = &nlu.Worker{ExpectedModelSHA256: d.ModelSHA256, Config: nlu.WorkerConfig{Kind: nlu.DistilBERT, ModelDir: d.Directory, RuntimeLibrary: rt}}
				s.extractorIdentity = d.Identity
			}
			s.mu.Unlock()
		}
	}
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
	extractor := s.extractor
	s.mu.Unlock()
	if enabled && extractor == nil {
		return fmt.Errorf("install a reviewed DistilBERT extractor before enabling suggestions")
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
	prefs.IntentExtractorEnabled = &enabled
	prefs.IntentAssistEnabled = false
	if err := prefs.Save(c.cfg.DataDir); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = enabled
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

// migratedExtractorEnabled does not carry a retired dictionary-only opt-in
// forward to an extractor installed later.
func migratedExtractorEnabled(prefs config.Prefs, validExtractor bool) bool {
	if prefs.IntentExtractorEnabled != nil {
		return *prefs.IntentExtractorEnabled
	}
	return prefs.IntentAssistEnabled && validExtractor
}

func (c *Container) intentSuggestions(ctx context.Context, prompt string, _ *core.IntentTranslation) []core.IntentProposal {
	s := &c.intentAssist
	s.mu.Lock()
	if !s.enabled || s.extractor == nil {
		s.mu.Unlock()
		return nil
	}
	extractor, extractorID := s.extractor, s.extractorIdentity
	s.mu.Unlock()
	bounded, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	result, err := extractor.Propose(bounded, prompt)
	if err != nil || result.Abstained {
		return nil
	}
	var proposals []core.IntentProposal
	for _, p := range result.Proposals {
		kind, role, _ := strings.Cut(p.Label, ":")
		if p.Start < 0 || p.End > len(prompt) || p.End <= p.Start || prompt[p.Start:p.End] != p.Text {
			continue
		}
		proposals = append(proposals, core.IntentProposal{Origin: "distilbert", Kind: kind, Role: role, Value: p.Text, Source: core.SourceEvidence{Text: p.Text, Start: p.Start, End: p.End}, Model: extractorID, Score: p.Score, Advisory: true})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled || s.extractor != extractor || s.extractorIdentity != extractorID || ctx.Err() != nil {
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
	if !nlu.RuntimeReady(c.intentAssetRoot()) {
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
	// A legacy dictionary-only opt-in must not enable this newly imported model.
	if prefs.IntentExtractorEnabled == nil {
		enabled := migratedExtractorEnabled(prefs, false)
		prefs.IntentExtractorEnabled = &enabled
	}
	prefs.IntentAssistEnabled = false
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
