package updater

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/process"
)

type job struct {
	Target    string
	Payload   string
	Kind      string
	ParentPID int
	OldHash   string
	NewHash   string
}
type result struct {
	Target  string `json:"target"`
	Message string `json:"message"`
	Success bool   `json:"success"`
}

func readJob(filename string) (job, error) {
	var j job
	info, err := os.Lstat(filename)
	if err != nil {
		return j, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16384 {
		return j, fmt.Errorf("invalid update job")
	}
	raw, err := os.ReadFile(filename)
	if err != nil {
		return j, err
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return j, err
	}
	dir := filepath.Dir(filename)
	stagingParent := filepath.Dir(j.Target)
	if j.Kind == "windows-installer" {
		if runtime.GOOS != "windows" || !strings.EqualFold(filepath.Base(j.Target), "playlist-ai.exe") {
			return j, fmt.Errorf("invalid installer target")
		}
		stagingParent, err = installerStagingRoot()
		if err != nil {
			return j, err
		}
	}
	if !filepath.IsAbs(dir) || !strings.HasPrefix(filepath.Base(dir), ".playlist-ai-update-") || filepath.Dir(dir) != stagingParent || j.ParentPID <= 0 || j.ParentPID == os.Getpid() {
		return j, fmt.Errorf("invalid update job location or parent")
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil || resolved != dir {
		return j, fmt.Errorf("update staging folder must not be a link")
	}
	rel, err := filepath.Rel(dir, j.Payload)
	if err != nil || !filepath.IsLocal(rel) || rel == "." {
		return j, fmt.Errorf("update payload is outside staging")
	}
	if j.Kind != "linux" && j.Kind != "windows" && j.Kind != "windows-installer" && j.Kind != "darwin" && j.Kind != "appimage" {
		return j, fmt.Errorf("invalid update kind")
	}
	return j, nil
}

// RunWorker is dispatched before any desktop, model or catalog startup. It
// never terminates the parent, and cannot replace anything without the commit
// marker written after explicit user acceptance and a successful handoff.
func RunWorker(filename string) error {
	j, err := readJob(filename)
	if err != nil {
		return err
	}
	dir := filepath.Dir(filename)
	record := func(success bool, message string) {
		raw, _ := json.Marshal(result{Target: j.Target, Success: success, Message: message})
		_ = os.WriteFile(filepath.Join(dir, "result.json"), raw, 0600)
	}
	if hash, err := treeHash(j.Payload); err != nil || hash != j.NewHash {
		return fmt.Errorf("staged update changed before handoff")
	}
	if err := os.WriteFile(filepath.Join(dir, "ready"), []byte("ready"), 0600); err != nil {
		return err
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		alive, err := processAlive(j.ParentPID)
		if err != nil {
			record(false, "The updater could not confirm that Playlist AI closed. The application was not replaced.")
			return err
		}
		if !alive {
			break
		}
		if time.Now().After(deadline) {
			record(false, "Playlist AI did not close within two minutes. The application was not replaced. Quit and reopen it to try again.")
			return fmt.Errorf("waiting for application exit timed out")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(dir, "commit")); err != nil {
		return fmt.Errorf("update handoff was not committed")
	}
	if j.Kind == "windows-installer" {
		if hash, err := treeHash(j.Target); err != nil || hash != j.OldHash {
			return fmt.Errorf("installed application changed")
		}
		backup := filepath.Join(dir, "previous.exe")
		if err := copyExecutable(j.Target, backup); err != nil {
			record(false, "Could not save a backup before running the installer: "+err.Error())
			_ = launch(j.Target, "windows")
			return err
		}
		if err := runInstaller(j.Payload, filepath.Dir(j.Target), j.NewHash); err != nil { //nolint:staticcheck // The non-Windows stub always fails; the Windows implementation can succeed.
			record(false, "The Windows update installer did not complete: "+err.Error()+". A backup of the previous executable is at "+backup+". Retry the update or use the release installer.")
			_ = launch(j.Target, "windows")
			return err
		}
		if err := launch(j.Target, "windows"); err != nil {
			record(false, "The installer finished but Playlist AI could not restart: "+err.Error())
			return err
		}
		record(true, "")
		return nil
	}
	if err := replace(j, filepath.Join(dir, "previous"), launch); err != nil {
		record(false, "The update could not be installed: "+err.Error()+". Your previous application was retained; retry or use the release installer.")
		// If replacement rolled back successfully, reopen the old application so
		// its startup check can show the persisted failure notice.
		if hash, hashErr := treeHash(j.Target); hashErr == nil && hash == j.OldHash {
			_ = launch(j.Target, j.Kind)
		}
		return err
	}
	record(true, "")
	// Retain the backup until a subsequent normal startup confirms that the
	// application can run. That startup removes completed staging directories.
	return nil
}

func replace(j job, backup string, start func(string, string) error) error {
	if hash, err := treeHash(j.Target); err != nil || hash != j.OldHash {
		return fmt.Errorf("the installed application changed while the update was pending")
	}
	if hash, err := treeHash(j.Payload); err != nil || hash != j.NewHash {
		return fmt.Errorf("the staged application changed")
	}
	if _, err := os.Lstat(backup); !os.IsNotExist(err) {
		return fmt.Errorf("update backup already exists")
	}
	if err := renameRetry(j.Target, backup); err != nil {
		return fmt.Errorf("cannot retain the old application: %w", err)
	}
	rollback := func(cause error) error {
		if err := os.Rename(backup, j.Target); err != nil {
			return fmt.Errorf("%v; recovery failed (%v); the previous application remains at %s", cause, err, backup)
		}
		return cause
	}
	if err := os.Rename(j.Payload, j.Target); err != nil {
		return rollback(err)
	}
	if err := start(j.Target, j.Kind); err != nil {
		if moveErr := os.Rename(j.Target, j.Payload); moveErr != nil {
			return fmt.Errorf("restart failed: %v; previous application remains at %s", err, backup)
		}
		return rollback(fmt.Errorf("restart failed: %w", err))
	}
	return nil
}

func renameRetry(from, to string) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := os.Rename(from, to)
		if err == nil || runtime.GOOS != "windows" || time.Now().After(deadline) {
			return err
		}
		time.Sleep(200 * time.Millisecond) // Windows antivirus may briefly retain a handle.
	}
}

func launch(target, kind string) error {
	cmd := exec.Command(target)
	if kind == "darwin" {
		cmd = exec.Command("/usr/bin/open", "-n", target)
	}
	cmd.Dir = filepath.Dir(target)
	cmd.Env = cleanEnvironment()
	process.Background(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	if kind == "darwin" {
		return cmd.Wait()
	}
	return cmd.Process.Release()
}

func cleanEnvironment() []string {
	var out []string
	appDir := os.Getenv("APPDIR")
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "PLAYLISTAI_CONFIG":
			if value != "" && !filepath.IsAbs(value) {
				if absolute, err := filepath.Abs(value); err == nil {
					entry = key + "=" + absolute
				}
			}
		case "APPIMAGE", "APPDIR", "ARGV0", "OWD", "LD_LIBRARY_PATH", "LD_PRELOAD":
			continue
		case "PATH":
			if appDir != "" {
				var parts []string
				for _, p := range filepath.SplitList(value) {
					if p != appDir && !strings.HasPrefix(p, appDir+string(filepath.Separator)) {
						parts = append(parts, p)
					}
				}
				entry = key + "=" + strings.Join(parts, string(os.PathListSeparator))
			}
		}
		out = append(out, entry)
	}
	return out
}

func previousResult(target string) string {
	if target == "" {
		return ""
	}
	dirs, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".playlist-ai-update-*"))
	if runtime.GOOS == "windows" {
		if root, err := installerStagingRoot(); err == nil {
			cached, _ := filepath.Glob(filepath.Join(root, ".playlist-ai-update-*"))
			dirs = append(dirs, cached...)
		}
	}
	type outcome struct {
		dir    string
		at     time.Time
		result result
	}
	var outcomes []outcome
	var latest outcome
	for _, dir := range dirs {
		j, err := readJob(filepath.Join(dir, "job.json"))
		if err != nil || j.Target != target {
			continue
		}
		filename := filepath.Join(dir, "result.json")
		info, err := os.Lstat(filename)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 16384 {
			continue
		}
		raw, err := os.ReadFile(filename)
		if err != nil {
			continue
		}
		var r result
		if json.Unmarshal(raw, &r) != nil || r.Target != target {
			continue
		}
		o := outcome{dir: dir, at: info.ModTime(), result: r}
		outcomes = append(outcomes, o)
		if o.at.After(latest.at) {
			latest = o
		}
	}
	for _, o := range outcomes {
		if !o.result.Success {
			if latest.result.Success && o.at.Before(latest.at) {
				_ = os.Rename(filepath.Join(o.dir, "result.json"), filepath.Join(o.dir, "superseded-result.json"))
			}
			continue
		}
		// Do not race the helper's exit (notably its executable lock on Windows).
		if time.Since(o.at) > time.Minute {
			_ = os.RemoveAll(o.dir)
		}
	}
	return latest.result.Message
}

func installerStagingRoot() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "playlist-ai", "updates"), nil
}
