package llama

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/process"
)

// These scripts were reviewed for their output paths. Pinning the script as
// well as isolating its child environment keeps future upstream changes from
// silently extending the directories this installer writes or removes.
// https://github.com/ggml-org/llama-install.sh/tree/578ef7d479daf6c4cbe1ce2deb673c89be20201e
const installerRevision = "578ef7d479daf6c4cbe1ce2deb673c89be20201e"
const installerScriptLimit = 128 << 10

func installerScript(platform string) (name, checksum string) {
	if platform == "windows" {
		return "install.ps1", "455084203db0c864f4eb218bc82792b4304458a96211c385275d8337a5049851"
	}
	return "install.sh", "cccdfcbd1b55bf6003ac3037588c9f5b3b79aa0a75fe991e97bb218ccdb55e4d"
}

// Legacy flat installations remain discoverable until a complete generation
// has been activated. A malformed pointer never supplies an external path.
func activeRuntimeDirectory(stageDir string) string {
	f, err := os.Open(filepath.Join(stageDir, "active"))
	if err != nil {
		return stageDir
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 1025))
	name := string(raw)
	if err != nil || len(raw) > 1024 || !strings.HasPrefix(name, "generation-") || filepath.Base(name) != name || strings.ContainsAny(name, `/\:`) {
		return stageDir
	}
	dir := filepath.Join(stageDir, name)
	for _, label := range []string{"primary", "cpu"} {
		if isFile(filepath.Join(dir, stagedName(label))) {
			return dir
		}
	}
	return stageDir
}

// CleanStaged removes only the explicitly supplied app-owned runtime directory.
// It must never be used before replacement: InstallOfficial retains the active
// generation while preparing its successor.
func CleanStaged(stageDir string) { _ = os.RemoveAll(stageDir) }

type installerRunner func(context.Context, string, []string, func(string)) error
type runtimeValidator func(context.Context, string) error

// InstallOfficial stages the complete GPU/CPU pair in a fresh generation. The
// official script runs with a private child profile, so its fixed llama-app
// output and legacy migration cannot touch an independently installed runtime.
// Only the small activation pointer changes after both builds pass validation.
func InstallOfficial(ctx context.Context, stageDir string, report func(int, int, string)) error {
	return installOfficial(ctx, stageDir, report, runInstaller, validateRuntimeBinary)
}

func installOfficial(ctx context.Context, stageDir string, report func(int, int, string), run installerRunner, validate runtimeValidator) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	if report == nil {
		report = func(int, int, string) {}
	}
	stageDir, err = filepath.Abs(stageDir)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(stageDir, 0700); err != nil {
		return err
	}
	generation, err := os.MkdirTemp(stageDir, "generation-")
	if err != nil {
		return err
	}
	activated := false
	defer func() {
		if !activated {
			err = errors.Join(err, os.RemoveAll(generation))
		}
	}()
	scratch := filepath.Join(generation, "scratch")
	if err = os.Mkdir(scratch, 0700); err != nil {
		return err
	}
	steps := 2
	if runtime.GOOS == "darwin" {
		steps = 1
	}
	for step := 1; step <= steps; step++ {
		label := "primary"
		var extra []string
		if step == 2 {
			label = "cpu"
			extra = []string{"SKIP_CUDA=1", "SKIP_ROCM=1", "SKIP_VULKAN=1"}
		}
		profile := filepath.Join(scratch, label)
		if err = os.Mkdir(profile, 0700); err != nil {
			return err
		}
		report(step, steps, "installing "+label+" build")
		if err = run(ctx, profile, extra, func(line string) { report(step, steps, line) }); err != nil {
			return fmt.Errorf("%s install: %w", label, err)
		}
		target := filepath.Join(generation, stagedName(label))
		if err = stageBinary(ctx, installerSource(profile), target); err != nil {
			return err
		}
		if err = validate(ctx, target); err != nil {
			return fmt.Errorf("%s runtime validation: %w", label, err)
		}
	}
	// Remove only this operation's scratch, before committing a usable result.
	if err = os.RemoveAll(scratch); err != nil {
		return fmt.Errorf("clean runtime installer scratch: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	pointer, err := os.CreateTemp(stageDir, ".active-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(pointer.Name()) }()
	_, writeErr := io.WriteString(pointer, filepath.Base(generation))
	if err = errors.Join(writeErr, pointer.Sync(), pointer.Close()); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Rename(pointer.Name(), filepath.Join(stageDir, "active")); err != nil {
		return err
	}
	activated = true
	report(steps, steps, "ready")
	return nil
}

func installerSource(profile string) string {
	dir := ".llama-app"
	if runtime.GOOS == "windows" {
		dir = "llama-app"
	}
	return filepath.Join(profile, dir, exeName("llama"))
}

func installerEnvironment(base []string, profile string, extra []string) []string {
	// Modify only the child profile, never the application's process environment.
	// Do not forward credentials or unofficial distribution overrides to helpers.
	replacements := map[string]string{"HOME": profile, "USERPROFILE": profile, "LOCALAPPDATA": profile, "APPDATA": filepath.Join(profile, "roaming"), "SKIP_INSTALL": "1"}
	blocked := map[string]bool{"HF_TOKEN": true, "LLAMA_BUCKET": true, "LLAMA_VERSION": true, "SKIP_CUDA": true, "SKIP_ROCM": true, "SKIP_VULKAN": true}
	env := make([]string, 0, len(base)+len(replacements)+len(extra))
	for _, item := range base {
		key, _, _ := strings.Cut(item, "=")
		if _, replaced := replacements[strings.ToUpper(key)]; !replaced && !blocked[strings.ToUpper(key)] {
			env = append(env, item)
		}
	}
	for _, key := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA", "SKIP_INSTALL"} {
		env = append(env, key+"="+replacements[key])
	}
	return append(env, extra...)
}

func installerCommand(ctx context.Context, profile, script string, extra []string) *exec.Cmd {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script)
	} else {
		cmd = exec.CommandContext(ctx, "sh", script)
	}
	cmd.Env = installerEnvironment(os.Environ(), profile, extra)
	cmd.Dir = profile
	cmd.WaitDelay = 2 * time.Second
	process.Background(cmd)
	return cmd
}

func runInstaller(ctx context.Context, profile string, extra []string, onLine func(string)) error {
	name, sum := installerScript(runtime.GOOS)
	script := filepath.Join(profile, name)
	client := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) >= 5 {
			return errors.New("unsafe runtime installer redirect")
		}
		return nil
	}}
	source := "https://raw.githubusercontent.com/ggml-org/llama-install.sh/" + installerRevision + "/" + name
	if err := downloadInstallerScript(ctx, client, source, script, sum); err != nil {
		return err
	}
	cmd := installerCommand(ctx, profile, script, extra)
	pr, pw := io.Pipe()
	defer pr.Close()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		return err
	}
	scanned := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 8<<10), 1<<20)
		for scanner.Scan() {
			if line := strings.TrimRight(scanner.Text(), "\r"); line != "" && onLine != nil {
				onLine(line)
			}
		}
		err := scanner.Err()
		_ = pr.CloseWithError(err)
		scanned <- err
	}()
	err := cmd.Wait()
	_ = pw.Close()
	return errors.Join(err, <-scanned)
}

func downloadInstallerScript(ctx context.Context, client *http.Client, source, target, checksum string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("runtime installer HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, installerScriptLimit+1))
	if err != nil {
		return err
	}
	if len(data) > installerScriptLimit || fmt.Sprintf("%x", sha256.Sum256(data)) != checksum {
		return errors.New("runtime installer checksum or size verification failed")
	}
	return os.WriteFile(target, data, 0600)
}

func stageBinary(ctx context.Context, src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("runtime installer produced no regular executable")
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, &runtimeContextReader{ctx, in})
	return errors.Join(copyErr, out.Sync(), out.Close())
}

type runtimeContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *runtimeContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type runtimeVersionOutput struct{ bytes.Buffer }

func (b *runtimeVersionOutput) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len() < 64<<10 {
		_, _ = b.Buffer.Write(p[:min(n, (64<<10)-b.Len())])
	}
	return n, nil
}
func validateRuntimeBinary(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version")
	cmd.Dir = filepath.Dir(path)
	cmd.WaitDelay = 2 * time.Second
	process.Background(cmd)
	var output runtimeVersionOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		return err
	}
	if strings.TrimSpace(output.String()) == "" {
		return errors.New("runtime returned no version")
	}
	return nil
}
