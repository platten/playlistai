// mertpack manages a local optional MERT installation without Python or network.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/audioruntime"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--mert-worker" {
		if err := audioruntime.RunMERT(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(os.Args[1:], os.Stdout); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: mertpack status|install-local|remove --directory <managed-model-directory> [--source <prepared-pack>]")
	}
	command := args[0]
	if command != "status" && command != "install-local" && command != "remove" {
		return fmt.Errorf("unknown mertpack command")
	}
	flags := flag.NewFlagSet("mertpack "+command, flag.ContinueOnError)
	directory := flags.String("directory", "", "explicit managed MERT installation directory, not the application data directory")
	source := flags.String("source", "", "prepared local MERT pack directory; install-local only")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*directory) == "" {
		return fmt.Errorf("an explicit --directory and no positional paths are required")
	}
	dir, err := safeDirectory(*directory)
	if err != nil {
		return err
	}
	if command != "install-local" && *source != "" {
		return fmt.Errorf("--source applies only to install-local")
	}
	manager := &audio.MERTBundleManager{Directory: dir}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	switch command {
	case "install-local":
		if strings.TrimSpace(*source) == "" {
			return fmt.Errorf("install-local requires --source")
		}
		src, err := filepath.Abs(*source)
		if err != nil {
			return err
		}
		if err := sourceOutside(dir, src); err != nil {
			return err
		}
		manifest, err := audio.ReadMERTBundle(src)
		if err != nil {
			return err
		}
		if err := prepareAttempt(dir, manifest); err != nil {
			return err
		}
		installed, err := manager.InstallLocal(ctx, src, nil)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]any{"installed": true, "directory": dir, "activeDirectory": installed})
	case "remove":
		if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
			return json.NewEncoder(out).Encode(map[string]any{"installed": false, "directory": dir})
		} else if err != nil {
			return err
		}
		// A prepared source pack or an unrelated application directory must never
		// be recursively removed by a mistaken destination argument.
		if err := managedContents(dir, false); err != nil {
			return err
		}
		if err := manager.Remove(); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]any{"installed": false, "directory": dir})
	default:
		active, m, err := manager.Active()
		if errors.Is(err, os.ErrNotExist) {
			return json.NewEncoder(out).Encode(map[string]any{"installed": false, "directory": dir, "nativeInferenceAvailable": audio.NativeInferenceAvailable()})
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]any{"installed": true, "directory": dir, "activeDirectory": active, "model": m.Model, "license": m.License, "nativeInferenceAvailable": audio.NativeInferenceAvailable()})
	}
}
func safeDirectory(value string) (string, error) {
	dir, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	dir = filepath.Clean(dir)
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if filepath.Dir(dir) == dir || strings.EqualFold(dir, filepath.Clean(cwd)) || strings.EqualFold(dir, filepath.Clean(home)) {
		return "", fmt.Errorf("managed directory cannot be a filesystem root, home directory or current directory")
	}
	if info, err := os.Lstat(dir); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.IsDir()) {
		return "", fmt.Errorf("managed directory must be a regular directory")
	}
	return dir, nil
}

const attemptMarker = ".mertpack-attempt"
const attemptMagic = "playlistai-mertpack-owned-attempt/v1\n"

func sourceOutside(directory, source string) error {
	// Rel cannot express paths across Windows drives or UNC shares. Distinct
	// nonempty volumes are necessarily outside one another, not invalid input.
	left, right := filepath.VolumeName(directory), filepath.VolumeName(source)
	if left != "" && right != "" && !strings.EqualFold(left, right) {
		return nil
	}
	rel, err := filepath.Rel(directory, source)
	if err != nil {
		return err
	}
	if rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("source assets must be outside the managed installation directory")
	}
	return nil
}

func prepareAttempt(directory string, m audio.MERTBundleManifest) error {
	if err := managedContents(directory, true); err != nil {
		return err
	}
	for _, a := range m.Artifacts {
		if strings.EqualFold(a.Name, attemptMarker) || a.ArchiveMember != "" && strings.EqualFold(filepath.Base(a.ArchiveMember), attemptMarker) {
			return fmt.Errorf("bundle artifact uses reserved CLI ownership marker")
		}
	}
	version := m.ID + "-" + audio.Fingerprint(m)[:16]
	if filepath.Base(version) != version || strings.ContainsAny(version, "/\\:") {
		return fmt.Errorf("invalid managed attempt name")
	}
	dir := filepath.Join(directory, version)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// Persist ownership before the first artifact copy. An interrupted first
	// install or upgrade can then be removed even without an active pointer or
	// completed manifest. Unrelated directories are never adopted implicitly.
	return os.WriteFile(filepath.Join(dir, attemptMarker), []byte(attemptMagic+version), 0600)
}

func managedContents(directory string, allowEmpty bool) error {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) && allowEmpty {
		return nil
	}
	if err != nil {
		return err
	}
	recognized := false
	for _, entry := range entries {
		if (entry.Name() == "active.json" || entry.Name() == "active.json.tmp") && entry.Type().IsRegular() {
			recognized = true
			continue
		}
		if !entry.IsDir() {
			return fmt.Errorf("refusing operation: unexpected file in managed directory")
		}
		child := filepath.Join(directory, entry.Name())
		if info, err := os.Lstat(filepath.Join(child, "mert-bundle.json")); err == nil && info.Mode().IsRegular() {
			recognized = true
			continue
		}
		raw, err := os.ReadFile(filepath.Join(child, attemptMarker))
		if err != nil || string(raw) != attemptMagic+entry.Name() {
			return fmt.Errorf("refusing operation: unexpected directory in managed installation")
		}
		recognized = true
	}
	if !recognized && !allowEmpty {
		return fmt.Errorf("refusing removal: directory has no managed MERT installation or owned attempt")
	}
	return nil
}
