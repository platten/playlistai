package deejai_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/reco/deejai"
	"github.com/platten/playlistai/internal/similarity/brute"
)

type goldenCase struct {
	Params struct {
		Mode       string   `json:"mode"`
		Seeds      []string `json:"seeds"`
		Creativity float64  `json:"creativity"`
		Lookback   int      `json:"lookback"`
		Count      int      `json:"count"`
	} `json:"params"`
	TrackIDs []string `json:"track_ids"`
}

// TestGoldenParity runs the Go engine against fixtures generated from a faithful
// reimplementation of upstream backend/deejai.py (python/parity_playlist.py) on
// the same catalog. The full sequence must be within edit distance 1 (int8
// quantization + float32 vs float64 can nudge one near-tied pick), and the first
// three picks must match exactly.
func TestGoldenParity(t *testing.T) {
	t.Parallel()

	cat, err := catalog.Open(filepath.Join("..", "..", "catalog", "testdata"))
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })
	eng := deejai.New(cat, brute.New(cat))

	dir := filepath.Join("testdata", "golden")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v (regenerate with python/parity_playlist.py)", dir, err)
	}
	files := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		files++
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			var gc goldenCase
			if err := json.Unmarshal(raw, &gc); err != nil {
				t.Fatal(err)
			}
			if gc.Params.Mode == string(core.ModeJourney) {
				t.Skip("legacy parity baseline uses upstream per-segment count semantics; fixture retained intentionally")
			}

			intent := core.MusicIntent{
				Seeds:       core.IntentSeeds{TrackIDs: gc.Params.Seeds},
				Count:       gc.Params.Count,
				Mode:        core.Mode(gc.Params.Mode),
				Creativity:  gc.Params.Creativity,
				Lookback:    gc.Params.Lookback,
				Seed:        "1", // irrelevant: fixtures are noise-free
				Constraints: core.IntentConstraints{NoRepeatArtistBackToBack: true},
			}

			pl, err := eng.Build(context.Background(), intent)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			got := pl.IDs()
			want := gc.TrackIDs

			if len(got) != len(want) {
				t.Fatalf("length %d != %d\n got=%v\nwant=%v", len(got), len(want), got, want)
			}
			for i := 0; i < 3 && i < len(want); i++ {
				if got[i] != want[i] {
					t.Fatalf("pick %d: got %s want %s\n got=%v\nwant=%v", i, got[i], want[i], got, want)
				}
			}
			d := editDistance(got, want)
			t.Logf("edit distance %d / %d", d, len(want))
			if d > 1 {
				t.Fatalf("edit distance %d > 1\n got=%v\nwant=%v", d, got, want)
			}
		})
	}
	if files == 0 {
		t.Fatal("no golden fixtures found")
	}
}

// editDistance is Levenshtein over two string slices.
func editDistance(a, b []string) int {
	m, n := len(a), len(b)
	prev := make([]int, n+1)
	curr := make([]int, n+1)
	for j := 0; j <= n; j++ {
		prev[j] = j
	}
	for i := 1; i <= m; i++ {
		curr[0] = i
		for j := 1; j <= n; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min3(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[n]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
