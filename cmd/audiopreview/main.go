// audiopreview validates the authorized preview -> Go -> native CLAP -> SQLite
// slice without ranking tracks or claiming calibrated musical judgments.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/audioruntime"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/preview/deezer"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--audio-worker" {
		if err := audioruntime.Run(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil && err != flag.ErrHelp {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	flag := flag.NewFlagSet("audiopreview", flag.ContinueOnError)
	bundle := flag.String("bundle", "", "local parity-validated runtime bundle")
	artist := flag.String("artist", "", "exact catalog artist")
	title := flag.String("title", "", "exact catalog title/version")
	id := flag.String("track-id", "", "catalog track ID")
	catalog := flag.String("catalog-version", "", "catalog identity")
	directory := flag.String("data-dir", "", "local derived-feature store")
	authorized := flag.Bool("authorized", false, "provider agreement covers analysis, permanent derivatives, and desktop distribution")
	screenVocals := flag.Bool("screen-vocals", false, "also run preview-only CLAP instrumental/vocal screening")
	if err := flag.Parse(os.Args[1:]); err != nil {
		return err
	}
	if !*authorized || *artist == "" || *title == "" || *id == "" || *catalog == "" || *directory == "" {
		return fmt.Errorf("explicit provider authorization, exact recording, catalog version, and data directory are required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	m, err := audio.ReadRuntimeBundle(*bundle)
	if err != nil {
		return err
	}
	w := &audio.Worker{Executable: m.File(*bundle, "worker"), BundleDir: *bundle, Model: m.Model}
	defer func() { _ = w.Close() }()
	started := time.Now()
	if err := w.Health(ctx); err != nil {
		return err
	}
	healthMS := time.Since(started).Milliseconds()
	store, err := audio.OpenStore(*directory)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	track := core.TrackRef{ID: *id, Artist: *artist, Title: *title}
	record, hit, err := store.Find(ctx, *catalog, track.ID, core.ProvisionalRecordingKey(track), m.Model)
	if err != nil {
		return err
	}
	var fetched int64
	started = time.Now()
	if !hit {
		service := &audio.Service{Resolver: deezer.New(deezer.Config{}), Analyzer: w, Store: store, Authorized: *authorized, ParityValidated: m.Parity.Valid()}
		record, fetched, err = service.AnalyzePreview(ctx, track, *catalog)
		if err != nil {
			return err
		}
	}
	usage, err := store.Usage(ctx)
	if err != nil {
		return err
	}
	var assessment *core.AudioAssessment
	if *screenVocals {
		service := &audio.Service{Resolver: deezer.New(deezer.Config{}), Analyzer: w, Store: store, Authorized: *authorized, ParityValidated: m.Parity.Valid()}
		intent := core.MusicIntent{OriginalDescription: "Instrumental, no vocals", HardConstraints: []core.HardConstraint{{Kind: "exclude_vocals"}}}
		session, e := service.Begin(ctx, intent, *catalog, nil)
		if e != nil {
			return e
		}
		defer session.Close()
		checked, e := session.Check(ctx, track, false)
		if e != nil {
			return e
		}
		assessment = &checked
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		AnalysisID           string                    `json:"analysisId"`
		Identity             core.PreviewIdentity      `json:"identity"`
		Coverage             core.PreviewCoverage      `json:"coverage"`
		CacheHit             bool                      `json:"cacheHit"`
		BytesFetched         int64                     `json:"bytesFetched"`
		HealthMilliseconds   int64                     `json:"healthMilliseconds"`
		AnalysisMilliseconds int64                     `json:"analysisMilliseconds"`
		Storage              core.AnalysisStorageUsage `json:"storage"`
		VocalScreening       *core.AudioAssessment     `json:"vocalScreening,omitempty"`
	}{record.ID, record.Identity, record.Coverage, hit, fetched, healthMS, time.Since(started).Milliseconds(), usage, assessment})
}
