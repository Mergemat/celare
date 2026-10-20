// Package api is the daemon's HTTP API over a unix socket, and the client
// the CLI and the app use to talk to it.
package api

import (
	"time"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/daemon"
)

// DefaultSocket is where the installed daemon listens.
const DefaultSocket = "/var/run/celare.sock"

// ChangeRequest is the body of up, down and reload.
type ChangeRequest struct {
	// Node to bring up; empty means config.toml's active node.
	Node string `json:"node,omitempty"`
	// Confirm is a duration ("30s"): the change reverts unless confirmed
	// within it.
	Confirm string `json:"confirm,omitempty"`
	// Probe is a URL fetched through the new state; failure reverts it.
	Probe string `json:"probe,omitempty"`
}

func (r ChangeRequest) confirmWithin() (time.Duration, error) {
	if r.Confirm == "" {
		return 0, nil
	}
	return time.ParseDuration(r.Confirm)
}

// Status is the daemon's state plus what it knows about the system.
type Status struct {
	daemon.Status
	System *SystemInfo `json:"system,omitempty"`
}

// SystemInfo describes the machine-level side of the tunnel.
type SystemInfo struct {
	// Interface is the physical interface the tunnel is bound to.
	Interface string `json:"interface,omitempty"`
	Tun       string `json:"tun,omitempty"`
	// ForeignTunnels are other VPNs holding a default route. They compete
	// with celare for traffic.
	ForeignTunnels []string `json:"foreignTunnels,omitempty"`
}

// Error is every failure the API returns. Code is stable; branch on it.
type Error struct {
	Code     string           `json:"code"`
	Message  string           `json:"message"`
	Hint     string           `json:"hint,omitempty"`
	Problems []config.Problem `json:"problems,omitempty"`
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
