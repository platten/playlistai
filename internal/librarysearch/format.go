// Package librarysearch builds and queries immutable packed float32 vector
// generations. It is deliberately independent of the desktop catalog ports.
package librarysearch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	FormatVersion = "playlist-library-vectors/v1"
	vectorMagic   = "PAIF32V1"
	idMagic       = "PAIIDS1\x00"
	headerBytes   = 32
)

type Artifact struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type ShardManifest struct {
	Index    int      `json:"index"`
	RowStart int64    `json:"rowStart"`
	Rows     int      `json:"rows"`
	Vectors  Artifact `json:"vectors"`
	IDs      Artifact `json:"ids"`
}

type Manifest struct {
	Format           string          `json:"format"`
	Generation       string          `json:"generation"`
	SourceGeneration string          `json:"sourceGeneration"`
	Contract         string          `json:"contract"`
	Dimension        int             `json:"dimension"`
	Rows             int64           `json:"rows"`
	Shards           []ShardManifest `json:"shards"`
}

func (m Manifest) Validate() error {
	if m.Format != FormatVersion || !safeGeneration(m.Generation) || m.SourceGeneration == "" || m.Contract == "" ||
		m.Dimension <= 0 || m.Dimension > 8192 || m.Rows <= 0 || m.Rows > 100_000_000 || len(m.Shards) == 0 || len(m.Shards) > 1_000_000 {
		return errors.New("librarysearch: unsupported or incomplete manifest")
	}
	var rows int64
	for i, shard := range m.Shards {
		if shard.Index != i || shard.RowStart != rows || shard.Rows <= 0 ||
			!validArtifact(shard.Vectors, ".f32") || !validArtifact(shard.IDs, ".ids") {
			return errors.New("librarysearch: invalid shard manifest")
		}
		rows += int64(shard.Rows)
	}
	if rows != m.Rows {
		return errors.New("librarysearch: shard row count mismatch")
	}
	return nil
}

func validArtifact(artifact Artifact, suffix string) bool {
	if artifact.Name == "" || filepath.Base(artifact.Name) != artifact.Name || !strings.HasSuffix(artifact.Name, suffix) ||
		artifact.Size <= 0 || strings.ContainsAny(artifact.Name, "/\\") {
		return false
	}
	raw, err := hex.DecodeString(artifact.SHA256)
	return err == nil && len(raw) == sha256.Size
}

func safeGeneration(value string) bool {
	if len(value) < 8 || len(value) > 80 || filepath.Base(value) != value || !strings.HasPrefix(value, "gen-") {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

func loadManifest(path string) (Manifest, error) {
	var manifest Manifest
	f, err := os.Open(path)
	if err != nil {
		return manifest, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err := errors.Join(readErr, f.Close()); err != nil {
		return manifest, err
	}
	if len(raw) > 4<<20 {
		return manifest, errors.New("librarysearch: manifest exceeds size limit")
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return manifest, err
	}
	return manifest, manifest.Validate()
}

func fileArtifact(path string) (Artifact, error) {
	f, err := os.Open(path)
	if err != nil {
		return Artifact{}, err
	}
	h := sha256.New()
	n, copyErr := io.Copy(h, f)
	err = errors.Join(copyErr, f.Close())
	if err != nil {
		return Artifact{}, err
	}
	return Artifact{Name: filepath.Base(path), Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

func verifyArtifact(dir string, artifact Artifact) error {
	got, err := fileArtifact(filepath.Join(dir, artifact.Name))
	if err != nil {
		return err
	}
	if got.Size != artifact.Size || got.SHA256 != artifact.SHA256 {
		return fmt.Errorf("librarysearch: checksum mismatch for %s", artifact.Name)
	}
	return nil
}

func writeSynced(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	return errors.Join(writeErr, f.Close())
}
