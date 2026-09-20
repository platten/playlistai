// discoverypack audits and builds public music-discovery releases offline.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/platten/playlistai/internal/discoveryasset"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		stop()
		os.Exit(1)
	}
	stop()
}
func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: discoverypack audit PACK... | verify DIRECTORY | build --output DIR --base-url HTTPS_URL --version VERSION PACK [PACK]")
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	switch args[0] {
	case "audit":
		if len(args) < 2 {
			return fmt.Errorf("audit requires at least one pack")
		}
		for _, p := range args[1:] {
			a, e := discoveryasset.AuditPack(ctx, p)
			if e != nil {
				return e
			}
			if e = encoder.Encode(a); e != nil {
				return e
			}
		}
		return nil
	case "verify":
		if len(args) != 2 {
			return fmt.Errorf("verify requires one release directory")
		}
		m, e := discoveryasset.Verify(ctx, args[1])
		if e != nil {
			return e
		}
		return encoder.Encode(m)
	case "build":
		fs := flag.NewFlagSet("build", flag.ContinueOnError)
		var o discoveryasset.BuildOptions
		fs.StringVar(&o.Output, "output", "", "new output directory")
		fs.StringVar(&o.BaseURL, "base-url", "", "immutable HTTPS release prefix")
		fs.StringVar(&o.Version, "version", "", "release version")
		fs.IntVar(&o.MaxTracks, "max-tracks", 100000, "maximum total selected tracks")
		fs.IntVar(&o.MaxPerAlbum, "max-per-album", 8, "maximum selected tracks per album")
		if e := fs.Parse(args[1:]); e != nil {
			return e
		}
		if o.Output == "" {
			return fmt.Errorf("--output is required")
		}
		o.Inputs = fs.Args()
		m, e := discoveryasset.Build(ctx, o)
		if e != nil {
			return e
		}
		return encoder.Encode(m)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
