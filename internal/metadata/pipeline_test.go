package metadata

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/core"
)

func TestImportWorkers(t *testing.T) {
	for _, n := range []int{-1, 33} {
		if _, err := ImportWorkers(n); err == nil {
			t.Fatal("accepted invalid worker count", n)
		}
	}
	got, err := ImportWorkers(0)
	if err != nil || got != min(8, runtime.GOMAXPROCS(0)) {
		t.Fatal(got, err)
	}
}

func TestParallelFramingMatchesStandardXML(t *testing.T) {
	raw := `<?xml version="1.0"?><releases><!-- a > <release/> -->
<release id="1" note="a > b"><title><![CDATA[Music <release/> > & stuff]]></title><?note > ?>
<artists><artist><id>7</id><name>音楽家</name></artist></artists>
<tracklist><track><position>1</position><title>A &amp; B</title></track></tracklist></release>
<release id="2"/><release id="3"><notes>` + strings.Repeat("long note ", 20000) + `</notes><title>Last</title></release>
</releases ><!--done-->`
	lookup := map[string]string{core.ProvisionalRecordingKey(core.TrackRef{Artist: "音楽家", Title: "A & B"}): "real-id"}
	var expected []preparedEntity
	d := xml.NewDecoder(strings.NewReader(raw))
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if start, ok := tok.(xml.StartElement); ok && start.Name.Local == "release" {
			var e entity
			if err := d.DecodeElement(&e, &start); err != nil {
				t.Fatal(err)
			}
			expected = append(expected, prepareEntity(e, lookup))
		}
	}
	for _, workers := range []int{1, 2, 4, 8, 16} {
		var got []preparedEntity
		err := processEntities(context.Background(), strings.NewReader(raw), workers, lookup, func(_ string, e preparedEntity) error {
			got = append(got, e)
			return nil
		})
		if err != nil || !reflect.DeepEqual(got, expected) {
			t.Fatalf("workers=%d: standard decoder mismatch: %v", workers, err)
		}
	}
}

func TestParallelImportRejectsMalformedXML(t *testing.T) {
	for _, raw := range []string{
		`<releases><release id="1"><title>x</oops></release></releases>`,
		`<releases><release id="1"/></masters>`,
		`<releases><release id="1"/>`,
		`<releases/><releases/>`,
		`<releases>junk<release/></releases>`,
		`<!DOCTYPE releases><releases/>`,
		`<releases xmlns="example"><release/></releases>`,
		`<releases><master/></releases>`,
		`<releases><!-- unterminated`,
		`<releases><release><title><![CDATA[unterminated</title></release></releases>`,
		`<releases><release id="1></release></releases>`,
		`<releases><release><title>` + strings.Repeat("a", maxEntityBytes) + `</title></release></releases>`,
	} {
		err := processEntities(context.Background(), strings.NewReader(raw), 4, nil, func(string, preparedEntity) error { return nil })
		if err == nil {
			t.Fatalf("accepted malformed XML: %.100s", raw)
		}
	}
}

func TestParallelImportStopsOnWriterErrorOrCancellation(t *testing.T) {
	raw := "<releases>" + strings.Repeat(`<release id="1"><title>Album</title></release>`, 1000) + "</releases>"
	for _, cancelInstead := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		want := errors.New("writer failed")
		err := processEntities(ctx, strings.NewReader(raw), 4, nil, func(string, preparedEntity) error {
			calls++
			if cancelInstead {
				cancel()
				return ctx.Err()
			}
			return want
		})
		cancel()
		if cancelInstead {
			want = context.Canceled
		}
		if !errors.Is(err, want) || calls != 1 {
			t.Fatal(calls, err)
		}
	}
}

func TestParallelBuildDeterministicAndMasterDependencies(t *testing.T) {
	// Slow first record deliberately exercises out-of-order worker completion.
	releases := strings.Replace(fixtureReleases, "<title>Fixture Album</title>", "<notes>"+strings.Repeat("notes ", 10000)+"</notes><title>Fixture Album</title>", 1)
	// Repeated matching source must not replace the first source's provenance.
	releases = strings.Replace(releases, "</releases>", strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(fixtureReleases, "<releases>"), "</releases>"), `id="1"`, `id="3"`)+"</releases>", 1)
	master := `<masters><master id="11"><title>Original</title><year>1998</year></master></masters>`
	inputs := []Input{fixtureInput(t, releases), fixtureInput(t, master)}
	inputs[1].Path = renameFixture(t, inputs[1].Path, "masters.xml.gz")
	var expected []byte
	for _, workers := range []int{1, 2, 4, 8} {
		out := filepath.Join(t.TempDir(), "index.sqlite")
		info, err := Build(context.Background(), BuildOptions{Output: out, Date: "20260901", CatalogVersion: "fixture", Workers: workers, Inputs: inputs, Tracks: []core.TrackRef{{ID: "a", Artist: "Artist", Title: "Song"}}})
		if err != nil || info.Entities != 3 || info.Tracks != 1 {
			t.Fatal(info, err)
		}
		s, err := Open(out)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.Genre(context.Background(), "Electronic", 10)
		_ = s.Close()
		if err != nil || len(got) != 1 || got[0].Source != "https://www.discogs.com/release/1" {
			t.Fatal("first source provenance changed", got, err)
		}
		raw, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if expected == nil {
			expected = raw
		} else if !bytes.Equal(expected, raw) {
			t.Fatalf("workers=%d: SQLite output changed", workers)
		}
	}
}

// Opt-in real import mechanics benchmark, not musical quality evidence. Uses the
// first 20,000 downloaded releases and the installed catalog, without modifying
// either. All sample XML/SQLite files are temporary and removed by testing.
func BenchmarkBuildDump(b *testing.B) {
	path, catPath := os.Getenv("METADATAPACK_BENCH_DUMP"), os.Getenv("METADATAPACK_BENCH_CATALOG")
	if path == "" || catPath == "" {
		b.Skip("set METADATAPACK_BENCH_DUMP and METADATAPACK_BENCH_CATALOG")
	}
	f, err := os.Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		b.Fatal(err)
	}
	defer gz.Close()
	var raw bytes.Buffer
	raw.WriteString("<releases>")
	count := 0
	stop := errors.New("sample complete")
	err = frameEntities(gz, func(kind string, record []byte) error {
		if kind != "release" {
			return errors.New("benchmark needs releases dump")
		}
		raw.Write(record)
		count++
		if count == 20000 {
			return stop
		}
		return nil
	})
	if err != nil && !errors.Is(err, stop) {
		b.Fatal(err)
	}
	raw.WriteString("</releases>")
	in := fixtureInput(b, raw.String())
	cat, err := catalog.Open(catPath)
	if err != nil {
		b.Fatal(err)
	}
	defer cat.Close()
	tracks := make([]core.TrackRef, 0, cat.Len())
	for i := 0; i < cat.Len(); i++ {
		if m, ok := cat.Meta(cat.ID(i)); ok {
			tracks = append(tracks, m.Ref)
		}
	}
	for _, workers := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("workers=%d", workers), func(b *testing.B) {
			dir := b.TempDir()
			b.ReportAllocs()
			b.SetBytes(int64(raw.Len()))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				info, err := Build(context.Background(), BuildOptions{Output: filepath.Join(dir, fmt.Sprintf("%d.sqlite", i)), Date: "20260901", CatalogVersion: cat.CatalogVersion(), Tracks: tracks, Inputs: []Input{in}, Workers: workers})
				if err != nil || info.Scanned != int64(count) {
					b.Fatal(info, err)
				}
				b.ReportMetric(float64(info.ComposerCredits), "composer-credits/op")
				b.ReportMetric(float64(info.Tracks), "matched-tracks/op")
			}
		})
	}
}
