// musicbrainzpack downloads official MusicBrainz JSON dumps, builds the compact
// PlaylistAI SQLite index, compresses it, and emits upload-ready parts.
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
	"path/filepath"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/genrevocab"
	"github.com/platten/playlistai/internal/mbindex"
)

const genreUserAgent = "PlaylistAI/musicbrainzpack (https://github.com/platten/playlistai)"

func main() {
	if err := run(os.Args[1:]); err != nil && err != flag.ErrHelp {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	return runWithOutput(ctx, args, os.Stdout, os.Stderr)
}

func runWithOutput(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("musicbrainzpack", flag.ContinueOnError)
	flags.SetOutput(stderr)
	base := flags.String("source", mbindex.DefaultDumpBase, "official MusicBrainz JSON dump directory")
	work := flags.String("work-dir", "musicbrainz-downloads", "verified dump and intermediate SQLite directory")
	bundle := flags.String("bundle-dir", "", "new or empty directory for R2 upload files")
	partBytes := flags.Int64("part-bytes", mbindex.DefaultPartBytes, "maximum compressed part size; must not exceed 200000000")
	replace := flags.Bool("replace-index", false, "rebuild the intermediate SQLite index instead of reusing its saved snapshot")
	cacheMiB := flags.Int("sqlite-cache-mib", mbindex.DefaultSQLiteCacheMiB, "SQLite page cache per database in MiB (1-4096; two concurrent import databases)")
	artistArchive := flags.String("artist-archive", "", "verified local artist.tar.xz; requires -recording-archive and -snapshot")
	recordingArchive := flags.String("recording-archive", "", "verified local recording.tar.xz; requires -artist-archive and -snapshot")
	snapshot := flags.String("snapshot", "", "MusicBrainz snapshot YYYYMMDD-HHMMSS for local archives")
	downloadOnly := flags.Bool("download-only", false, "download and verify the official dumps, then stop")
	verify := flags.String("verify-bundle", "", "verify an existing upload directory and exit")
	genreEndpoint := flags.String("genre-endpoint", genrevocab.DefaultEndpoint, "official MusicBrainz genre API endpoint")
	genreVocabulary := flags.String("genre-vocabulary", "", "prepared genre vocabulary JSON; skips the API fetch")
	skipGenres := flags.Bool("skip-genres", false, "package without the optional official genre vocabulary")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *cacheMiB < 1 || *cacheMiB > 4096 {
		return errors.New("-sqlite-cache-mib must be between 1 and 4096")
	}
	if *verify != "" {
		verifyProgress := newProgressDisplay(stderr)
		verifyProgress.Add("verify-parts", "Verify bundle parts", "bytes")
		verifyProgress.Add("verify-index", "Verify packed index", "bytes")
		verifyProgress.Add("verify-genres", "Verify genre vocabulary", "bytes")
		m, err := mbindex.VerifyBundleWithProgress(ctx, *verify, func(update mbindex.BundleProgress) {
			label := map[string]string{"verify-parts": "Verify bundle parts", "verify-index": "Verify packed index", "verify-genres": "Verify genre vocabulary"}[update.Stage]
			verifyProgress.Update(update.Stage, label, update.Done, update.Total, 0)
		})
		verifyProgress.Close()
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(m)
	}
	if *bundle == "" && !*downloadOnly {
		return errors.New("-bundle-dir is required")
	}
	if *skipGenres && *genreVocabulary != "" {
		return errors.New("-skip-genres cannot be combined with -genre-vocabulary")
	}
	if (*artistArchive == "") != (*recordingArchive == "") {
		return errors.New("supply both local archives or neither")
	}
	if *artistArchive != "" || *snapshot != "" {
		if _, err := time.Parse("20060102-150405", *snapshot); err != nil {
			return errors.New("-snapshot YYYYMMDD-HHMMSS is required for local archives")
		}
	}
	index := filepath.Join(*work, "musicbrainz.sqlite")
	var info mbindex.Info
	reuse := false
	if !*downloadOnly {
		if *partBytes != 0 && (*partBytes < 1024 || *partBytes > 200_000_000) {
			return errors.New("part size must be between 1024 and 200000000 bytes")
		}
		if entries, err := os.ReadDir(*bundle); err == nil {
			if len(entries) != 0 {
				return errors.New("bundle directory is not empty; choose a new directory or use -verify-bundle to verify an existing bundle")
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("bundle directory: %w", err)
		}
		if _, err := os.Stat(index); err == nil && !*replace {
			store, err := mbindex.Open(index)
			if err != nil {
				return fmt.Errorf("existing index cannot be reused; use -replace-index to rebuild: %w", err)
			}
			info = store.Info()
			if err := store.Close(); err != nil {
				return err
			}
			if *snapshot != "" && *snapshot != info.Snapshot {
				return fmt.Errorf("existing index snapshot is %s; use -replace-index to build %s", info.Snapshot, *snapshot)
			}
			reuse = true
			fmt.Fprintln(stderr, "Reusing completed MusicBrainz index, snapshot", info.Snapshot, "(skipping download/import)")
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.MkdirAll(*work, 0o755); err != nil {
		return err
	}
	if !reuse {
		inputs := map[string]string{}
		if *artistArchive == "" && *recordingArchive == "" {
			set, err := mbindex.Latest(ctx, *base)
			if err != nil {
				return err
			}
			fmt.Fprintln(stderr, "MusicBrainz snapshot", set.Snapshot)
			downloadProgress := newProgressDisplay(stderr)
			downloadProgress.Add("artist.tar.xz", "Download artists", "bytes")
			downloadProgress.Add("recording.tar.xz", "Download recordings", "bytes")
			set, err = mbindex.Download(ctx, set, *work, func(name string, done, total int64) {
				label := "Download " + strings.TrimSuffix(name, ".tar.xz") + "s"
				downloadProgress.Update(name, label, done, total, 0)
			})
			downloadProgress.Close()
			if err != nil {
				return err
			}
			if *downloadOnly {
				return json.NewEncoder(stdout).Encode(set)
			}
			*snapshot = set.Snapshot
			*artistArchive = set.Files["artist.tar.xz"].Path
			*recordingArchive = set.Files["recording.tar.xz"].Path
			inputs["artist.tar.xz"] = set.Files["artist.tar.xz"].SHA256
			inputs["recording.tar.xz"] = set.Files["recording.tar.xz"].SHA256
		} else if *artistArchive == "" || *recordingArchive == "" {
			return errors.New("supply both local archives or neither")
		}
		if _, err := time.Parse("20060102-150405", *snapshot); err != nil {
			return errors.New("-snapshot YYYYMMDD-HHMMSS is required for local archives")
		}
		buildProgress := newProgressDisplay(stderr)
		buildProgress.Add("artists", "Process artists", "bytes")
		buildProgress.Add("recordings", "Process recordings", "bytes")
		buildProgress.Add("finalize", "Finalize index", "steps")
		builtInfo, err := mbindex.Build(ctx, mbindex.BuildOptions{Output: index, Snapshot: *snapshot, ArtistArchive: *artistArchive, RecordingArchive: *recordingArchive, Inputs: inputs, Replace: *replace, SQLiteCacheMiB: *cacheMiB, Progress: func(update mbindex.BuildProgress) {
			label := "Process " + update.Entity
			if update.Entity == "finalize" {
				label = "Finalize index"
			}
			buildProgress.Update(update.Entity, label, update.Done, update.Total, update.Rows)
		}})
		buildProgress.Close()
		if err != nil {
			return err
		}
		info = builtInfo
	}
	preparedGenre := *genreVocabulary
	if !*skipGenres && preparedGenre == "" {
		fmt.Fprintln(stderr, "Fetching official MusicBrainz genre vocabulary")
		vocabulary, err := genrevocab.Fetch(ctx, nil, *genreEndpoint, genreUserAgent, time.Now())
		if err != nil {
			return fmt.Errorf("fetch genre vocabulary: %w", err)
		}
		file, err := os.CreateTemp(*work, ".musicbrainz-genres-*.json")
		if err != nil {
			return err
		}
		preparedGenre = file.Name()
		if err = file.Close(); err != nil {
			_ = os.Remove(preparedGenre)
			return err
		}
		defer os.Remove(preparedGenre)
		if err = genrevocab.Write(preparedGenre, vocabulary); err != nil {
			return err
		}
	}
	packageProgress := newProgressDisplay(stderr)
	packageProgress.Add("hash-index", "Hash while compressing", "bytes")
	packageProgress.Add("compress-index", "Compress bundle", "bytes")
	_, err := mbindex.PackageBundle(ctx, mbindex.BundlePackageOptions{Index: index, GenreVocabulary: preparedGenre, Directory: *bundle, PartBytes: *partBytes, Progress: func(update mbindex.BundleProgress) {
		label := map[string]string{"hash-index": "Hash while compressing", "compress-index": "Compress bundle"}[update.Stage]
		packageProgress.Update(update.Stage, label, update.Done, update.Total, 0)
	}})
	packageProgress.Close()
	if err != nil {
		return err
	}
	verifyProgress := newProgressDisplay(stderr)
	verifyProgress.Add("verify-parts", "Verify bundle parts", "bytes")
	verifyProgress.Add("verify-index", "Verify packed index", "bytes")
	verifyProgress.Add("verify-genres", "Verify genre vocabulary", "bytes")
	manifest, err := mbindex.VerifyBundleWithProgress(ctx, *bundle, func(update mbindex.BundleProgress) {
		label := map[string]string{"verify-parts": "Verify bundle parts", "verify-index": "Verify packed index", "verify-genres": "Verify genre vocabulary"}[update.Stage]
		verifyProgress.Update(update.Stage, label, update.Done, update.Total, 0)
	})
	verifyProgress.Close()
	if err != nil {
		return fmt.Errorf("verify packaged bundle: %w", err)
	}
	return json.NewEncoder(stdout).Encode(struct {
		Info     mbindex.Info           `json:"index"`
		Manifest mbindex.BundleManifest `json:"bundle"`
	}{info, manifest})
}
