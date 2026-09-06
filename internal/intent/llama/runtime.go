package llama

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// errRuntimeMissing is returned by New when no llama runtime can be found.
var errRuntimeMissing = errors.New("llama: no runtime found — install llama.cpp (see the first-run wizard or https://github.com/ggml-org/llama.cpp)")

// RuntimeKind distinguishes the classic standalone server from the unified
// binary that takes a `serve` subcommand.
type RuntimeKind string

const (
	KindNone   RuntimeKind = ""
	KindServer RuntimeKind = "server" // llama-server[.exe]
	KindLlama  RuntimeKind = "llama"  // llama[.exe] — run as `llama serve`
)

// RuntimeStatus is what DetectRuntime found.
type RuntimeStatus struct {
	Available bool
	Path      string
	Kind      RuntimeKind
	// Source is where it was found: "config", "app-dir", "user-bin",
	// "llama-app", or "path".
	Source string
}

func (s RuntimeStatus) subcmd() []string {
	if s.Kind == KindLlama {
		return []string{"serve"}
	}
	return nil
}

// Runtime is one resolved llama runtime binary. Label is "gpu" or "cpu" for
// the two builds the installer stages, or "" for a manually-installed one.
type Runtime struct {
	Path  string
	Kind  RuntimeKind
	Label string
}

// Device is one accelerator that a particular llama.cpp runtime can use.
// Memory values come from llama.cpp's --list-devices output.
type Device struct {
	ID         string
	Name       string
	TotalBytes int64
	FreeBytes  int64
}

var deviceLine = regexp.MustCompile(`(?m)^\s*([^:\s]+):\s+(.+?)\s+\(([0-9]+)\s+MiB,\s+([0-9]+)\s+MiB free\)\s*$`)

// ProbeDevices asks the runtime itself which accelerator backends it can use.
// This deliberately does not rely on nvidia-smi or OS GPU inventory: a GPU is
// useful here only when this exact llama.cpp build can offload to it.
func ProbeDevices(ctx context.Context, r Runtime) ([]Device, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := append(r.subcmd(), "--list-devices")
	out, err := exec.CommandContext(probeCtx, r.Path, args...).CombinedOutput() //nolint:gosec // validated local runtime
	devices := ParseDeviceList(string(out))
	if len(devices) > 0 {
		return devices, nil
	}
	if err != nil {
		return nil, fmt.Errorf("llama: list devices: %w", err)
	}
	return nil, nil
}

// ParseDeviceList parses llama.cpp's stable human-readable device rows, such
// as "CUDA0: NVIDIA GeForce RTX 5060 (4096 MiB, 3900 MiB free)".
func ParseDeviceList(output string) []Device {
	matches := deviceLine.FindAllStringSubmatch(output, -1)
	out := make([]Device, 0, len(matches))
	for _, match := range matches {
		totalMiB, errTotal := strconv.ParseInt(match[3], 10, 64)
		freeMiB, errFree := strconv.ParseInt(match[4], 10, 64)
		if errTotal != nil || errFree != nil || totalMiB <= 0 {
			continue
		}
		out = append(out, Device{
			ID: match[1], Name: strings.TrimSpace(match[2]),
			TotalBytes: totalMiB << 20, FreeBytes: freeMiB << 20,
		})
	}
	return out
}

func (r Runtime) subcmd() []string {
	if r.Kind == KindLlama {
		return []string{"serve"}
	}
	return nil
}

func (s RuntimeStatus) asRuntime() Runtime {
	return Runtime{Path: s.Path, Kind: s.Kind}
}

func exeName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func kindOf(path string) RuntimeKind {
	b := strings.ToLower(filepath.Base(path))
	b = strings.TrimSuffix(b, ".exe")
	switch b {
	case "llama-server":
		return KindServer
	case "llama":
		return KindLlama
	}
	// An explicit path with an unusual name: assume the classic server.
	return KindServer
}

func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// installDirs are the fixed locations the official installer
// (https://llama.app/install.sh / install.ps1) drops the `llama` binary,
// which a GUI app's minimal PATH often misses.
func installDirs() []string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "windows" {
		la := os.Getenv("LOCALAPPDATA")
		if la == "" && home != "" {
			la = filepath.Join(home, "AppData", "Local")
		}
		return []string{
			filepath.Join(la, "Microsoft", "WindowsApps", "llama.exe"),
			filepath.Join(la, "llama-app", "llama.exe"),
		}
	}
	if home == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".local", "bin", "llama"),
		filepath.Join(home, ".llama-app", "llama"),
	}
}

// DetectRuntime resolves a llama runtime. Order: explicit config path → next
// to the running app → the official installer's fixed dirs → PATH.
func DetectRuntime(explicit string) RuntimeStatus {
	if explicit != "" {
		if isFile(explicit) {
			return RuntimeStatus{true, explicit, kindOf(explicit), "config"}
		}
		return RuntimeStatus{} // configured but wrong — surface as missing
	}

	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, base := range []string{"llama-server", "llama"} {
			cand := filepath.Join(dir, exeName(base))
			if isFile(cand) {
				return RuntimeStatus{true, cand, kindOf(cand), "app-dir"}
			}
		}
	}

	for _, cand := range installDirs() {
		if isFile(cand) {
			src := "user-bin"
			if strings.Contains(cand, "llama-app") {
				src = "llama-app"
			}
			return RuntimeStatus{true, cand, KindLlama, src}
		}
	}

	for _, base := range []string{"llama-server", "llama"} {
		if p, err := exec.LookPath(exeName(base)); err == nil {
			return RuntimeStatus{true, p, kindOf(p), "path"}
		}
	}

	return RuntimeStatus{}
}

// stagedName is the file name for a staged runtime label ("primary" | "cpu").
func stagedName(label string) string { return exeName("llama-" + label) }

// StagedRuntimes returns the runtimes InstallOfficial staged into stageDir,
// GPU/primary first. Empty if none are staged.
func StagedRuntimes(stageDir string) []Runtime {
	var out []Runtime
	for _, lab := range []struct{ file, label string }{{"primary", "gpu"}, {"cpu", "cpu"}} {
		p := filepath.Join(stageDir, stagedName(lab.file))
		if isFile(p) {
			out = append(out, Runtime{Path: p, Kind: KindLlama, Label: lab.label})
		}
	}
	return out
}

// installerScratch is the directory a single installer run downloads into.
// We run with SKIP_INSTALL so the installer never copies out of it, then move
// the binary into the app's data dir ourselves and delete this.
func installerScratch() string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "windows" {
		la := os.Getenv("LOCALAPPDATA")
		if la == "" && home != "" {
			la = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(la, "llama-app")
	}
	return filepath.Join(home, ".llama-app")
}

func installerSource() string { return filepath.Join(installerScratch(), exeName("llama")) }

// CleanStaged removes every trace of an app-installed llama.cpp: the staged
// runtimes under stageDir and the installer's scratch dir. Used before a
// reinstall.
func CleanStaged(stageDir string) {
	_ = os.RemoveAll(stageDir)
	_ = os.RemoveAll(installerScratch())
}

// InstallOfficial runs ggml-org's official cross-platform installer
// (https://llama.app/install.sh / install.ps1) and stages the result entirely
// under stageDir (the app's data dir) — it runs with SKIP_INSTALL so the
// installer never touches ~/.local/bin or %WindowsApps%, and its scratch dir
// is deleted afterward. Two builds are staged:
//
//   - "primary" — GPU build (CUDA / ROCm / Vulkan / Metal) when one is
//     available on this machine, else CPU.
//   - "cpu"     — a CPU-only build, always (Linux/Windows), so the app can
//     fall back to it if the GPU build won't run a given model. macOS has
//     only the Metal build, so no separate CPU stage there.
//
// report is called with (step, totalSteps, line) as each phase runs and as
// the installer streams output.
func InstallOfficial(ctx context.Context, stageDir string, report func(step, steps int, line string)) error {
	if report == nil {
		report = func(int, int, string) {}
	}
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(installerScratch()) //nolint:errcheck // best-effort cleanup

	steps := 2
	if runtime.GOOS == "darwin" {
		steps = 1 // Metal only
	}

	// Step 1: default install (GPU-capable when possible).
	report(1, steps, "installing GPU-capable build")
	if err := runInstaller(ctx, nil, func(l string) { report(1, steps, l) }); err != nil {
		return fmt.Errorf("primary install: %w", err)
	}
	if err := stageBinary(installerSource(), filepath.Join(stageDir, stagedName("primary"))); err != nil {
		return err
	}

	if steps == 2 {
		// Step 2: force CPU by skipping every GPU backend probe.
		report(2, steps, "installing CPU fallback build")
		cpuEnv := []string{"SKIP_CUDA=1", "SKIP_ROCM=1", "SKIP_VULKAN=1"}
		if err := runInstaller(ctx, cpuEnv, func(l string) { report(2, steps, l) }); err != nil {
			return fmt.Errorf("cpu install: %w", err)
		}
		if err := stageBinary(installerSource(), filepath.Join(stageDir, stagedName("cpu"))); err != nil {
			return err
		}
	}

	report(steps, steps, "ready")
	return nil
}

func runInstaller(ctx context.Context, extraEnv []string, onLine func(string)) error {
	// SKIP_INSTALL: the installer downloads to its scratch dir but does not
	// copy the binary anywhere on PATH — the app moves it into its data dir.
	env := append([]string{"SKIP_INSTALL=1"}, extraEnv...)

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		var b strings.Builder
		for _, e := range env {
			k, v, _ := strings.Cut(e, "=")
			fmt.Fprintf(&b, "$env:%s='%s'; ", k, v)
		}
		b.WriteString("irm https://llama.app/install.ps1 | iex")
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", b.String())
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", "curl -fsSL https://llama.app/install.sh | sh")
		cmd.Env = append(os.Environ(), env...)
	}

	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		return err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 0, 8<<10), 1<<20)
		for sc.Scan() {
			if line := strings.TrimRight(sc.Text(), "\r"); line != "" && onLine != nil {
				onLine(line)
			}
		}
	}()
	err := cmd.Wait()
	_ = pw.Close()
	<-done
	return err
}

func stageBinary(src, dst string) error {
	if !isFile(src) {
		return fmt.Errorf("llama: installer produced no binary at %s", src)
	}
	in, err := os.Open(src) //nolint:gosec // src is the installer's fixed output path
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755) //nolint:gosec
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp) //nolint:errcheck
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
