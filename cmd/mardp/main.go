// Command mardp is an RDP host for macOS (docs/SPEC.md). It opens the app's
// window; with -headless it runs the server without one, and -version prints
// its version.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, mac{})
	stop()
	os.Exit(code)
}
