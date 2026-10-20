package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Client talks to the daemon.
type Client struct {
	socket string
	http   *http.Client
}

func NewClient(socket string) *Client {
	return &Client{
		socket: socket,
		http: &http.Client{
			// Probes run inside a request, so allow more than they take.
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socket)
				},
			},
		},
	}
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	return c.call(ctx, http.MethodGet, "/v1/status", nil)
}

func (c *Client) Up(ctx context.Context, r ChangeRequest) (Status, error) {
	return c.call(ctx, http.MethodPost, "/v1/up", r)
}

func (c *Client) Down(ctx context.Context, r ChangeRequest) (Status, error) {
	return c.call(ctx, http.MethodPost, "/v1/down", r)
}

func (c *Client) Reload(ctx context.Context, r ChangeRequest) (Status, error) {
	return c.call(ctx, http.MethodPost, "/v1/reload", r)
}

func (c *Client) Confirm(ctx context.Context) (Status, error) {
	return c.call(ctx, http.MethodPost, "/v1/confirm", nil)
}

func (c *Client) Rollback(ctx context.Context) (Status, error) {
	return c.call(ctx, http.MethodPost, "/v1/rollback", nil)
}

func (c *Client) call(ctx context.Context, method, path string, body any) (Status, error) {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://celare"+path, &buf)
	if err != nil {
		return Status{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Status{}, &Error{
			Code:    "daemon_unreachable",
			Message: fmt.Sprintf("cannot reach the celare daemon at %s: %v", c.socket, err),
			Hint:    "run `sudo celare install` once to install it, or `celare doctor` to see what is wrong",
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct{ Error *Error }
		if err := json.NewDecoder(resp.Body).Decode(&e); err != nil || e.Error == nil {
			return Status{}, &Error{Code: "internal", Message: "daemon answered " + resp.Status}
		}
		return Status{}, e.Error
	}
	var st Status
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return Status{}, fmt.Errorf("daemon sent an unreadable status: %w", err)
	}
	return st, nil
}
