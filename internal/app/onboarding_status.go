package app

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/intent/modelmgr"
	"github.com/platten/playlistai/internal/intent/nlu"
	"github.com/platten/playlistai/internal/metadata"
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
	Catalog   SetupCapability
	Metadata  SetupCapability
	Model     SetupCapability
	Intent    SetupCapability
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
	status := SetupReadiness{Onboarded: prefs.OnboardingDone}
	cat := c.cfg.Catalog
	catalogReady := rt.Catalog != nil && rt.Resolver != nil
	// A bare default directory is not evidence of a prior install or opt-in.
	// This also preserves an explicit wizard skip in builds without a source.
	catalogConfigured := cat.ArchiveURL != "" || cat.ManifestURL != "" || c.CatalogBundled() ||
		setupPathExists(filepath.Join(cat.Dir, "catalog.sqlite")) || setupPathExists(filepath.Join(cat.Dir, "vectors.i8")) ||
		(cat.Dir != "" && !sameSetupPath(cat.Dir, filepath.Join(c.cfg.DataDir, "catalog")))
	status.Catalog = SetupCapability{Ready: catalogReady, Supported: catalogReady || catalogConfigured, Required: catalogConfigured}

	metadataDir := filepath.Join(c.cfg.DataDir, "metadata")
	metadataPath := metadata.ActivePath(metadataDir)
	metadataPrior := setupPathExists(filepath.Join(metadataDir, "active")) || setupPathExists(metadataPath)
	metadataReady := false
	if catalogReady {
		if store, openErr := metadata.Open(metadataPath); openErr == nil {
			metadataReady = store.Compatible(rt.Resolver.CatalogVersion())
			_ = store.Close()
		}
	}
	// A new default metadata source is not prior opt-in. Compatibility can
	// only be assessed once the catalog it belongs to has loaded.
	status.Metadata = SetupCapability{Ready: metadataReady, Supported: c.cfg.Metadata.ManifestURL != "", Required: metadataPrior && catalogReady}

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

	c.intentAssist.mu.Lock()
	intentInstalled, extractor := c.intentAssist.installed, c.intentAssist.extractor
	extractorDir := ""
	if extractor != nil {
		extractorDir = extractor.Config.ModelDir
	}
	c.intentAssist.mu.Unlock()
	intentSupported := nlu.CheckPackagedRuntime() == nil
	intentReady := intentInstalled && setupIntentFilesPresent(c.intentAssetRoot())
	if prefs.IntentExtractorDir != "" {
		intentReady = intentReady && extractorDir != "" && sameSetupPath(extractorDir, prefs.IntentExtractorDir) && setupExtractorFilesPresent(extractorDir)
	}
	status.Intent = SetupCapability{Ready: intentReady, Supported: intentSupported, Required: prefs.IntentAssistEnabled || prefs.IntentExtractorDir != ""}

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
	return status, nil
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

func setupIntentFilesPresent(root string) bool {
	dir := nlu.AssetDir(root)
	for _, source := range nlu.Sources() {
		if !source.Setup {
			continue
		}
		name := strings.ReplaceAll(source.Name, "/", "_")
		if source.Name == "onnx/model.onnx" {
			name = "model.onnx"
		}
		if !setupFilePresent(filepath.Join(dir, source.Model, name), source.Size) {
			return false
		}
	}
	artifact, err := audio.NativeRuntimeArtifact(runtime.GOOS + "/" + runtime.GOARCH)
	runtimePath, pathErr := nlu.RuntimePath(dir)
	if err != nil || pathErr != nil || !setupFilePresent(runtimePath, artifact.UnpackedSize) {
		return false
	}
	for _, name := range audio.MERTWindowsRuntimeDependencies(runtime.GOOS + "/" + runtime.GOARCH) {
		if !setupFilePresent(filepath.Join(dir, "runtime", name), 0) {
			return false
		}
	}
	return true
}

func setupExtractorFilesPresent(dir string) bool {
	for _, name := range []string{"model.onnx", "config.json", "vocab.txt", "nlu-head.json", "calibration.json"} {
		if !setupFilePresent(filepath.Join(dir, name), 0) {
			return false
		}
	}
	return true
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
