package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/httpretry"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "--app-update-worker" {
		if err := RunWorker(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) == 2 && os.Args[1] == "--test-update-parent" {
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestVersionsAndPlatformPackages(t *testing.T) {
	for _, tc := range []struct {
		new, old string
		want     bool
	}{
		{"v0.8.0", "0.7.0", true}, {"v0.10.0", "v0.9.9", true}, {"v1.0.0", "0.99.99", true},
		{"v0.7.0", "0.7.0", false}, {"v0.6.0", "0.7.0", false}, {"v0.8.0-rc.1", "0.7.0", false},
		{"v0.8.0", "dev", false}, {"v0.8.0", "", false}, {"v01.8.0", "0.7.0", false},
		{"v0.7.0+build", "0.7.0", false}, {"v999999999999999999999.0.0", "0.7.0", false},
	} {
		if newer(tc.new, tc.old) != tc.want {
			t.Errorf("%s > %s", tc.new, tc.old)
		}
	}
	for _, tc := range []struct{ platform, arch, exe, want string }{
		{"linux", "amd64", "/home/me/playlist-ai", "playlist-ai-linux-amd64.tar.gz"},
		{"windows", "arm64", "playlist-ai.exe", "playlist-ai-windows-arm64.zip"},
		{"darwin", "arm64", "/Applications/playlist-ai.app/Contents/MacOS/playlist-ai", "playlist-ai-macos.zip"},
	} {
		i, err := locate(tc.platform, tc.arch, tc.exe, "")
		if err != nil || i.assetName() != tc.want {
			t.Fatalf("%+v %v", i, err)
		}
	}
	if _, err := locate("linux", "amd64", "/usr/bin/playlist-ai", ""); err == nil {
		t.Fatal("package-managed binary can be overwritten")
	}
}

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestReleaseSelectionRejectsUntrustedOrIncompleteAssets(t *testing.T) {
	i := installation{Kind: "linux", Arch: "amd64"}
	for _, scenario := range []string{"new", "same", "draft", "prerelease", "missing", "checksum", "url", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			r := release{Tag: "v0.8.0", Body: "Release notes", Assets: []asset{{Name: i.assetName(), URL: Repository + "/releases/download/v0.8.0/" + i.assetName(), Size: 20, Digest: "sha256:" + strings.Repeat("a", 64)}}}
			switch scenario {
			case "same":
				r.Tag = "v0.7.0"
			case "draft":
				r.Draft = true
			case "prerelease":
				r.Prerelease = true
			case "missing":
				r.Assets = nil
			case "checksum":
				r.Assets[0].Digest = ""
			case "url":
				r.Assets[0].URL = "https://evil.invalid/update"
			case "oversized":
				r.Assets[0].Size = maxDownload + 1
			}
			raw, _ := json.Marshal(r)
			client := &http.Client{Transport: fixtureTransport(func(req *http.Request) (*http.Response, error) {
				if req.Header.Get("User-Agent") == "" {
					t.Error("missing GitHub User-Agent")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw)), Header: make(http.Header)}, nil
			})}
			o, _, err := check(context.Background(), "0.7.0", i, client, latestURL)
			if o.CanInstall != (scenario == "new") || (err != nil) != (scenario == "url") {
				t.Fatalf("%+v %v", o, err)
			}
		})
	}
	c := releaseClient(time.Second)
	for _, address := range []string{"http://github.com/a", "https://user@github.com/a", "https://evil.invalid/a", "https://github.com:443/a"} {
		r, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, address, nil)
		if c.CheckRedirect(r, nil) == nil {
			t.Fatal("unsafe redirect allowed")
		}
	}
}

func TestDownloadVerificationCancellationAndRetry(t *testing.T) {
	for _, scenario := range []string{"valid", "checksum", "oversized", "truncated", "retry", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			content := []byte("verified update")
			sum := sha256.Sum256(content)
			calls := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := &http.Client{Transport: fixtureTransport(func(*http.Request) (*http.Response, error) {
				calls++
				data := content
				status := 200
				switch scenario {
				case "oversized":
					data = append(append([]byte{}, content...), 'x')
				case "truncated":
					data = data[:3]
				case "retry":
					if calls == 1 {
						status = 429
					}
				case "cancel":
					cancel()
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data))}, nil
			})}
			// Exercise the same backoff wrapper used in production without network.
			client = httpretry.Client(client)
			a := asset{URL: Repository, Size: int64(len(content)), Digest: "sha256:" + hex.EncodeToString(sum[:])}
			if scenario == "checksum" {
				a.Digest = "sha256:" + strings.Repeat("0", 64)
			}
			err := download(ctx, a, filepath.Join(t.TempDir(), "update"), client, func(int64, int64, string) {})
			wantSuccess := scenario == "valid" || scenario == "retry"
			if (err == nil) != wantSuccess {
				t.Fatalf("download: %v", err)
			}
			if scenario == "retry" && calls != 2 {
				t.Fatal("429 not retried")
			}
		})
	}
}

func TestArchiveTraversalLinksAndExpansion(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mode  os.FileMode
		valid bool
	}{
		{"playlist-ai.exe", 0755, true}, {"../escape", 0644, false}, {"/absolute", 0644, false},
		{"playlist-ai.exe:stream", 0644, false}, {"..\\escape", 0644, false}, {"playlist-ai.exe", os.ModeSymlink | 0777, false},
	} {
		dir := t.TempDir()
		name := filepath.Join(dir, "update.zip")
		f, err := os.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		z := zip.NewWriter(f)
		h := &zip.FileHeader{Name: tc.name}
		h.SetMode(tc.mode)
		w, err := z.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("fixture"))
		_ = z.Close()
		_ = f.Close()
		_, err = unpack(context.Background(), name, dir, installation{Kind: "windows"})
		if (err == nil) != tc.valid {
			t.Fatalf("%s mode=%v: %v", tc.name, tc.mode, err)
		}
	}
	dir := t.TempDir()
	archive := filepath.Join(dir, "update.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tr := tar.NewWriter(gz)
	_ = tr.WriteHeader(&tar.Header{Name: "playlist-ai", Mode: 0755, Size: maxUnpacked + 1})
	_ = tr.Close()
	_ = gz.Close()
	_ = f.Close()
	if _, err := unpack(context.Background(), archive, dir, installation{Kind: "linux"}); err == nil {
		t.Fatal("oversized archive accepted")
	}
}

func makeJob(t *testing.T) (job, string) {
	t.Helper()
	parent := t.TempDir()
	target := filepath.Join(parent, "playlist-ai")
	dir, err := os.MkdirTemp(parent, ".playlist-ai-update-")
	if err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(dir, "payload")
	if err := os.WriteFile(target, []byte("old"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(payload, []byte("new"), 0700); err != nil {
		t.Fatal(err)
	}
	oldHash, err := treeHash(target)
	if err != nil {
		t.Fatal(err)
	}
	newHash, err := treeHash(payload)
	if err != nil {
		t.Fatal(err)
	}
	return job{Target: target, Payload: payload, Kind: "linux", ParentPID: 1, OldHash: oldHash, NewHash: newHash}, dir
}

func TestReplacementRollbackAndChangedPayload(t *testing.T) {
	for _, scenario := range []string{"success", "restart-error", "changed-payload", "changed-target"} {
		t.Run(scenario, func(t *testing.T) {
			j, dir := makeJob(t)
			if scenario == "changed-payload" {
				_ = os.WriteFile(j.Payload, []byte("tampered"), 0700)
			}
			if scenario == "changed-target" {
				_ = os.WriteFile(j.Target, []byte("another install"), 0700)
			}
			calls := 0
			err := replace(j, filepath.Join(dir, "previous"), func(target, kind string) error {
				calls++
				if target != j.Target {
					t.Fatal("wrong relaunch path")
				}
				if scenario == "restart-error" {
					return errors.New("fixture restart failed")
				}
				return nil
			})
			if (err == nil) != (scenario == "success") {
				t.Fatal(err)
			}
			raw, readErr := os.ReadFile(j.Target)
			if readErr != nil {
				t.Fatal(readErr)
			}
			want := "old"
			if scenario == "success" {
				want = "new"
			}
			if scenario == "changed-target" {
				want = "another install"
			}
			if string(raw) != want {
				t.Fatalf("installed file corrupted: %q", raw)
			}
			if strings.HasPrefix(scenario, "changed") && calls != 0 {
				t.Fatal("changed update executed")
			}
		})
	}
}

func TestWorkerWaitsForExitAndCommit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell launch fixture is Unix-only; Windows platform compile is checked separately")
	}
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			j, dir := makeJob(t)
			if err := os.WriteFile(j.Payload, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
			j.NewHash, _ = treeHash(j.Payload)
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			parent := exec.Command(exe, "--test-update-parent")
			if err := parent.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = parent.Process.Kill(); _ = parent.Wait() }()
			j.ParentPID = parent.Process.Pid
			raw, _ := json.Marshal(j)
			filename := filepath.Join(dir, "job.json")
			if err := os.WriteFile(filename, raw, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			worker := exec.CommandContext(ctx, exe, "--app-update-worker", filename)
			var output bytes.Buffer
			worker.Stderr = &output
			if err := worker.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = worker.Process.Kill() }()
			deadline := time.Now().Add(3 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("worker not ready")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if commit {
				_ = os.WriteFile(filepath.Join(dir, "commit"), []byte("approved"), 0600)
			}
			time.Sleep(250 * time.Millisecond)
			before, _ := os.ReadFile(j.Target)
			if string(before) != "old" {
				t.Fatal("live application replaced")
			}
			_ = parent.Process.Kill()
			_ = parent.Wait()
			err = worker.Wait()
			if (err == nil) != commit {
				t.Fatalf("worker: %v %s", err, output.String())
			}
			after, _ := os.ReadFile(j.Target)
			if commit && !strings.HasPrefix(string(after), "#!/bin/sh") || !commit && string(after) != "old" {
				t.Fatal("worker ignored commit marker")
			}
		})
	}
}

func TestDevelopmentBuildDoesNotCheckNetwork(t *testing.T) {
	m := New("dev")
	o, err := m.Check(context.Background())
	if err != nil || o.Available || o.CanInstall {
		t.Fatalf("%+v %v", o, err)
	}
	if err := m.Install(context.Background(), func(int64, int64, string) {}); err == nil {
		t.Fatal("unchecked update installed")
	}
}

// Keep one real HTTP fixture for declared-size mismatch before file creation.
func TestHTTPDownloadSizeMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "bad") }))
	defer srv.Close()
	err := download(context.Background(), asset{URL: srv.URL, Size: 10}, filepath.Join(t.TempDir(), "update"), srv.Client(), func(int64, int64, string) {})
	if err == nil {
		t.Fatal("mismatching content length accepted")
	}
}

func TestSuccessfulRetrySupersedesOldFailure(t *testing.T) {
	j, failedDir := makeJob(t)
	writeOutcome := func(dir string, success bool, message string, age time.Duration) {
		t.Helper()
		raw, _ := json.Marshal(j)
		if err := os.WriteFile(filepath.Join(dir, "job.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		raw, _ = json.Marshal(result{Target: j.Target, Success: success, Message: message})
		filename := filepath.Join(dir, "result.json")
		if err := os.WriteFile(filename, raw, 0600); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(filename, at, at); err != nil {
			t.Fatal(err)
		}
	}
	writeOutcome(failedDir, false, "old failure", 5*time.Minute)
	if got := previousResult(j.Target); got != "old failure" {
		t.Fatalf("failure notice = %q", got)
	}
	successDir, err := os.MkdirTemp(filepath.Dir(j.Target), ".playlist-ai-update-")
	if err != nil {
		t.Fatal(err)
	}
	j.Payload = filepath.Join(successDir, "payload")
	writeOutcome(successDir, true, "", 2*time.Minute)
	if got := previousResult(j.Target); got != "" {
		t.Fatalf("stale failure survived success: %q", got)
	}
	if got := previousResult(j.Target); got != "" {
		t.Fatalf("cleanup resurrected failure: %q", got)
	}
	if _, err := os.Stat(filepath.Join(failedDir, "payload")); err != nil {
		t.Fatal("recovery payload removed")
	}
}

func TestHelperEnvironmentPreservesConfigurationAndDropsMountPaths(t *testing.T) {
	t.Setenv("PLAYLISTAI_CONFIG", "custom.toml")
	t.Setenv("APPDIR", "/tmp/old-mount")
	t.Setenv("APPIMAGE", "/home/me/playlist-ai.AppImage")
	t.Setenv("LD_LIBRARY_PATH", "/tmp/old-mount/lib")
	t.Setenv("PATH", strings.Join([]string{"/tmp/old-mount/bin", "/usr/bin"}, string(os.PathListSeparator)))
	values := map[string]string{}
	for _, entry := range cleanEnvironment() {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	if !filepath.IsAbs(values["PLAYLISTAI_CONFIG"]) || values["APPIMAGE"] != "" || values["LD_LIBRARY_PATH"] != "" || strings.Contains(values["PATH"], "old-mount") {
		t.Fatal("unsafe or broken helper environment")
	}
}
