package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/enrich/musicbrainz"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/metadata"
	"github.com/platten/playlistai/internal/ports"
)

func TestDefaultWizardMetadataIsConfiguredWithoutNetwork(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	c := &Container{cfg: cfg}
	info := c.GetMetadataBundleInfo()
	if !info.Configured || info.Installed || info.CatalogReady {
		t.Fatal(info)
	}
	c.runtime.Resolver = fakes.NewCatalog(2, fakes.CatalogTrack{ID: "a", Display: "Artist - Song"})
	if info := c.GetMetadataBundleInfo(); !info.Configured || !info.CatalogReady || info.Installed {
		t.Fatal(info)
	}
}

func TestWizardMetadataInstallAndRestart(t *testing.T) {
	cat := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "a", Display: "Artist - Song"})
	var raw bytes.Buffer
	z := gzip.NewWriter(&raw)
	_, err := z.Write([]byte(`<releases><release id="1"><artists><artist><id>7</id><name>Artist</name></artist></artists><genres><genre>Electronic</genre></genres><tracklist><track><position>1</position><title>Song</title></track></tracklist></release></releases>`))
	if err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "releases.xml.gz")
	if err = os.WriteFile(input, raw.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(t.TempDir(), "full.sqlite")
	_, err = metadata.Build(context.Background(), metadata.BuildOptions{Output: full, Date: "20260901", CatalogVersion: cat.CatalogVersion(), Tracks: []core.TrackRef{{ID: "a", Artist: "Artist", Title: "Song"}}, Inputs: []metadata.Input{{Path: input, SHA256: fmt.Sprintf("%x", sha256.Sum256(raw.Bytes()))}}})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "bundle")
	if _, err = metadata.Package(context.Background(), full, dir); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer server.Close()
	cfg := testConfig(t)
	cfg.Metadata.ManifestURL = server.URL + "/metadata-manifest.json"
	c, err := New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.runtime.Resolver = cat
	c.runtime.Catalog = cat
	if !c.GetMetadataBundleInfo().Configured || c.GetMetadataBundleInfo().Installed {
		t.Fatal("setup status")
	}
	if err = c.InstallMetadataBundle(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !c.GetMetadataBundleInfo().Installed {
		t.Fatal("install not activated")
	}
	if !c.Knowledge.(*musicbrainz.Client).IsCachedGenre(context.Background(), "Electronic") {
		t.Fatal("running client did not activate local index")
	}
	stream := c.Knowledge.(ports.MusicCandidateSource).OpenCandidates(core.MusicIntent{Seed: "42", Preferences: core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "Electronic", Influence: core.InfluencePositive}}}}, cat, cat)
	track, err := stream.Next(context.Background())
	if err != nil || track.ID != "a" {
		t.Fatal("downloaded index not used for discovery", track, err)
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	server.Close()
	c, err = New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.runtime.Resolver = cat
	c.runtime.Catalog = cat
	if !c.GetMetadataBundleInfo().Installed || !c.Knowledge.(*musicbrainz.Client).IsCachedGenre(context.Background(), "Electronic") {
		t.Fatal("restart did not load offline active index")
	}
}
