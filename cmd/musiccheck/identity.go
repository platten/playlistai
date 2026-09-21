package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/discoveryasset"
)

type artifactIdentity struct {
	Name   string `json:"name,omitempty"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

type discoveryIdentity struct {
	Version        string   `json:"version,omitempty"`
	Snapshot       string   `json:"snapshot,omitempty"`
	ManifestDigest string   `json:"manifestDigest,omitempty"`
	PackIDs        []string `json:"packIds,omitempty"`
	Tracks         int      `json:"tracks,omitempty"`
}

type buildIdentity struct {
	Revision string `json:"revision,omitempty"`
	Modified bool   `json:"modified"`
}

type runIdentities struct {
	CatalogVersion string                           `json:"catalogVersion,omitempty"`
	LanguageModel  artifactIdentity                 `json:"languageModel"`
	Discovery      discoveryIdentity                `json:"discovery"`
	CLAP           core.AudioModelIdentity          `json:"clap"`
	CLAPBundle     string                           `json:"clapBundle,omitempty"`
	MERT           core.AudioRepresentationIdentity `json:"mert"`
	MERTBundle     string                           `json:"mertBundle,omitempty"`
	WorkingTree    buildIdentity                    `json:"workingTree"`
}

type identityOptions struct {
	Model, DiscoveryState, CLAPBundle, MERTBundle string
}

func collectRunIdentities(ctx context.Context, options identityOptions) (runIdentities, error) {
	identity := runIdentities{WorkingTree: currentBuildIdentity()}
	var err error
	if options.Model != "" {
		identity.LanguageModel, err = identifyFile(ctx, options.Model)
		if err != nil {
			return identity, fmt.Errorf("language model identity: %w", err)
		}
	}
	if options.DiscoveryState != "" {
		if _, err = os.Stat(filepath.Join(options.DiscoveryState, "active.json")); err != nil {
			return identity, fmt.Errorf("installed discovery activation record: %w", err)
		}
		manager, openErr := discoveryasset.Open(ctx, options.DiscoveryState)
		if openErr != nil {
			return identity, openErr
		}
		status := manager.Status()
		identity.Discovery = discoveryIdentity{Version: status.Version, Snapshot: manager.SnapshotID(), ManifestDigest: status.ManifestDigest, PackIDs: append([]string(nil), status.PackIDs...), Tracks: status.Tracks}
		_ = manager.Close()
		if !status.Installed {
			return identity, fmt.Errorf("shared discovery state is not installed: %s", status.Error)
		}
	}
	if options.CLAPBundle != "" {
		manifest, readErr := audio.ReadRuntimeBundle(options.CLAPBundle)
		if readErr != nil {
			return identity, readErr
		}
		identity.CLAP, identity.CLAPBundle = manifest.Model, audio.Fingerprint(manifest)
	}
	if options.MERTBundle != "" {
		manifest, readErr := audio.ReadMERTBundleContext(ctx, options.MERTBundle)
		if readErr != nil {
			return identity, readErr
		}
		identity.MERT, identity.MERTBundle = manifest.Model, audio.Fingerprint(manifest)
	}
	return identity, nil
}

func identifyFile(ctx context.Context, path string) (artifactIdentity, error) {
	f, err := os.Open(path)
	if err != nil {
		return artifactIdentity{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return artifactIdentity{}, fmt.Errorf("not a regular file")
	}
	hash := sha256.New()
	buffer := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return artifactIdentity{}, err
		}
		n, readErr := f.Read(buffer)
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return artifactIdentity{}, readErr
		}
	}
	return artifactIdentity{Name: filepath.Base(path), Size: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func currentBuildIdentity() buildIdentity {
	identity := buildIdentity{}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				identity.Revision = setting.Value
			case "vcs.modified":
				identity.Modified = setting.Value == "true"
			}
		}
	}
	return identity
}
