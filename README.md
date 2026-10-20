# celare

A macOS VPN client for every protocol [3x-ui](https://github.com/MHSanaei/3x-ui) serves, built so an agent can run it without touching the screen.

- **Protocols:** VLESS (REALITY, Vision, XHTTP), VMess, Trojan, Shadowsocks (including 2022), Hysteria2 (salamander, port hopping), WireGuard. Transports: raw, XHTTP, WebSocket, gRPC, HTTPUpgrade, mKCP, Finalmask. Each one is tested end to end against a real Xray server.
- **Engine:** xray-core 26.3.27, linked in. There's no separate core process.
- **System-wide:** TUN mode, so terminal tools go through it too. DNS goes through the tunnel as well.
- **Routing:** per-domain and per-IP rules send traffic to proxy, direct or block.
- **Readable config:** `~/.config/celare/config.toml`. Comments survive when the CLI edits it.
- **Agent-safe changes:** a tunnel change can be provisional. It reverts on its own unless confirmed, or right away if a probe through the new tunnel fails. An agent can switch nodes over the connection it depends on without cutting itself off.

## Install

```sh
scripts/build-app.sh                 # dist/Celare.app, with the CLI inside
ln -s "$PWD/dist/Celare.app/Contents/Resources/celare" /usr/local/bin/celare
sudo celare service install          # once: the root daemon that owns the tunnel
```

Or open `dist/Celare.app`. It offers to install the service and asks for your password once.

## Use

```sh
celare config init
celare subs add main 'https://panel.example.com/sub/...'
celare nodes test                                   # fastest first
celare connect main/Tokyo --confirm 60s --probe https://www.gstatic.com/generate_204
celare confirm <id>                                 # keep it, or it reverts
celare rules add proxy openai.com anthropic.com --name ai --first
celare apply --dry-run && celare apply --confirm 60s
celare rules explain api.openai.com                 # which rule decides, and why
celare doctor
```

Every command takes `--json` and then prints one document: `{"ok", "data", "next"}` on success, `{"ok": false, "error": {"code", "message", "hint"}}` on failure. Exit codes, the API and the reasoning behind them are in [docs/design.md](docs/design.md).

## For agents

```sh
mkdir -p ~/.claude/skills/celare && celare skill > ~/.claude/skills/celare/SKILL.md
```

## Layout

| Path | What it is |
|---|---|
| `internal/node` | share links and subscriptions → nodes |
| `internal/xrayconf` | nodes, rules, DNS → Xray config |
| `internal/engine` | xray-core in process; route explanations from Xray's own router |
| `internal/daemon` | changes with confirm, probe and revert |
| `internal/system`, `internal/netconf` | utun, routes and DNS on macOS, with crash recovery |
| `internal/api` | the daemon's API on `/var/run/celare.sock` |
| `internal/cli` | the `celare` command |
| `app/` | the SwiftUI menu bar app, a client of the CLI |

`go test ./...` runs everything that doesn't need root. `scripts/try-tunnel.sh <node>` checks the real tunnel once the service is installed.
