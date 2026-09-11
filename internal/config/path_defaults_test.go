package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestDataDirectoryOverrideRebasesOnlyImplicitPaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	for _, explicit := range []bool{false, true} {
		text := fmt.Sprintf("data_dir = %q\n", dir)
		catalog, cache := filepath.Join(dir, "catalog"), filepath.Join(dir, "musicbrainz-cache.sqlite")
		if explicit {
			catalog, cache = filepath.Join(dir, "other-catalog"), ""
			text += fmt.Sprintf("[catalog]\ndir = %q\n[enrich]\ncache_path = %q\n", catalog, cache)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.DataDir != dir || cfg.Catalog.Dir != catalog || cfg.Enrich.CachePath != cache {
			t.Fatalf("implicit/explicit paths confused: %+v", cfg)
		}
	}
}
