package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/enrich/musicbrainz"
	"github.com/platten/playlistai/internal/intent/llama"
	"github.com/platten/playlistai/internal/libraryannotate"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/ports"
)

func runSubset(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	flags := flag.NewFlagSet("playlist-indexer subset", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", "", "read-only source music directory")
	out := flags.String("out", "", "new bounded subset directory outside the source")
	limit := flags.Int("limit", 40, "maximum copied audio files (1..512), lexical order")
	maxBytes := flags.Int64("max-bytes", 4<<30, "maximum total copied bytes")
	fileList := flags.String("file-list", "", "optional JSON array of exact root-relative source paths")
	timeout := flags.Duration("timeout", 10*time.Minute, "maximum copy time")
	if err := flags.Parse(args); err != nil {
		return 1, err
	}
	if *root == "" || *out == "" || *timeout <= 0 || flags.NArg() != 0 {
		return 1, errors.New("subset requires --root, --out and positive --timeout")
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	var paths []string
	if *fileList != "" {
		f, err := os.Open(*fileList)
		if err != nil {
			return 1, err
		}
		defer f.Close()
		if err = json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&paths); err != nil {
			return 1, err
		}
		if len(paths) == 0 {
			return 1, errors.New("file list must not be empty")
		}
	}
	manifest, err := libraryannotate.SubsetSelection(ctx, *root, *out, *limit, *maxBytes, paths)
	if err != nil {
		return 1, err
	}
	return 0, json.NewEncoder(stdout).Encode(manifest)
}
func runAnnotate(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	flags := flag.NewFlagSet("playlist-indexer annotate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	pack := flags.String("pack", "", "verified .paipack produced by run/export")
	state := flags.String("state", "", "isolated annotation cache directory")
	out := flags.String("out", "", "new JSONL web-source evidence sidecar")
	limit := flags.Int("limit", 40, "maximum recordings (1..512)")
	timeout := flags.Duration("timeout", 10*time.Minute, "total acquisition deadline")
	offline := flags.Bool("offline", false, "produce unknown assessments without network acquisition")
	model := flags.String("model", "", "optional local GGUF for quotation extraction; no model downloads")
	runtimeDir := flags.String("runtime-dir", "", "optional directory containing llama-server or llama for --model")
	threads := flags.Int("model-threads", 2, "CPU threads for optional local quotation extraction")
	var criterionFlags stringList
	flags.Var(&criterionFlags, "criterion", "required KIND:complete phrase; repeatable")
	if err := flags.Parse(args); err != nil {
		return 1, err
	}
	if *pack == "" || *state == "" || *out == "" || *limit < 1 || *limit > 512 || *timeout <= 0 || flags.NArg() != 0 {
		return 1, errors.New("annotate requires --pack, --state, --out, 1..512 --limit and positive --timeout")
	}
	if *runtimeDir != "" && *model == "" {
		return 1, errors.New("--runtime-dir requires --model for annotation extraction")
	}
	if *offline && *model != "" {
		return 1, errors.New("--model requires online source acquisition; omit --offline")
	}
	var criteria []core.MusicalCriterion
	for _, raw := range criterionFlags {
		kind, value, ok := strings.Cut(raw, ":")
		kind, value = strings.TrimSpace(kind), strings.TrimSpace(value)
		if !ok || kind == "" || value == "" || len(value) > 250 {
			return 1, errors.New("criterion must be KIND:complete phrase (up to 250 bytes)")
		}
		criteria = append(criteria, core.MusicalCriterion{Kind: kind, Value: value})
	}
	if len(criteria) == 0 || len(criteria) > 32 {
		return 1, errors.New("supply 1..32 --criterion values; missing descriptions remain unknown")
	}
	if err := os.MkdirAll(*state, 0700); err != nil {
		return 1, err
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	manager, err := librarypack.OpenManager(ctx, filepath.Join(*state, "source-pack"), librarypack.Limits{})
	if err != nil {
		return 1, err
	}
	defer manager.Close()
	staged, err := manager.Stage(ctx, *pack)
	if err != nil {
		return 1, err
	}
	defer func() { _ = manager.Discard(staged) }()
	tracks, err := staged.Generation().List(ctx, "", *limit)
	if err != nil {
		return 1, err
	}
	var provider libraryannotate.Provider
	if !*offline {
		client, err := musicbrainz.New(musicbrainz.Config{UserAgent: "PlaylistAI-library-annotations/1 (https://github.com/platten/playlistai)", CachePath: filepath.Join(*state, "metadata.sqlite")})
		if err != nil {
			return 1, err
		}
		defer client.Close()
		var extractor ports.RecordingSourceExtractor = libraryannotate.LiteralExtractor{Criteria: criteria}
		if *model != "" {
			if *threads < 1 || *threads > 64 {
				return 1, errors.New("model-threads must be 1..64")
			}
			binary := ""
			if *runtimeDir != "" {
				for _, name := range []string{"llama-server", "llama-server.exe", "llama", "llama.exe"} {
					candidate := filepath.Join(*runtimeDir, name)
					if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
						binary = candidate
						break
					}
				}
				if binary == "" {
					return 1, errors.New("runtime-dir contains no llama-server or llama executable")
				}
			}
			parser, err := llama.New(ctx, llama.Options{ModelPath: *model, BinaryPath: binary, NCtx: 4096, NThreads: *threads, GPULayers: -1, StartTimeout: 30 * time.Second})
			if err != nil {
				return 1, err
			}
			defer parser.Close()
			extractor = parser
		}
		client.WithRecordingSourceExtractor(libraryannotate.QuoteBoundedExtractor{Extractor: extractor})
		provider = client
	}
	file, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return 1, err
	}
	encoder := json.NewEncoder(file)
	completed := 0
	for _, track := range tracks {
		if gracefulStopRequested(ctx) {
			break
		}
		row, err := libraryannotate.Annotate(ctx, provider, track, criteria)
		if err != nil {
			file.Close()
			return 1, err
		}
		row.PackID, row.PackSHA256 = staged.Manifest().PackID, staged.PackSHA256()
		if err = encoder.Encode(row); err != nil {
			file.Close()
			return 1, err
		}
		if err = file.Sync(); err != nil {
			file.Close()
			return 1, err
		}
		completed++
	}
	if err = file.Close(); err != nil {
		return 1, err
	}
	fmt.Fprintf(stdout, "Wrote %d web-source evidence rows. These are not independent human listening labels.\n", completed)
	if gracefulStopRequested(ctx) {
		return 130, context.Canceled
	}
	return 0, nil
}
