package localaudio

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	ManifestName    = "manifest.json"
	manifestMaxSize = 1 << 20
	artifactMaxSize = 128 << 20
	payloadMaxSize  = 256 << 20
	ffmpegSourceSHA = "464beb5e7bf0c311e68b45ae2f04e9cc2af88851abb4082231742a74d97b524c"
)

type Artifact struct {
	Name       string `json:"name"`
	Role       string `json:"role"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	Executable bool   `json:"executable,omitempty"`
}

type Manifest struct {
	SchemaVersion    int        `json:"schemaVersion"`
	ID               string     `json:"id"`
	Platform         string     `json:"platform"`
	FFmpegVersion    string     `json:"ffmpegVersion"`
	SourceURL        string     `json:"sourceUrl"`
	SourceSHA256     string     `json:"sourceSha256"`
	License          string     `json:"license"`
	NetworkDisabled  bool       `json:"networkDisabled"`
	EnabledProtocols []string   `json:"enabledProtocols"`
	EnabledDemuxers  []string   `json:"enabledDemuxers"`
	EnabledDecoders  []string   `json:"enabledDecoders"`
	Configure        []string   `json:"configure"`
	Artifacts        []Artifact `json:"artifacts"`
}

func safePayloadName(value string) bool {
	return value != "" && filepath.Base(value) == value && value != "." && value != ".." &&
		!strings.ContainsAny(value, "/\\:\x00")
}

func validSHA256(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == sha256.Size
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (m Manifest) Validate() error {
	if m.SchemaVersion != 1 || !safePayloadName(m.ID) || m.Platform != runtime.GOOS+"/"+runtime.GOARCH ||
		m.FFmpegVersion != "8.1.2" || m.SourceURL != "https://ffmpeg.org/releases/ffmpeg-8.1.2.tar.xz" ||
		!strings.EqualFold(m.SourceSHA256, ffmpegSourceSHA) || m.License != "LGPL-2.1-or-later" || !m.NetworkDisabled {
		return fmt.Errorf("localaudio: incompatible codec manifest identity, platform, source, license, or network policy")
	}
	protocols := append([]string(nil), m.EnabledProtocols...)
	sort.Strings(protocols)
	if strings.Join(protocols, ",") != "file,pipe" {
		return fmt.Errorf("localaudio: codec payload must expose only file and pipe protocols")
	}
	for _, value := range []string{"flac", "mp3", "aac", "mov"} {
		if !contains(m.EnabledDemuxers, value) {
			return fmt.Errorf("localaudio: required demuxer %s is absent", value)
		}
	}
	for _, value := range []string{"flac", "mp3float", "aac"} {
		if !contains(m.EnabledDecoders, value) {
			return fmt.Errorf("localaudio: required decoder %s is absent", value)
		}
	}
	roles, names := map[string]bool{}, map[string]bool{}
	var total int64
	for _, artifact := range m.Artifacts {
		if !safePayloadName(artifact.Name) || names[artifact.Name] || roles[artifact.Role] ||
			artifact.Size <= 0 || artifact.Size > artifactMaxSize || !validSHA256(artifact.SHA256) {
			return fmt.Errorf("localaudio: invalid codec artifact")
		}
		if artifact.Role != "ffmpeg" && artifact.Role != "ffprobe" && artifact.Role != "licenses" && artifact.Role != "build_info" {
			return fmt.Errorf("localaudio: unknown codec artifact role")
		}
		if (artifact.Role == "ffmpeg" || artifact.Role == "ffprobe") != artifact.Executable {
			return fmt.Errorf("localaudio: invalid codec executable policy")
		}
		total += artifact.Size
		if total > payloadMaxSize {
			return fmt.Errorf("localaudio: codec payload exceeds size limit")
		}
		names[artifact.Name], roles[artifact.Role] = true, true
	}
	for _, role := range []string{"ffmpeg", "ffprobe", "licenses", "build_info"} {
		if !roles[role] {
			return fmt.Errorf("localaudio: missing codec artifact role %s", role)
		}
	}
	return nil
}

func decodeManifest(reader io.Reader) (Manifest, error) {
	var m Manifest
	raw, err := io.ReadAll(io.LimitReader(reader, manifestMaxSize+1))
	if err != nil {
		return m, err
	}
	if len(raw) > manifestMaxSize {
		return m, fmt.Errorf("localaudio: codec manifest exceeds size limit")
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, err
	}
	return m, m.Validate()
}

func readManifest(directory string) (Manifest, error) {
	file, err := os.Open(filepath.Join(directory, ManifestName))
	if err != nil {
		return Manifest{}, err
	}
	defer file.Close()
	return decodeManifest(file)
}
