package modelpack

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPackageRoundTripAndPartLimit(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(filepath.Join(source, "encoder"), 0o700); err != nil {
		t.Fatal(err)
	}
	weights := make([]byte, 7000)
	if _, err := rand.Read(weights); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"bundle.json":        []byte(`{"version":2}`),
		"encoder/model.onnx": weights,
	} {
		if err := os.WriteFile(filepath.Join(source, filepath.FromSlash(name)), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(root, "upload")
	manifest, err := Package(context.Background(), "clap-test", source, output, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Parts) < 2 {
		t.Fatalf("expected split archive, got %d part", len(manifest.Parts))
	}
	for _, part := range manifest.Parts {
		if part.Size > 1024 {
			t.Fatalf("oversized part: %+v", part)
		}
	}
	second := filepath.Join(root, "upload-second")
	secondManifest, err := Package(context.Background(), "clap-test", source, second, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(secondManifest, manifest) {
		t.Fatal("same source produced a different manifest")
	}
	for _, part := range manifest.Parts {
		firstBytes, _ := os.ReadFile(filepath.Join(output, part.Path))
		secondBytes, _ := os.ReadFile(filepath.Join(second, part.Path))
		if !reflect.DeepEqual(firstBytes, secondBytes) {
			t.Fatalf("same source produced different bytes for %s", part.Path)
		}
	}
	destination := filepath.Join(root, "unpacked")
	if err := Fetch(context.Background(), filepath.Join(output, "manifest.json"), filepath.Join(root, "cache"), destination, nil); err != nil {
		t.Fatal(err)
	}
	for _, file := range manifest.Files {
		want, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(file.Path)))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(destination, filepath.FromSlash(file.Path)))
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("round trip mismatch for %s: %v", file.Path, err)
		}
	}
}

func TestPackageRejectsUnsafeInputs(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "model"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, output string
		part         int64
	}{
		{"bad/name", filepath.Join(root, "bad-name"), 1024},
		{"valid", filepath.Join(root, "too-large"), MaxPartBytes},
	} {
		if _, err := Package(context.Background(), test.name, source, test.output, test.part); err == nil {
			t.Fatalf("accepted invalid package options: %+v", test)
		}
	}
	occupied := filepath.Join(root, "occupied")
	if err := os.Mkdir(occupied, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(occupied, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Package(context.Background(), "valid", source, occupied, 1024); err == nil {
		t.Fatal("accepted occupied output")
	}
}
