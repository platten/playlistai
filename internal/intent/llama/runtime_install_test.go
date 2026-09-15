package llama

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func writeInstallerFixture(t *testing.T, profile, content string) {
	t.Helper()
	source := installerSource(profile)
	if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte(content), 0700); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeInstallPreservesPreviousUntilActivation(t *testing.T) {
	for _, scenario := range []string{"success", "download-failed", "health-failed", "canceled", "activation-failed"} {
		t.Run(scenario, func(t *testing.T) {
			stage := filepath.Join(t.TempDir(), "staged")
			old := filepath.Join(stage, stagedName("primary"))
			touchExec(t, old)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			run := func(ctx context.Context, profile string, extra []string, _ func(string)) error {
				calls++
				if got := StagedRuntimes(stage); len(got) != 1 || got[0].Path != old {
					t.Fatalf("published while incomplete: %+v", got)
				}
				if scenario == "download-failed" && (calls == 2 || runtime.GOOS == "darwin") {
					return errors.New("download failed")
				}
				writeInstallerFixture(t, profile, fmt.Sprintf("runtime-%d", calls))
				if scenario == "canceled" {
					cancel()
				}
				return ctx.Err()
			}
			validate := func(context.Context, string) error {
				if scenario == "health-failed" {
					return errors.New("invalid runtime")
				}
				return nil
			}
			if scenario == "activation-failed" {
				if err := os.Mkdir(filepath.Join(stage, "active"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			err := installOfficial(ctx, stage, nil, run, validate)
			if scenario == "success" {
				if err != nil {
					t.Fatal(err)
				}
				got := StagedRuntimes(stage)
				expected := 2
				if runtime.GOOS == "darwin" {
					expected = 1
				}
				if len(got) != expected || got[0].Path == old {
					t.Fatalf("not activated: %+v", got)
				}
				if _, err := os.Stat(filepath.Join(filepath.Dir(got[0].Path), "scratch")); !os.IsNotExist(err) {
					t.Fatalf("scratch retained: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("failure accepted")
				}
				if got := StagedRuntimes(stage); len(got) != 1 || got[0].Path != old {
					t.Fatalf("lost old runtime: %+v", got)
				}
				generations, _ := filepath.Glob(filepath.Join(stage, "generation-*"))
				if len(generations) != 0 {
					t.Fatalf("failed stage retained: %v", generations)
				}
			}
			if _, err := os.Stat(old); err != nil {
				t.Fatalf("previous executable removed: %v", err)
			}
		})
	}
}

func TestConcurrentRuntimeInstallsOwnTheirScratch(t *testing.T) {
	stage := t.TempDir()
	var profiles sync.Map
	var ready sync.WaitGroup
	ready.Add(2)
	var done sync.WaitGroup
	done.Add(2)
	for _, label := range []string{"first", "second"} {
		go func() {
			defer done.Done()
			phase := 0
			run := func(_ context.Context, profile string, _ []string, _ func(string)) error {
				if _, seen := profiles.LoadOrStore(profile, true); seen {
					return errors.New("shared scratch")
				}
				phase++
				if phase == 1 {
					ready.Done()
					ready.Wait()
				}
				source := installerSource(profile)
				if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
					return err
				}
				return os.WriteFile(source, []byte(label), 0700)
			}
			if err := installOfficial(context.Background(), stage, nil, run, func(context.Context, string) error { return nil }); err != nil {
				t.Error(err)
			}
		}()
	}
	done.Wait()
	var selected string
	for _, rt := range StagedRuntimes(stage) {
		raw, err := os.ReadFile(rt.Path)
		if err != nil {
			t.Fatal(err)
		}
		if selected != "" && string(raw) != selected {
			t.Fatal("mixed runtime generation")
		}
		selected = string(raw)
	}
	if selected == "" {
		t.Fatal("no complete runtime generation")
	}
}

func TestInstallerSubprocessUsesPrivateProfile(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(root, "external")
	if err := os.MkdirAll(external, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(external, "independent-runtime")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(root, "private profile 'unicode-é'")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	name, _ := installerScript(runtime.GOOS)
	script := filepath.Join(profile, name)
	body := "mkdir -p \"$HOME/.llama-app\"\nprintf fixture > \"$HOME/.llama-app/llama\"\n"
	if runtime.GOOS == "windows" {
		body = "$ErrorActionPreference='Stop'\n$fixtureDir=Join-Path $env:LOCALAPPDATA 'llama-app'\nNew-Item -ItemType Directory -Force -Path $fixtureDir | Out-Null\n[System.IO.File]::WriteAllText((Join-Path $fixtureDir 'llama.exe'),'fixture')\n"
	}
	if err := os.WriteFile(script, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := installerCommand(context.Background(), profile, script, nil)
	cmd.Env = installerEnvironment([]string{"PATH=" + os.Getenv("PATH"), "SystemRoot=" + os.Getenv("SystemRoot"), "LOCALAPPDATA=" + external, "HOME=" + external}, profile, nil)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("private installer: %v: %s", err, output)
	}
	if raw, err := os.ReadFile(installerSource(profile)); err != nil || string(raw) != "fixture" {
		t.Fatalf("unexpected output: %q %v", raw, err)
	}
	if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "keep" {
		t.Fatal("external runtime changed")
	}
}

func TestInstallerEnvironmentRemovesOverridesAndPreservesParent(t *testing.T) {
	original := []string{"HOME=external", "LocalAppData=external", "HF_TOKEN=secret", "LLAMA_BUCKET=untrusted", "LLAMA_VERSION=unexpected", "SKIP_CUDA=1", "PATH=bin"}
	got := installerEnvironment(original, "private", nil)
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "external") || strings.Contains(joined, "secret") || strings.Contains(joined, "untrusted") || strings.Contains(joined, "SKIP_CUDA=") {
		t.Fatalf("unsafe environment: %v", got)
	}
	if original[0] != "HOME=external" {
		t.Fatal("mutated parent environment")
	}
	if !strings.Contains(joined, "SKIP_INSTALL=1") || !strings.Contains(joined, "HOME=private") || !strings.Contains(joined, "LOCALAPPDATA=private") {
		t.Fatal("missing output confinement")
	}
}

func TestDownloadInstallerScriptRequiresPinnedBytes(t *testing.T) {
	source := []byte("approved installer fixture")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(source) }))
	defer server.Close()
	target := filepath.Join(t.TempDir(), "install")
	if err := downloadInstallerScript(context.Background(), server.Client(), server.URL, target, strings.Repeat("0", 64)); err == nil {
		t.Fatal("untrusted installer accepted")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("untrusted installer written")
	}
	if err := downloadInstallerScript(context.Background(), server.Client(), server.URL, target, fmt.Sprintf("%x", sha256.Sum256(source))); err != nil {
		t.Fatal(err)
	}
}

func TestStagedRuntimePointerRejectsExternalPaths(t *testing.T) {
	for _, name := range []string{"../outside", "generation-../outside", "generation-..\\outside", "generation-x:stream", strings.Repeat("x", 1025)} {
		stage := t.TempDir()
		touchExec(t, filepath.Join(stage, stagedName("primary")))
		if err := os.WriteFile(filepath.Join(stage, "active"), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		if got := StagedRuntimes(stage); len(got) != 1 || filepath.Dir(got[0].Path) != stage {
			t.Fatalf("unsafe pointer %q: %+v", name, got)
		}
	}
}
