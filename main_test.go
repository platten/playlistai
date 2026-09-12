package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/bridge"
	"github.com/platten/playlistai/internal/logging"
)

func TestHeadlessCommandsBypassConfigurationAndDesktop(t *testing.T) {
	invalidConfig := filepath.Join(t.TempDir(), "invalid.toml")
	if err := os.WriteFile(invalidConfig, []byte("[invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLAYLISTAI_CONFIG", invalidConfig)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var out bytes.Buffer
	if err := dispatch([]string{"--version"}, &out, log); err != nil || strings.TrimSpace(out.String()) != bridge.Version {
		t.Fatalf("version: %q %v", out.String(), err)
	}
	_, expected := audio.RecommendedBundle()
	if err := dispatch([]string{"--check-audio-worker"}, &out, log); (err == nil) != (expected == nil) {
		t.Fatalf("packaging capability disagrees with this build: %v / %v", err, expected)
	}
	for _, command := range []string{"--audio-worker", "--mert-worker", "--app-update-worker"} {
		if err := dispatch([]string{command, filepath.Join(t.TempDir(), "absent")}, &out, log); err == nil {
			t.Fatalf("%s accepted missing input", command)
		}
	}
	if err := dispatch(nil, &out, log); err == nil || !strings.Contains(err.Error(), "invalid.toml") {
		t.Fatalf("desktop config error not propagated: %v", err)
	}
}

func TestStartupOwnsContainerUntilHostReturns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	data := filepath.Join(dir, "data")
	config := fmt.Sprintf("data_dir = %q\n[catalog]\ndir = %q\n[enrich]\ncache_path = %q\n", data, filepath.Join(data, "catalog"), filepath.Join(data, "musicbrainz.sqlite"))
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLAYLISTAI_CONFIG", path)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	want := errors.New("host stopped")
	var captured *app.Container
	closed := false
	err := runWithHost(log, func(c *app.Container, _ *slog.Logger, logs *logging.Store) error {
		captured = c
		if c.Config().DataDir != data || c.History == nil || logs == nil {
			t.Fatal("host received incomplete startup state")
		}
		c.RegisterCloser(func() error { closed = true; return nil })
		if _, err := os.Stat(filepath.Join(data, "history.sqlite")); err != nil {
			t.Fatal("startup did not create real temporary history", err)
		}
		return want
	})
	if !errors.Is(err, want) || captured == nil || !closed {
		t.Fatalf("host lifetime not owned: closed=%v err=%v", closed, err)
	}
	if err := captured.LoadCatalog(); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("container remained usable after host exit: %v", err)
	}
}

func TestStartupFailureDoesNotInvokeNativeHost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	blocked := filepath.Join(dir, "file")
	if err := os.WriteFile(blocked, []byte("do not replace"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf("data_dir = %q\n", filepath.Join(blocked, "data"))), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLAYLISTAI_CONFIG", path)
	err := runWithHost(slog.New(slog.NewTextHandler(io.Discard, nil)), func(*app.Container, *slog.Logger, *logging.Store) error {
		t.Fatal("native host started after initialization failure")
		return nil
	})
	if err == nil {
		t.Fatal("unwritable data directory accepted")
	}
	if data, _ := os.ReadFile(blocked); string(data) != "do not replace" {
		t.Fatal("startup modified existing file")
	}
}

func TestEmbeddedFrontendHasEntrypoint(t *testing.T) {
	data, err := assets.ReadFile("frontend/dist/index.html")
	if err != nil || !bytes.Contains(data, []byte("<div id=\"root\">")) {
		t.Fatalf("embedded frontend entrypoint missing: %v", err)
	}
}
