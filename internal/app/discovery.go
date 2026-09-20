package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/discoveryasset"
	"github.com/platten/playlistai/internal/localcatalog"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/reco/multichannel"
)

type DiscoveryAssetStatus struct {
	Configured       bool     `json:"configured"`
	HostedConfigured bool     `json:"hostedConfigured"`
	Installed        bool     `json:"installed"`
	Version          string   `json:"version"`
	PackIDs          []string `json:"packIds"`
	Tracks           int      `json:"tracks"`
	DownloadBytes    int64    `json:"downloadBytes"`
	Error            string   `json:"error"`
	Source           string   `json:"source"`
	ManifestDigest   string   `json:"manifestDigest"`
}

type DiscoveryAssetUpdate struct {
	Version         string `json:"version"`
	DownloadBytes   int64  `json:"downloadBytes"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Digest          string `json:"digest"`
	Files           int    `json:"files"`
	Source          string `json:"source"`
	Format          string `json:"format"`
}

func (c *Container) CheckDiscoveryAssetUpdate(ctx context.Context) (DiscoveryAssetUpdate, error) {
	ctx, release := c.OperationContext(ctx)
	defer release()
	manager, err := c.discoveryManager()
	if err != nil {
		return DiscoveryAssetUpdate{}, err
	}
	update, err := manager.CheckUpdate(ctx, c.cfg.Discovery.ManifestURL)
	if err != nil {
		return DiscoveryAssetUpdate{}, err
	}
	return DiscoveryAssetUpdate{Version: update.Version, DownloadBytes: update.DownloadBytes, UpdateAvailable: update.UpdateAvailable, Digest: update.Digest, Files: update.Files, Source: update.Source, Format: update.Format}, nil
}

func (c *Container) discoveryManager() (*discoveryasset.Manager, error) {
	c.discoveryMu.Lock()
	defer c.discoveryMu.Unlock()
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, errors.New("application is closed")
	}
	if c.discovery != nil {
		return c.discovery, nil
	}
	m, err := discoveryasset.Open(context.Background(), filepath.Join(c.cfg.DataDir, "discovery-data"))
	if err != nil {
		return nil, err
	}
	c.discovery = m
	c.RegisterCloser(m.Close)
	return m, nil
}

func (c *Container) GetDiscoveryAssetStatus() (DiscoveryAssetStatus, error) {
	s := DiscoveryAssetStatus{Configured: strings.TrimSpace(c.cfg.Discovery.ManifestURL) != "", PackIDs: []string{}}
	s.HostedConfigured = s.Configured
	m, err := c.discoveryManager()
	if err != nil {
		return s, err
	}
	installed := m.Status()
	s.Installed, s.Version, s.PackIDs = installed.Installed, installed.Version, installed.PackIDs
	s.Tracks, s.DownloadBytes, s.Error = installed.Tracks, installed.DownloadBytes, installed.Error
	s.Source, s.ManifestDigest = installed.Source, installed.ManifestDigest
	// A local override is a usable shared discovery source even in builds
	// without a hosted manifest configured.
	s.Configured = s.Configured || s.Installed
	return s, nil
}

func (c *Container) ImportDiscoveryAsset(ctx context.Context, path string, p ports.Progress) (DiscoveryAssetStatus, error) {
	ctx, release := c.OperationContext(ctx)
	defer release()
	if strings.TrimSpace(path) == "" {
		return DiscoveryAssetStatus{}, errors.New("a discovery pack path is required")
	}
	manager, err := c.discoveryManager()
	if err != nil {
		return DiscoveryAssetStatus{}, err
	}
	if _, err = manager.ImportLocal(ctx, path, p); err != nil {
		return DiscoveryAssetStatus{}, err
	}
	return c.GetDiscoveryAssetStatus()
}

// SaveDiscoveryArchive creates a new child directory, never overwriting files
// in the chosen folder. Export does not change the active recommendation source.
func (c *Container) SaveDiscoveryArchive(ctx context.Context, parent string, p ports.Progress) (discoveryasset.ArchiveResult, error) {
	ctx, release := c.OperationContext(ctx)
	defer release()
	if strings.TrimSpace(parent) == "" {
		return discoveryasset.ArchiveResult{}, errors.New("an archive destination is required")
	}
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return discoveryasset.ArchiveResult{}, err
	}
	destination := filepath.Join(parent, "playlist-ai-discovery-"+time.Now().Format("20060102-150405")+"-"+hex.EncodeToString(suffix[:]))
	return discoveryasset.DownloadArchive(ctx, c.cfg.Discovery.ManifestURL, destination, p)
}

func (c *Container) InstallDiscoveryAsset(ctx context.Context, p ports.Progress) (DiscoveryAssetStatus, error) {
	ctx, release := c.OperationContext(ctx)
	defer release()
	if strings.TrimSpace(c.cfg.Discovery.ManifestURL) == "" {
		return DiscoveryAssetStatus{}, errors.New("no music discovery release is configured")
	}
	m, err := c.discoveryManager()
	if err != nil {
		return DiscoveryAssetStatus{}, err
	}
	if _, err = m.Install(ctx, c.cfg.Discovery.ManifestURL, p); err != nil {
		return DiscoveryAssetStatus{}, err
	}
	return c.GetDiscoveryAssetStatus()
}

func (c *Container) pinDiscoveryRecommendationOverlay(ctx context.Context, intent core.MusicIntent, base ports.Catalog, resolver ports.ReferenceResolver, retriever ports.CandidateRetriever) (multichannel.RequestOverlay, error) {
	personal, err := c.pinLocalRecommendationOverlay(ctx, base, resolver, retriever)
	if err != nil {
		return multichannel.RequestOverlay{}, err
	}
	if intent.Controls.RecommendationMode != core.EnhancedHybrid {
		return personal, nil
	}
	status, err := c.GetDiscoveryAssetStatus()
	if err == nil && !status.Configured {
		return personal, nil
	}
	if err == nil && !status.Installed {
		err = errors.New("music discovery data is required for Enhanced hybrid; complete setup to download it")
	}
	if err != nil {
		if personal.Release != nil {
			personal.Release()
		}
		return multichannel.RequestOverlay{}, err
	}
	if personal.LibraryOnly {
		return personal, nil
	}
	m, err := c.discoveryManager()
	if err != nil {
		if personal.Release != nil {
			personal.Release()
		}
		return multichannel.RequestOverlay{}, err
	}
	packs, release, err := m.Pin(ctx)
	if err != nil {
		if personal.Release != nil {
			personal.Release()
		}
		return multichannel.RequestOverlay{}, err
	}
	overlay, err := localcatalog.NewDiscoveryOverlay(ctx, packs, personal.Catalog, personal.Resolver, personal.Retriever, 2)
	if err != nil {
		release()
		if personal.Release != nil {
			personal.Release()
		}
		return multichannel.RequestOverlay{}, err
	}
	return multichannel.RequestOverlay{Catalog: overlay.Catalog, Resolver: overlay.Resolver, Retriever: overlay.Retriever, EnableLibraryEvidence: true, Release: func() {
		overlay.Close()
		release()
		if personal.Release != nil {
			personal.Release()
		}
	}}, nil
}

func (c *Container) discoveryCatalogVersion(base string) string {
	manager, err := c.discoveryManager()
	if err != nil {
		return base
	}
	snapshot := manager.SnapshotID()
	if snapshot == "" {
		return base
	}
	return base + "+discovery-v1:" + snapshot
}
