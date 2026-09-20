// modelpack verifies and unpacks a segmented distribution without Python.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"

	"github.com/platten/playlistai/internal/modelpack"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil && err != flag.ErrHelp {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("modelpack", flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifest := flags.String("manifest", "", "HTTPS URL or local manifest.json path")
	checksum := flags.String("manifest-sha256", "", "optional pinned manifest SHA-256")
	cache := flags.String("cache", "", "resumable compressed segment cache")
	out := flags.String("out", "", "new destination directory (must not exist)")
	source := flags.String("pack-source", "", "directory to package into a segmented tar.zst stream")
	bundle := flags.String("pack-output", "", "new or empty directory for upload-ready parts")
	name := flags.String("name", "", "portable model pack identifier")
	recommended := flags.String("recommended", "", "download the pinned mert or clap pack for this platform")
	partBytes := flags.Int64("part-bytes", modelpack.DefaultPartBytes, "maximum part size; must be below 200000000")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *recommended != "" {
		if *manifest != "" || *checksum != "" || *source != "" || *bundle != "" || *name != "" || *cache == "" || *out == "" {
			flags.Usage()
			return fmt.Errorf("--recommended requires cache and out, without another model source")
		}
		var distribution modelpack.Distribution
		var err error
		switch *recommended {
		case "mert":
			distribution, err = modelpack.RecommendedMERT(runtime.GOOS, runtime.GOARCH)
		case "clap":
			distribution, err = modelpack.RecommendedCLAP(runtime.GOOS, runtime.GOARCH)
		default:
			return fmt.Errorf("--recommended must be mert or clap")
		}
		if err != nil {
			return err
		}
		*manifest, *checksum = distribution.URL, distribution.SHA256
	}
	packMode := *source != "" || *bundle != "" || *name != ""
	if packMode {
		if *source == "" || *bundle == "" || *name == "" || *manifest != "" || *cache != "" || *out != "" {
			flags.Usage()
			return fmt.Errorf("provide manifest, cache and out to unpack; or pack-source, pack-output and name to package")
		}
	} else if *manifest == "" || *cache == "" || *out == "" {
		flags.Usage()
		return fmt.Errorf("provide manifest, cache and out to unpack; or pack-source, pack-output and name to package")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if packMode {
		m, err := modelpack.Package(ctx, *name, *source, *bundle, *partBytes)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Created %d model pack parts in %s\n", len(m.Parts), *bundle)
		return nil
	}
	if err := modelpack.FetchPinned(ctx, *manifest, *checksum, *cache, *out, nil); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Verified and unpacked model pack:", *out)
	return nil
}
