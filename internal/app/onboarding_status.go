package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/intent/modelmgr"
	"github.com/platten/playlistai/internal/mbindex"
)

// SetupCapability separates an optional setup opportunity from an existing
// choice that needs repair. Unsupported capabilities never require a wizard.
type SetupCapability struct {
	Ready     bool
	Supported bool
	Required  bool
}

type SetupReadiness struct {
	Onboarded bool
	Pending   bool
	Catalog   SetupCapability
	Metadata  SetupCapability
	Model     SetupCapability
	Analysis  SetupCapability
	MERT      SetupCapability
	Preview   SetupCapability
}

// SetupReadiness only inspects local configuration and previously verified
// activation state. It does not start models, download, probe providers, hash
// weights, or change preferences. File presence checks detect removed assets;
// activation remains responsible for integrity and worker health verification.
func (c *Container) SetupReadiness() (SetupReadiness, error) {
	c.mu.Lock()
	prefs, err := config.LoadPrefsChecked(c.cfg.DataDir)
	if err != nil {
		c.mu.Unlock()
		return SetupReadiness{}, err
	}
	rt, modelPath, previewName, preview := c.runtime, c.modelPath, c.previewName, c.preview
	c.mu.Unlock()
	status := SetupReadiness{Onboarded: prefs.OnboardingDone, Pending: c.AudioStartupPending()}
	if status.Pending {
		return status, nil
	}
	cat := c.cfg.Catalog
	catalogReady := rt.Catalog != nil && rt.Resolver != nil
	// A bare default directory is not evidence of a prior install or opt-in.
	// This also preserves an explicit wizard skip in builds without a source.
	catalogConfigured := cat.ArchiveURL != "" || cat.ManifestURL != "" || c.CatalogBundled() ||
		setupPathExists(filepath.Join(cat.Dir, "catalog.sqlite")) || setupPathExists(filepath.Join(cat.Dir, "vectors.i8")) ||
		(cat.Dir != "" && !sameSetupPath(cat.Dir, filepath.Join(c.cfg.DataDir, "catalog")))
	status.Catalog = SetupCapability{Ready: catalogReady, Supported: catalogReady || catalogConfigured, Required: catalogConfigured}

	musicBrainzDir := filepath.Join(c.cfg.DataDir, "musicbrainz-metadata")
	musicBrainzPath := mbindex.ActivePath(musicBrainzDir)
	musicBrainzPrior := setupPathExists(filepath.Join(musicBrainzDir, "active")) || setupPathExists(musicBrainzPath)
	musicBrainzReady := false
	if store, openErr := mbindex.Open(musicBrainzPath); openErr == nil {
		musicBrainzReady = true
		_ = store.Close()
	}
	musicBrainzSupported := c.cfg.Metadata.MusicBrainzManifestURL != ""
	status.Metadata = SetupCapability{Ready: !musicBrainzSupported || musicBrainzReady, Supported: musicBrainzSupported, Required: musicBrainzPrior}

	if prefs.ModelDisabled {
		modelPath = ""
	} else if prefs.ModelPath != "" {
		modelPath = prefs.ModelPath
	} else if modelPath == "" {
		// CurrentModel stays empty during asynchronous llama startup. Selected
		// files and the executable suffice for setup, independently of startup.
		modelPath = c.cfg.AI.ModelPath
	}
	modelReady := modelPath != "" && modelmgr.ValidateGGUF(modelPath) == nil && len(c.LlamaRuntimes()) > 0
	status.Model = SetupCapability{Ready: modelReady, Supported: true, Required: modelPath != ""}

	c.analysis.mu.Lock()
	analysisReady := c.analysis.manifest != nil && c.analysis.service.InferenceReady()
	var manifest *audio.BundleManifest
	if c.analysis.manifest != nil {
		copyManifest := *c.analysis.manifest
		manifest = &copyManifest
	}
	bundleDir := ""
	if c.analysis.worker != nil {
		bundleDir = c.analysis.worker.BundleDir
	}
	c.analysis.mu.Unlock()
	activeDir, activeManifest := setupAnalysisActivation(filepath.Join(c.cfg.DataDir, "music-analysis"))
	if analysisReady {
		analysisReady = activeManifest != nil && sameSetupPath(activeDir, bundleDir) && activeManifest.Model == manifest.Model &&
			activeManifest.ID == manifest.ID && setupAnalysisFilesPresent(bundleDir, *manifest)
	}
	_, recommendedErr := audio.RecommendedBundle()
	// Legacy custom bundles bring their own worker and can remain usable in
	// a build without the built-in native worker.
	analysisSupported := recommendedErr == nil || analysisReady || manifest != nil && manifest.Version == 1 ||
		activeManifest != nil && activeManifest.Version == 1 && activeManifest.Platform == runtime.GOOS+"/"+runtime.GOARCH
	analysisPrior := manifest != nil || setupPathExists(filepath.Join(c.cfg.DataDir, "music-analysis", "active.json"))
	status.Analysis = SetupCapability{Ready: analysisReady, Supported: analysisSupported, Required: prefs.AnalysisEnabled || analysisPrior}
	status.MERT = c.setupMERTReadiness()
	status.Preview = SetupCapability{Ready: isValidPreviewProvider(previewName) && (previewName == config.PreviewOff || preview != nil), Supported: true}
	// An existing explicit preview-off choice is retained. Fresh setup asks
	// for a usable provider, just as it requires every supported module.
	if !prefs.OnboardingDone && previewName == config.PreviewOff {
		status.Preview.Ready = false
	}
	return status, nil
}

// CompletionSteps checks every supported capability on first setup and only
// repairs to existing choices for already-onboarded users. Unsupported native
// features must never create an impossible completion loop.
func (r SetupReadiness) CompletionSteps() []string {
	missing := []string{}
	for _, step := range []struct {
		name       string
		capability SetupCapability
	}{{"catalog", r.Catalog}, {"metadata", r.Metadata}, {"model", r.Model}, {"analysis", r.Analysis}, {"mert", r.MERT}, {"preview", r.Preview}} {
		if step.capability.Supported && !step.capability.Ready && (!r.Onboarded || step.capability.Required) {
			missing = append(missing, step.name)
		}
	}
	return missing
}

// CompleteSetup is the user-facing completion boundary. SetOnboarded remains
// the preference writer; callers must not report success before this check and
// the subsequent atomic preference save have both completed.
func (c *Container) CompleteSetup() error {
	readiness, err := c.SetupReadiness()
	if err != nil {
		return err
	}
	if readiness.Pending {
		return fmt.Errorf("installed music models are still being validated; try again when checking finishes")
	}
	if missing := readiness.CompletionSteps(); len(missing) > 0 {
		return fmt.Errorf("setup is incomplete: %s", strings.Join(missing, ", "))
	}
	return c.SetOnboarded()
}

func (c *Container) setupMERTReadiness() SetupCapability {
	c.enhanced.mu.Lock()
	var manifest *audio.MERTBundleManifest
	if c.enhanced.manifest != nil {
		copyManifest := *c.enhanced.manifest
		manifest = &copyManifest
	}
	bundleDir := ""
	if c.enhanced.worker != nil {
		bundleDir = c.enhanced.worker.BundleDir
	}
	c.enhanced.mu.Unlock()
	root := filepath.Join(c.cfg.DataDir, "mert-analysis")
	prior := manifest != nil || setupPathExists(filepath.Join(root, "active.json"))
	_, supportErr := c.recommendedMERT()
	ready := manifest != nil && bundleDir != "" && audio.NativeInferenceAvailable()
	id := strings.TrimSpace(string(setupReadSmallFile(filepath.Join(root, "active.json"), 4096)))
	if id == "" || id == "." || id == ".." || filepath.Base(id) != id || strings.ContainsAny(id, `/\:`) {
		ready = false
	}
	if ready {
		dir := filepath.Join(root, id)
		var active audio.MERTBundleManifest
		ready = sameSetupPath(dir, bundleDir) && json.Unmarshal(setupReadSmallFile(filepath.Join(dir, "mert-bundle.json"), 1<<20), &active) == nil && audio.Fingerprint(active) == audio.Fingerprint(*manifest)
		if ready {
			for _, a := range manifest.Artifacts {
				if !setupFilePresent(filepath.Join(dir, a.Name), a.Size) || a.ArchiveMember != "" && !setupFilePresent(manifest.File(dir, a.Role), a.UnpackedSize) {
					ready = false
					break
				}
			}
		}
	}
	return SetupCapability{Ready: ready, Supported: supportErr == nil || ready, Required: prior}
}

func setupPathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !os.IsNotExist(err)
}

func sameSetupPath(a, b string) bool {
	a, errA := filepath.Abs(a)
	b, errB := filepath.Abs(b)
	return errA == nil && errB == nil && (a == b || runtime.GOOS == "windows" && strings.EqualFold(a, b))
}

func setupFilePresent(path string, size int64) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0 && (size <= 0 || info.Size() == size)
}

func setupAnalysisFilesPresent(dir string, manifest audio.BundleManifest) bool {
	if dir == "" || !setupFilePresent(filepath.Join(dir, "bundle.json"), 0) {
		return false
	}
	for _, artifact := range manifest.Artifacts {
		// Active() verifies the archive as well as its extracted library on
		// restart; both must survive even while the current worker is healthy.
		if !setupFilePresent(filepath.Join(dir, artifact.Name), artifact.Size) ||
			artifact.ArchiveMember != "" && !setupFilePresent(manifest.File(dir, artifact.Role), artifact.UnpackedSize) {
			return false
		}
	}
	return true
}

// Only small activation metadata is read. The bundle's models are not opened
// or revalidated here; a legacy manifest also tells a non-native build that a
// failed custom worker is repairable instead of an unsupported new feature.
func setupAnalysisActivation(root string) (string, *audio.BundleManifest) {
	raw := setupReadSmallFile(filepath.Join(root, "active.json"), 4096)
	id := strings.TrimSpace(string(raw))
	if id == "" || id == "." || id == ".." || filepath.Base(id) != id || strings.ContainsAny(id, `/\:`) {
		return "", nil
	}
	dir := filepath.Join(root, id)
	var manifest audio.BundleManifest
	if json.Unmarshal(setupReadSmallFile(filepath.Join(dir, "bundle.json"), 1<<20), &manifest) != nil {
		return "", nil
	}
	return dir, &manifest
}

func setupReadSmallFile(path string, limit int64) []byte {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil
	}
	return data
}
