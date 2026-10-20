---
name: celare
description: Drive the celare VPN on this Mac. Use when the task touches the VPN, proxy nodes, share links (vless://, vmess://, trojan://, ss://, hysteria2://, wireguard://), subscriptions, which sites go through the proxy, or a site being blocked or unreachable.
---

# celare

celare is the Mac's VPN. Drive it only through the `celare` command; it never prompts. Add `--json` to every command: stdout is then one document, `{"ok": true, "data": ..., "next": [...]}` or `{"ok": false, "error": {"code", "message", "hint"}}`. `next` lists the usual follow-up commands; `hint` is the fix for an error.

## Two kinds of state

- **Policy**: nodes, subscriptions, rules, DNS. Plain text in `config.toml` (`celare config path`). Edit it by hand or with `celare nodes|subs|rules ...`. Editing never touches traffic.
- **Tunnel**: up or down, through which node. Changed only by `connect`, `disconnect` and `apply` (`apply` pushes edited policy to the running tunnel).

## Changing the tunnel safely

Your own connection probably runs through this tunnel. A bad change can cut you off mid-task, so make every tunnel change **provisional**:

1. `celare connect <node> --confirm 60s --probe https://www.gstatic.com/generate_204 --json`
   The daemon reverts by itself if the probe fails, or if you don't confirm within 60s.
2. Check that what you need works through the new tunnel (one real request to it).
3. `celare confirm <id> --json`, with the `id` the change returned.

`apply` takes the same flags; `disconnect` takes `--confirm` and `--dry-run`. To see a change before making it, add `--dry-run`: it returns the diff and changes nothing. If you lost contact, `celare changes --json` shows how each change ended and why.

## Common tasks

- Pick a node: `celare nodes test --json` (fastest first), then connect as above.
- Route a site through the proxy: `celare rules add proxy domain:example.com --name example --first`, then `celare apply --dry-run`, then `celare apply --confirm 60s`.
- Why does X go direct, or get blocked: `celare rules explain X`. It names the matching rule and works with the tunnel down.
- Add a server: `celare nodes add '<share-link>'`, with the link in single quotes.
- Something is wrong: `celare doctor --json`. Each failed check carries its fix.

## Exit codes

`0` ok · `1` error · `2` usage · `3` daemon unreachable (`celare service install`; it shows the user a password dialog to approve) · `4` a change is pending (confirm or revert it first) · `5` reverted (the probe failed; the previous tunnel is back).

Credentials are redacted unless you pass `--reveal`. Leave them redacted unless the user asks to see them.
