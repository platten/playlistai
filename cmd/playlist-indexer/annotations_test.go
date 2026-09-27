package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/libraryannotate"
	"github.com/platten/playlistai/internal/librarypack"
)

func TestAnnotateCommandWritesIdentityLinkedUnknownSidecarOffline(t *testing.T) {
	dir := t.TempDir()
	pack := filepath.Join(dir, "fixture.paipack")
	out := filepath.Join(dir, "claims.jsonl")
	_, err := librarypack.Write(context.Background(), pack, librarypack.Pack{CorpusGeneration: "fixture", MetadataGeneration: "fixture", Tracks: []librarypack.Track{{ID: "local:fixture:one", Artist: "Fixture Artist", Title: "Fixture Song"}}}, librarypack.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code, err := execute(context.Background(), []string{"annotate", "--pack", pack, "--state", filepath.Join(dir, "state"), "--out", out, "--offline", "--criterion", "instrumentation:soft piano"}, &stdout, &stderr)
	if err != nil || code != 0 {
		t.Fatal(code, err, stderr.String())
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var row libraryannotate.Row
	if err = json.Unmarshal(raw, &row); err != nil {
		t.Fatal(err)
	}
	if row.PackID == "" || row.PackSHA256 == "" || row.EvidenceClass != "web_source" || len(row.Assessments) != 1 || row.Assessments[0].State != "unknown" {
		t.Fatal(row)
	}
	if code, err = execute(context.Background(), []string{"annotate", "--pack", pack, "--state", filepath.Join(dir, "state"), "--out", out, "--offline", "--criterion", "instrumentation:soft piano"}, &stdout, &stderr); err == nil || code == 0 {
		t.Fatal("existing report overwritten")
	}
}
func TestSubsetCommandUsesBoundedExplicitFileList(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "source")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.flac", "b.flac"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	paths := filepath.Join(dir, "paths.json")
	if err := os.WriteFile(paths, []byte(`["b.flac"]`), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code, err := execute(context.Background(), []string{"subset", "--root", root, "--out", filepath.Join(dir, "sample"), "--file-list", paths, "--limit", "1", "--max-bytes", "100"}, &stdout, &stderr)
	if err != nil || code != 0 {
		t.Fatal(code, err)
	}
	var manifest libraryannotate.SubsetManifest
	if err = json.Unmarshal(stdout.Bytes(), &manifest); err != nil || len(manifest.Files) != 1 || manifest.Files[0].Path != "b.flac" {
		t.Fatal(manifest, err)
	}
}
