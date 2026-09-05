// Package browser opens a URL in the user's default browser.
//
// It exists because Wails v3's own opener (application.Browser.OpenURL) shells
// out to xdg-open only, which is absent on WSL and on minimal Linux installs —
// there the Soundiiz handoff would appear to do nothing. The candidate list
// below also reaches the Windows browser through the WSL interop. Mechanism
// follows github.com/platten/playlistforge (browser.go).
package browser

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
)

// Indirected for tests.
var (
	lookPath  = exec.LookPath
	startProc = func(c *exec.Cmd) error { return c.Start() }
)

// OpenURL launches target in the user's default browser, trying each opener in
// turn and returning nil as soon as one starts. target is expected to be an
// already-validated https URL (see soundiizhandoff.validateShareURL); callers
// must not pass unsanitized input — the WSL interop openers route through
// cmd.exe.
func OpenURL(target string) error {
	if target == "" {
		return errors.New("browser: empty URL")
	}

	var candidates [][]string
	switch runtime.GOOS {
	case "darwin":
		candidates = [][]string{{"open", target}}
	case "windows":
		candidates = [][]string{{"rundll32", "url.dll,FileProtocolHandler", target}}
	default: // linux, including WSL
		candidates = [][]string{
			{"xdg-open", target},
			{"wslview", target},
			{"cmd.exe", "/c", "start", "", target},
			{"powershell.exe", "-NoProfile", "-Command", "Start-Process", target},
			{"sensible-browser", target},
			{"x-www-browser", target},
			{"gio", "open", target},
		}
	}

	lastErr := errors.New("browser: no opener available")
	for _, argv := range candidates {
		path, err := lookPath(argv[0])
		if err != nil {
			lastErr = err
			continue
		}
		cmd := exec.Command(path, argv[1:]...) //nolint:gosec // argv is a fixed list; target is a validated https URL
		if err := startProc(cmd); err != nil {
			lastErr = fmt.Errorf("%s: %w", argv[0], err)
			continue
		}
		go func() { _ = cmd.Wait() }() // reap without blocking
		return nil
	}
	return lastErr
}
