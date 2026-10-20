package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/daemon"
	"github.com/Mergemat/celare/internal/library"
)

// Server serves the daemon on a unix socket.
type Server struct {
	Daemon *daemon.Daemon
	// ConfigDir holds config.toml and the subscription cache.
	ConfigDir string
	// StatePath remembers the committed tunnel across restarts.
	StatePath string
	// System, if set, adds machine details to GET /v1/tunnel.
	System func() *SystemInfo
	// Version is reported in GET /v1/tunnel.
	Version string
	// Diagnose, if set, explains a failed traffic check: it tests the
	// node outside the tunnel to tell a blocked server from a tunnel fault.
	Diagnose func(ctx context.Context, spec *daemon.Spec) string

	mu       sync.Mutex
	lastNode string
}

// savedState is the committed tunnel; Node is kept while disconnected so
// a bare connect knows what to reconnect.
type savedState struct {
	Connected bool   `json:"connected"`
	Node      string `json:"node"`
}

// Resume loads the saved state and, if the tunnel was up, brings the same
// node back up with the current config.toml.
func (s *Server) Resume(ctx context.Context) error {
	s.Daemon.OnSettled = s.save
	b, err := os.ReadFile(s.StatePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var st savedState
	if err := json.Unmarshal(b, &st); err != nil {
		return fmt.Errorf("state file %s is corrupt: %w", s.StatePath, err)
	}
	s.lastNode = st.Node
	if !st.Connected {
		return nil
	}
	spec, apiErr := s.spec(st.Node)
	if apiErr != nil {
		return apiErr
	}
	_, err = s.Daemon.Apply(ctx, daemon.Request{Action: daemon.Connect, Spec: spec})
	return err
}

func (s *Server) save(spec *daemon.Spec) {
	s.mu.Lock()
	if spec != nil {
		s.lastNode = spec.NodeName
	}
	st := savedState{Connected: spec != nil, Node: s.lastNode}
	s.mu.Unlock()
	b, _ := json.Marshal(st)
	os.MkdirAll(filepath.Dir(s.StatePath), 0o755)
	os.WriteFile(s.StatePath, b, 0o600)
}

// Listen serves on path, a socket only uid may open. The returned func
// shuts the server down.
func (s *Server) Listen(path string, uid int) (func(), error) {
	s.Daemon.OnSettled = s.save
	os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		l.Close()
		return nil, err
	}
	if err := os.Chown(path, uid, -1); err != nil {
		l.Close()
		return nil, err
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(l)
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/tunnel", func(w http.ResponseWriter, r *http.Request) {
		reply(w, s.tunnel())
	})
	mux.HandleFunc("POST /v1/changes", s.createChange)
	mux.HandleFunc("GET /v1/changes", func(w http.ResponseWriter, r *http.Request) {
		reply(w, s.Daemon.Changes())
	})
	mux.HandleFunc("GET /v1/changes/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "pending" {
			if p := s.Daemon.Tunnel().Pending; p != nil {
				reply(w, p)
				return
			}
			fail(w, &Error{Code: "nothing_pending", Message: "no change is pending"})
			return
		}
		c, ok := s.Daemon.Change(id)
		if !ok {
			fail(w, &Error{Code: "change_not_found", Message: "no change " + id, Hint: "run `celare changes` for recent ids"})
			return
		}
		reply(w, c)
	})
	mux.HandleFunc("POST /v1/changes/{id}/confirm", s.settle(s.Daemon.Confirm))
	mux.HandleFunc("POST /v1/changes/{id}/revert", s.settle(s.Daemon.Revert))
	mux.HandleFunc("GET /v1/events", func(w http.ResponseWriter, r *http.Request) {
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		events := s.Daemon.Events(after)
		if events == nil {
			events = []daemon.Event{}
		}
		reply(w, events)
	})
	return mux
}

func (s *Server) createChange(w http.ResponseWriter, r *http.Request) {
	var req ChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, &Error{Code: "bad_request", Message: "request body is not valid JSON: " + err.Error()})
		return
	}
	within, err := req.confirmWithin()
	if err != nil {
		fail(w, &Error{Code: "bad_request", Message: fmt.Sprintf("confirm %q is not a duration", req.Confirm), Hint: "use a duration such as 30s or 2m"})
		return
	}
	var spec *daemon.Spec
	switch req.Action {
	case daemon.Connect:
		name := req.Node
		if name == "" {
			s.mu.Lock()
			name = s.lastNode
			s.mu.Unlock()
		}
		if name == "" {
			fail(w, &Error{Code: "no_node", Message: "no node given and none connected before", Hint: "run `celare connect <node>`; `celare nodes list` shows the names"})
			return
		}
		var apiErr *Error
		if spec, apiErr = s.spec(name); apiErr != nil {
			fail(w, apiErr)
			return
		}
	case daemon.Disconnect:
	case daemon.Apply:
		current := s.Daemon.Current()
		if current == nil {
			fail(w, &Error{Code: "tunnel_down", Message: "the tunnel is down, so there is nothing to apply config.toml to", Hint: "run `celare connect`; it reads config.toml as it is"})
			return
		}
		var apiErr *Error
		if spec, apiErr = s.spec(current.NodeName); apiErr != nil {
			fail(w, apiErr)
			return
		}
	default:
		fail(w, &Error{Code: "bad_request", Message: fmt.Sprintf("action %q is not one of connect, disconnect, apply", req.Action)})
		return
	}
	c, err := s.Daemon.Apply(r.Context(), daemon.Request{
		Action: req.Action, Spec: spec, ConfirmWithin: within, Probe: req.Probe, DryRun: req.DryRun,
	})
	if err != nil {
		e := daemonError(err)
		if c.ID != "" {
			e.Change = &c
		}
		if errors.Is(err, daemon.ErrProbeFailed) && s.Diagnose != nil && spec != nil {
			e.Hint = s.Diagnose(r.Context(), spec)
		}
		fail(w, e)
		return
	}
	reply(w, c)
}

func (s *Server) settle(op func(id string) (daemon.Change, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "pending" {
			id = ""
		}
		c, err := op(id)
		if err != nil {
			fail(w, daemonError(err))
			return
		}
		reply(w, c)
	}
}

// spec resolves a node against config.toml as it is right now.
func (s *Server) spec(name string) (*daemon.Spec, *Error) {
	b, err := os.ReadFile(filepath.Join(s.ConfigDir, "config.toml"))
	if err != nil {
		return nil, &Error{Code: "config_unreadable", Message: err.Error(), Hint: "run `celare config init` to create config.toml"}
	}
	cfg, err := config.Parse(b)
	if err != nil {
		return nil, &Error{Code: "config_invalid", Message: err.Error(), Hint: "fix it and run `celare config check`"}
	}
	if problems := cfg.Check(); len(problems) > 0 {
		return nil, &Error{Code: "config_invalid", Message: "config.toml has problems", Problems: problems, Hint: "fix them and run `celare config check`"}
	}
	n, err := library.Library{Dir: s.ConfigDir}.Resolve(cfg, name)
	if err != nil {
		return nil, &Error{Code: "node_not_found", Message: fmt.Sprintf("no node named %q", name), Hint: "run `celare nodes list` for the names"}
	}
	def := cfg.Default
	if def == "" {
		def = config.Proxy
	}
	return &daemon.Spec{NodeName: name, Node: n, Default: def, Rules: cfg.Rules, DNS: cfg.DNS}, nil
}

func (s *Server) tunnel() Tunnel {
	s.mu.Lock()
	last := s.lastNode
	s.mu.Unlock()
	t := Tunnel{TunnelState: s.Daemon.Tunnel(), Version: s.Version, LastNode: last}
	if s.System != nil {
		t.System = s.System()
	}
	return t
}

func daemonError(err error) *Error {
	msg := err.Error()
	switch {
	case errors.Is(err, daemon.ErrPending):
		return &Error{Code: "change_pending", Message: msg, Hint: "confirm or revert the pending change first; `celare status` shows it"}
	case errors.Is(err, daemon.ErrNothingPending):
		return &Error{Code: "nothing_pending", Message: msg}
	case errors.Is(err, daemon.ErrNotPending):
		return &Error{Code: "not_pending", Message: msg, Hint: "run `celare changes` to see how it ended"}
	case errors.Is(err, daemon.ErrNotFound):
		return &Error{Code: "change_not_found", Message: msg}
	case errors.Is(err, daemon.ErrProbeFailed):
		return &Error{Code: "probe_failed", Message: msg, Hint: "the previous tunnel is back; try another node or `celare nodes test`"}
	case errors.Is(err, daemon.ErrStartFailed):
		return &Error{Code: "start_failed", Message: msg, Hint: "run `celare doctor`"}
	default:
		return &Error{Code: "internal", Message: msg}
	}
}

var statusCodes = map[string]int{
	"bad_request":       http.StatusBadRequest,
	"node_not_found":    http.StatusNotFound,
	"change_not_found":  http.StatusNotFound,
	"change_pending":    http.StatusConflict,
	"nothing_pending":   http.StatusConflict,
	"not_pending":       http.StatusConflict,
	"tunnel_down":       http.StatusConflict,
	"probe_failed":      http.StatusBadGateway,
	"start_failed":      http.StatusBadGateway,
	"config_invalid":    http.StatusUnprocessableEntity,
	"config_unreadable": http.StatusUnprocessableEntity,
	"no_node":           http.StatusUnprocessableEntity,
}

func fail(w http.ResponseWriter, e *Error) {
	code, ok := statusCodes[e.Code]
	if !ok {
		code = http.StatusInternalServerError
	}
	e.Message = strings.TrimSpace(e.Message)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]*Error{"error": e})
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
