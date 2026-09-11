package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrefsCheckedPreservesCorruption(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadPrefsChecked(dir); err != nil {
		t.Fatal(err)
	}
	path := prefsPath(dir)
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPrefsChecked(dir); err == nil {
		t.Fatal("corruption hidden")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "{broken" {
		t.Fatal("load changed corrupt file")
	}
}

func TestPrefsSaveReplacesFileWithoutTruncatingPreviousInode(t *testing.T) {
	dir := t.TempDir()
	old := Prefs{ModelPath: "original"}
	if err := old.Save(dir); err != nil {
		t.Fatal(err)
	}
	// A second hard link exposes whether Save truncates the live inode.
	backup := filepath.Join(dir, "previous.json")
	if err := os.Link(prefsPath(dir), backup); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if err := (Prefs{ModelPath: "updated"}).Save(dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" {
		t.Fatal("old contents lost")
	}
	oldDir := t.TempDir()
	if err := os.WriteFile(prefsPath(oldDir), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if LoadPrefs(oldDir).ModelPath != "original" || LoadPrefs(dir).ModelPath != "updated" {
		t.Fatal("save modified original inode")
	}
}
