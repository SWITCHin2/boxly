// Command boxly is the Boxly CLI client.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/SWITCHin2/boxly/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cli.NewRootCmd().ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
