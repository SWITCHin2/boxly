//go:build windows

package cli

import (
	"context"

	"github.com/coder/websocket"
)

// watchResize is a no-op on Windows, which has no SIGWINCH. The initial size is
// still sent once by the caller; live resizing simply isn't propagated.
func watchResize(_ context.Context, _ *websocket.Conn, _ int) func() {
	return func() {}
}
