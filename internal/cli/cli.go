// Package cli is the celare command line. Every command prints human text
// by default and exactly one JSON document with --json; see
// docs/design.md for the contract.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Mergemat/celare/internal/api"
	"github.com/Mergemat/celare/internal/engine"
	"github.com/Mergemat/celare/internal/library"
	"github.com/spf13/cobra"
)

// Env is everything a command reads from the outside world.
type Env struct {
	Stdout, Stderr io.Writer
	// ConfigDir holds config.toml (~/.config/celare).
	ConfigDir string
	// Socket is the daemon's unix socket.
	Socket string
	// JSON forces --json (CELARE_OUTPUT=json).
	JSON bool
}

// Exit codes.
const (
	exitOK          = 0
	exitError       = 1
	exitUsage       = 2
	exitUnreachable = 3
	exitConflict    = 4
	exitReverted    = 5
)

type app struct {
	env  Env
	json bool
	// warnings go out with the next ok result.
	warnings []string
}

// Run executes one command line and returns the exit code.
func Run(ctx context.Context, args []string, env Env) int {
	a := &app{env: env, json: env.JSON}
	if len(args) < 2 || args[0] != "service" || args[1] != "run" {
		engine.QuietLogs()
	}
	root := a.root()
	root.SetArgs(args)
	root.SetOut(env.Stdout)
	root.SetErr(env.Stderr)
	if err := root.ExecuteContext(ctx); err != nil {
		var code exitCode
		if errors.As(err, &code) {
			return int(code)
		}
		if strings.HasPrefix(err.Error(), "unknown command") || strings.HasPrefix(err.Error(), "unknown flag") ||
			strings.HasPrefix(err.Error(), "unknown shorthand") {
			err = usageError{err}
		}
		return a.fail(err)
	}
	return exitOK
}

func (a *app) root() *cobra.Command {
	root := &cobra.Command{
		Use:   "celare",
		Short: "A VPN client for VLESS, VMess, Trojan, Shadowsocks, Hysteria2 and WireGuard, built for agents",
		Long: `celare routes your Mac's traffic through VLESS, VMess, Trojan, Shadowsocks,
Hysteria2 or WireGuard nodes, with per-domain and per-IP rules.

Policy (nodes, subscriptions, rules, DNS) lives in config.toml; edit it or use
the nodes/subs/rules commands. The tunnel is changed with connect, disconnect
and apply. Any tunnel change can be provisional: --confirm 30s reverts it
unless 'celare confirm <id>' runs in time; --probe <url> reverts it at once
if the url is unreachable through the new tunnel.

Add --json to any command for one JSON document on stdout:
  {"ok": true, "data": ..., "next": [...]}  or  {"ok": false, "error": {...}}

Exit codes: 0 ok, 1 error, 2 usage, 3 daemon unreachable, 4 conflict (a change
is pending), 5 reverted (probe failed).`,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.PersistentFlags().BoolVar(&a.json, "json", a.json, "print one JSON document on stdout")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	root.AddGroup(
		&cobra.Group{ID: "tunnel", Title: "Tunnel:"},
		&cobra.Group{ID: "policy", Title: "Policy (config.toml):"},
		&cobra.Group{ID: "system", Title: "System:"},
	)
	for _, c := range []*cobra.Command{a.statusCmd(), a.connectCmd(), a.disconnectCmd(), a.applyCmd(), a.confirmCmd(), a.revertCmd(), a.changesCmd()} {
		c.GroupID = "tunnel"
		root.AddCommand(c)
	}
	for _, c := range []*cobra.Command{a.nodesCmd(), a.subsCmd(), a.rulesCmd(), a.configCmd()} {
		c.GroupID = "policy"
		root.AddCommand(c)
	}
	for _, c := range []*cobra.Command{a.serviceCmd(), a.doctorCmd(), a.skillCmd(), a.versionCmd()} {
		c.GroupID = "system"
		root.AddCommand(c)
	}
	root.SetHelpCommandGroupID("system")
	root.SetCompletionCommandGroupID("system")
	return root
}

func (a *app) client() *api.Client { return api.NewClient(a.env.Socket) }

func (a *app) library() library.Library { return library.Library{Dir: a.env.ConfigDir} }

// usageError is a malformed command line.
type usageError struct{ err error }

func (u usageError) Error() string { return u.err.Error() }

// ok prints a successful result. human writes the text form; next lists
// the commands that usually follow.
func (a *app) ok(data any, human func(w io.Writer), next ...string) error {
	if a.json {
		if next == nil {
			next = []string{}
		}
		doc := map[string]any{"ok": true, "data": data, "next": next}
		if len(a.warnings) > 0 {
			doc["warnings"] = a.warnings
		}
		return encodeJSON(a.env.Stdout, doc)
	}
	human(a.env.Stdout)
	for _, w := range a.warnings {
		fmt.Fprintln(a.env.Stdout, "warning: "+w)
	}
	if len(next) > 0 {
		fmt.Fprintln(a.env.Stdout, "\nnext:")
		for _, n := range next {
			fmt.Fprintln(a.env.Stdout, "  "+n)
		}
	}
	return nil
}

func (a *app) fail(err error) int {
	var apiErr *api.Error
	var usage usageError
	switch {
	case errors.As(err, &apiErr):
	case errors.As(err, &usage):
		apiErr = &api.Error{Code: "usage", Message: usage.Error(), Hint: "run `celare help` or add --help to the command"}
	default:
		apiErr = &api.Error{Code: "error", Message: err.Error()}
	}
	if a.json {
		encodeJSON(a.env.Stdout, map[string]any{"ok": false, "error": apiErr})
	} else {
		fmt.Fprintln(a.env.Stderr, "error: "+apiErr.Error())
	}
	switch apiErr.Code {
	case "usage":
		return exitUsage
	case "daemon_unreachable":
		return exitUnreachable
	case "change_pending", "not_pending", "nothing_pending":
		return exitConflict
	case "probe_failed":
		return exitReverted
	default:
		return exitError
	}
}

type apiError = api.Error

// failure builds a coded error for the envelope.
func failure(code, message, hint string) error {
	return &api.Error{Code: code, Message: message, Hint: hint}
}

func exactArgs(n int, names string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return usageError{fmt.Errorf("%s takes %s, got %d arguments", cmd.CommandPath(), names, len(args))}
		}
		return nil
	}
}

func maxArgs(n int, names string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > n {
			return usageError{fmt.Errorf("%s takes at most %s, got %d arguments", cmd.CommandPath(), names, len(args))}
		}
		return nil
	}
}

func minArgs(n int, names string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < n {
			return usageError{fmt.Errorf("%s takes %s", cmd.CommandPath(), names)}
		}
		return nil
	}
}

// encodeJSON writes indented JSON without HTML escaping, so "<redacted>"
// and "a & b" stay readable.
func encodeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
