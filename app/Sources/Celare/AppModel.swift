import AppKit
import Foundation
import Observation

/// What the menu shows, in order of what blocks what.
enum Phase: Equatable {
    case loading
    /// The background service isn't installed or isn't answering.
    case needsService
    /// Installed, but there's nothing to connect to yet.
    case needsNodes
    case ready
    /// Something unexpected; the message says what.
    case broken(CLI.Failure)
}

@MainActor
@Observable
final class AppModel {
    let cli: CLI

    var tunnel: Tunnel?
    var nodes: [NodeView] = []
    var nodesLoaded = false
    var serviceMissing = false
    var latency: [String: TestResult] = [:]
    var busy: String?
    /// The node a connect is in progress for, shown while it runs.
    var connecting: String?
    /// The last failed action, shown until the next one succeeds.
    var error: CLI.Failure?
    var statusError: CLI.Failure?
    /// The bundled CLI's version; the daemon should match it.
    var appVersion: String?

    private var poller: Task<Void, Never>?

    init(cli: CLI = .locate()) {
        self.cli = cli
    }

    var phase: Phase {
        if serviceMissing { return .needsService }
        if let statusError { return .broken(statusError) }
        guard tunnel != nil, nodesLoaded else { return .loading }
        return nodes.isEmpty ? .needsNodes : .ready
    }

    /// What Connect connects to: the last node if it still exists, else
    /// the only node there is. nil means the user has to pick one.
    var connectTarget: String? {
        if let last = tunnel?.lastNode, nodes.contains(where: { $0.name == last }) { return last }
        return nodes.count == 1 ? nodes[0].name : nil
    }

    /// After the app is updated the running daemon is still the old one
    /// until the service is installed again.
    var serviceOutdated: Bool {
        guard let app = appVersion, let daemon = tunnel?.version else { return false }
        return app != daemon
    }

    func start() {
        guard poller == nil else { return }
        poller = Task { [weak self] in
            while !Task.isCancelled {
                await self?.refreshTunnel()
                try? await Task.sleep(for: .seconds(2))
            }
        }
        Task { await refreshNodes() }
        Task {
            struct V: Decodable, Sendable { let version: String }
            appVersion = try? await cli.run(["version"], as: V.self).version
        }
    }

    func refreshTunnel() async {
        do {
            apply(tunnel: .success(try await cli.run(["status"], as: Tunnel.self)))
        } catch let failure as CLI.Failure {
            apply(tunnel: .failure(failure))
        } catch {}
    }

    func apply(tunnel result: Result<Tunnel, CLI.Failure>) {
        switch result {
        case .success(let t):
            (tunnel, serviceMissing, statusError) = (t, false, nil)
        case .failure(let f) where f.code == "daemon_unreachable":
            (tunnel, serviceMissing, statusError) = (nil, true, nil)
        case .failure(let f):
            (tunnel, statusError) = (nil, f)
        }
    }

    func refreshNodes() async {
        do {
            nodes = try await cli.run(["nodes", "list"], as: [NodeView].self)
        } catch let failure as CLI.Failure where failure.code == "config_missing" {
            nodes = []
        } catch {}
        nodesLoaded = true
    }

    /// Loads everything synchronously, for snapshots of the live state.
    func loadSync() {
        do {
            apply(tunnel: .success(try cli.runSync(["status"], as: Tunnel.self)))
        } catch let f as CLI.Failure {
            apply(tunnel: .failure(f))
        } catch {}
        nodes = (try? cli.runSync(["nodes", "list"], as: [NodeView].self)) ?? []
        nodesLoaded = true
    }

    /// Adds a subscription URL or a share link, creating config.toml on
    /// first use.
    func add(_ input: String) async -> Bool {
        let text = input.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return false }
        busy = "Adding…"
        defer { busy = nil }
        do {
            struct Path: Decodable, Sendable { let path: String }
            _ = try? await cli.run(["config", "init"], as: Path.self)
            if text.hasPrefix("http://") || text.hasPrefix("https://") {
                _ = try await cli.run(["subs", "add", subscriptionName(for: text), text], as: Ignored.self)
            } else {
                _ = try await cli.run(["nodes", "add", text], as: Ignored.self)
            }
            error = nil
            await refreshNodes()
            return true
        } catch let failure as CLI.Failure {
            error = failure
        } catch {}
        return false
    }

    /// Names a subscription after its host, avoiding names already taken.
    private func subscriptionName(for url: String) -> String {
        let host = URL(string: url)?.host() ?? "sub"
        var base = host.replacingOccurrences(of: ".", with: "-")
        if base.allSatisfy({ $0.isNumber || $0 == "-" }) { base = "sub" }
        let taken = Set(nodes.compactMap { $0.source.split(separator: ":").last.map(String.init) })
        var name = base
        var n = 2
        while taken.contains(name) {
            name = "\(base)-\(n)"
            n += 1
        }
        return name
    }

    /// Humans get a probe rather than a confirm window: if the node can't
    /// reach the internet, the daemon puts the previous tunnel back.
    func connect(_ node: String?) async {
        var args = ["connect", "--probe", "https://www.gstatic.com/generate_204"]
        if let node { args.insert(node, at: 1) }
        connecting = node ?? connectTarget
        defer { connecting = nil }
        await perform("Connecting…", args)
    }

    func disconnect() async { await perform("Disconnecting…", ["disconnect"]) }
    func confirm(_ id: String) async { await perform("Keeping…", ["confirm", id]) }
    func revert(_ id: String) async { await perform("Reverting…", ["revert", id]) }

    func testNodes() async {
        busy = "Testing nodes…"
        defer { busy = nil }
        struct Tests: Decodable, Sendable { let results: [TestResult] }
        if let tests = try? await cli.run(["nodes", "test"], as: Tests.self) {
            latency = Dictionary(tests.results.map { ($0.node, $0) }, uniquingKeysWith: { a, _ in a })
        }
    }

    func updateSubscriptions() async {
        busy = "Updating…"
        defer { busy = nil }
        do {
            _ = try await cli.run(["subs", "update"], as: Ignored.self)
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
        struct Path: Decodable, Sendable { let path: String }
        _ = try? await cli.run(["config", "init"], as: Path.self)
        if let paths = try? await cli.run(["config", "path"], as: Paths.self) {
            NSWorkspace.shared.open(URL(fileURLWithPath: paths.config))
        }
    }

    private func perform(_ label: String, _ args: [String]) async {
        busy = label
        defer { busy = nil }
        do {
            _ = try await cli.run(args, as: Ignored.self)
            error = nil
        } catch let failure as CLI.Failure {
            error = failure
        } catch {}
        await refreshTunnel()
    }
}

/// Decodes any JSON value and keeps nothing.
struct Ignored: Decodable, Sendable {
    init(from decoder: Decoder) throws {}
}
