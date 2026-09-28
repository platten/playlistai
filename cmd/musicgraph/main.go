// musicgraph prepares public aggregate discovery data outside the desktop path.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/platten/playlistai/internal/musicgraph"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	cancel()
	if err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: musicgraph prepare|fetch|inspect|inventory|resume|coverage|enrich-pack -input file [options]; use COMMAND -h for flags")
	}
	command := args[0]
	if command == "enrich-pack" {
		return runEnrichPack(ctx, args[1:], stdout, stderr)
	}
	if command == "inventory" || command == "resume" || command == "coverage" {
		return runPrepared(ctx, command, args[1:], stdout, stderr)
	}
	if command != "prepare" && command != "fetch" && command != "inspect" {
		return errors.New("unknown musicgraph command")
	}
	flags := flag.NewFlagSet("musicgraph "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "", "explicit input JSON; fetch expects an array of seed artist MBIDs")
	output := flags.String("output", "", "new immutable output file; no default or replacement")
	expected := flags.String("sha256", "", "expected snapshot SHA256 for inspect")
	timeout := flags.Duration("timeout", 30*time.Minute, "overall offline preparation limit, at most one hour")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *input == "" || *timeout <= 0 || *timeout > time.Hour {
		return errors.New("input file and bounded positive timeout required; no positional arguments")
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	if command == "inspect" {
		if *output != "" {
			return errors.New("inspect does not write artifacts")
		}
		r, err := musicgraph.Open(ctx, *input, *expected)
		if err != nil {
			return err
		}
		s := r.Manifest()
		return json.NewEncoder(stdout).Encode(map[string]any{"sha256": r.SnapshotIdentity(), "version": s.Version, "preparedAt": s.PreparedAt, "artists": len(s.Artists), "recordings": len(s.Recordings), "topRecordingGroups": len(s.TopRecordings), "neighborRelationships": len(s.Neighbors), "omittedDiscovery": len(s.OmittedDiscovery)})
	}
	if *output == "" || *expected != "" {
		return errors.New("prepare/fetch requires a new output path; sha256 is for inspect")
	}
	if _, err := os.Lstat(*output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output must not exist")
	}
	f, err := os.Open(*input)
	if err != nil {
		return err
	}
	defer f.Close()
	var snapshot musicgraph.Snapshot
	if command == "prepare" {
		snapshot, err = musicgraph.Decode(f)
	} else {
		b, readErr := io.ReadAll(io.LimitReader(f, 128*1024+1))
		if readErr != nil {
			return readErr
		}
		if len(b) > 128*1024 {
			return errors.New("seed input too large")
		}
		var seeds []string
		if err = json.Unmarshal(b, &seeds); err != nil {
			return err
		}
		client, clientErr := musicgraph.NewClient(nil, musicgraph.UserAgent)
		if clientErr != nil {
			return clientErr
		}
		snapshot, err = client.Prepare(ctx, seeds)
	}
	if err != nil {
		return err
	}
	hash, err := musicgraph.Write(ctx, *output, snapshot)
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(map[string]any{"sha256": hash, "version": snapshot.Version, "preparedAt": snapshot.PreparedAt, "artists": len(snapshot.Artists), "recordings": len(snapshot.Recordings), "omittedDiscovery": len(snapshot.OmittedDiscovery)})
}
