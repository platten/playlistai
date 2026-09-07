// audioworker is the optional Go-managed CPU inference child. The desktop
// application downloads a matching native runtime; Python is never required.
package main

import (
	"flag"
	"os"

	"github.com/platten/playlistai/internal/audioruntime"
)

func main() {
	dir := flag.String("bundle", "", "verified model bundle directory")
	flag.Parse()
	if err := audioruntime.Run(*dir); err != nil {
		os.Exit(1)
	}
}
