package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/coder/websocket"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/SWITCHin2/boxly/internal/template"
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

// shellBody is the bash-or-sh fallthrough appended after any per-box setup.
const shellBody = "if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi"

// runShell opens an interactive shell on a box. For AI sandbox boxes it loads
// the user's Claude key into the session (resolving it from env, or prompting)
// and prints the themed welcome banner.
func runShell(ctx context.Context, id string) error {
	return connectShell(ctx, id, "")
}

// connectShell is runShell with an optionally pre-supplied Claude token (the
// launcher passes the one it collected so the user isn't asked twice).
func connectShell(ctx context.Context, id, claudeToken string) error {
	shellCmd := robustShell

	// AI boxes get the Claude key + the themed welcome.
	if vm, err := newClient().Get(ctx, id); err == nil && vm.Template == template.AITemplate {
		if claudeToken == "" {
			claudeToken = resolveClaudeToken()
		}
		shellCmd = aiShell(claudeToken)
		fmt.Fprint(os.Stderr, renderAIWelcome(id))
	}

	conn, err := newClient().ExecConn(ctx, id, shellCmd, true)
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
		stop := watchResize(ctx, conn, fd)
		defer stop()
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

// aiShell builds the entry command for an AI sandbox box: it puts the
// pre-installed claude binary on PATH and, when a key is supplied, exports it
// into the session so `claude` is authenticated immediately. The key lives only
// in this shell process — it is never written to disk or sent to the server.
func aiShell(token string) []string {
	pre := "export PATH=/work/.npm-global/bin:$PATH; "
	if token != "" {
		pre += "export ANTHROPIC_API_KEY=" + shellQuote(token) + "; "
	}
	return []string{"/bin/sh", "-c", pre + shellBody}
}

// resolveClaudeToken finds the user's Claude key without storing it: first from
// the environment, otherwise by prompting once (input hidden) when on a TTY.
func resolveClaudeToken() string {
	for _, k := range []string{"ANTHROPIC_API_KEY", "BOXLY_CLAUDE_TOKEN", "CLAUDE_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return ""
	}
	fmt.Fprint(os.Stderr, aiSky.Render("  Paste your Claude API key (hidden, leave blank to skip): "))
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// shellQuote wraps s in single quotes for safe use in `sh -c`, escaping any
// embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
