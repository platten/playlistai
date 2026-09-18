package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	os.Exit(runMain())
}

func runMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code, err := execute(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
	}
	return code
}
