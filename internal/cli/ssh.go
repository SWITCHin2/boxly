package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/coder/websocket"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newSSHCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ssh <id>",
		Short: "Open an interactive shell on a VM",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShell(cmd.Context(), args[0])
		},
	}
}

func newExecCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exec <id> -- <command>...",
		Short: "Run a one-shot command on a VM",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := newClient().ExecConn(cmd.Context(), args[0], args[1:], false)
			if err != nil {
				return err
			}
			defer conn.Close(websocket.StatusNormalClosure, "")
			return pump(cmd.Context(), conn, os.Stdin, os.Stdout)
		},
	}
}

// runShell sets the local terminal to raw mode, forwards resize events, and
// bridges stdin/stdout to the VM's shell so it feels like SSH.
// robustShell prefers bash but falls back to sh, so it works on both Ubuntu and
// Alpine-based boxes. We must test for bash with `command -v` first: a bare
// `exec bash` would terminate the shell with code 127 when bash is absent
// (Alpine), instead of falling through to sh.
var robustShell = []string{"/bin/sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi"}

func runShell(ctx context.Context, id string) error {
	conn, err := newClient().ExecConn(ctx, id, robustShell, true)
	if err != nil {
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		oldState, err := term.MakeRaw(fd)
		if err == nil {
			defer term.Restore(fd, oldState)
		}
		sendResize(ctx, conn, fd)

		winch := make(chan os.Signal, 1)
		signal.Notify(winch, syscall.SIGWINCH)
		defer signal.Stop(winch)
		go func() {
			for range winch {
				sendResize(ctx, conn, fd)
			}
		}()
	}

	err = pump(ctx, conn, os.Stdin, os.Stdout)
	if err != nil && !isClosed(err) {
		return err
	}
	fmt.Fprintln(os.Stderr, "\r\nconnection closed")
	return nil
}

func sendResize(ctx context.Context, conn *websocket.Conn, fd int) {
	w, h, err := term.GetSize(fd)
	if err != nil {
		return
	}
	msg, _ := json.Marshal(map[string]any{"resize": map[string]uint16{"cols": uint16(w), "rows": uint16(h)}})
	_ = conn.Write(ctx, websocket.MessageText, msg)
}

// pump bridges a local stdin/stdout pair to the exec websocket: binary frames
// carry terminal bytes in both directions. The session ends only when the
// server closes the stream (the remote command exits); local stdin EOF merely
// half-closes stdin so remaining output still drains.
func pump(ctx context.Context, conn *websocket.Conn, stdin io.Reader, stdout io.Writer) error {
	errc := make(chan error, 2)

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdin.Read(buf)
			if n > 0 {
				if werr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					errc <- werr
					return
				}
			}
			if err != nil {
				// Tell the server stdin is done, then keep the connection open
				// so the stdout reader can finish.
				_ = conn.Write(ctx, websocket.MessageText, []byte(`{"stdinEof":true}`))
				return
			}
		}
	}()

	go func() {
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				errc <- err
				return
			}
			if typ == websocket.MessageBinary {
				if _, werr := stdout.Write(data); werr != nil {
					errc <- werr
					return
				}
			}
		}
	}()

	// A normal server close (the remote command exited) is success, not error.
	if err := <-errc; err != nil && !isClosed(err) {
		return err
	}
	return nil
}

func isClosed(err error) bool {
	status := websocket.CloseStatus(err)
	return status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway
}
