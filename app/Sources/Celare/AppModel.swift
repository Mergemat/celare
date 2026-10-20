import AppKit
import Foundation
import Observation

@MainActor
@Observable
final class AppModel {
    private let cli = CLI.locate()

    var tunnel: Tunnel?
    var nodes: [NodeView] = []
    var latency: [String: TestResult] = [:]
    /// Set when the daemon can't be reached; the app offers to install it.
    var daemonMissing = false
    var busy: String?
    var error: CLI.Failure?

    private var poller: Task<Void, Never>?

    func start() {
        guard poller == nil else { return }
        poller = Task { [weak self] in
            while !Task.isCancelled {
                await self?.refreshTunnel()
                try? await Task.sleep(for: .seconds(2))
            }
        }
        Task { await refreshNodes() }
    }

    func refreshTunnel() async {
        do {
            tunnel = try await cli.run(["status"], as: Tunnel.self)
            daemonMissing = false
        } catch let failure as CLI.Failure where failure.code == "daemon_unreachable" {
            tunnel = nil
            daemonMissing = true
        } catch {}
    }

    func refreshNodes() async {
        if let list = try? await cli.run(["nodes", "list"], as: [NodeView].self) {
            nodes = list
        }
    }

    /// Humans get a probe instead of a confirm window: if the node can't
    /// reach the internet, the daemon puts the previous tunnel back.
    func connect(_ node: String?) async {
        var args = ["connect", "--probe", "https://www.gstatic.com/generate_204"]
        if let node { args.insert(node, at: 1) }
        await perform("Connecting…", args, as: Change.self)
    }

    func disconnect() async {
        await perform("Disconnecting…", ["disconnect"], as: Change.self)
    }

    func confirm(_ id: String) async {
        await perform("Confirming…", ["confirm", id], as: Change.self)
    }

    func revert(_ id: String) async {
        await perform("Reverting…", ["revert", id], as: Change.self)
    }

    func testNodes() async {
        busy = "Testing nodes…"
        defer { busy = nil }
        if let results = try? await cli.run(["nodes", "test"], as: [TestResult].self) {
            latency = Dictionary(results.map { ($0.node, $0) }, uniquingKeysWith: { a, _ in a })
        }
    }

    func updateSubscriptions() async {
        busy = "Updating subscriptions…"
        defer { busy = nil }
        do {
            _ = try await cli.run(["subs", "update"], as: [SubResult].self)
            error = nil
        } catch let failure as CLI.Failure {
            error = failure
        } catch {}
        await refreshNodes()
    }

    func installService() async {
        busy = "Installing…"
        defer { busy = nil }
        do {
            try await cli.installService()
            error = nil
        } catch let failure as CLI.Failure {
            error = failure
        } catch {}
        await refreshTunnel()
    }

    func openConfig() async {
        struct Paths: Decodable, Sendable { let config: String }
        if let paths = try? await cli.run(["config", "path"], as: Paths.self) {
            NSWorkspace.shared.open(URL(fileURLWithPath: paths.config))
        }
    }

    private func perform<T: Decodable & Sendable>(_ label: String, _ args: [String], as: T.Type) async {
        busy = label
        defer { busy = nil }
        do {
            _ = try await cli.run(args, as: T.self)
            error = nil
        } catch let failure as CLI.Failure {
            error = failure
        } catch {}
        await refreshTunnel()
    }
}

struct SubResult: Decodable, Sendable {
    let name: String
    let error: String?
}
