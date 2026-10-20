# celare

A macOS VPN client for every protocol [3x-ui](https://github.com/MHSanaei/3x-ui) serves, built so an agent can run it without touching the screen.

- **Protocols:** VLESS (REALITY, Vision, XHTTP), VMess, Trojan, Shadowsocks (including 2022), Hysteria2 (salamander, port hopping), WireGuard. Transports: raw, XHTTP, WebSocket, gRPC, HTTPUpgrade, mKCP, Finalmask. Each one is tested end to end against a real Xray server.
- **Engine:** xray-core 26.3.27, linked in. There's no separate core process.
- **System-wide:** TUN mode, so terminal tools go through it too. DNS goes through the tunnel as well.
- **Routing:** per-domain and per-IP rules send traffic to proxy, direct or block.
- **Readable config:** `~/.config/celare/config.toml`. Comments survive when the CLI edits it.
- **Agent-safe changes:** a tunnel change can be provisional. It reverts on its own unless confirmed, or right away if a probe through the new tunnel fails. An agent can switch nodes over the connection it depends on without cutting itself off.

## Install

**Homebrew**, for the app and the CLI together:

```sh
brew install mergemat/tap/celare
sudo celare service install        # once, and again after each upgrade
open "$(brew --prefix)/opt/celare/Celare.app"
```

**Download**: get `Celare-<version>.zip` from [Releases](https://github.com/Mergemat/celare/releases) and move `Celare.app` to Applications. The app isn't notarized, so the first launch is blocked: open System Settings → Privacy & Security and click **Open Anyway**. The app then offers to install the background service, and doing so also puts `celare` on your PATH.

**CLI only**: `celare-<version>-darwin.tar.gz` from Releases, or `go install github.com/Mergemat/celare/cmd/celare@latest`. Then `sudo celare service install`.

The background service runs as root because it creates the tunnel interface and changes routes and DNS. After it is installed, nothing else needs sudo.

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

## Development

```sh
make test       # Go tests (none need root) and the app build
make run        # build dist/Celare.app and open it
make release    # the release zip, CLI tarball and checksums, as CI builds them
```

Pushing a `v*` tag runs `.github/workflows/release.yml`: it publishes the release and, if the `HOMEBREW_TAP_TOKEN` secret is set, updates the formula in `Mergemat/homebrew-tap`.

With the tunnel up, `celare doctor` checks that it carries traffic and shows the exit IP.

## License

MIT. celare links [xray-core](https://github.com/XTLS/Xray-core), which is MPL-2.0.
