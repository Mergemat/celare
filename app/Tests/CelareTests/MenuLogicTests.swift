import Foundation
import Testing
@testable import Celare

@MainActor
struct MenuLogicTests {
    @Test func pastedTextYieldsOnlySubscriptionURLsAndShareLinks() {
        let pasted = """
        Your nodes:
        vless://uuid@203.0.113.1:443?security=reality#Tokyo
        trojan://pw@203.0.113.3:8444#home  vless://uuid@203.0.113.1:443?security=reality#Tokyo
        ftp://example.com/file  see https://panel.example.com/sub/abc
        ss://
        """
        #expect(Links.find(in: pasted) == [
            "vless://uuid@203.0.113.1:443?security=reality#Tokyo",
            "trojan://pw@203.0.113.3:8444#home",
            "https://panel.example.com/sub/abc",
        ])
    }

    @Test func connectPrefersThePickedNodeThenTheLastThenTheFastestMeasured() {
        let model = AppModel(cli: CLI(binary: URL(fileURLWithPath: "/nonexistent")))
        model.nodes = [node("a"), node("b"), node("c")]
        model.tunnel = tunnel(last: nil)
        #expect(model.connectTarget == nil)

        model.latency = [
            "a": TestResult(node: "a", ok: true, latencyMs: 300, error: nil),
            "b": TestResult(node: "b", ok: false, latencyMs: nil, error: "timeout"),
            "c": TestResult(node: "c", ok: true, latencyMs: 90, error: nil),
        ]
        #expect(model.connectTarget == "c")

        model.tunnel = tunnel(last: "a")
        #expect(model.connectTarget == "a")

        model.tunnel = tunnel(last: "gone")
        #expect(model.connectTarget == "c")

        model.tunnel = tunnel(last: "a")
        model.chosen = "b"
        #expect(model.connectTarget == "b")

        model.chosen = "removed"
        #expect(model.connectTarget == "a")
    }

    @Test func pickingANodeWhileDisconnectedDoesNotConnect() async {
        let model = AppModel(cli: CLI(binary: URL(fileURLWithPath: "/nonexistent")))
        model.nodes = [node("a"), node("b")]
        model.tunnel = tunnel(last: "a")
        await model.choose("b")
        #expect(model.connectTarget == "b")
        #expect(model.busy == nil && model.error == nil)
    }

    @Test func linkNodesComeFirstAndSpeedSortPutsSilentNodesLast() {
        let model = AppModel(cli: CLI(binary: URL(fileURLWithPath: "/nonexistent")))
        model.nodes = [node("main/slow", sub: "main"), node("home"), node("main/dead", sub: "main"),
                       node("main/untested", sub: "main"), node("main/fast", sub: "main")]
        model.latency = [
            "main/slow": TestResult(node: "main/slow", ok: true, latencyMs: 400, error: nil),
            "main/dead": TestResult(node: "main/dead", ok: false, latencyMs: nil, error: "timeout"),
            "main/fast": TestResult(node: "main/fast", ok: true, latencyMs: 80, error: nil),
        ]
        let inOrder = model.groups()
        #expect(inOrder.map(\.subscription) == [nil, "main"])
        #expect(inOrder[1].nodes.map(\.shortName) == ["slow", "dead", "untested", "fast"])

        model.sortBySpeed = true
        #expect(model.groups()[1].nodes.map(\.shortName) == ["fast", "slow", "untested", "dead"])
        #expect(model.groups(matching: "FA").flatMap(\.nodes).map(\.name) == ["main/fast"])
    }

    @Test func usageLineWarnsNearTheQuotaAndAfterExpiry() {
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        let roomy = Usage(uploadBytes: 1_000_000_000, downloadBytes: 4_000_000_000, totalBytes: 50_000_000_000,
                          expire: Int64(now.timeIntervalSince1970) + 10 * 86400)
        #expect(GroupHeader.usage(roomy, now: now) == "5 GB of 50 GB · 10 days left")
        #expect(!GroupHeader.runningOut(roomy, now: now))

        let full = Usage(uploadBytes: 0, downloadBytes: 46_000_000_000, totalBytes: 50_000_000_000, expire: 0)
        #expect(GroupHeader.runningOut(full, now: now))

        let lapsed = Usage(uploadBytes: 0, downloadBytes: 2_000_000_000, totalBytes: 0, expire: Int64(now.timeIntervalSince1970) - 60)
        #expect(GroupHeader.usage(lapsed, now: now) == "2 GB used · expired")
        #expect(GroupHeader.runningOut(lapsed, now: now))
    }

    private func node(_ name: String, sub: String? = nil) -> NodeView {
        NodeView(name: name, source: sub.map { "subscription:\($0)" } ?? "config", protocol: "vless",
                 address: "203.0.113.1", port: 443, transport: "tcp", security: "reality")
    }

    private func tunnel(last: String?) -> Tunnel {
        Tunnel(state: "down", node: nil, since: .now, pending: nil, version: nil, lastNode: last, system: nil)
    }
}
