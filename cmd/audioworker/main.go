// audioworker is the optional Go-managed CPU inference child. The desktop
// application downloads a matching native runtime; Python is never required.
package main

import (
	"flag"
	"os"

	"github.com/platten/playlistai/internal/audioruntime"
)

func main() {
	if err := run(os.Args[1:], audioruntime.Run); err != nil && err != flag.ErrHelp {
		os.Exit(1)
	}
}

func run(args []string, worker func(string) error) error {
	flag := flag.NewFlagSet("audioworker", flag.ContinueOnError)
	dir := flag.String("bundle", "", "verified model bundle directory")
	if err := flag.Parse(args); err != nil {
		return err
	}
	return worker(*dir)
}
