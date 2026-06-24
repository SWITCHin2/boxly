//go:build !windows

package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/coder/websocket"
)

// watchResize forwards terminal size changes to the remote shell by listening
// for SIGWINCH. It returns a stop func to unsubscribe.
func watchResize(ctx context.Context, conn *websocket.Conn, fd int) func() {
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	go func() {
		for range winch {
			sendResize(ctx, conn, fd)
		}
	}()
	return func() { signal.Stop(winch) }
}
