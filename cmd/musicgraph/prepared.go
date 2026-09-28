package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/musicgraph"
)

func runPrepared(ctx context.Context, command string, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("musicgraph "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "", "input pack, catalog inventory, or graph")
	output := flags.String("output", "", "new immutable inventory or graph path")
	catalogPath := flags.String("catalog", "", "catalog identity inventory for coverage")
	sha := flags.String("sha256", "", "expected graph SHA256 for coverage")
	state := flags.String("state", "", "dedicated resumable preparation directory")
	reuseState := flags.String("reuse-state", "", "explicit v1 checkpoint source; destination must use a separate v2 state directory")
	license := flags.String("license", "", "source terms for inventory (required; no redistribution permission is inferred)")
	source := flags.String("source", "", "public source identifier or URL for inventory")
	batch := flags.Int("batch-size", 16, "artists per durable checkpoint, at most 1000")
	maxArtists := flags.Int("max-artists", 1000, "maximum stratified artists for this job; 0 means all inventory artists")
	installed := flags.Int64("installed-bytes", 0, "already installed additional prepared assets; counted against 10 GB ceiling")
	timeout := flags.Duration("timeout", 30*time.Minute, "preparation time limit, at most one hour")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *input == "" || *timeout <= 0 || *timeout > time.Hour {
		return errors.New("explicit input and bounded timeout required; no positional arguments")
	}
	if *reuseState != "" && command != "resume" {
		return errors.New("reuse-state is only valid for resume")
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	if command == "inventory" {
		if *output == "" || *license == "" || *source == "" {
			return errors.New("inventory requires new output path and explicit source/license")
		}
		inventory, err := inventoryFromPack(ctx, *input, *source, *license, *installed)
		if err != nil {
			return err
		}
		if err = musicgraph.WriteInventory(ctx, *output, inventory); err != nil {
			return err
		}
		seeds, err := inventory.PreparationSeeds()
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(map[string]any{"catalogSha256": inventory.CatalogSHA256, "tracks": len(inventory.Tracks), "artists": len(seeds)})
	}
	path := *input
	if command == "coverage" {
		if *catalogPath == "" || *sha == "" || *output != "" {
			return errors.New("coverage requires catalog inventory and expected graph SHA256; writes only stdout")
		}
		path = *catalogPath
	} else if *output == "" || *state == "" {
		return errors.New("resume requires a dedicated state directory and new graph output path")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	inventory, err := musicgraph.DecodeInventory(f)
	if err != nil {
		return err
	}
	if command == "coverage" {
		graph, err := musicgraph.Open(ctx, *input, *sha)
		if err != nil {
			return err
		}
		report, err := graph.Coverage(ctx, inventory)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(report)
	}
	if _, err := os.Lstat(*output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output must not exist")
	}
	client, err := musicgraph.NewClient(nil, musicgraph.UserAgent)
	if err != nil {
		return err
	}
	graph, receipt, err := client.PrepareJob(ctx, inventory, musicgraph.JobOptions{StateDirectory: *state, BatchSize: *batch, MaxArtists: *maxArtists, ExistingInstalledBytes: *installed, ReuseStateDirectory: *reuseState})
	if err != nil {
		_ = json.NewEncoder(stdout).Encode(receipt)
		return err
	}
	hash, err := musicgraph.Write(ctx, *output, graph)
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(struct {
		musicgraph.JobReceipt
		SHA256 string `json:"sha256"`
	}{receipt, hash})
}

func inventoryFromPack(ctx context.Context, path, source, license string, installed int64) (musicgraph.CatalogInventory, error) {
	out := musicgraph.CatalogInventory{Version: musicgraph.CatalogInventoryVersion, Source: source, License: license}
	if err := musicgraph.CheckAdditionalDataBudget(installed); err != nil {
		return out, err
	}
	limits := librarypack.DefaultLimits()
	// The original catalog is not an additional asset. The inventory itself is
	// small and contains no audio; these standard limits still validate the pack.
	dir, err := os.MkdirTemp("", "musicgraph-inventory-*")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(dir)
	manager, err := librarypack.OpenManager(ctx, dir, limits)
	if err != nil {
		return out, err
	}
	defer manager.Close()
	staged, err := manager.Stage(ctx, path)
	if err != nil {
		return out, err
	}
	defer manager.Discard(staged) //nolint:errcheck // The private temporary root is removed after closing.
	g := staged.Generation()
	out.CatalogSHA256 = g.PackSHA256()
	classifierIDs, err := g.ClassifierTrackIDs(ctx)
	if err != nil {
		return out, err
	}
	classified := make(map[string]bool, len(classifierIDs))
	for _, id := range classifierIDs {
		classified[id] = true
	}
	after := ""
	for {
		rows, err := g.List(ctx, after, 1000)
		if err != nil {
			return out, err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			identity := musicgraph.CatalogIdentity{ID: row.ID, RecordingMBID: row.MusicBrainzRecording, ArtistMBIDs: librarypack.ArtistMBIDs(row.RawTags), Genres: genreTags(row.RawTags), CLAP: slices.Contains(row.Capabilities, "clap"), MERT: slices.Contains(row.Capabilities, "mert")}
			identity.Classifier = classified[row.ID]
			out.Tracks = append(out.Tracks, identity)
		}
		after = rows[len(rows)-1].ID
	}
	return out, out.Validate()
}

func genreTags(raw json.RawMessage) []string {
	var tags map[string]json.RawMessage
	if json.Unmarshal(raw, &tags) != nil {
		return nil
	}
	var out []string
	for key, value := range tags {
		if !strings.EqualFold(strings.TrimSpace(key), "genre") {
			continue
		}
		var scalar string
		var values []string
		if json.Unmarshal(value, &scalar) == nil {
			values = []string{scalar}
		} else if json.Unmarshal(value, &values) != nil {
			continue
		}
		for _, value := range values {
			for _, genre := range strings.Split(value, ";") {
				genre = strings.ToLower(strings.TrimSpace(genre))
				if genre != "" && len(genre) <= 256 && !slices.Contains(out, genre) {
					out = append(out, genre)
				}
			}
		}
	}
	slices.Sort(out)
	return out
}
