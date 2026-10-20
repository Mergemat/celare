# Tunnel failure investigation

Investigated on 2026-10-21, starting at local commit `91a69cd`, with installed daemon v0.1.5. Read `docs/design.md` and `README.md` first.

No installed VPN was stopped, disconnected, quit, restarted or reconfigured. Celare's daemon stayed down. No system routes or DNS settings were changed. No root commands, releases, tags or pushes were used. Temporary test processes had only loopback SOCKS and DNS listeners. The report is on local branch `investigate/tunnel`; temporary harnesses and credential-bearing configurations were removed.

## A. Diagnosis

The best-supported immediate cause is DNS starvation while Xray's internal DoH requests fail. The evidence does **not** establish why those requests stall. Calling the bug a proven fake-IP collision, IPv6 failure, or an unbound DoH socket would overstate what is known.

### What the existing evidence proves

The daemon log itself contains failures to resolve `www.gstatic.com` through `https://1.1.1.1/dns-query`, including at 00:21:15 and 00:21:32. The first DoH dispatch in that session appears at 00:21:00.642481, followed by a timeout and another dispatch around 00:21:04.88. Queries to both `198.18.0.53:53` and ordinary DNS addresses reached `tun-in -> dns-out`. At least those queries crossed the system-to-TUN boundary successfully.

The likely chain is:

1. The daemon calls `http.DefaultClient.Do` for the hostname URL.
2. Its normal resolver needs an address for `www.gstatic.com`.
3. DNS packets entering celare go to `dns-out`, which invokes Xray's DNS client for A and AAAA.
4. DoH transactions fail; this Xray version can return no DNS packet on such lookup failures.
5. Resolver retries and the unfinished HTTP request consume the daemon's 15-second deadline.

The log does not associate each DNS query with the originating process. This sequence is a strong inference, not a process-correlated trace of the daemon's request.

The SOCKS successes in the original brief do not validate this path. `probe.Node` supplies no DNS block. Even a SOCKS configuration containing the same DNS block can send the hostname to the remote proxy without invoking Xray DNS. Celare's `IPIfNonMatch` router has a matching catch-all `tcp,udp` rule, and Xray returns the first matching rule before its second DNS-assisted pass.

Sources: [HTTP probe](../internal/cli/service.go), function `httpProbe.Probe`, line 382; [deadline and revert](../internal/daemon/daemon.go), `Daemon.Apply`, line 217; [SOCKS probe](../internal/probe/probe.go), `Node`, line 35; [renderer](../internal/xrayconf/config.go), `Config`, line 92; Xray `app/router/router.go:247`.

### Verified Xray behavior

All Xray source references below use the installed module at `/Users/b.omarov/go/pkg/mod/github.com/xtls/xray-core@v1.260327.0`.

- `app/dns/nameserver.go:72` selects dispatcher-backed DoH for `https://`. `https+local://` is a different path.
- `app/dns/nameserver_doh.go:53` uses HTTP/2; lines 62-88 obtain a dispatcher connection, and lines 101-104 perform a Chrome uTLS handshake over it. Thus normal DoH reaches the selected proxy outbound and uses that outbound's socket options. It does not independently open an unbound physical socket to `1.1.1.1`.
- `[dns-internal -> proxy]` means that routing selected the proxy. It does not prove TCP connection, proxy handshake, inner TLS handshake, HTTP/2 response or valid DNS content.
- `app/dns/nameserver.go:228` defaults the per-server timeout to four seconds; `nameserver_doh.go:149` preserves the query deadline. `proxy/dns/dns.go:284` can return without sending an answer when lookup fails without a DNS response code. Increasing the outer 15-second timeout does not fix this.
- `app/dns/dnscommon.go:250` detaches the transport context while retaining DNS metadata. There is no demonstrated per-query cancellation bug in this path.
- Darwin sets `IP_BOUND_IF` or `IPV6_BOUND_IF` in `transport/internet/sockopt_darwin.go:136`. However, `system_dialer.go:123` logs option-setting errors at INFO and continues. A rendered interface name and warning-level log are therefore insufficient proof that binding succeeded.

### Additional safe experiments

V2Box remained connected on `utun4`; `celare status --json` reported daemon v0.1.5 and state `down`. `scutil --dns` showed the primary resolver associated with `utun4`, while Wi-Fi had its own scoped resolver. This is a current-state observation, not a reconstruction of the failed sessions.

The experimental configuration replaced the TUN inbound with loopback-only `dokodemo-door` DNS and SOCKS inbounds. The DNS listener retained tag `tun-in`, so actual DNS packets exercised the existing `dns-hijack -> dns-out -> app/dns -> DoH` path. Outbound socket binding was explicitly changed to `utun4`. No TUN, route or system DNS changes occurred.

- Ordinary curl through the system returned gstatic HTTP 204 in 0.48 seconds; explicitly binding curl to `utun4` returned 204 in 0.37 seconds.
- Homebrew Xray 26.3.27, built with Go 1.26.1, carried the Trojan node through `utun4`: gstatic HTTP 204 in 0.71 seconds and an explicit DoH A answer in 0.92 seconds.
- A temporary runner built from celare's current `internal/engine`, using Go 1.27.1, repeated that Trojan test: HTTP 204 in 3.16 seconds and DoH A answer in 0.78 seconds. The installed daemon also reports Go 1.27.1.
- Homebrew Xray with `freedom` bound to `utun4` returned a DoH A answer in 0.42 seconds. This still traversed V2Box; it was not a physical-interface bypass.
- The VLESS node timed out for both SOCKS HTTP and explicit DNS when nested through `utun4`. Removing the explicit binding also timed out. Its bound A, AAAA and TCP-inbound DNS checks timed out. These results isolate neither the nested TCP connection nor the REALITY handshake. They do show that choosing the active interface cannot guarantee that every node works over every upstream VPN.

These experiments prove that Xray's DNS module and the Trojan outbound can work over the existing VPN, including with celare's build dependencies. They do not reproduce the root daemon with a real TUN, system DNS changes, simultaneous application load or its previous engine instances.

### Alternatives weighed

**System DNS scoping and the fake-IP address.** Both are real design hazards. Changing Wi-Fi's DNS does not establish that every macOS resolver scope will use it. Apple's DNS discussion confirms that classic resolver configuration is not the source of truth for scoped DNS. However, DNS queries already arriving at `dns-out` and then failing upstream cannot be explained solely by failure to deliver queries to celare. The supplied V2Box response at `198.18.0.53` proves a collision when V2Box is active, not its responsibility for every session reported as disconnected. [Apple DNS discussion](https://developer.apple.com/forums/thread/742655).

The log also contains application connections to `198.18.0.x` forwarded to `proxy`. These are consistent with cached foreign fake-IP answers. With `routeOnly: true`, sniffing does not replace such a destination with a real address. Moving only the DNS server address will not repair already-cached synthetic destinations.

**IPv6.** Xray's Darwin setup assigns link-local IPv6 only, while celare installs both IPv6 half-default routes. That deserves a real source-selection and return-path test. Nevertheless, Xray's gVisor stack supports IPv6, and the daemon log contains an accepted connection from a link-local source to a global IPv6 target. The absence of a global address on the TUN is not, by itself, proof that proxying IPv6 is impossible. Internal DoH targets literal IPv4 `1.1.1.1` and bypasses the TUN inbound, so IPv6 is a poor explanation for its failures. Source: Xray `proxy/tun/tun_darwin.go:237,304`; `stack_gvisor.go:208`.

**gVisor refusals.** The log message is emitted when `tcp.ForwarderRequest.CreateEndpoint` fails during the local TCP handshake, before the target is dispatched. It is not evidence that the remote VPN server refused the connection. Internal DoH does not use this forwarder. Correlating packets with that error remains necessary to explain the earlier user-process refusal. Source: Xray `proxy/tun/stack_gvisor.go:78`.

**Start ordering.** `Tunnel.Start` starts Xray before adding half-default routes and sets system DNS last. Xray constructs and starts the TUN stack during handler initialization. The source does not support a simple “routes installed before a listener exists” diagnosis. There is still a transition window: the log shows old DNS destinations entering the TUN before the new DNS address appears. Readiness and resolver convergence should be observed rather than assumed.

**Root privileges, server credentials and interface binding.** There is no demonstrated root-specific resolver or TLS bug. The original root/TUN and user/SOCKS comparisons exercise different DNS paths. Successful node tests weaken an invalid-node explanation, while established outer sockets prove only TCP establishment. Binding failures remain possible because Xray does not propagate socket-option errors. The endpoint's IPv4 address also removes server-name bootstrap as the explanation for this particular VLESS case.

**DoH TLS/HTTP2 or MTU.** These remain possible lower-level causes, not established findings. A failed shared HTTP/2 connection can affect many queries. The successful same-build Trojan DoH test disproves a universal Go/Xray DoH incompatibility. Reducing celare's TUN MTU is not a targeted fix for internal DoH, which bypasses that TUN.

### A separate verified teardown defect

This Xray version does not reliably release its TUN on `Engine.Close()`:

- `proxy/tun/handler.go:57,66-97` creates a detached context, device and stack, with no successful handler shutdown method.
- Its `Network()` is empty, so `app/proxyman/inbound/always.go:110-167` creates no workers for it.
- `AlwaysOnInboundHandler.Close`, line 183, closes workers and mux, not the underlying TUN proxy.
- Even `stackGVisor.Close`, line 191, does not close the device. `DarwinTun.Close`, line 94, owns that file descriptor.

An old celare-style `169.254.10.1` route on `utun7` was visible while celare reported down, consistent with leaked state. The source establishes the lifetime defect; the route alone does not identify the owning process. This can contaminate repeated attempts and makes reliable restarts impossible. It does not establish why a first fresh TUN session fails.

## B. Coexistence design

Treat the current usable network as celare's upstream. It may be Wi-Fi, Ethernet or another VPN. The intended path is application → celare → current upstream VPN → celare's server. The Trojan experiment demonstrates this nesting at the socket and proxy level.

1. Replace `Physical()` as the outbound selector with an upstream-path abstraction. Resolve routes for the actual server endpoints before installing celare's capture routes. Track interface index, gateway, address family, relevant addresses and network generation. A first `default` line is insufficient: split tunnels, scoped routes, specific endpoint routes and IPv4/IPv6 differences matter. Reuse the existing `golang.org/x/net/route` dependency for route messages where suitable.
2. Keep outbound selection separate from DNS service management. An upstream utun does not necessarily map to a `networksetup` hardware service. Bind proxy and freedom sockets to the selected upstream and require successful binding. “Direct” means outside celare, while still respecting the user's existing VPN.
3. Observe upstream route/address changes, excluding celare's own routes. Replace the current five-second comparison of interface names with debounced route/SystemConfiguration events. Interface removal, gateway changes and reused utun names matter. Rebuild celare's session transactionally and recheck traffic. If no usable upstream exists, report that state without manipulating another VPN.
4. Allocate a private, owned DNS endpoint outside `198.18.0.0/15`. A checked link-local address plus an explicit host route is an option for the raw-TUN design; a loopback listener with proper resolver registration is another. No fixed address is collision-proof. Publish DNS with an explicit macOS resolver scope and ownership, verify effective `scutil --dns` state and real system resolution, and remove only celare's own registration. Rewriting Wi-Fi alone is insufficient.
5. Preserve the upstream VPN's synthetic-address mapping. Traffic to its fake-IP range must remain with that provider while it is active; forwarding those opaque addresses to celare's remote proxy loses their meaning. Resolve celare's own server through a controlled upstream bootstrap path and refresh pinned results when that upstream changes. Do not carry a foreign fake-IP pin into a different network.
6. Keep `dns-internal` routing explicit and recursion-free. The DoH endpoint needs a known bootstrap address; its transport must use the chosen upstream through the intended outbound. Do not switch blindly to `https+local://` or an unbound freedom outbound.
7. Own route changes precisely. Do not delete or replace another provider's routes, and do not assume adding `/1` routes wins against every VPN's more-specific routes or enforced policy. Journal successful mutations and roll back those exact objects.

There is a real platform limit. Apple documents enforced routes and `includeAllNetworks`, and discourages raw route-table VPN implementations. Binding an outbound to a utun does not override those policies or guarantee that celare captures applications ahead of the other provider. A supported NetworkExtension architecture should be evaluated for the durable design, with explicit simultaneous-provider acceptance tests. Two full-tunnel providers are not universally composable merely because ordinary applications work. Apple's documented Personal/Enterprise `includeAllNetworks` conflict illustrates this limit; it does not prove that this V2Box installation has that policy. [Apple routing guidance](https://developer.apple.com/videos/play/wwdc2025/234/), [documented VPN conflict](https://developer.apple.com/forums/thread/669086).

## C. Concrete fix plan and tests

1. **Expose the failing stage.** In `internal/cli/service.go:httpProbe.Probe` and `internal/probe`, use a fresh HTTP transport per check, disable unintended environment proxies and connection reuse, and attach `httptrace` DNS/TCP/TLS/response timing. Preserve the underlying error. Retain the unbound end-to-end system-resolver probe; replacing it with a SOCKS-only check would miss this bug. Add a separate explicit DNS-module diagnostic before route mutation. Record resolver state, selected upstream, endpoint family and verified socket binding.

   Test with controlled local resolvers and HTTP/TLS servers: DNS failure, TCP refusal, TLS failure, response timeout and success must produce distinct results. An earlier HTTP connection must not satisfy a new session's check. Assert actual requests and errors, not copies of configuration structs.

2. **Fix TUN lifetime before adding automatic restart.** Update `internal/engine` and its use in `Tunnel.Start/Stop`. Either use a verified upstream lifecycle repair that closes handler, stack and device, or replace the daemon's in-process TUN engine with a celare-owned child process whose exit releases the device. A child using the bundled binary avoids depending on a Homebrew installation. Choose one production ownership model; do not maintain a compatibility path. Ensure failed construction and failed start also close partial resources.

   A macOS privileged integration fixture should run repeated create/close and failed-start cycles and assert that owned utuns, route entries, file descriptors and goroutines disappear. For child ownership, rootless tests can already prove process exit, readiness failure and reaping of only the owned child. No such privileged test was run in this investigation.

3. **Replace physical-only behavior.** Change `internal/system/tunnel.go:Physical, Start, pinAddress`, `internal/system/reach.go:DialBound`, `internal/netconf` route selection and `internal/xrayconf.Plan.BindInterface`. Update `internal/cli/service.go:watchNetwork, diagnose`, `nodestest.go`, `doctor.go` and `configcmd.go` to use the same upstream policy.

   Test full VPN defaults, split routes, scoped defaults, IPv6-only and dual-stack endpoints, a route to the node that differs from the general default, upstream disappearance, changed gateway on the same interface, reused utun names and ignoring celare's own capture routes. An invalid binding must fail closed. Integration tests should carry real traffic through a local nested proxy chain.

4. **Replace DNS ownership and repair diagnostics.** Remove `netconf.TunnelDNS = 198.18.0.53` and service-wide DNS overwrite assumptions from `UpCommands/DownCommands` and `session`. Implement a separate resolver owner and a route journal. Keep real A/AAAA answers authoritative and handle foreign fake-IP traffic explicitly.

   Add real loopback DNS tests through `dns-out`: A, AAAA, TCP, UDP, NXDOMAIN, timeout and concurrent queries over a local DoH fixture. Exercise actual DNS packets; a SOCKS HTTP request alone is insufficient. Test address collision detection, resolver selection and rollback after partial route/DNS installation. Verify that another provider's later state is preserved.

5. **Establish IPv6 behavior.** In `netconf.UpCommands` and the TUN backend, validate IPv6 source selection, return traffic and proxy transport separately. Configure an appropriate address if required by measured failures. Do not capture IPv6 silently into a broken path, and do not remove IPv6 routes merely to make a test green while leaving unintended traffic outside celare.

6. **Remove the obsolete product policy.** Delete the foreign-VPN refusal and disconnect advice in `Tunnel.Start`. Remove `AppModel.turnOff`, the “Turn off VPN” branch in `MenuView`, the physical-firewall assumptions in diagnostics, and the “One VPN at a time” claim in `docs/design.md`. Show selected upstream and concrete reachability failures instead.

The next decisive reproduction is a controlled root TUN test with the same node and selected upstream, recording explicit DNS-module lookup, system lookup, IP-only HTTPS and hostname HTTPS independently before rollback. It must also inspect effective binding and TUN cleanup. That was intentionally not attempted on this host because the active V2Box connection must remain untouched and `celare connect` is forbidden while it is connected.

`go test ./...` passed for the checkout. The existing tests do not cover real macOS TUN routing, resolver scoping or root-daemon traffic. No production fix is claimed by this report.
