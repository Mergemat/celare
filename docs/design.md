# celare: CLI and API design

celare is driven the same way by a person, an agent, and the menu bar app.
Everything the app can do, the CLI can do.

## Two kinds of state

| | Where it lives | Changed by |
|---|---|---|
| **Policy**: nodes, subscriptions, rules, DNS | `~/.config/celare/config.toml`, plain text | editing the file, or `celare nodes/subs/rules ...` |
| **Tunnel**: connected or not, through which node | the daemon (`/var/run/celare.sock`) | `celare connect`, `disconnect`, `apply` |

Editing policy never touches the running tunnel. `celare apply` pushes it.
That makes the file safe to edit and every effect on traffic explicit.

## Changes

Every change to the tunnel (connect, disconnect, apply) is a **change**
with an id. A change can be provisional:

- `--confirm 30s`: reverts by itself unless `celare confirm <id>` runs within 30s.
- `--probe <url>`: the url is fetched through the new tunnel; if that fails the change reverts at once.
- `--dry-run`: returns the planned diff and changes nothing.

States: `planned` (dry run), `pending`, `committed`, `reverted`, `failed`.
Only one change can be pending at a time. `confirm` and `revert` take the id,
so an agent never confirms a change it didn't make.

The daemon reverts to the exact previous tunnel, including the policy it
was running then, even if `config.toml` was edited in between.

## CLI

```
celare status                                   tunnel, pending change, warnings
celare connect [node] [--confirm D] [--probe URL] [--dry-run]
celare disconnect     [--confirm D] [--dry-run]
celare apply          [--confirm D] [--probe URL] [--dry-run]   push config.toml to the tunnel
celare confirm [id]                             keep the pending change
celare revert  [id]                             undo the pending change now
celare changes                                  recent changes and how they ended

celare nodes list | show <node> | add <link> [--name N] | remove <node> | test [node...]
celare subs  list | add <name> <url> | update [name...] | remove <name>
celare rules list | add <action> <match>... [--name N] [--first] | remove <name|position> | explain <target> | geo-update
celare config init | check | path | render [node]

celare service install [--uid U] | uninstall | status   (install/uninstall ask for your password themselves)
celare doctor                                     every check, with the fix for each failure
celare skill                                      SKILL.md for agents
```

Node names: `<name>` for nodes in `config.toml`, `<subscription>/<name>` for
subscription nodes. `connect` without a node reconnects the last one.

Rule matches are classified automatically: `domain:x`, `full:x`, `keyword:x`,
`regexp:x`, `geosite:x` are domains; IPs, CIDRs and `geoip:x` are IPs.
`celare rules add proxy domain:openai.com 203.0.113.0/24 --name ai`.

### Output contract

- Human text by default. `--json` (or `CELARE_OUTPUT=json`) prints exactly one JSON document on stdout:
  - success: `{"ok": true, "data": ..., "next": ["celare confirm c1a2b3 (before 12:00:30)"]}`
  - failure: `{"ok": false, "error": {"code": "node_not_found", "message": "...", "hint": "..."}}`
- `next` lists the commands that usually follow. Errors carry a `hint` with the fix.
- Secrets (passwords, UUIDs, private keys) are redacted unless `--reveal`.
- Never interactive. Never prompts.
- Every command that writes has `--dry-run`. File edits show the diff.

Exit codes: `0` ok · `1` error · `2` usage · `3` daemon unreachable · `4` conflict (a change is pending) · `5` reverted (probe failed or not confirmed).

### One VPN at a time

`connect` refuses while another VPN holds the default route. Two VPNs can't
both route all traffic, and a kill switch in the other one silently drops
celare's traffic. `celare doctor` names the interface.

## Daemon API

HTTP over a unix socket only the installing user can open. JSON in and out.
Errors: `{"error": {"code", "message", "hint", "problems"}}`.

```
GET  /v1/tunnel                    state, node, since, pending change, system info
POST /v1/changes                   {"action": "connect"|"disconnect"|"apply", "node", "confirm", "probe", "dryRun"}
GET  /v1/changes                   recent changes, newest first
GET  /v1/changes/{id}
POST /v1/changes/{id}/confirm
POST /v1/changes/{id}/revert
GET  /v1/events?after=<seq>        what happened since seq (reverts, restarts, failures)
```

A change response is the change itself:

```json
{
  "id": "c1a2b3",
  "action": "connect",
  "state": "pending",
  "from": {"node": "main/Tokyo"},
  "to": {"node": "main/Osaka"},
  "diff": {"node": {"from": "main/Tokyo", "to": "main/Osaka"}},
  "deadline": "2026-10-20T12:00:30Z"
}
```
