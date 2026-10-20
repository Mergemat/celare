// Package api is the daemon's HTTP API over a unix socket, and the client
// the CLI and the app use to talk to it. See docs/design.md.
package api

import (
	"time"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/daemon"
)

// DefaultSocket is where the installed daemon listens.
const DefaultSocket = "/var/run/celare.sock"

// ChangeRequest is the body of POST /v1/changes.
type ChangeRequest struct {
	Action daemon.Action `json:"action"`
	// Node for connect; empty reconnects the last node.
	Node string `json:"node,omitempty"`
	// Confirm is a duration ("30s"): the change reverts unless confirmed
	// within it.
	Confirm string `json:"confirm,omitempty"`
	// Probe is a URL fetched through the new tunnel; failure reverts it.
	Probe  string `json:"probe,omitempty"`
	DryRun bool   `json:"dryRun,omitempty"`
}

func (r ChangeRequest) confirmWithin() (time.Duration, error) {
	if r.Confirm == "" {
		return 0, nil
	}
	return time.ParseDuration(r.Confirm)
}

// Tunnel is GET /v1/tunnel: the daemon's state plus the system's.
type Tunnel struct {
	daemon.TunnelState
	// Version is the daemon's celare version; after an upgrade it can lag
	// behind the CLI until the service is reinstalled.
	Version string      `json:"version,omitempty"`
	System  *SystemInfo `json:"system,omitempty"`
}

// SystemInfo is the machine-level side of the tunnel.
type SystemInfo struct {
	// Interface is the physical interface the tunnel is bound to.
	Interface string `json:"interface,omitempty"`
	Tun       string `json:"tun,omitempty"`
	// ForeignTunnels are other VPNs holding a default route; they
	// compete with celare for traffic.
	ForeignTunnels []string `json:"foreignTunnels,omitempty"`
}

// Error is every failure the API returns. Code is stable; branch on it.
type Error struct {
	Code     string           `json:"code"`
	Message  string           `json:"message"`
	Hint     string           `json:"hint,omitempty"`
	Problems []config.Problem `json:"problems,omitempty"`
	// Change is set when the failure is a change that failed or reverted.
	Change *daemon.Change `json:"change,omitempty"`
}

func (e *Error) Error() string {
	msg := e.Message
	for _, p := range e.Problems {
		msg += "\n  " + p.String()
	}
	if e.Hint != "" {
		msg += "\nhint: " + e.Hint
	}
	return msg
}
