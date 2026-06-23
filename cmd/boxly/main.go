// Command ongo is the OnGo CLI client.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/devtron-labs/ongo/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cli.NewRootCmd().ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
