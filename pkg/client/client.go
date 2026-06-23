// Package client is the Go HTTP/websocket client the boxly CLI uses to talk to
// the boxlyd control plane.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/SWITCHin2/boxly/pkg/api"
)

// Client calls the boxlyd API with a static bearer token.
type Client struct {
	base  string
	token string
	http  *http.Client
}

func New(base, token string) *Client {
	return &Client{
		base:  strings.TrimRight(base, "/"),
		token: token,
		// Generous: a templated create blocks server-side until the box is
		// running and its setup script has finished (cold image pulls included).
		http: &http.Client{Timeout: 180 * time.Second},
	}
}

func (c *Client) Create(ctx context.Context, req api.CreateRequest) (*api.VM, error) {
	var out api.VM
	if err := c.do(ctx, http.MethodPost, "/v1/vms", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Templates(ctx context.Context) ([]api.TemplateInfo, error) {
	var out []api.TemplateInfo
	if err := c.do(ctx, http.MethodGet, "/v1/templates", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) Me(ctx context.Context) (*api.Identity, error) {
	var out api.Identity
	if err := c.do(ctx, http.MethodGet, "/v1/me", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) List(ctx context.Context) ([]api.VM, error) {
	var out []api.VM
	if err := c.do(ctx, http.MethodGet, "/v1/vms", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) Get(ctx context.Context, id string) (*api.VM, error) {
	var out api.VM
	if err := c.do(ctx, http.MethodGet, "/v1/vms/"+id, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Delete(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/vms/"+id, nil, nil)
}

// ExecConn dials the exec websocket for a VM. The caller drives the stream
// (binary frames = stdin/stdout, text frames = resize control JSON).
func (c *Client) ExecConn(ctx context.Context, id string, cmd []string, tty bool) (*websocket.Conn, error) {
	u, err := url.Parse(c.base + "/v1/vms/" + id + "/exec")
	if err != nil {
		return nil, err
	}
	u.Scheme = strings.Replace(u.Scheme, "http", "ws", 1)
	q := u.Query()
	if tty {
		q.Set("tty", "true")
	}
	for _, part := range cmd {
		q.Add("cmd", part)
	}
	u.RawQuery = q.Encode()

	conn, _, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{
		HTTPHeader:   http.Header{"Authorization": {"Bearer " + c.token}},
		Subprotocols: []string{"boxly.exec.v1"},
	})
	if err != nil {
		return nil, fmt.Errorf("dial exec: %w", err)
	}
	conn.SetReadLimit(10 << 20)
	return conn, nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var e api.ErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return fmt.Errorf("boxlyd: %s", e.Error)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
