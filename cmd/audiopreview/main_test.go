package main

import (
	"os"
	"testing"
)

func TestPreviewHelpDoesNotOpenProvider(t *testing.T) {
	old := os.Args
	t.Cleanup(func() { os.Args = old })
	os.Args = []string{"audiopreview", "-help"}
	main()
}

func TestPreviewRequiresAuthorizationAndValidatedBundle(t *testing.T) {
	for _, values := range [][]string{nil, {"-bogus"}, {"-authorized", "-artist", "Fixture", "-title", "Song", "-track-id", "id", "-catalog-version", "fixture", "-data-dir", t.TempDir(), "-bundle", t.TempDir()}} {
		old := os.Args
		os.Args = append([]string{"audiopreview"}, values...)
		err := run()
		os.Args = old
		if err == nil {
			t.Fatal("invalid preview request accepted")
		}
	}
}
