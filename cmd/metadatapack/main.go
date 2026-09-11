// metadatapack compacts publisher-verified Discogs dumps into a local index.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"time"

	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/metadata"
)

func main() {
	if err := run(); err != nil && err != flag.ErrHelp {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	flag := flag.NewFlagSet("metadatapack", flag.ContinueOnError)
	defaults := config.Default()
	catalogDir := flag.String("catalog", defaults.Catalog.Dir, "recommendation catalog directory")
	out := flag.String("output", filepath.Join(defaults.DataDir, "metadata", "discogs.sqlite"), "consolidated SQLite output path")
	replace := flag.Bool("replace", false, "rebuild an existing index, preserving a timestamped backup; close the app first")
	workers := flag.Int("workers", 0, "XML decode/catalog matching workers: 0=automatic (up to 8), 1=single worker, maximum 32")
	latest := flag.Bool("download-latest", false, "download latest complete set (about 11 GB); never runs during generation")
	downloadOnly := flag.Bool("download-only", false, "download and verify latest dumps without building an index")
	list := flag.Bool("list", false, "print latest complete publisher dump date and exit")
	year := flag.Int("year", 0, "publisher dump year; 0 discovers latest across published years")
	cache := flag.String("download-dir", "discogs-downloads", "bulk download directory")
	date := flag.String("date", "", "snapshot date YYYYMMDD for supplied files")
	releases := flag.String("releases", "", "local releases.xml.gz")
	releaseSum := flag.String("releases-sha256", "", "publisher releases SHA-256")
	masters := flag.String("masters", "", "optional local masters.xml.gz")
	masterSum := flag.String("masters-sha256", "", "publisher masters SHA-256")
	inspect := flag.String("inspect", "", "inspect an existing index and benchmark read-only lookups")
	query := flag.String("query", "Electronic", "genre/style for -inspect")
	pack := flag.String("pack", "", "full import SQLite to compact and compress for wizard distribution")
	bundleDir := flag.String("bundle-dir", "", "new output directory for compressed index and hosting manifest")
	compression := flag.String("compression-benchmark", "", "compare gzip/zstd sizes and time on an existing runtime SQLite index")
	verifyBundle := flag.String("verify-bundle", "", "verify an upload directory with the actual wizard decompressor; does not activate")
	if err := flag.Parse(os.Args[1:]); err != nil {
		return err
	}
	workerCount, err := metadata.ImportWorkers(*workers)
	if err != nil {
		return err
	}
	if !*list && *inspect == "" && *releases == "" && *masters == "" {
		*latest = true
	}
	if !*list && !*downloadOnly && *inspect == "" && (*catalogDir == "" || *out == "") {
		return fmt.Errorf("-catalog and -output are required")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if *verifyBundle != "" {
		manifest, err := metadata.VerifyBundle(ctx, *verifyBundle)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(manifest)
	}
	if *compression != "" {
		result, err := metadata.CompareCompression(ctx, *compression)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	if *pack != "" {
		if *bundleDir == "" {
			return errors.New("-bundle-dir required with -pack")
		}
		manifest, err := metadata.Package(ctx, *pack, *bundleDir)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(manifest)
	}
	if *inspect != "" {
		return inspectDataset(ctx, *inspect, *query)
	}
	var cat *catalog.Catalog
	var abs string
	if !*list && !*downloadOnly {
		var err error
		abs, err = filepath.Abs(*out)
		if err != nil {
			return err
		}
		if st, err := os.Lstat(abs); err == nil {
			if !*replace || !st.Mode().IsRegular() {
				return fmt.Errorf("output exists; use -replace with the app closed, or choose a new path")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if _, err := os.Lstat(abs + ".build.lock"); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("build lock exists or cannot be inspected; wait for the current build, or inspect a stale lock before retrying")
		}
		cat, err = catalog.Open(*catalogDir)
		if err != nil {
			return err
		}
		defer cat.Close()
	}
	var inputs []metadata.Input
	if *list || *latest || *downloadOnly {
		set, err := metadata.Latest(ctx, *year)
		if err != nil {
			return err
		}
		if *list {
			return json.NewEncoder(os.Stdout).Encode(set)
		}
		fmt.Fprintln(os.Stderr, "Downloading Discogs snapshot", set.Date)
		set, err = metadata.Download(ctx, set, *cache)
		if err != nil {
			return err
		}
		if *downloadOnly {
			return json.NewEncoder(os.Stdout).Encode(set)
		}
		*date = set.Date
		inputs = []metadata.Input{set.Files["releases"], set.Files["masters"]}
	} else {
		if *releases != "" {
			inputs = append(inputs, metadata.Input{Path: *releases, SHA256: *releaseSum})
		}
		if *masters != "" {
			inputs = append(inputs, metadata.Input{Path: *masters, SHA256: *masterSum})
		}
	}
	if _, err := time.Parse("20060102", *date); err != nil {
		return fmt.Errorf("valid -date required")
	}
	tracks := make([]core.TrackRef, 0, cat.Len())
	for i := 0; i < cat.Len(); i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if m, ok := cat.Meta(cat.ID(i)); ok {
			tracks = append(tracks, m.Ref)
		}
	}
	started := time.Now()
	fmt.Fprintf(os.Stderr, "Importing with %d XML decode/catalog matching workers\n", workerCount)
	info, err := metadata.Build(ctx, metadata.BuildOptions{Output: abs, Date: *date, CatalogVersion: cat.CatalogVersion(), Tracks: tracks, Inputs: inputs, Replace: *replace, Workers: workerCount, Progress: func(n int64) {
		fmt.Fprintf(os.Stderr, "processed %d entities (%s)\n", n, time.Since(started).Round(time.Second))
	}})
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(info)
}

func inspectDataset(ctx context.Context, path, query string) error {
	s, err := metadata.Open(path)
	if err != nil {
		return err
	}
	defer s.Close()
	stat, err := os.Stat(path)
	if err != nil {
		return err
	}
	var timings []float64
	var first float64
	var matches int
	for i := 0; i < 101; i++ {
		start := time.Now()
		rows, err := s.SampleGenre(ctx, query, 1000, uint32(i)*2654435761)
		if err != nil {
			return err
		}
		ms := float64(time.Since(start).Nanoseconds()) / 1e6
		if i == 0 {
			first = ms
		} else {
			timings = append(timings, ms)
		}
		matches = len(rows)
	}
	sort.Float64s(timings)
	return json.NewEncoder(os.Stdout).Encode(struct {
		Info              metadata.Info `json:"manifest"`
		Bytes             int64         `json:"bytes"`
		Query             string        `json:"query"`
		Limit             int           `json:"limit"`
		LastWindowMatches int           `json:"lastWindowMatches"`
		FirstMS           float64       `json:"firstMs"`
		MedianMS          float64       `json:"warmMedianMs"`
		P95MS             float64       `json:"warmP95Ms"`
	}{s.Info(), stat.Size(), query, 1000, matches, first, timings[50], timings[94]})
}
