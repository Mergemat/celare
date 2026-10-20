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
	"strings"
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
	// StatePath remembers the committed node across restarts.
	StatePath string
	// System, if set, adds machine details to status.
	System func() *SystemInfo
}

type savedState struct {
	Node string `json:"node"` // "" when down
}

// Listen serves on path, a socket only uid may use. The returned func
// shuts the server down.
func (s *Server) Listen(path string, uid int) (func(), error) {
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
	s.Daemon.OnSettled = s.save
	srv := &http.Server{Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(l)
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}, nil
}

// Resume brings back the node that was committed before the daemon
// stopped.
func (s *Server) Resume(ctx context.Context) error {
	b, err := os.ReadFile(s.StatePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var st savedState
	if err := json.Unmarshal(b, &st); err != nil || st.Node == "" {
		return err
	}
	spec, apiErr := s.spec(st.Node)
	if apiErr != nil {
		return apiErr
	}
	s.Daemon.OnSettled = s.save
	return s.Daemon.Apply(ctx, daemon.Change{Spec: spec})
}

func (s *Server) save(spec *daemon.Spec) {
	st := savedState{}
	if spec != nil {
		st.Node = spec.NodeName
	}
	b, _ := json.Marshal(st)
	os.MkdirAll(filepath.Dir(s.StatePath), 0o700)
	os.WriteFile(s.StatePath, b, 0o600)
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		reply(w, s.status())
	})
	mux.HandleFunc("POST /v1/up", s.change(func(req ChangeRequest) (*daemon.Spec, *Error) {
		return s.spec(req.Node)
	}))
	mux.HandleFunc("POST /v1/down", s.change(func(ChangeRequest) (*daemon.Spec, *Error) {
		return nil, nil
	}))
	mux.HandleFunc("POST /v1/reload", s.change(func(ChangeRequest) (*daemon.Spec, *Error) {
		current := s.Daemon.Current()
		if current == nil {
			return nil, &Error{Code: "tunnel_down", Message: "the tunnel is down, so there is nothing to reload", Hint: "run `celare up`"}
		}
		return s.spec(current.NodeName)
	}))
	mux.HandleFunc("POST /v1/confirm", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Daemon.Confirm(); err != nil {
			fail(w, daemonError(err))
			return
		}
		reply(w, s.status())
	})
	mux.HandleFunc("POST /v1/rollback", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Daemon.Rollback(); err != nil {
			fail(w, daemonError(err))
			return
		}
		reply(w, s.status())
	})
	return mux
}

func (s *Server) change(target func(ChangeRequest) (*daemon.Spec, *Error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ChangeRequest
		if r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				fail(w, &Error{Code: "bad_request", Message: "request body is not valid JSON: " + err.Error()})
				return
			}
		}
		within, err := req.confirmWithin()
		if err != nil {
			fail(w, &Error{Code: "bad_request", Message: fmt.Sprintf("confirm %q is not a duration", req.Confirm), Hint: "use a Go duration such as 30s or 2m"})
			return
		}
		spec, apiErr := target(req)
		if apiErr != nil {
			fail(w, apiErr)
			return
		}
		if err := s.Daemon.Apply(r.Context(), daemon.Change{Spec: spec, ConfirmWithin: within, Probe: req.Probe}); err != nil {
			fail(w, daemonError(err))
			return
		}
		reply(w, s.status())
	}
}

// spec resolves a node (empty: the active one) against config.toml as it
// is right now.
func (s *Server) spec(name string) (*daemon.Spec, *Error) {
	b, err := os.ReadFile(filepath.Join(s.ConfigDir, "config.toml"))
	if err != nil {
		return nil, &Error{Code: "config_unreadable", Message: err.Error(), Hint: "run `celare init` to create config.toml"}
	}
	cfg, err := config.Parse(b)
	if err != nil {
		return nil, &Error{Code: "config_invalid", Message: err.Error()}
	}
	lib := library.Library{Dir: s.ConfigDir}
	var problems []config.Problem
	for _, p := range cfg.Check(lib.SubscriptionNodeNames(cfg)) {
		if name != "" && p.Field == "active" {
			continue // an explicit node makes a stale active irrelevant
		}
		problems = append(problems, p)
	}
	if len(problems) > 0 {
		return nil, &Error{Code: "config_invalid", Message: "config.toml has problems", Problems: problems, Hint: "fix them and run `celare check`"}
	}
	if name == "" {
		name = cfg.Active
	}
	if name == "" {
		return nil, &Error{Code: "no_active_node", Message: "no node given and config.toml sets no active node", Hint: "run `celare up <node>` or `celare use <node>`; `celare node list` shows the names"}
	}
	n, err := lib.Resolve(cfg, name)
	if err != nil {
		return nil, &Error{Code: "node_not_found", Message: fmt.Sprintf("no node named %q", name), Hint: "run `celare node list` for the names"}
	}
	def := cfg.Default
	if def == "" {
		def = config.Proxy
	}
	return &daemon.Spec{NodeName: name, Node: n, Default: def, Rules: cfg.Rules, DNS: cfg.DNS}, nil
}

func (s *Server) status() Status {
	st := Status{Status: s.Daemon.Status()}
	if s.System != nil {
		st.System = s.System()
	}
	return st
}

func daemonError(err error) *Error {
	msg := err.Error()
	switch {
	case errors.Is(err, daemon.ErrPending):
		return &Error{Code: "change_pending", Message: msg}
	case errors.Is(err, daemon.ErrNothingPending):
		return &Error{Code: "nothing_pending", Message: msg}
	case errors.Is(err, daemon.ErrProbeFailed):
		return &Error{Code: "probe_failed", Message: msg}
	case errors.Is(err, daemon.ErrStartFailed):
		return &Error{Code: "start_failed", Message: msg, Hint: "run `celare doctor`"}
	default:
		return &Error{Code: "internal", Message: msg}
	}
}

var statusCodes = map[string]int{
	"bad_request":       http.StatusBadRequest,
	"node_not_found":    http.StatusNotFound,
	"change_pending":    http.StatusConflict,
	"nothing_pending":   http.StatusConflict,
	"tunnel_down":       http.StatusConflict,
	"probe_failed":      http.StatusBadGateway,
	"config_invalid":    http.StatusUnprocessableEntity,
	"config_unreadable": http.StatusUnprocessableEntity,
	"no_active_node":    http.StatusUnprocessableEntity,
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
