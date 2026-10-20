import Foundation
import Testing
@testable import Celare

@MainActor
struct NodeTestsTests {
    @Test func completedNodesAppearBeforeSlowNodesAndControlsStayAvailable() async throws {
        let fixture = try Fixture()
        defer { fixture.remove() }
        let model = AppModel(cli: fixture.cli)
        model.nodes = [node("fast"), node("slow")]
        model.tunnel = Tunnel(state: "up", node: "fast", since: .now, pending: nil,
                              version: "test", lastNode: "fast", system: nil)
        let task = Task { await model.testNodes() }
        for _ in 0..<500 {
            if model.latency["fast"] != nil { break }
            try await Task.sleep(for: .milliseconds(10))
        }
        let fast = try #require(model.latency["fast"])
        #expect(fast.ok)
        #expect(model.latency["slow"] == nil)
        #expect(model.testingNodes == ["slow"])
        #expect(model.busy == nil)

        await model.disconnect()
        #expect(model.tunnel?.state == "down")
        #expect(model.testingNodes.contains("slow"))
        try Data().write(to: fixture.directory.appendingPathComponent("release"))
        await task.value
        #expect(model.testingNodes.isEmpty)
        #expect(model.latency["slow"]?.error == "probe released")
    }

    @Test func commandFailureIsShownOnItsNode() async throws {
        let fixture = try Fixture()
        defer { fixture.remove() }
        let model = AppModel(cli: fixture.cli)
        model.nodes = [node("broken")]
        await model.testNodes()
        #expect(model.latency["broken"]?.ok == false)
        #expect(model.latency["broken"]?.error == "node unavailable")
        #expect(model.testingNodes.isEmpty)
        #expect(model.busy == nil)
    }

    private func node(_ name: String) -> NodeView {
        NodeView(name: name, source: "config", protocol: "trojan", address: "127.0.0.1",
                 port: 443, transport: "tcp", security: "tls")
    }
}

private struct Fixture {
    let directory: URL
    let cli: CLI

    init() throws {
        directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let script = directory.appendingPathComponent("celare")
        cli = CLI(binary: script)
        // The slow node cannot finish until the test explicitly releases it.
        // A finite guard prevents a regression from leaving a child behind.
        let contents = #"""
        #!/bin/sh
        case "$1" in
          disconnect) echo '{"ok":true,"data":{}}'; exit 0 ;;
          status) echo '{"ok":true,"data":{"state":"down","since":"2026-10-21T00:00:00Z"}}'; exit 0 ;;
        esac
        case "$3" in
          fast) echo '{"ok":true,"data":{"results":[{"node":"fast","ok":true,"latencyMs":15}]}}' ;;
          broken) echo '{"ok":false,"error":{"code":"test_failed","message":"node unavailable"}}'; exit 1 ;;
          slow)
            dir=$(/usr/bin/dirname "$0")
            count=0
            while [ ! -f "$dir/release" ]; do
              count=$((count+1))
              [ "$count" -gt 1000 ] && exit 1
              /bin/sleep 0.01
            done
            echo '{"ok":true,"data":{"results":[{"node":"slow","ok":false,"error":"probe released"}]}}'
            ;;
        esac
        """#
        try contents.write(to: script, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: script.path)
    }

    func remove() { try? FileManager.default.removeItem(at: directory) }
}
