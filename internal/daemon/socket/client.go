package socket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/nats-io/nuid"
)

// ErrNoDaemon means nothing answered on the socket.
var ErrNoDaemon = errors.New("no daemon is listening; start one with \"skylands daemon up\", or point --socket at its socket")

// Client talks to the daemon on this host over its Unix socket.
type Client struct {
	path string
	http *http.Client
}

// Dial prepares a client for the socket at path. Nothing connects until the
// first call.
func Dial(path string) *Client {
	return &Client{
		path: path,
		http: &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", path)
			},
		}},
	}
}

// Status reports the daemon's host, world, version, and repositories.
func (c *Client) Status(ctx context.Context) (StatusInfo, error) {
	var out StatusInfo
	err := c.call(ctx, http.MethodGet, "/v1/status", nil, &out)
	return out, err
}

// RegisterRepo registers the repository containing path as a terrace.
func (c *Client) RegisterRepo(ctx context.Context, path string) (RepoInfo, error) {
	var out RepoInfo
	err := c.call(ctx, http.MethodPost, "/v1/repos", RegisterRepoRequest{Path: path, CommandID: nuid.Next()}, &out)
	return out, err
}

func (c *Client) call(ctx context.Context, method, route string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://daemon"+route, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		var opErr *net.OpError
		if errors.As(err, &opErr) {
			return fmt.Errorf("%w (%s)", ErrNoDaemon, c.path)
		}
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		var e ErrorResponse
		if json.NewDecoder(resp.Body).Decode(&e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("daemon replied %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
