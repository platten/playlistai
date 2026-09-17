package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/localcatalog"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/reco/multichannel"
)

type LocalLibraryMode string

const (
	LocalLibraryCombined     LocalLibraryMode = "combined"
	LocalLibraryOnly         LocalLibraryMode = "library_only"
	localLibrarySourceID                      = "primary"
	localLibrarySettingsFile                  = "settings.json"
)

func (m LocalLibraryMode) valid() bool {
	return m == LocalLibraryCombined || m == LocalLibraryOnly
}

type LocalLibraryCoverage struct {
	Tracks      int `json:"tracks"`
	Metadata    int `json:"metadata"`
	MERT        int `json:"mert"`
	DSP         int `json:"dsp"`
	Failed      int `json:"failed"`
	Unsupported int `json:"unsupported"`
}

type LocalLibraryRoot struct {
	Alias     string `json:"alias"`
	Path      string `json:"path,omitempty"`
	Mapped    bool   `json:"mapped"`
	Available bool   `json:"available"`
	Detail    string `json:"detail,omitempty"`
}

// LocalLibraryStatus is safe for the settings UI. It contains no track paths;
// root mappings are local preferences and never came from the portable pack.
type LocalLibraryStatus struct {
	Installed            bool                    `json:"installed"`
	Mode                 LocalLibraryMode        `json:"mode"`
	Format               string                  `json:"format,omitempty"`
	Version              int                     `json:"version,omitempty"`
	PackID               string                  `json:"packId,omitempty"`
	PackSHA256           string                  `json:"packSha256,omitempty"`
	CreatedAt            string                  `json:"createdAt,omitempty"`
	CorpusGeneration     string                  `json:"corpusGeneration,omitempty"`
	MetadataGeneration   string                  `json:"metadataGeneration,omitempty"`
	MERTGeneration       string                  `json:"mertGeneration,omitempty"`
	ClusterGeneration    string                  `json:"clusterGeneration,omitempty"`
	StatisticsGeneration string                  `json:"statisticsGeneration,omitempty"`
	Coverage             LocalLibraryCoverage    `json:"coverage"`
	MERT                 librarypack.VectorSpace `json:"mert"`
	Roots                []LocalLibraryRoot      `json:"roots"`
}

type localLibrarySettings struct {
	Version      int               `json:"version"`
	Mode         LocalLibraryMode  `json:"mode"`
	RootMappings map[string]string `json:"rootMappings"`
}

type localLibraryState struct {
	root     string
	manager  *librarypack.Manager
	opMu     sync.Mutex
	settings sync.RWMutex
	current  localLibrarySettings
}

type localLibraryHolder struct {
	once  sync.Once
	state *localLibraryState
	err   error
}

var localLibraryRegistry = struct {
	sync.Mutex
	entries map[*Container]*localLibraryHolder
}{entries: make(map[*Container]*localLibraryHolder)}

func (c *Container) localLibrary() (*localLibraryState, error) {
	localLibraryRegistry.Lock()
	holder := localLibraryRegistry.entries[c]
	if holder == nil {
		holder = &localLibraryHolder{}
		localLibraryRegistry.entries[c] = holder
	}
	localLibraryRegistry.Unlock()
	holder.once.Do(func() {
		root := filepath.Join(c.Config().DataDir, "local-library")
		manager, err := librarypack.OpenManager(context.Background(), root, librarypack.Limits{})
		if err != nil {
			holder.err = err
			return
		}
		settings, err := loadLocalLibrarySettings(root)
		if err != nil {
			_ = manager.Close()
			holder.err = err
			return
		}
		holder.state = &localLibraryState{root: root, manager: manager, current: settings}
		c.RegisterCloser(func() error {
			localLibraryRegistry.Lock()
			delete(localLibraryRegistry.entries, c)
			localLibraryRegistry.Unlock()
			return manager.Close()
		})
	})
	if holder.err != nil {
		// A repaired settings file or filesystem may be retried without an app
		// restart; concurrent callers that already hold holder still see the
		// same initialization error.
		localLibraryRegistry.Lock()
		if localLibraryRegistry.entries[c] == holder {
			delete(localLibraryRegistry.entries, c)
		}
		localLibraryRegistry.Unlock()
	}
	return holder.state, holder.err
}

func defaultLocalLibrarySettings() localLibrarySettings {
	return localLibrarySettings{Version: 1, Mode: LocalLibraryCombined, RootMappings: map[string]string{}}
}

func loadLocalLibrarySettings(root string) (localLibrarySettings, error) {
	settings := defaultLocalLibrarySettings()
	file, err := os.Open(filepath.Join(root, localLibrarySettingsFile))
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, fmt.Errorf("read local library settings: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		return settings, fmt.Errorf("decode local library settings: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF || settings.Version != 1 || !settings.Mode.valid() || len(settings.RootMappings) > 1024 {
		return settings, errors.New("invalid local library settings")
	}
	for alias, root := range settings.RootMappings {
		if strings.TrimSpace(alias) == "" || !filepath.IsAbs(root) {
			return settings, errors.New("invalid local library root mapping")
		}
		settings.RootMappings[alias] = filepath.Clean(root)
	}
	return settings, nil
}

func (s *localLibraryState) saveSettings(settings localLibrarySettings) error {
	settings.Version = 1
	if settings.RootMappings == nil {
		settings.RootMappings = map[string]string{}
	}
	raw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.root, ".local-library-settings-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(append(raw, '\n')); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := publishLocalLibrarySettings(name, filepath.Join(s.root, localLibrarySettingsFile)); err != nil {
		return err
	}
	if err := syncLocalLibraryDirectory(s.root); err != nil {
		return err
	}
	s.settings.Lock()
	s.current = settings
	s.settings.Unlock()
	return nil
}

func (s *localLibraryState) settingsSnapshot() localLibrarySettings {
	s.settings.RLock()
	defer s.settings.RUnlock()
	settings := s.current
	settings.RootMappings = cloneRootMappings(settings.RootMappings)
	return settings
}

func cloneRootMappings(input map[string]string) map[string]string {
	output := make(map[string]string, len(input))
	for alias, root := range input {
		output[alias] = root
	}
	return output
}

// ImportLocalLibrary verifies and copies source into app-managed storage, then
// atomically publishes it. The source pack and original music are read-only.
func (c *Container) ImportLocalLibrary(ctx context.Context, source string) (LocalLibraryStatus, error) {
	state, err := c.localLibrary()
	if err != nil {
		return LocalLibraryStatus{}, err
	}
	state.opMu.Lock()
	defer state.opMu.Unlock()
	if err := ctx.Err(); err != nil {
		return LocalLibraryStatus{}, err
	}
	staged, err := state.manager.Stage(ctx, source)
	if err != nil {
		return LocalLibraryStatus{}, fmt.Errorf("verify local library pack: %w", err)
	}
	activated := false
	defer func() {
		if !activated {
			_ = state.manager.Discard(staged)
		}
	}()
	if err := state.manager.Activate(ctx, staged); err != nil {
		activated = true // Activate owns cleanup after it accepts staged.
		return LocalLibraryStatus{}, fmt.Errorf("activate local library pack: %w", err)
	}
	activated = true
	settings := state.settingsSnapshot()
	settings.RootMappings = mappingsForAliases(settings.RootMappings, staged.Manifest().RootAliases)
	if err := state.saveSettings(settings); err != nil {
		return LocalLibraryStatus{}, fmt.Errorf("local library activated but settings could not be saved: %w", err)
	}
	return state.status()
}

func mappingsForAliases(mappings map[string]string, aliases []string) map[string]string {
	allowed := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		allowed[alias] = struct{}{}
	}
	filtered := make(map[string]string)
	for alias, root := range mappings {
		if _, ok := allowed[alias]; ok {
			filtered[alias] = root
		}
	}
	return filtered
}

func (c *Container) LocalLibraryStatus() (LocalLibraryStatus, error) {
	state, err := c.localLibrary()
	if err != nil {
		return LocalLibraryStatus{}, err
	}
	return state.status()
}

func (s *localLibraryState) status() (LocalLibraryStatus, error) {
	settings := s.settingsSnapshot()
	status := LocalLibraryStatus{Mode: settings.Mode, Roots: []LocalLibraryRoot{}}
	lease, err := s.manager.Pin()
	if errors.Is(err, librarypack.ErrNoActiveGeneration) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	defer lease.Release()
	manifest := lease.Generation().Manifest()
	status.Installed = true
	status.Format, status.Version, status.PackID, status.PackSHA256 = manifest.Format, manifest.Version, manifest.PackID, lease.Generation().PackSHA256()
	status.CreatedAt, status.CorpusGeneration, status.MetadataGeneration = manifest.CreatedAt, manifest.CorpusGeneration, manifest.MetadataGeneration
	status.MERTGeneration, status.ClusterGeneration, status.StatisticsGeneration = manifest.MERTGeneration, manifest.ClusterGeneration, manifest.StatisticsGeneration
	status.Coverage = LocalLibraryCoverage(manifest.Coverage)
	status.MERT = manifest.MERT
	for _, alias := range manifest.RootAliases {
		root := LocalLibraryRoot{Alias: alias}
		if path, ok := settings.RootMappings[alias]; ok {
			root.Path, root.Mapped = path, true
			if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
				root.Available = true
			} else {
				root.Detail = "Mapped root is currently unavailable"
			}
		} else {
			root.Detail = "Choose this root on the current machine to enable local playback"
		}
		status.Roots = append(status.Roots, root)
	}
	return status, nil
}

func (c *Container) SetLocalLibraryMode(mode LocalLibraryMode) (LocalLibraryStatus, error) {
	if !mode.valid() {
		return LocalLibraryStatus{}, errors.New("invalid local library mode")
	}
	state, err := c.localLibrary()
	if err != nil {
		return LocalLibraryStatus{}, err
	}
	state.opMu.Lock()
	defer state.opMu.Unlock()
	if mode == LocalLibraryOnly {
		lease, err := state.manager.Pin()
		if errors.Is(err, librarypack.ErrNoActiveGeneration) {
			return LocalLibraryStatus{}, errors.New("library-only mode requires an imported library")
		} else if err != nil {
			return LocalLibraryStatus{}, err
		}
		// The mode check needs only the existence barrier; do not retain a pin.
		lease.Release()
	}
	settings := state.settingsSnapshot()
	settings.Mode = mode
	if err := state.saveSettings(settings); err != nil {
		return LocalLibraryStatus{}, err
	}
	return state.status()
}

func (c *Container) SetLocalLibraryRoot(alias, root string) (LocalLibraryStatus, error) {
	state, err := c.localLibrary()
	if err != nil {
		return LocalLibraryStatus{}, err
	}
	state.opMu.Lock()
	defer state.opMu.Unlock()
	lease, err := state.manager.Pin()
	if err != nil {
		return LocalLibraryStatus{}, err
	}
	manifest := lease.Generation().Manifest()
	lease.Release()
	validAlias := false
	for _, candidate := range manifest.RootAliases {
		if candidate == alias {
			validAlias = true
			break
		}
	}
	if !validAlias {
		return LocalLibraryStatus{}, errors.New("root alias is absent from the active library")
	}
	settings := state.settingsSnapshot()
	if strings.TrimSpace(root) == "" {
		delete(settings.RootMappings, alias)
	} else {
		if !filepath.IsAbs(root) {
			return LocalLibraryStatus{}, errors.New("local library root must be an absolute path")
		}
		settings.RootMappings[alias] = filepath.Clean(root)
	}
	if err := state.saveSettings(settings); err != nil {
		return LocalLibraryStatus{}, err
	}
	return state.status()
}

func (c *Container) RemoveLocalLibrary(ctx context.Context) (LocalLibraryStatus, error) {
	state, err := c.localLibrary()
	if err != nil {
		return LocalLibraryStatus{}, err
	}
	state.opMu.Lock()
	defer state.opMu.Unlock()
	if err := state.manager.Remove(ctx); err != nil {
		return LocalLibraryStatus{}, err
	}
	settings := state.settingsSnapshot()
	settings.Mode, settings.RootMappings = LocalLibraryCombined, map[string]string{}
	if err := state.saveSettings(settings); err != nil {
		return LocalLibraryStatus{}, fmt.Errorf("library removed but settings could not be saved: %w", err)
	}
	return state.status()
}

// PinLocalCatalog gives one future playlist request a complete immutable pack
// plus the root settings snapshot from the same call. The caller must Close it.
func (c *Container) PinLocalCatalog() (*localcatalog.Catalog, error) {
	state, err := c.localLibrary()
	if err != nil {
		return nil, err
	}
	state.opMu.Lock()
	lease, err := state.manager.Pin()
	if err != nil {
		state.opMu.Unlock()
		return nil, err
	}
	settings := state.settingsSnapshot()
	manifest := lease.Generation().Manifest()
	settings.RootMappings = mappingsForAliases(settings.RootMappings, manifest.RootAliases)
	state.opMu.Unlock()
	return localcatalog.Open(lease, localcatalog.Options{SourceID: localLibrarySourceID, RootMappings: settings.RootMappings})
}

func (c *Container) pinLocalRecommendationOverlay(ctx context.Context, base ports.Catalog, resolver ports.ReferenceResolver, retriever ports.CandidateRetriever) (multichannel.RequestOverlay, error) {
	state, err := c.localLibrary()
	if err != nil {
		return multichannel.RequestOverlay{}, err
	}
	settings := state.settingsSnapshot()
	mode := localcatalog.ModeCombined
	if settings.Mode == LocalLibraryOnly {
		mode = localcatalog.ModeLibraryOnly
	}
	overlay, err := localcatalog.PinRecommendationOverlay(ctx, c, base, resolver, retriever, mode, 2)
	if errors.Is(err, librarypack.ErrNoActiveGeneration) {
		return multichannel.RequestOverlay{Catalog: base, Resolver: resolver, Retriever: retriever}, nil
	}
	if err != nil {
		return multichannel.RequestOverlay{}, err
	}
	return multichannel.RequestOverlay{Catalog: overlay.Catalog, Resolver: overlay.Resolver, Retriever: overlay.Retriever, Release: overlay.Close}, nil
}

func (c *Container) LocalLibraryCatalogVersion(base string) string {
	state, err := c.localLibrary()
	if err != nil {
		return base
	}
	lease, err := state.manager.Pin()
	if err != nil {
		return base
	}
	defer lease.Release()
	packID := lease.Generation().Manifest().PackID
	if state.settingsSnapshot().Mode == LocalLibraryOnly {
		return "local-library:" + packID
	}
	return base + "+local-library:" + packID
}
