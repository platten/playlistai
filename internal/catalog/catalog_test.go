package catalog

import (
	"database/sql"
	"math"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestdata(t *testing.T) *Catalog {
	t.Helper()
	c, err := Open("testdata")
	if err != nil {
		t.Fatalf("Open(testdata): %v (regenerate with python/make_test_catalog.py)", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestOpenShape(t *testing.T) {
	t.Parallel()
	c := openTestdata(t)

	if c.Len() != 256 {
		t.Fatalf("Len = %d, want 256", c.Len())
	}
	if c.Dim() != 100 {
		t.Fatalf("Dim = %d, want 100", c.Dim())
	}
	if got := c.ID(0); got != "seed0001" {
		t.Fatalf("ID(0) = %q, want seed0001", got)
	}
	if got := c.ID(-1); got != "" {
		t.Fatalf("ID(-1) = %q, want empty", got)
	}
	if r, ok := c.RowOf("seed0004"); !ok || r != 3 {
		t.Fatalf("RowOf(seed0004) = %d, %v", r, ok)
	}
	if _, ok := c.RowOf("nope"); ok {
		t.Fatal("RowOf(nope) should miss")
	}
}

func TestMeta(t *testing.T) {
	t.Parallel()
	c := openTestdata(t)

	m, ok := c.Meta("seed0001")
	if !ok {
		t.Fatal("Meta(seed0001) missing")
	}
	if m.Ref.Artist != "Justice" || m.Ref.Title != "Genesis" {
		t.Fatalf("Meta ref = %+v", m.Ref)
	}
	if m.PreviewURL != "https://cdn.example/preview/genesis.mp3" {
		t.Fatalf("preview = %q", m.PreviewURL)
	}
	if _, ok := c.Meta("does-not-exist"); ok {
		t.Fatal("Meta(does-not-exist) should miss")
	}
}

func TestVectorsDequantized(t *testing.T) {
	t.Parallel()
	c := openTestdata(t)

	v, ok := c.Vectors("seed0002")
	if !ok {
		t.Fatal("Vectors(seed0002) missing")
	}
	if len(v.Audio) != 100 || len(v.Track) != 100 {
		t.Fatalf("dims: audio=%d track=%d", len(v.Audio), len(v.Track))
	}
	// Source vectors are unit vectors; int8 quantization keeps the norm close to 1.
	for _, name := range []string{"audio", "track"} {
		vec := v.Audio
		if name == "track" {
			vec = v.Track
		}
		var sum float64
		for _, x := range vec {
			sum += float64(x) * float64(x)
		}
		norm := math.Sqrt(sum)
		if norm < 0.95 || norm > 1.05 {
			t.Fatalf("%s norm = %.4f, want ~1", name, norm)
		}
	}

	byRow, ok := c.VectorsByRow(1)
	if !ok || byRow.Audio[0] != v.Audio[0] {
		t.Fatal("VectorsByRow(1) disagrees with Vectors(seed0002)")
	}
	if _, ok := c.VectorsByRow(9999); ok {
		t.Fatal("VectorsByRow(9999) should miss")
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()
	c := openTestdata(t)

	cases := []struct {
		query string
		want  []string // subset / exact as noted
		exact bool
	}{
		{"justice", []string{"seed0001", "seed0002", "seed0007"}, true},
		{"justice genesis", []string{"seed0001"}, true},
		{"sigur ros", []string{"seed0005"}, true},
		{"Björk", []string{"seed0006"}, true},
		{"justice radio edit", []string{"seed0007"}, true},
		{"", nil, true},
		{"   ", nil, true},
		{"zzzznotarealthing", nil, true},
	}
	for _, tc := range cases {
		got := c.Resolve(tc.query, 50)
		var gotIDs []string
		for _, r := range got {
			gotIDs = append(gotIDs, r.ID)
		}
		if tc.exact {
			if !equalStrings(gotIDs, tc.want) {
				t.Errorf("Resolve(%q) = %v, want %v", tc.query, gotIDs, tc.want)
			}
		}
	}

	// row ordering
	got := c.Resolve("justice", 50)
	for i := 1; i < len(got); i++ {
		ri, _ := c.RowOf(got[i-1].ID)
		rj, _ := c.RowOf(got[i].ID)
		if ri >= rj {
			t.Fatalf("Resolve not row-ordered: %v", got)
		}
	}

	// max cap
	if n := len(c.Resolve("a", 3)); n > 3 {
		t.Fatalf("Resolve honored max? got %d", n)
	}
}

// TestSearchColumnParity checks the Go normalizer against the values Python
// wrote into the fixture, for every fixture row.
func TestSearchColumnParity(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join("testdata", DBFile))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query("SELECT artist, title, search FROM tracks")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	n := 0
	for rows.Next() {
		var artist, title, stored string
		if err := rows.Scan(&artist, &title, &stored); err != nil {
			t.Fatal(err)
		}
		if got := normalizeSearch(artist + " " + title); got != stored {
			t.Errorf("row %q / %q: Go %q != Python %q", artist, title, got, stored)
		}
		n++
	}
	if n != 256 {
		t.Fatalf("checked %d rows, want 256", n)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
