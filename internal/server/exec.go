package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sync"

	"github.com/coder/websocket"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/devtron-labs/ongo/internal/auth"
)

// Wire protocol between CLI and ongod over the exec websocket:
//   - Binary messages carry raw stdin (client->server) / stdout (server->client).
//   - Text messages carry control JSON, currently only terminal resize.
type controlMsg struct {
	Resize *struct {
		Cols uint16 `json:"cols"`
		Rows uint16 `json:"rows"`
	} `json:"resize,omitempty"`
	// StdinEOF half-closes stdin so the command sees end-of-input while the
	// connection stays open to drain remaining stdout.
	StdinEOF bool `json:"stdinEof,omitempty"`
}

func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cmd := r.URL.Query()["cmd"]
	if len(cmd) == 0 {
		cmd = []string{"/bin/bash"}
	}
	tty := r.URL.Query().Get("tty") == "true"

	podName, err := s.mgr.PodNameForExec(r.Context(), id, auth.Owner(r.Context()))
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: []string{"ongo.exec.v1"},
	})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusInternalError, "closing")

	// Detach from the request context so the stream is not cancelled when the
	// HTTP handler returns; tie it to the websocket lifetime instead.
	ctx := context.Background()

	stdinR, stdinW := io.Pipe()
	sizeCh := make(chan remotecommand.TerminalSize, 4)
	out := &wsWriter{ctx: ctx, c: c}

	// Pump inbound websocket frames into stdin / resize.
	go func() {
		defer stdinW.Close()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			switch typ {
			case websocket.MessageBinary:
				if _, err := stdinW.Write(data); err != nil {
					return
				}
			case websocket.MessageText:
				var m controlMsg
				if json.Unmarshal(data, &m) != nil {
					continue
				}
				if m.Resize != nil {
					select {
					case sizeCh <- remotecommand.TerminalSize{Width: m.Resize.Cols, Height: m.Resize.Rows}:
					default:
					}
				}
				if m.StdinEOF {
					stdinW.Close() // command sees EOF; stdout keeps streaming
				}
			}
		}
	}()

	req := s.k8s.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").Name(podName).Namespace(s.mgr.Namespace()).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "vm",
			Command:   cmd,
			Stdin:     true,
			Stdout:    true,
			Stderr:    !tty, // TTY merges stderr into stdout
			TTY:       tty,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(s.k8s.RestConfig, "POST", req.URL())
	if err != nil {
		log.Printf("exec: build executor: %v", err)
		c.Close(websocket.StatusInternalError, "executor")
		return
	}

	opts := remotecommand.StreamOptions{
		Stdin:  stdinR,
		Stdout: out,
		Tty:    tty,
	}
	if !tty {
		opts.Stderr = out
	} else {
		opts.TerminalSizeQueue = sizeQueue(sizeCh)
	}

	if err := exec.StreamWithContext(ctx, opts); err != nil {
		log.Printf("exec: stream for vm %s: %v", id, err)
		c.Close(websocket.StatusInternalError, "stream ended")
		return
	}
	c.Close(websocket.StatusNormalClosure, "done")
}

// wsWriter adapts a websocket connection to an io.Writer for stdout/stderr.
// Writes are serialised because remotecommand may write from goroutines.
type wsWriter struct {
	ctx context.Context
	c   *websocket.Conn
	mu  sync.Mutex
}

func (w *wsWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.c.Write(w.ctx, websocket.MessageBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// sizeQueue implements remotecommand.TerminalSizeQueue over a channel.
type sizeQueue <-chan remotecommand.TerminalSize

func (q sizeQueue) Next() *remotecommand.TerminalSize {
	s, ok := <-q
	if !ok {
		return nil
	}
	return &s
}
