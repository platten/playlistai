package bridge

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFakeGGUF(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, n)
	copy(b, "GGUF")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGetInstalledModels(t *testing.T) {
	t.Parallel()
	c := newTestContainer(t)
	api := New(c, nil)
	dataDir := c.Config().DataDir

	if got := api.GetInstalledModels(); len(got) != 0 {
		t.Fatalf("fresh dir: %+v", got)
	}

	// A curated model and a stray one, both in models/.
	writeFakeGGUF(t, filepath.Join(dataDir, "models", "llama-3.2-3b-instruct-q4km.gguf"), 128)
	writeFakeGGUF(t, filepath.Join(dataDir, "models", "my-tuned-thing.gguf"), 128)
	// A non-.gguf and a directory are ignored.
	writeFakeGGUF(t, filepath.Join(dataDir, "models", "notes.txt"), 10)

	got := api.GetInstalledModels()
	if len(got) != 2 {
		t.Fatalf("want 2 GGUFs, got %d: %+v", len(got), got)
	}

	var curated, stray *InstalledModel
	for i := range got {
		switch got[i].Name {
		case "llama-3.2-3b-instruct-q4km.gguf":
			curated = &got[i]
		case "my-tuned-thing.gguf":
			stray = &got[i]
		}
	}
	if curated == nil || curated.CatalogID != "llama-3.2-3b-instruct-q4km" || curated.Label == "" {
		t.Fatalf("curated entry not tagged: %+v", curated)
	}
	if stray == nil || stray.CatalogID != "" || stray.SizeBytes != 128 {
		t.Fatalf("stray entry wrong: %+v", stray)
	}
	if curated.Active || stray.Active {
		t.Fatal("nothing should be active")
	}
}
