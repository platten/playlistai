package discoveryasset

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/localcatalog"
	"github.com/platten/playlistai/internal/sqliteuri"
)

func fixtureRelease(t *testing.T, version string) (string, Manifest) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	var inputs []string
	for i, name := range []string{"first", "second"} {
		path := filepath.Join(root, name+".paipack")
		p := librarypack.Pack{CorpusGeneration: name, MetadataGeneration: name, Tracks: []librarypack.Track{
			{ID: "track-a", Artist: "Artist " + name, Title: "Dawn", Album: "Early", RootAlias: "private-root", RelativePath: "private/secret.flac", SourceIdentity: "private-source", RawTags: json.RawMessage(`{"genre":"ambient","mood":"calm","originaldate":"1997","date":"2020","comment":"private-note"}`)},
			{ID: "track-b", Artist: "Artist " + name, Title: "Night", Album: "Later", RawTags: json.RawMessage(`{"genre":"jazz","mood":"energetic","originaldate":"2008"}`)},
			{ID: "track-c", Artist: "Artist " + name, Title: "Edition", Album: "Reissue", RawTags: json.RawMessage(`{"genre":"jazz","date":"2024"}`)},
		}}
		if i == 1 {
			p.MERT = librarypack.VectorSpace{Name: "library_mert", Dimension: 2, DType: "float32", ByteOrder: "little", Normalized: true, Model: "fixture", ModelRevision: "v1", GraphSHA256: strings.Repeat("a", 64), Decoder: "test", Preprocessing: "test", Sampling: "test", Pooling: "mean", Scope: "preview", Missingness: "explicit"}
			p.MERTGeneration = "test"
			p.Tracks[0].MERT = []float32{1, 0}
		}
		if _, e := librarypack.Write(ctx, path, p, librarypack.Limits{}); e != nil {
			t.Fatal(e)
		}
		inputs = append(inputs, path)
	}
	out := filepath.Join(root, "release")
	m, e := Build(ctx, BuildOptions{Inputs: inputs, Output: out, BaseURL: "https://example.invalid/releases/" + version, Version: version, MaxTracks: 6})
	if e != nil {
		t.Fatal(e)
	}
	return out, m
}

func TestRealReleaseOptIn(t *testing.T) {
	dir := os.Getenv("PLAYLISTAI_DISCOVERY_RELEASE")
	if dir == "" {
		t.Skip("set PLAYLISTAI_DISCOVERY_RELEASE to explicitly test a local curated release")
	}
	ctx := context.Background()
	manifest, e := Verify(ctx, dir)
	if e != nil {
		t.Fatal(e)
	}
	m, e := Open(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	cacheRelease(t, m, dir, manifest)
	status, e := m.install(ctx, manifest, nil)
	if e != nil {
		t.Fatal(e)
	}
	if !status.Installed || status.Tracks == 0 {
		t.Fatalf("empty installed asset: %+v", status)
	}
	catalogs, release, e := m.Pin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	hits := 0
	for _, c := range catalogs {
		if c.Provenance().Source == "local_library" {
			t.Fatal("shared asset exposed as owned library")
		}
		matches, e := c.Search(ctx, localcatalog.MetadataQuery{Text: "ambient", Limit: 5})
		if e != nil {
			t.Fatal(e)
		}
		hits += len(matches)
		if len(matches) > 0 {
			profileCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			started := time.Now()
			profiles, err := c.DiscoveryProfiles(profileCtx, core.MusicIntent{References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: matches[0].Track.Artist}}}, 12)
			elapsed := time.Since(started)
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			if len(profiles) == 0 || len(profiles) > 12 {
				t.Fatalf("invalid profile result count: %d", len(profiles))
			}
			for _, profile := range profiles {
				if len(profile.SupportingTracks) > 256 || len(profile.Genres) > 8 || len(profile.Moods) > 8 {
					t.Fatal("profile evidence exceeded retrieval bounds")
				}
			}
			t.Logf("real corpus artist-profile lookup returned %d bounded profiles in %s (single local observation)", len(profiles), elapsed)
		}
	}
	if hits == 0 {
		t.Fatal("real validation corpus has no retrievable ambient candidates")
	}
	t.Logf("installed and queried %d packs, %d tracks; %d bounded metadata hits", len(catalogs), status.Tracks, hits)
}

func TestActivationSyncFailureRetainsCommittedRelease(t *testing.T) {
	ctx := context.Background()
	dir, manifest := fixtureRelease(t, "durability")
	m, e := Open(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	cacheRelease(t, m, dir, manifest)
	m.syncDir = func(string) error { return errors.New("injected directory sync failure") }
	status, e := m.install(ctx, manifest, nil)
	if e == nil || !status.Installed || status.Error == "" {
		t.Fatalf("committed durability error status=%+v error=%v", status, e)
	}
	root := m.root
	_ = m.Close()
	reopened, e := Open(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if !reopened.Status().Installed {
		t.Fatalf("committed release removed: %+v", reopened.Status())
	}
}

func TestCurationBalancesArtistsAndPreservesMultipleValues(t *testing.T) {
	ctx := context.Background()
	p := librarypack.Pack{CorpusGeneration: "test", MetadataGeneration: "test", Tracks: []librarypack.Track{
		{ID: "a", Artist: "Alpha", Title: "One", Album: "Album 1", RawTags: json.RawMessage(`{"genre":["ambient","jazz"],"mood":["calm","dark"],"comment":["private"]}`)},
		{ID: "b", Artist: "Alpha", Title: "Two", Album: "Album 2"},
		{ID: "c", Artist: "Alpha", Title: "Three", Album: "Album 3"},
		{ID: "z", Artist: "Zeta", Title: "Four", Album: "Album 1"},
	}}
	path := filepath.Join(t.TempDir(), "input.paipack")
	if _, e := librarypack.Write(ctx, path, p, librarypack.Limits{}); e != nil {
		t.Fatal(e)
	}
	e := withPack(ctx, path, func(g *librarypack.Generation) error {
		tracks, e := selectTracks(ctx, g, 2, 8, map[string][]librarypack.Track{})
		if e != nil {
			return e
		}
		artists := map[string]bool{}
		for _, tr := range tracks {
			artists[tr.Artist] = true
			if tr.Artist == "Alpha" {
				tags := safeTags(tr.RawTags)
				if len(tags["genre"]) != 2 || len(tags["mood"]) != 2 || len(tags["comment"]) != 0 {
					t.Fatalf("tags lost or private: %s", tr.RawTags)
				}
			}
		}
		if !artists["Alpha"] || !artists["Zeta"] {
			t.Fatalf("artists not balanced: %v", artists)
		}
		model, e := metadataModel(ctx, tracks)
		if e != nil {
			return e
		}
		albums := map[string]bool{}
		for _, a := range model.Associations {
			albums[a.AlbumID] = true
		}
		if len(albums) != 2 {
			t.Fatalf("unrelated same-name albums merged: %+v", model.Associations)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}

func TestRecordingDedupeUsesAllIdentifiers(t *testing.T) {
	if publicTrackID("pack", "Track") == publicTrackID("pack", "track") {
		t.Fatal("case-sensitive source IDs collapsed")
	}
	one := librarypack.Track{Artist: "Artist", Title: "Song", MusicBrainzRecording: "12345678-1234-1234-1234-123456789012", ISRC: "USAAA2400001"}
	two := one
	two.MusicBrainzRecording = ""
	seen := map[string][]librarypack.Track{}
	rememberRecording(one, seen)
	if !duplicateRecording(two, seen) {
		t.Fatal("shared ISRC ignored when one row also has MBID")
	}
	two.ISRC = "USAAA2400002"
	if duplicateRecording(two, seen) {
		t.Fatal("distinct identity removed")
	}
}

func TestHTTPSInstallAndCorruptionRollback(t *testing.T) {
	ctx := context.Background()
	dir, manifest := fixtureRelease(t, "v1")
	var current Manifest
	corrupt := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manifest.json" {
			_ = json.NewEncoder(w).Encode(current)
			return
		}
		if corrupt {
			_, _ = w.Write([]byte("corrupt"))
			return
		}
		http.ServeFile(w, r, filepath.Join(dir, filepath.Base(r.URL.Path)))
	}))
	defer server.Close()
	// Production clients inherit the default transport; the local TLS fixture
	// supplies its trust roots without weakening production certificate checks.
	previous := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = previous }()
	current = manifest
	current.Packs = append([]File(nil), manifest.Packs...)
	for i := range current.Packs {
		current.Packs[i].URL = server.URL + "/" + current.Packs[i].Name
	}
	current.Companion.URL = server.URL + "/" + current.Companion.Name
	m, e := Open(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	status, e := m.Install(ctx, server.URL+"/manifest.json", nil)
	if e != nil || !status.Installed {
		t.Fatalf("install status=%+v error=%v", status, e)
	}
	corrupt = true
	current.Version = "bad"
	current.Packs[0].SHA256 = strings.Repeat("c", 64)
	if _, e = m.Install(ctx, server.URL+"/manifest.json", nil); e == nil {
		t.Fatal("corruption accepted")
	}
	if m.Status().Version != "v1" {
		t.Fatal("corruption replaced installed release")
	}
	if _, e = FetchManifest(ctx, "http://example.invalid/manifest.json"); e == nil {
		t.Fatal("HTTP manifest accepted")
	}
}
func cacheRelease(t *testing.T, m *Manager, dir string, manifest Manifest) {
	t.Helper()
	if e := os.MkdirAll(filepath.Join(m.root, "downloads"), 0700); e != nil {
		t.Fatal(e)
	}
	for _, f := range append(append([]File(nil), manifest.Packs...), manifest.Companion) {
		target := filepath.Join(m.root, "downloads", f.SHA256)
		if _, e := os.Stat(target); e == nil {
			continue
		}
		if e := copyFile(context.Background(), filepath.Join(dir, f.Name), target); e != nil {
			t.Fatal(e)
		}
	}
}
func TestBuildSanitizesAndProfilesOriginalPeriods(t *testing.T) {
	dir, m := fixtureRelease(t, "v1")
	ctx := context.Background()
	if _, e := Verify(ctx, dir); e != nil {
		t.Fatal(e)
	}
	if m.TotalBytes() > MaxDownloadBytes {
		t.Fatal("oversized")
	}
	if e := withPack(ctx, filepath.Join(dir, m.Packs[0].Name), func(g *librarypack.Generation) error {
		rows, e := g.List(ctx, "", 100)
		if e != nil {
			return e
		}
		for _, r := range rows {
			if r.RootAlias != "" || r.RelativePath != "" || r.SourceIdentity != "library:"+r.ID || strings.Contains(string(r.RawTags), "private") {
				t.Errorf("private fields persisted: %+v", r)
			}
			if !strings.HasPrefix(r.ID, "track:") {
				t.Errorf("public identity not rekeyed: %s", r.ID)
			}
		}
		learning, e := g.Learning(ctx)
		if e != nil {
			return e
		}
		if !strings.Contains(string(learning), "ambient") {
			t.Error("metadata model not rebuilt")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	u, e := sqliteuri.ReadOnly(filepath.Join(dir, m.Companion.Name), true)
	if e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("sqlite", u)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var n int
	if e = db.QueryRow("SELECT count(*) FROM profiles WHERE period='1990' AND kind='mood' AND value='calm'").Scan(&n); e != nil || n != 2 {
		t.Fatalf("original decade count=%d err=%v", n, e)
	}
	if e = db.QueryRow("SELECT count(*) FROM profiles WHERE period='2020'").Scan(&n); e != nil || n != 0 {
		t.Fatalf("reissue date polluted periods: %d %v", n, e)
	}
}
func TestAtomicInstallPinsRollbackAndRepair(t *testing.T) {
	ctx := context.Background()
	dir, manifest := fixtureRelease(t, "v1")
	manager, e := Open(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer manager.Close()
	cacheRelease(t, manager, dir, manifest)
	status, e := manager.install(ctx, manifest, nil)
	if e != nil {
		t.Fatal(e)
	}
	if !status.Installed || status.Tracks != 6 || len(status.PackIDs) != 2 {
		t.Fatalf("bad status %+v", status)
	}
	catalogs, release, e := manager.Pin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	oldDir := manager.active.dir
	bad := manifest
	bad.Version = "bad"
	bad.Packs = append([]File(nil), manifest.Packs...)
	bad.Packs[0].PackID = strings.Repeat("b", 64)
	if _, e = manager.install(ctx, bad, nil); e == nil {
		t.Fatal("accepted mismatched pack identity")
	}
	if manager.Status().Version != "v1" {
		t.Fatal("failed update replaced release")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e = manager.install(canceled, manifest, nil); e == nil {
		t.Fatal("accepted canceled install")
	}
	next := manifest
	next.Version = "v2"
	if _, e = manager.install(ctx, next, nil); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(oldDir); e != nil {
		t.Fatal("pinned old set was removed")
	}
	for _, c := range catalogs {
		tracks, e := c.ArtistRecordings(ctx, "Artist first")
		if e != nil {
			t.Fatal(e)
		}
		_ = tracks
	}
	release()
	if _, e = os.Stat(oldDir); !os.IsNotExist(e) {
		t.Fatal("retired unpinned release retained")
	}
	root := manager.root
	if e = manager.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	if reopened.Status().Version != "v2" {
		t.Fatalf("reopen: %+v", reopened.Status())
	}
	active := reopened.active.dir
	_ = reopened.Close()
	if e = os.WriteFile(filepath.Join(active, "discovery.sqlite"), []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	repair, e := Open(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	defer repair.Close()
	if repair.Status().Installed || repair.Status().Error == "" {
		t.Fatal("corruption not reported for repair")
	}
	if _, e = repair.install(ctx, manifest, nil); e != nil {
		t.Fatal(e)
	}
}
func TestManifestRejectsUnsafeOversizedAndDuplicateEntries(t *testing.T) {
	_, m := fixtureRelease(t, "v1")
	tests := map[string]func(*Manifest){
		"http":      func(m *Manifest) { m.Packs[0].URL = "http://example.com/a" },
		"path":      func(m *Manifest) { m.Packs[0].Name = "../a.paipack" },
		"duplicate": func(m *Manifest) { m.Packs[1].PackID = m.Packs[0].PackID },
		"size":      func(m *Manifest) { m.Packs[0].Size = MaxDownloadBytes },
		"expanded":  func(m *Manifest) { m.Packs[0].ExpandedBytes = 12_000_000_001 },
		"companion": func(m *Manifest) { m.Companion.Name = "other.sqlite" },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := m
			candidate.Packs = append([]File(nil), m.Packs...)
			change(&candidate)
			if candidate.Validate() == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
}

func TestManifestDownloadCapExcludesGeneratedCompanion(t *testing.T) {
	_, base := fixtureRelease(t, "large-generated-index")
	base.Packs[0].Size = 2_456_997_645
	base.Companion.Size = 600_000_000
	for _, tc := range []struct {
		name            string
		source          string
		transportFormat string
		wantValid       bool
	}{
		{name: "curated remote", wantValid: false},
		{name: "local generated", source: "local", transportFormat: "paipack-v5", wantValid: true},
		{name: "hosted generated", source: "hosted", transportFormat: "modelpack-v1", wantValid: true},
		{name: "unrecognized provenance", source: "hosted", transportFormat: "paipack-v5", wantValid: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := base
			candidate.Source = tc.source
			candidate.TransportFormat = tc.transportFormat
			candidate.ManifestDigest = strings.Repeat("a", 64)
			if got := candidate.Validate() == nil; got != tc.wantValid {
				t.Fatalf("valid=%v, want %v", got, tc.wantValid)
			}
		})
	}
	generated := base
	generated.Source = "local"
	generated.TransportFormat = "paipack-v5"
	generated.ManifestDigest = strings.Repeat("a", 64)
	generated.Companion.Size = maxGeneratedCompanionBytes + 1
	if generated.Validate() == nil {
		t.Fatal("oversized generated companion accepted")
	}
	generated.Companion.Size = 600_000_000
	generated.Packs = append([]File(nil), base.Packs...)
	generated.Packs[0].Size = MaxDownloadBytes
	if generated.Validate() == nil {
		t.Fatal("oversized input pack set accepted")
	}
}

func TestEmbeddedIndexManifestRequiresNewProvenanceAndNoExternalCompanion(t *testing.T) {
	_, base := fixtureRelease(t, "embedded-indexes")
	base.EmbeddedIndexes = true
	base.Companion = File{}
	base.Packs[0].Size = MaxDownloadBytes + 1
	base.Source = "local"
	base.TransportFormat = "paipack-v8"
	base.ManifestDigest = strings.Repeat("a", 64)
	if err := base.Validate(); err != nil {
		t.Fatalf("valid indexed release rejected: %v", err)
	}
	for name, change := range map[string]func(*Manifest){
		"legacy local format": func(m *Manifest) { m.TransportFormat = "paipack-v5" },
		"external companion":  func(m *Manifest) { m.Companion = File{Name: "discovery.sqlite"} },
		"over download cap":   func(m *Manifest) { m.Packs[0].Size = MaxIndexedDownloadBytes },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.Packs = append([]File(nil), base.Packs...)
			change(&candidate)
			if candidate.Validate() == nil {
				t.Fatal("invalid indexed release accepted")
			}
		})
	}
}
