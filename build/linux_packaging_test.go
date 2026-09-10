package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Exercise the actual task runner without building packages or changing bin/.
// nfpm defaults an empty GOARCH to amd64 even on native ARM64 hosts.
func TestLinuxPackageTasksExportTargetArchitecture(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux package task fixture requires a POSIX shell")
	}
	cli, err := exec.LookPath("wails3")
	if err != nil {
		t.Skip("wails3 is required for the packaging integration check")
	}
	taskfile, err := filepath.Abs("linux/Taskfile.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"native", "amd64", "arm64"} {
		for _, format := range []string{"deb", "rpm", "aur"} {
			t.Run(target+"/"+format, func(t *testing.T) {
				dir := t.TempDir()
				shimDir := filepath.Join(dir, "shim")
				if err := os.Mkdir(shimDir, 0o700); err != nil {
					t.Fatal(err)
				}
				files := map[string]string{
					filepath.Join(dir, "Taskfile.yml"): fmt.Sprintf("version: '3'\nvars:\n  APP_NAME: playlist-ai\nincludes:\n  linux: %q\n", taskfile),
					filepath.Join(shimDir, "wails3"):   "#!/bin/sh\nprintf '%s' \"$GOARCH\" > \"$PLAYLISTAI_PACKAGE_ENV_LOG\"\n",
				}
				for path, contents := range files {
					if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
						t.Fatal(err)
					}
				}
				args := []string{"task", "linux:generate:" + format}
				want := runtime.GOARCH
				if target != "native" {
					args = append(args, "ARCH="+target)
					want = target
				}
				logPath := filepath.Join(dir, "architecture")
				cmd := exec.Command(cli, args...)
				cmd.Dir = dir
				for _, variable := range os.Environ() {
					if !strings.HasPrefix(variable, "GOARCH=") && !strings.HasPrefix(variable, "ARCH=") && !strings.HasPrefix(variable, "PATH=") {
						cmd.Env = append(cmd.Env, variable)
					}
				}
				cmd.Env = append(cmd.Env, "PATH="+shimDir+string(os.PathListSeparator)+os.Getenv("PATH"), "PLAYLISTAI_PACKAGE_ENV_LOG="+logPath)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("package task: %v\n%s", err, output)
				}
				got, err := os.ReadFile(logPath)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != want {
					t.Fatalf("GOARCH = %q, want %q", got, want)
				}
			})
		}
	}
}
