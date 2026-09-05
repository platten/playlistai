package browser

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func stub(t *testing.T, found map[string]bool, startErr map[string]error) *[]string {
	t.Helper()
	var mu sync.Mutex
	var started []string

	origLook, origStart := lookPath, startProc
	lookPath = func(file string) (string, error) {
		if found[file] {
			return "/usr/bin/" + file, nil
		}
		return "", exec.ErrNotFound
	}
	startProc = func(c *exec.Cmd) error {
		name := c.Path
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		if err := startErr[name]; err != nil {
			return err
		}
		mu.Lock()
		started = append(started, name)
		mu.Unlock()
		return nil
	}
	t.Cleanup(func() { lookPath, startProc = origLook, origStart })
	return &started
}

func TestOpenURLEmpty(t *testing.T) {
	if err := OpenURL(""); err == nil {
		t.Fatal("want error for empty URL")
	}
}

func TestOpenURLFallsThroughToWorkingOpener(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("candidate list is linux-specific")
	}
	// xdg-open and wslview are missing (the WSL case); cmd.exe works.
	started := stub(t,
		map[string]bool{"cmd.exe": true, "powershell.exe": true},
		nil,
	)
	if err := OpenURL("https://soundiiz.com/go/import-playlist/abc"); err != nil {
		t.Fatalf("OpenURL: %v", err)
	}
	if len(*started) != 1 || (*started)[0] != "cmd.exe" {
		t.Fatalf("want cmd.exe started, got %v", *started)
	}
}

func TestOpenURLSkipsOpenerThatFailsToStart(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("candidate list is linux-specific")
	}
	// xdg-open is present but fails to exec; powershell.exe is the next that works.
	started := stub(t,
		map[string]bool{"xdg-open": true, "powershell.exe": true},
		map[string]error{"xdg-open": errors.New("boom")},
	)
	if err := OpenURL("https://soundiiz.com/go/import-playlist/abc"); err != nil {
		t.Fatalf("OpenURL: %v", err)
	}
	if len(*started) != 1 || (*started)[0] != "powershell.exe" {
		t.Fatalf("want powershell.exe started, got %v", *started)
	}
}

func TestOpenURLNoOpenerAvailable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("candidate list is linux-specific")
	}
	stub(t, map[string]bool{}, nil)
	if err := OpenURL("https://soundiiz.com/go/import-playlist/abc"); err == nil {
		t.Fatal("want error when no opener is on PATH")
	}
}
