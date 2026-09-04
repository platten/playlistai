package app

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/intent/llama"
	"github.com/platten/playlistai/internal/intent/modelmgr"
	"github.com/platten/playlistai/internal/ports"
)

// modelStartTimeout bounds how long a model swap waits for llama-server to
// become healthy.
const modelStartTimeout = 3 * time.Minute

// CurrentModel returns the active model's path and catalog id (both empty when
// the rules parser is in use).
func (c *Container) CurrentModel() (path, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.modelPath, c.modelID
}

// SetModel starts llama-server on the given GGUF and makes it the active parser,
// persisting the choice. On any failure the current parser is left untouched.
func (c *Container) SetModel(ctx context.Context, modelPath, modelID string) error {
	if err := modelmgr.ValidateGGUF(modelPath); err != nil {
		return err
	}

	sctx, cancel := context.WithTimeout(ctx, modelStartTimeout)
	defer cancel()

	p, err := llama.New(sctx, llama.Options{
		BinaryPath:   c.cfg.AI.LlamaServerPath,
		ModelPath:    modelPath,
		NCtx:         c.cfg.AI.NCtx,
		NThreads:     c.cfg.AI.NThreads,
		StartTimeout: modelStartTimeout,
		Logger:       c.log,
	})
	if err != nil {
		return err
	}

	c.setLlama(p, modelPath, modelID)
	prefs := config.LoadPrefs(c.cfg.DataDir)
	prefs.ModelPath, prefs.ModelID = modelPath, modelID
	if serr := prefs.Save(c.cfg.DataDir); serr != nil {
		c.log.Warn("could not persist model choice", "err", serr)
	}
	c.log.Info("model set", "path", modelPath, "id", modelID)
	return nil
}

// DownloadModel fetches a catalog model and switches to it. Progress is reported
// under modelmgr.ProgressOp.
func (c *Container) DownloadModel(ctx context.Context, id string, p ports.Progress) error {
	m, ok := modelmgr.Get(id)
	if !ok {
		return fmt.Errorf("app: unknown model %q", id)
	}
	dir := filepath.Join(c.cfg.DataDir, "models")
	path, err := modelmgr.Download(ctx, m, dir, p)
	if err != nil {
		return err
	}
	return c.SetModel(ctx, path, id)
}

// ClearModel stops llama-server and reverts to the rules parser.
func (c *Container) ClearModel() error {
	c.setLlama(nil, "", "")
	prefs := config.LoadPrefs(c.cfg.DataDir)
	prefs.ModelPath, prefs.ModelID = "", ""
	if serr := prefs.Save(c.cfg.DataDir); serr != nil {
		c.log.Warn("could not persist model choice", "err", serr)
	}
	c.log.Info("model cleared; using rules parser")
	return nil
}

// setLlama swaps the active parser and closes any previously-running
// llama-server. Passing nil reverts to the rules parser.
func (c *Container) setLlama(p *llama.Parser, modelPath, modelID string) {
	c.mu.Lock()
	old := c.llama
	c.llama = p
	c.modelPath, c.modelID = modelPath, modelID
	if p != nil {
		c.parser = p
	} else {
		c.parser = c.rulesParser
	}
	c.mu.Unlock()

	if old != nil {
		_ = old.Close()
	}
}
