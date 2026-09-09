package metadata

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func fixtureInput(t testing.TB, raw string) Input {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	if _, err := gz.Write([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "releases.xml.gz")
	if err := os.WriteFile(path, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return Input{Path: path, SHA256: fmt.Sprintf("%x", sha256.Sum256(b.Bytes()))}
}

// This measures import mechanics, not musical relevance or full-dump throughput.
func BenchmarkBuild(b *testing.B) {
	var xml strings.Builder
	xml.WriteString("<releases>")
	for i := 1; i <= 2000; i++ {
		fmt.Fprintf(&xml, `<release id="%d"><title>Album</title><artists><artist><id>7</id><name>Artist</name></artist></artists><genres><genre>Electronic</genre></genres><styles><style>Ambient</style></styles><tracklist><track><position>1</position><title>Song</title></track></tracklist></release>`, i)
	}
	xml.WriteString("</releases>")
	in := fixtureInput(b, xml.String())
	dir := b.TempDir()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := Build(context.Background(), BuildOptions{Output: filepath.Join(dir, fmt.Sprintf("%d.sqlite", i)), Date: "20260901", CatalogVersion: "benchmark", Tracks: []core.TrackRef{{ID: "a", Artist: "Artist", Title: "Song"}}, Inputs: []Input{in}})
		if err != nil {
			b.Fatal(err)
		}
	}
}

const fixtureReleases = `<releases><release id="1"><title>Fixture Album</title><master_id>11</master_id><released>2005-02-03</released><artists><artist><id>7</id><name>Artist (2)</name></artist></artists><genres><genre>Electronic</genre></genres><styles><style>Ambient</style></styles><tracklist><track><position>A1</position><title>Song</title></track><track><title>Heading</title></track><track><position>A2</position><title>Absent</title></track></tracklist></release><release id="2"><title>Rock record</title><artists><artist><id>8</id><name>Rock Artist</name></artist></artists><genres><genre>Rock</genre></genres><tracklist><track><position>1</position><title>Rock Song</title></track></tracklist></release></releases>`

func TestCompactStoreIdentityProvenanceAndIndexedLookup(t *testing.T) {
	in := fixtureInput(t, fixtureReleases)
	out := filepath.Join(t.TempDir(), "metadata.sqlite")
	tracks := []core.TrackRef{{ID: "electronic", Artist: "Artist", Title: "Song"}, {ID: "rock", Artist: "Rock Artist", Title: "Rock Song"}, {ID: "heading", Artist: "Artist", Title: "Heading"}}
	info, err := Build(context.Background(), BuildOptions{Output: out, Date: "20260901", CatalogVersion: "fixture", Tracks: tracks, Inputs: []Input{in}})
	if err != nil {
		t.Fatal(err)
	}
	if info.Tracks != 2 || info.Scanned != 2 {
		t.Fatalf("coverage: %+v", info)
	}
	s, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Genre(context.Background(), " electronic ", 20)
	if err != nil || len(got) != 1 || got[0].TrackID != "electronic" || got[0].Source != "https://www.discogs.com/release/1" || got[0].ArtistID != 7 || got[0].Artists[0] != "Artist" {
		t.Fatalf("grounding: %+v %v", got, err)
	}
	if s.HasGenre(context.Background(), "unknown") {
		t.Fatal("unknown category invented")
	}
	if !s.Compatible("fixture") || s.Compatible("new-catalog") {
		t.Fatal("catalog compatibility")
	}
	var date, year string
	if err = s.db.QueryRow("SELECT release_date,master_year FROM entities WHERE source='https://www.discogs.com/release/1'").Scan(&date, &year); err != nil || date != "2005-02-03" || year != "" {
		t.Fatal("edition promoted to master year", err)
	}
	var a, b, c int
	var plan string
	if err = s.db.QueryRow("EXPLAIN QUERY PLAN SELECT track_id FROM genre_tracks WHERE tag='electronic' ORDER BY artist_id,track_id").Scan(&a, &b, &c, &plan); err != nil {
		t.Fatal(err)
	}
	t.Log(plan)
	if err = s.db.QueryRow("EXPLAIN QUERY PLAN SELECT track_id,credits FROM genre_tracks WHERE tag='electronic' AND sample_key>=42 ORDER BY sample_key,artist_id,track_id LIMIT 1000").Scan(&a, &b, &c, &plan); err != nil || !strings.Contains(plan, "genre_sample") {
		t.Fatal("sampling lost indexed seek", plan, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.Genre(ctx, "electronic", 1); err == nil {
		t.Fatal("ignored cancellation")
	}
}
func TestInvalidOrCanceledImportNeverPublishes(t *testing.T) {
	for _, mode := range []string{"checksum", "xml", "cancel", "root", "empty"} {
		t.Run(mode, func(t *testing.T) {
			in := fixtureInput(t, fixtureReleases)
			if mode == "checksum" {
				in.SHA256 = fmt.Sprintf("%064d", 0)
			}
			if mode == "xml" {
				in = fixtureInput(t, "<releases><release>")
			}
			if mode == "root" {
				in = fixtureInput(t, "<unrelated/>")
			}
			if mode == "empty" {
				in = fixtureInput(t, "<releases/>")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			out := filepath.Join(t.TempDir(), "result.sqlite")
			_, err := Build(ctx, BuildOptions{Output: out, Date: "20260901", CatalogVersion: "fixture", Inputs: []Input{in}})
			if err == nil {
				t.Fatal("invalid import accepted")
			}
			if _, err = os.Stat(out); !os.IsNotExist(err) {
				t.Fatal("failed import published")
			}
		})
	}
}

func TestMasterJoinSamplingAndProvisionalIdentity(t *testing.T) {
	release := strings.ReplaceAll(fixtureReleases, "Artist (2)", "音楽家 (2)")
	master := `<masters><master id="11"><title>Original album</title><main_release>1</main_release><year>1998</year><genres><genre>Electronic</genre></genres></master><master id="12"><title>Unmatched</title></master></masters>`
	out := filepath.Join(t.TempDir(), "index.sqlite")
	releaseInput, masterInput := fixtureInput(t, release), fixtureInput(t, master)
	masterInput.Path = renameFixture(t, masterInput.Path, "masters.xml.gz")
	info, err := Build(context.Background(), BuildOptions{Output: out, Date: "20260901", CatalogVersion: "fixture", Tracks: []core.TrackRef{
		{ID: "a", Artist: "音楽家", Title: "Song"}, {ID: "z-duplicate", Artist: "音楽家", Title: "Song"},
		{ID: "b", Artist: "Rock Artist", Title: "Rock Song"},
	}, Inputs: []Input{releaseInput, masterInput}})
	if err != nil || info.Entities != 3 || info.Tracks != 2 || len(info.Inputs) != 2 {
		t.Fatal(info, err)
	}
	s, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var year, date string
	if err = s.db.QueryRow("SELECT master_year,release_date FROM entities WHERE source='https://www.discogs.com/master/11'").Scan(&year, &date); err != nil || year != "1998" || date != "" {
		t.Fatal("master/edition dates conflated", year, date, err)
	}
	for _, pivot := range []uint32{0, 123, 1 << 31, 1<<32 - 1} {
		got, err := s.SampleGenre(context.Background(), "electronic", 100, pivot)
		again, _ := s.SampleGenre(context.Background(), "electronic", 100, pivot)
		if err != nil || len(got) != 1 || got[0].TrackID != "a" || !reflect.DeepEqual(got, again) {
			t.Fatal("sampling lost/duplicated a recording", pivot, got, err)
		}
	}
	matches, err := s.Reference(context.Background(), core.IntentReference{Kind: core.ReferenceAlbum, Query: "Fixture Album"})
	if err != nil || len(matches) != 1 || matches[0].TrackID != "a" {
		t.Fatal(matches, err)
	}
	copy := s.Info()
	copy.Inputs["altered"] = "bad"
	if s.Info().Inputs["altered"] != "" {
		t.Fatal("manifest escaped mutable")
	}
	_, err = Build(context.Background(), BuildOptions{Output: out, Date: "20260901", CatalogVersion: "fixture", Inputs: []Input{releaseInput}})
	if err == nil {
		t.Fatal("existing dataset overwritten")
	}
}

func renameFixture(t *testing.T, path, name string) string {
	t.Helper()
	dest := filepath.Join(filepath.Dir(path), name)
	if err := os.Rename(path, dest); err != nil {
		t.Fatal(err)
	}
	return dest
}

func TestInvalidDownloadDate(t *testing.T) {
	for _, date := range []string{"", "2026", "20261301"} {
		if _, err := Download(context.Background(), DumpSet{Date: date}, t.TempDir()); err == nil {
			t.Fatal("invalid date accepted")
		}
	}
}

func TestRebuildBackupFailureAndLock(t *testing.T) {
	o := BuildOptions{Output: filepath.Join(t.TempDir(), "index.sqlite"), Date: "20260901", CatalogVersion: "fixture", Tracks: []core.TrackRef{{ID: "a", Artist: "Artist", Title: "Song"}}, Inputs: []Input{fixtureInput(t, fixtureReleases)}}
	if _, err := Build(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(o.Output)
	if err != nil {
		t.Fatal(err)
	}
	o.Replace = true
	o.Date = "20261001"
	valid := o.Inputs[0].SHA256
	o.Inputs[0].SHA256 = strings.Repeat("0", 64)
	if _, err := Build(context.Background(), o); err == nil {
		t.Fatal("invalid rebuild published")
	}
	unchanged, _ := os.ReadFile(o.Output)
	if !bytes.Equal(original, unchanged) {
		t.Fatal("failed rebuild changed the live index")
	}
	o.Inputs[0].SHA256 = valid
	if _, err := Build(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	backups, _ := filepath.Glob(o.Output + ".backup-*")
	if len(backups) != 1 {
		t.Fatal("previous index not preserved", backups)
	}
	backup, _ := os.ReadFile(backups[0])
	if !bytes.Equal(original, backup) {
		t.Fatal("backup differs")
	}
	s, err := Open(o.Output)
	if err != nil {
		t.Fatal(err)
	}
	if s.Info().Date != "20261001" {
		t.Fatal("new snapshot not installed")
	}
	_ = s.Close()
	if err := os.WriteFile(o.Output+".build.lock", []byte("active fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(context.Background(), o); err == nil {
		t.Fatal("concurrent rebuild allowed")
	}
	if _, err := os.Stat(o.Output + ".build.lock"); err != nil {
		t.Fatal("removed another build's lock")
	}
}
func TestLatestCompleteSet(t *testing.T) {
	raw := ""
	for _, date := range []string{"20260101", "20260901"} {
		for _, kind := range []string{"artists.xml.gz", "labels.xml.gz", "masters.xml.gz", "releases.xml.gz", "CHECKSUM.txt"} {
			raw += "discogs_" + date + "_" + kind + "\n"
		}
	}
	raw += "discogs_20261001_releases.xml.gz"
	set, err := ParseListing(raw, 2026)
	if err != nil || set.Date != "20260901" {
		t.Fatal(set, err)
	}
	if _, err = ParseListing("", 2026); err == nil {
		t.Fatal("missing listing fabricated a dump")
	}
}
