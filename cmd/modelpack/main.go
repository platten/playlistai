// modelpack verifies and unpacks a segmented distribution without Python.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/platten/playlistai/internal/modelpack"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	manifest := flag.String("manifest", "", "HTTPS URL or local manifest.json path")
	checksum := flag.String("manifest-sha256", "", "optional pinned manifest SHA-256")
	cache := flag.String("cache", "", "resumable compressed segment cache")
	out := flag.String("out", "", "new destination directory (must not exist)")
	flag.Parse()
	if *manifest == "" || *cache == "" || *out == "" {
		flag.Usage()
		return fmt.Errorf("manifest, cache and out are required")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := modelpack.FetchPinned(ctx, *manifest, *checksum, *cache, *out, nil); err != nil {
		return err
	}
	fmt.Println("Verified and unpacked model pack:", *out)
	return nil
}
