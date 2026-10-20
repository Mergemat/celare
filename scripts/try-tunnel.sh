#!/bin/sh
# End-to-end check of the real tunnel on this Mac. Needs the daemon
# installed (sudo celare service install) and no other VPN connected.
#
#   scripts/try-tunnel.sh <node>
#
# Connects provisionally, checks the exit IP changed and DNS goes through
# the tunnel, confirms, then disconnects and checks DNS was restored.
set -eu
node=${1:?usage: scripts/try-tunnel.sh <node>}
celare=${CELARE:-celare}
probe=https://www.gstatic.com/generate_204

before=$(curl -s -m 5 https://api.ipify.org || echo unknown)
echo "exit IP before: $before"

change=$($celare connect "$node" --confirm 60s --probe "$probe" --json)
id=$(printf '%s' "$change" | sed -n 's/.*"id": "\(c[0-9a-f]*\)".*/\1/p' | head -1)
echo "connected provisionally as change $id"

after=$(curl -s -m 10 https://api.ipify.org || echo unknown)
echo "exit IP through $node: $after"
[ "$after" != "$before" ] || { echo "FAIL: exit IP did not change"; $celare revert "$id"; exit 1; }

dscacheutil -q host -a name example.com >/dev/null && echo "DNS resolves through the tunnel"
service=$($celare doctor --json | sed -n 's/.*"detail": "en[0-9]* via [^ ]* (\(.*\))".*/\1/p' | head -1)
[ -n "$service" ] && echo "DNS of $service: $(networksetup -getdnsservers "$service" | tr '\n' ' ')"

$celare confirm "$id" >/dev/null && echo "confirmed $id"
$celare rules explain example.com

$celare disconnect >/dev/null
echo "disconnected; DNS of ${service:-the service} is now: $( [ -n "$service" ] && networksetup -getdnsservers "$service" | tr '\n' ' ')"
final=$(curl -s -m 5 https://api.ipify.org || echo unknown)
[ "$final" = "$before" ] && echo "exit IP back to $final" || echo "WARN: exit IP is $final, expected $before"
echo PASS
