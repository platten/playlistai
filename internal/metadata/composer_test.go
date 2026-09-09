package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

const composerFixture = `<releases><release id="42"><title>Album</title><artists><artist><id>1</id><name>Performer</name></artist></artists>
<extraartists>
<artist><id>10</id><name>作曲家 (2)</name><anv>別名</anv><role>Composed By [Theme, Variation], Conductor</role><tracks>A1 to A2</tracks></artist>
<artist><id>11</id><name>Release Composer</name><role>Music By</role></artist>
<artist><id>12</id><name>Unresolved Scope</name><role>Composed By</role><tracks>A1 to Z9</tracks></artist>
<artist><id>13</id><name>Writer</name><role>Written-By, Lyrics By, Arranged By</role></artist>
<artist><id>14</id><name>Other Track Composer</name><role>Composed By</role><tracks>A2</tracks></artist>
</extraartists><genres><genre>Classical</genre></genres><tracklist>
<track><position>A1</position><title>One</title><extraartists>
<artist><id>20</id><name>Track Composer</name><role>Composed By</role></artist>
<artist><id>20</id><name>Track Composer</name><role>Composed By</role></artist>
<artist><name>Unlinked Composer</name><role>Music By</role></artist>
</extraartists></track><track><position>A2</position><title>Two</title></track>
<track><title>Heading</title><extraartists><artist><name>Heading Composer</name><role>Composed By</role></artist></extraartists></track>
</tracklist></release>
<release id="43"><title>No Genre</title><artists><artist><id>1</id><name>Performer</name></artist></artists><tracklist><track><position>1</position><title>Three</title><extraartists><artist><name>Tagless Composer</name><role>Music By</role></artist></extraartists></track></tracklist></release></releases>`

func TestComposerRolesAndPositionScopes(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want []string
	}{
		{"Composed By [Theme, Variation], Conductor, Music By", []string{"Composed By [Theme, Variation]", "Music By"}},
		{"Lyrics By, Written-By, Arranged By, Producer", nil},
		{"Other [Composed By]", nil},
		{"Composed By [broken, Music By", nil},
		{"Not Composed By, Composed By Someone", nil},
	} {
		if got := composerRoles(tc.raw); !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc.raw, got)
		}
	}
	tracks := []xmlTrack{{Position: "A1"}, {Position: "A2"}, {Position: "B1"}, {Position: "1-1"}}
	for _, tc := range []struct {
		raw   string
		want  map[int]bool
		known bool
	}{
		{"A1, B1 to 1-1", map[int]bool{0: true, 2: true, 3: true}, true},
		{"A1 to B1", map[int]bool{0: true, 1: true, 2: true}, true},
		{"B1 to A1", nil, false}, {"A1, Z1", nil, false}, {"", nil, false}, {"A1-A2", nil, false},
	} {
		got, known := scopedPositions(tc.raw, tracks)
		if known != tc.known || !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc.raw, got, known)
		}
	}
	if _, known := scopedPositions("A1", []xmlTrack{{Position: "A1"}, {Position: "A1"}}); known {
		t.Fatal("ambiguous positions accepted")
	}
}

func TestComposerCreditsRoundTripThroughCompactBundle(t *testing.T) {
	ctx := context.Background()
	var first []byte
	for _, workers := range []int{1, 4} {
		out := filepath.Join(t.TempDir(), "full.sqlite")
		info, err := Build(ctx, BuildOptions{Output: out, Date: "20260901", CatalogVersion: "fixture", Workers: workers, Inputs: []Input{fixtureInput(t, composerFixture)}, Tracks: []core.TrackRef{
			{ID: "one", Artist: "Performer", Title: "One"}, {ID: "two", Artist: "Performer", Title: "Two"}, {ID: "three", Artist: "Performer", Title: "Three"}, {ID: "heading", Artist: "Performer", Title: "Heading"},
		}})
		if err != nil || info.ComposerVersion != ComposerVersion || info.ComposerCredits != 10 {
			t.Fatal(info, err)
		}
		raw, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = raw
		} else if !reflect.DeepEqual(first, raw) {
			t.Fatal("worker count changed composer index")
		}
		s, err := Open(out)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		got, err := s.Composers(ctx, "one")
		if err != nil || len(got) != 5 {
			t.Fatal(got, err)
		}
		for _, c := range got {
			if c.Source != "https://www.discogs.com/release/42" || c.SourceVersion != ComposerVersion+":20260901" || c.TrackPosition != "A1" {
				t.Fatal(c)
			}
			switch c.Name {
			case "作曲家 (2)":
				if c.Scope != "release_tracks" || c.NameVariation != "別名" || c.ArtistID != 10 {
					t.Fatal(c)
				}
			case "Release Composer", "Unresolved Scope":
				if c.Scope != "release_unknown" {
					t.Fatal(c)
				}
			case "Track Composer", "Unlinked Composer":
				if c.Scope != "track" {
					t.Fatal(c)
				}
			default:
				t.Fatal("invented composer", c)
			}
		}
		dir := filepath.Join(t.TempDir(), "bundle")
		m, err := Package(ctx, out, dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := VerifyBundle(ctx, dir); err != nil {
			t.Fatal(err)
		}
		c, err := Open(filepath.Join(dir, m.Index.Name))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		for _, id := range []string{"one", "two", "three", "heading", "unknown"} {
			a, err := s.Composers(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			b, err := c.Composers(ctx, id)
			if err != nil || !reflect.DeepEqual(a, b) {
				t.Fatal(id, a, b, err)
			}
			if (id == "heading" || id == "unknown") && len(b) != 0 {
				t.Fatal(b)
			}
		}
		if c.Info().ComposerCredits != info.ComposerCredits {
			t.Fatal(c.Info())
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := c.Composers(canceled, "one"); err == nil {
			t.Fatal("ignored cancellation")
		}
	}
}

func TestLegacyIndexesWithoutComposerExtension(t *testing.T) {
	full, dir, m := compactFixture(t)
	for _, path := range []string{full, filepath.Join(dir, m.Index.Name)} {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		var raw string
		if err := db.QueryRow("SELECT value FROM info WHERE key='manifest'").Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var info Info
		if err := json.Unmarshal([]byte(raw), &info); err != nil {
			t.Fatal(err)
		}
		info.ComposerVersion, info.ComposerCredits = "", 0
		encoded, _ := json.Marshal(info)
		if _, err := db.Exec("UPDATE info SET value=? WHERE key='manifest'", string(encoded)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("DROP TABLE IF EXISTS composer_credits; DROP TABLE IF EXISTS composer_links; DROP TABLE IF EXISTS composer_values; DROP TABLE IF EXISTS composer_tracks"); err != nil {
			t.Fatal(err)
		}
		_ = db.Close()
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.Composers(context.Background(), "a")
		_ = s.Close()
		if err != nil || len(got) != 0 {
			t.Fatal(got, err)
		}
	}
	// A legacy full index can still be compacted; missing evidence stays missing.
	if _, err := Compact(context.Background(), full, filepath.Join(t.TempDir(), "legacy.sqlite")); err != nil {
		t.Fatal(err)
	}
}
