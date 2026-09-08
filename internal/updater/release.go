// Package updater checks official GitHub releases and stages verified updates.
// Only a backend-selected release can be installed; the UI supplies no URLs or paths.
package updater

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/httpretry"
)

const Repository = "https://github.com/platten/playlistai"
const latestURL = "https://api.github.com/repos/platten/playlistai/releases/latest"
const maxDownload = 512 << 20

var stableVersion = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:\+[0-9A-Za-z.-]+)?$`)

func versionParts(v string) ([3]uint64, bool) {
	var parts [3]uint64
	m := stableVersion.FindStringSubmatch(v)
	if m == nil {
		return parts, false
	}
	for i := range parts {
		n, err := strconv.ParseUint(m[i+1], 10, 64)
		if err != nil {
			return parts, false
		}
		parts[i] = n
	}
	return parts, true
}

func newer(candidate, current string) bool {
	a, ok := versionParts(candidate)
	b, valid := versionParts(current)
	if !ok || !valid {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

type asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}
type release struct {
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Body       string  `json:"body"`
	Assets     []asset `json:"assets"`
}

type Offer struct {
	UsesInstaller bool   `json:"usesInstaller"`
	Current       string `json:"current"`
	Version       string `json:"version"`
	Available     bool   `json:"available"`
	CanInstall    bool   `json:"canInstall"`
	Reason        string `json:"reason"`
	Notes         string `json:"notes"`
	URL           string `json:"url"`
	Size          int64  `json:"size"`
	Notice        string `json:"notice"`
}

type installation struct {
	Target     string
	Executable string
	Kind       string
	Arch       string
}

func detectInstallation() (installation, error) {
	exe, err := os.Executable()
	if err != nil {
		return installation{}, err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return installation{}, err
	}
	return locate(runtime.GOOS, runtime.GOARCH, exe, os.Getenv("APPIMAGE"))
}

func locate(platform, arch, exe, appimage string) (installation, error) {
	i := installation{Target: exe, Executable: exe, Arch: arch, Kind: platform}
	if platform == "windows" {
		if info, err := os.Lstat(filepath.Join(filepath.Dir(exe), "uninstall.exe")); err == nil && info.Mode().IsRegular() {
			i.Kind = "windows-installer"
		}
	}
	if arch != "amd64" && arch != "arm64" {
		return i, fmt.Errorf("no update package is available for %s", arch)
	}
	if platform == "linux" && appimage != "" {
		path, err := filepath.EvalSymlinks(appimage)
		if err != nil || !filepath.IsAbs(path) {
			return i, fmt.Errorf("cannot locate the installed AppImage")
		}
		i.Target, i.Kind = path, "appimage"
	}
	if platform == "darwin" {
		root := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
		if !strings.HasSuffix(root, ".app") || filepath.Base(filepath.Dir(exe)) != "MacOS" || filepath.Base(filepath.Dir(filepath.Dir(exe))) != "Contents" {
			return i, fmt.Errorf("install Playlist AI in an Applications folder before updating")
		}
		i.Target = root
	}
	if platform == "linux" && appimage == "" && (strings.HasPrefix(exe, "/usr/") || strings.HasPrefix(exe, "/opt/") || strings.HasPrefix(exe, "/nix/") || strings.HasPrefix(exe, "/snap/")) {
		return i, fmt.Errorf("this installation is managed by the operating system; update it with your package manager or install the latest AppImage")
	}
	if platform != "windows" && platform != "darwin" && platform != "linux" {
		return i, fmt.Errorf("automatic updates are unavailable on %s", platform)
	}
	return i, nil
}

func (i installation) assetName() string {
	switch i.Kind {
	case "appimage":
		arch := "x86_64"
		if i.Arch == "arm64" {
			arch = "aarch64"
		}
		return "playlist-ai-" + arch + ".AppImage"
	case "windows":
		return "playlist-ai-windows-" + i.Arch + ".zip"
	case "windows-installer":
		return "playlist-ai-" + i.Arch + "-installer.exe"
	case "linux":
		return "playlist-ai-linux-" + i.Arch + ".tar.gz"
	case "darwin":
		if i.Arch != "arm64" {
			return "playlist-ai-macos-" + i.Arch + ".zip"
		}
		return "playlist-ai-macos.zip"
	}
	return ""
}

func releaseClient(timeout time.Duration) *http.Client {
	return httpretry.Client(&http.Client{Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many update redirects")
		}
		u := req.URL
		if u.Scheme != "https" || u.User != nil || u.Port() != "" {
			return fmt.Errorf("unsafe update redirect")
		}
		switch u.Hostname() {
		case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
			return nil
		}
		return fmt.Errorf("update redirect left GitHub")
	}})
}

func check(ctx context.Context, current string, i installation, client *http.Client, endpoint string) (Offer, asset, error) {
	o := Offer{Current: current, UsesInstaller: i.Kind == "windows-installer"}
	if _, ok := versionParts(current); !ok {
		return o, asset{}, nil
	} // Development builds never replace themselves.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return o, asset{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Playlist-AI/"+current)
	resp, err := client.Do(req)
	if err != nil {
		return o, asset{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return o, asset{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return o, asset{}, fmt.Errorf("release check: GitHub HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		return o, asset{}, fmt.Errorf("invalid release response size")
	}
	var r release
	if err := json.Unmarshal(raw, &r); err != nil {
		return o, asset{}, err
	}
	if r.Draft || r.Prerelease || !newer(r.Tag, current) {
		return o, asset{}, nil
	}
	o.Available, o.Version, o.Notes = true, r.Tag, r.Body
	o.URL = Repository + "/releases/tag/" + url.PathEscape(r.Tag)
	o.Reason = "This release does not include an automatic update package for this installation. Download the appropriate installer from the release page."
	names := []string{i.assetName()}
	if i.Kind == "darwin" && i.Arch == "arm64" {
		names = append(names, "playlist-ai.dmg")
	}
	for _, name := range names {
		for _, a := range r.Assets {
			if a.Name != name {
				continue
			}
			digest, err := hex.DecodeString(strings.TrimPrefix(a.Digest, "sha256:"))
			if !strings.HasPrefix(a.Digest, "sha256:") || err != nil || len(digest) != 32 || a.Size <= 0 || a.Size > maxDownload {
				o.Reason = "The update package has no valid SHA-256 checksum or supported size. Use the release page to review this release."
				return o, asset{}, nil
			}
			expected := Repository + "/releases/download/" + url.PathEscape(r.Tag) + "/" + a.Name
			if a.URL != expected {
				return o, asset{}, fmt.Errorf("release asset URL does not match the official repository")
			}
			o.CanInstall, o.Size, o.Reason = true, a.Size, ""
			return o, a, nil
		}
	}
	return o, asset{}, nil
}
