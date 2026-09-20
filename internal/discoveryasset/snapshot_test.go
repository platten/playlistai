package discoveryasset

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/localcatalog"
	"github.com/platten/playlistai/internal/sqliteuri"
)

func TestCompanionOnlyUpdateChangesSnapshotAndPinnedCatalogVersion(t *testing.T) {
	ctx := context.Background()
	dir, manifest := fixtureRelease(t, "same-label")
	m, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if m.SnapshotID() != "" {
		t.Fatal("empty manager has snapshot")
	}
	cacheRelease(t, m, dir, manifest)
	if _, err := m.install(ctx, manifest, nil); err != nil {
		t.Fatal(err)
	}
	original := m.SnapshotID()
	if len(original) != 64 {
		t.Fatal("invalid snapshot digest")
	}
	packs, release, err := m.Pin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	version := func(c *localcatalog.Catalog) string {
		return localcatalog.NewEvidenceCatalog(nil, c, "base").(interface{ CatalogVersion() string }).CatalogVersion()
	}
	priorVersion := version(packs[0])
	priorGeneration := packs[0].Provenance().ProfileGeneration
	if priorGeneration != manifest.Companion.SHA256 {
		t.Fatal("companion checksum not propagated")
	}
	path := filepath.Join(dir, manifest.Companion.Name)
	dsn, err := sqliteuri.Writable(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE profiles SET recordings=recordings+1"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := describeFile(ctx, path, "")
	if err != nil {
		t.Fatal(err)
	}
	manifest.Companion.Size, manifest.Companion.SHA256 = file.Size, file.SHA256
	cacheRelease(t, m, dir, manifest)
	if _, err := m.install(ctx, manifest, nil); err != nil {
		t.Fatal(err)
	}
	if m.SnapshotID() == original {
		t.Fatal("same-label companion update kept snapshot")
	}
	next, nextRelease, err := m.Pin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer nextRelease()
	if next[0].Provenance().PackSHA256 != packs[0].Provenance().PackSHA256 {
		t.Fatal("fixture changed source pack")
	}
	if version(next[0]) == priorVersion {
		t.Fatal("companion update kept catalog version")
	}
	if version(packs[0]) != priorVersion || packs[0].Provenance().ProfileGeneration != priorGeneration {
		t.Fatal("update mutated held generation")
	}
}
