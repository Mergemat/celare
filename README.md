# celare

A macOS VPN client for VLESS, VMess, Trojan, Shadowsocks, Hysteria2 and WireGuard that your agents can drive. [celare.omarov.sh](https://celare.omarov.sh)

## Install

```sh
brew install --cask mergemat/tap/celare
```

It asks for your password once, to install the background service that runs the tunnel. Or download `Celare.app` from [Releases](https://github.com/Mergemat/celare/releases).

## Use

Open Celare from the menu bar and paste a subscription URL or a share link. Or use the CLI:

```sh
celare subs add main 'https://panel.example.com/sub/...'
celare nodes test
celare connect main/Tokyo
celare rules add proxy openai.com --first && celare apply
celare doctor
```

Rules live in `~/.config/celare/config.toml`, which you can edit by hand.

## For agents

Any change can be provisional: `celare connect <node> --confirm 60s` reverts by itself unless `celare confirm <id>` runs in time, so an agent can switch servers without cutting off its own connection. Every command takes `--json`. Give your agent the skill:

```sh
mkdir -p ~/.claude/skills/celare && celare skill > ~/.claude/skills/celare/SKILL.md
```

## Build

```sh
make test   # tests
make run    # build and open the app
```

## License

GPL-3.0-or-later
