package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/platten/playlistai/internal/process"
)

type Manager struct {
	mu           sync.Mutex
	current      string
	offer        Offer
	asset        asset
	installation installation
	checked      bool
	installing   bool
	cancel       context.CancelFunc
}

func New(current string) *Manager { return &Manager{current: current} }

func (m *Manager) ReleaseURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.offer.URL != "" {
		return m.offer.URL
	}
	return Repository + "/releases/latest"
}

// Check is cached for this app session, including unsuccessful checks, to avoid
// startup remounts or multiple windows making duplicate API requests.
func (m *Manager) Check(ctx context.Context) (Offer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.checked {
		return m.offer, nil
	}
	m.checked = true
	m.offer = Offer{Current: m.current}
	if _, ok := versionParts(m.current); !ok {
		return m.offer, nil
	}
	i, installErr := detectInstallation()
	m.installation = i
	m.offer.Notice = previousResult(i.Target)
	o, a, err := check(ctx, m.current, i, releaseClient(15*time.Second), latestURL)
	if err != nil {
		return m.offer, err
	}
	if installErr != nil {
		o.CanInstall, o.Reason = false, installErr.Error()
	}
	if o.CanInstall && i.Kind != "windows-installer" {
		probe, err := os.MkdirTemp(filepath.Dir(i.Target), ".playlist-ai-update-probe-")
		if err != nil {
			o.CanInstall = false
			o.Reason = "The application folder is read-only or requires administrator access. Move Playlist AI to a writable folder, or install the update from the release page."
		} else {
			_ = os.Remove(probe)
		}
	}
	o.Notice = m.offer.Notice
	m.offer, m.asset = o, a
	return o, nil
}

func (m *Manager) Cancel() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
}

// Install returns only after a trusted helper is ready to wait for this process
// to exit. The desktop caller can then quit normally, closing all data stores.
func (m *Manager) Install(parent context.Context, progress func(int64, int64, string)) error {
	m.mu.Lock()
	if !m.offer.CanInstall || !m.checked || m.installing {
		m.mu.Unlock()
		return fmt.Errorf("no checked update is available, or an update is already running")
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Minute)
	m.installing, m.cancel = true, cancel
	a, i := m.asset, m.installation
	m.mu.Unlock()
	handedOff := false
	defer func() {
		cancel()
		m.mu.Lock()
		m.cancel = nil
		m.installing = handedOff
		m.mu.Unlock()
	}()
	stagingParent := filepath.Dir(i.Target)
	if i.Kind == "windows-installer" {
		var err error
		stagingParent, err = installerStagingRoot()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(stagingParent, 0700); err != nil {
			return err
		}
	}
	dir, err := os.MkdirTemp(stagingParent, ".playlist-ai-update-")
	if err != nil {
		return fmt.Errorf("cannot write to the application folder: %w", err)
	}
	defer func() {
		if !handedOff {
			_ = os.RemoveAll(dir)
		}
	}()
	archive := filepath.Join(dir, a.Name)
	if err := download(ctx, a, archive, releaseClient(0), progress); err != nil {
		return err
	}
	progress(0, 0, "Verifying the application")
	payload, err := unpack(ctx, archive, dir, i)
	if err != nil {
		return err
	}
	if err := verifyArchitecture(payloadExecutable(payload, i.Kind), i.Kind, i.Arch); err != nil {
		return err
	}
	if err := verifySignature(ctx, i.Target, payload); err != nil {
		return err
	}
	oldHash, err := treeHash(i.Target)
	if err != nil {
		return err
	}
	newHash, err := treeHash(payload)
	if err != nil {
		return err
	}
	j := job{Target: i.Target, Payload: payload, Kind: i.Kind, ParentPID: os.Getpid(), OldHash: oldHash, NewHash: newHash}
	raw, err := json.Marshal(j)
	if err != nil {
		return err
	}
	jobPath := filepath.Join(dir, "job.json")
	if err := os.WriteFile(jobPath, raw, 0600); err != nil {
		return err
	}
	helperSource := i.Executable
	if i.Kind == "appimage" {
		helperSource = i.Target
	}
	helper := filepath.Join(dir, "update-helper")
	if i.Kind == "windows" || i.Kind == "windows-installer" {
		helper += ".exe"
	}
	if err := copyExecutable(helperSource, helper); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd := exec.Command(helper, "--app-update-worker", jobPath)
	cmd.Env = cleanEnvironment()
	cmd.Dir = dir
	process.Background(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot start the update helper: %w", err)
	}
	defer func() {
		if !handedOff {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		} else {
			_ = cmd.Process.Release()
		}
	}()
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("the update helper did not become ready; the application has not been changed")
		case <-tick.C:
			if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
				if err := ctx.Err(); err != nil {
					return err
				}
				// A canceled or failed handoff never authorizes replacement.
				if err := os.WriteFile(filepath.Join(dir, "commit"), []byte("approved"), 0600); err != nil {
					return err
				}
				handedOff = true
				progress(0, 0, "Restarting to install the update")
				return nil
			}
		}
	}
}

func download(ctx context.Context, a asset, path string, client *http.Client, progress func(int64, int64, string)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Playlist-AI-Updater")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update download: GitHub HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > 0 && resp.ContentLength != a.Size {
		return fmt.Errorf("update download size changed")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 128<<10)
	var done int64
	reader := io.LimitReader(resp.Body, a.Size+1)
	last := time.Time{}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := reader.Read(buf)
		done += int64(n)
		if done > a.Size {
			return fmt.Errorf("update download exceeds declared size")
		}
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				return err
			}
			_, _ = h.Write(buf[:n])
		}
		if time.Since(last) >= 100*time.Millisecond || done == a.Size {
			progress(done, a.Size, "Downloading the update")
			last = time.Now()
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if done != a.Size || hex.EncodeToString(h.Sum(nil)) != strings.TrimPrefix(a.Digest, "sha256:") {
		return fmt.Errorf("update checksum or size verification failed; the current application is unchanged")
	}
	return f.Sync()
}

func copyExecutable(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	syncErr := out.Sync()
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
