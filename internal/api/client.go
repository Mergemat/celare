package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/Mergemat/celare/internal/daemon"
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
			// A change with a probe holds the request while it probes.
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

func (c *Client) Tunnel(ctx context.Context) (Tunnel, error) {
	var t Tunnel
	return t, c.call(ctx, http.MethodGet, "/v1/tunnel", nil, &t)
}

func (c *Client) CreateChange(ctx context.Context, r ChangeRequest) (daemon.Change, error) {
	var ch daemon.Change
	return ch, c.call(ctx, http.MethodPost, "/v1/changes", r, &ch)
}

func (c *Client) Changes(ctx context.Context) ([]daemon.Change, error) {
	var cs []daemon.Change
	return cs, c.call(ctx, http.MethodGet, "/v1/changes", nil, &cs)
}

// Change fetches one change; id "pending" is the pending one.
func (c *Client) Change(ctx context.Context, id string) (daemon.Change, error) {
	var ch daemon.Change
	return ch, c.call(ctx, http.MethodGet, "/v1/changes/"+orPending(id), nil, &ch)
}

// Confirm keeps a pending change; id "" means whichever is pending.
func (c *Client) Confirm(ctx context.Context, id string) (daemon.Change, error) {
	var ch daemon.Change
	return ch, c.call(ctx, http.MethodPost, "/v1/changes/"+orPending(id)+"/confirm", nil, &ch)
}

// Revert undoes a pending change; id "" means whichever is pending.
func (c *Client) Revert(ctx context.Context, id string) (daemon.Change, error) {
	var ch daemon.Change
	return ch, c.call(ctx, http.MethodPost, "/v1/changes/"+orPending(id)+"/revert", nil, &ch)
}

func (c *Client) Events(ctx context.Context, after int64) ([]daemon.Event, error) {
	var es []daemon.Event
	return es, c.call(ctx, http.MethodGet, "/v1/events?after="+strconv.FormatInt(after, 10), nil, &es)
}

func orPending(id string) string {
	if id == "" {
		return "pending"
	}
	return id
}

func (c *Client) call(ctx context.Context, method, path string, body, out any) error {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://celare"+path, &buf)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return &Error{
			Code:    "daemon_unreachable",
			Message: fmt.Sprintf("cannot reach the celare daemon at %s: %v", c.socket, err),
			Hint:    "run `sudo celare service install` once, or `celare doctor` to see what is wrong",
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct{ Error *Error }
		if err := json.NewDecoder(resp.Body).Decode(&e); err != nil || e.Error == nil {
			return &Error{Code: "internal", Message: "daemon answered " + resp.Status}
		}
		return e.Error
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("daemon sent an unreadable response: %w", err)
	}
	return nil
}
