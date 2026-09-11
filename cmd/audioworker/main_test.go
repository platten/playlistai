package main

import (
	"errors"
	"os"
	"testing"
)

func TestWorkerHelpDoesNotStartRuntime(t *testing.T) {
	old := os.Args
	t.Cleanup(func() { os.Args = old })
	os.Args = []string{"audioworker", "-help"}
	main()
}

func TestWorkerCLIForwardsBundleAndFailure(t *testing.T) {
	expected := errors.New("worker failed")
	called := false
	err := run([]string{"-bundle", "exact path"}, func(path string) error {
		called = true
		if path != "exact path" {
			t.Fatal(path)
		}
		return expected
	})
	if !called || !errors.Is(err, expected) {
		t.Fatalf("worker error lost: %v", err)
	}
	if err := run(nil, func(path string) error {
		if path != "" {
			t.Fatal(path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-unknown"}, func(string) error { t.Fatal("dispatched malformed arguments"); return nil }); err == nil {
		t.Fatal("unknown flag accepted")
	}
}
