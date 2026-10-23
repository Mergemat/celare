import AppKit
import Foundation
import Observation
import ServiceManagement

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

/// Nodes under one heading: a subscription's, or the ones added by link.
struct NodeGroup: Identifiable, Equatable {
    /// The subscription's name; nil for nodes added by link.
    let subscription: String?
    let nodes: [NodeView]

    var id: String { subscription ?? "" }
}

/// Something the user asked to remove, waiting for them to say yes.
enum Removal: Equatable {
    case node(String)
    case subscription(String)
}

@MainActor
@Observable
final class AppModel {
    let cli: CLI

    var tunnel: Tunnel?
    var nodes: [NodeView] = []
    var subscriptions: [Subscription] = []
    var nodesLoaded = false
    var serviceMissing = false
    var latency: [String: TestResult] = [:]
    var testingNodes: Set<String> = []
    /// How many nodes the measurement in progress started with.
    var testingTotal = 0
    /// When every node was last measured. Opening the menu measures again
    /// once this is older than `staleAfter`.
    var measuredAt: Date?
    static let staleAfter: TimeInterval = 600
    var busy: String?
    /// The node a connect is in progress for, shown while it runs.
    var connecting: String?
    /// Measuring every node before connecting to the fastest.
    var findingFastest = false
    /// The last failed action, shown until the next one succeeds.
    var error: CLI.Failure?
    var statusError: CLI.Failure?
    /// What the last action did, shown for a few seconds.
    var notice: String?
    var removal: Removal?
    /// The node picked in the list; Connect connects to it.
    var chosen: String?
    var sortBySpeed = false
    /// The bundled CLI's version; the daemon should match it.
    var appVersion: String?

    private var poller: Task<Void, Never>?
    private var noticeTask: Task<Void, Never>?

    init(cli: CLI = .locate()) {
        self.cli = cli
    }

    var phase: Phase {
        if serviceMissing { return .needsService }
        if let statusError { return .broken(statusError) }
        guard tunnel != nil, nodesLoaded else { return .loading }
        return nodes.isEmpty ? .needsNodes : .ready
    }

    /// What Connect connects to: the node picked in the list, else the
    /// last node, else the only node there is, else the fastest one
    /// measured. Picks that no longer exist are skipped. nil means Connect
    /// measures every node first.
    var connectTarget: String? {
        if let chosen, nodes.contains(where: { $0.name == chosen }) { return chosen }
        if let last = tunnel?.lastNode, nodes.contains(where: { $0.name == last }) { return last }
        if nodes.count == 1 { return nodes[0].name }
        return fastest?.node
    }

    /// The quickest node that answered the last test.
    var fastest: TestResult? {
        nodes.compactMap { latency[$0.name] }.filter(\.ok).min { ($0.latencyMs ?? .max) < ($1.latencyMs ?? .max) }
    }

    /// After the app is updated the running daemon is still the old one
    /// until the service is installed again.
    var serviceOutdated: Bool {
        guard let app = appVersion, let daemon = tunnel?.version else { return false }
        return app != daemon
    }

    /// Nodes added by link first, then each subscription in config order.
    /// Sorting by speed puts nodes that answered first, quickest on top.
    func groups(matching filter: String = "") -> [NodeGroup] {
        let query = filter.trimmingCharacters(in: .whitespaces)
        var order: [String?] = []
        var members: [String?: [NodeView]] = [:]
        for node in nodes where query.isEmpty || node.name.localizedCaseInsensitiveContains(query) {
            if members[node.subscription] == nil { order.append(node.subscription) }
            members[node.subscription, default: []].append(node)
        }
        order.sort { a, b in a == nil && b != nil }
        return order.map { NodeGroup(subscription: $0, nodes: sortBySpeed ? bySpeed(members[$0]!) : members[$0]!) }
    }

    private func bySpeed(_ nodes: [NodeView]) -> [NodeView] {
        func rank(_ node: NodeView) -> Int {
            guard let r = latency[node.name] else { return .max - 1 }
            return r.ok ? (r.latencyMs ?? 0) : .max
        }
        return nodes.enumerated().sorted { a, b in
            let (ra, rb) = (rank(a.element), rank(b.element))
            return ra != rb ? ra < rb : a.offset < b.offset
        }.map(\.element)
    }

    func subscription(named name: String) -> Subscription? {
        subscriptions.first { $0.name == name }
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
        subscriptions = (try? await cli.run(["subs", "list"], as: [Subscription].self)) ?? []
        latency = latency.filter { name, _ in nodes.contains { $0.name == name } }
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
        subscriptions = (try? cli.runSync(["subs", "list"], as: [Subscription].self)) ?? []
        nodesLoaded = true
    }

    func say(_ text: String) {
        notice = text
        noticeTask?.cancel()
        noticeTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(5))
            guard !Task.isCancelled else { return }
            self?.notice = nil
        }
    }

    /// Adds subscription URLs and share links, one per line, creating
    /// config.toml on first use. Measures what was added.
    func add(_ input: String) async -> Bool {
        let links = Links.find(in: input)
        guard !links.isEmpty else {
            if !input.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                error = CLI.Failure(code: "bad_link", message: "That isn't a subscription URL or share link",
                                    hint: "Paste an https:// URL from your panel, or a vless://, vmess://, trojan://, ss://, hysteria2:// or wireguard:// link.")
            }
            return false
        }
        busy = "Adding…"
        let before = Set(nodes.map(\.name))
        var failure: CLI.Failure?
        struct Path: Decodable, Sendable { let path: String }
        _ = try? await cli.run(["config", "init"], as: Path.self)
        for link in links {
            do {
                if link.hasPrefix("http://") || link.hasPrefix("https://") {
                    _ = try await cli.run(["subs", "add", subscriptionName(for: link), link], as: Ignored.self)
                } else {
                    _ = try await cli.run(["nodes", "add", link], as: Ignored.self)
                }
                await refreshNodes()
            } catch let f as CLI.Failure {
                failure = f
            } catch {}
        }
        busy = nil
        let added = nodes.map(\.name).filter { !before.contains($0) }
        error = failure
        guard !added.isEmpty else { return failure == nil }
        say(added.count == 1 ? "Added \(added[0])" : "Added \(added.count) nodes")
        Task { await testNodes(added) }
        return failure == nil
    }

    /// Names a subscription after its host, avoiding names already taken.
    private func subscriptionName(for url: String) -> String {
        let host = URL(string: url)?.host() ?? "sub"
        var base = host.replacingOccurrences(of: ".", with: "-")
        if base.allSatisfy({ $0.isNumber || $0 == "-" }) { base = "sub" }
        let taken = Set(subscriptions.map(\.name) + nodes.compactMap(\.subscription))
        var name = base
        var n = 2
        while taken.contains(name) {
            name = "\(base)-\(n)"
            n += 1
        }
        return name
    }

    /// Picks a node for Connect. While connected, the tunnel moves to it.
    func choose(_ node: String) async {
        chosen = node
        guard tunnel?.isUp == true, tunnel?.node != node else { return }
        await connect(node)
    }

    /// Humans get a probe rather than a confirm window: if the node can't
    /// reach the internet, the daemon puts the previous tunnel back.
    func connect(_ node: String?) async {
        var args = ["connect", "--probe", "https://www.gstatic.com/generate_204"]
        if let node {
            args.insert(node, at: 1)
            chosen = node
        }
        connecting = node ?? connectTarget
        defer { connecting = nil }
        await perform("Connecting…", args)
    }

    /// Measures every node, then connects to the quickest that answered.
    func connectFastest() async {
        findingFastest = true
        await testNodes()
        findingFastest = false
        guard let best = fastest else {
            error = CLI.Failure(code: "no_working_node", message: "No node answered",
                                hint: "Update your subscriptions, or add another node.")
            return
        }
        await connect(best.node)
    }

    func disconnect() async { await perform("Disconnecting…", ["disconnect"]) }
    func confirm(_ id: String) async { await perform("Keeping…", ["confirm", id]) }
    func revert(_ id: String) async { await perform("Reverting…", ["revert", id]) }

    /// Measures every node when the menu opens and the last results are
    /// old, so rows show speed without anyone asking.
    func measureIfStale(now: Date = .now) async {
        guard phase == .ready, connecting == nil else { return }
        if let measuredAt, now.timeIntervalSince(measuredAt) < Self.staleAfter { return }
        await testNodes(quietly: true)
    }

    /// Measures the given nodes, every node by default. Quietly skips the
    /// "2 of 3 answered" notice.
    func testNodes(_ only: [String]? = nil, quietly: Bool = false) async {
        guard busy == nil, testingNodes.isEmpty else { return }
        let names = only ?? nodes.map(\.name)
        guard !names.isEmpty else { return }
        if only == nil { measuredAt = .now }
        testingNodes = Set(names)
        testingTotal = names.count
        defer { testingNodes = [] }
        await cli.testNodes(names) { [weak self] result in
            self?.latency[result.node] = result
            self?.testingNodes.remove(result.node)
        }
        let results = names.compactMap { latency[$0] }
        let answered = results.filter(\.ok)
        // One node's result shows on its row.
        if names.count > 1, !quietly {
            say(answered.isEmpty ? "None of \(results.count) answered" : "\(answered.count) of \(results.count) answered")
        }
    }

    func updateSubscriptions(_ only: [String] = []) async {
        busy = "Updating…"
        let before = nodes.count
        do {
            _ = try await cli.run(["subs", "update"] + only, as: Ignored.self)
            error = nil
        } catch let failure as CLI.Failure {
            error = failure
        } catch {}
        await refreshNodes()
        busy = nil
        guard error == nil else { return }
        let delta = nodes.count - before
        say(delta == 0 ? "Up to date · \(nodes.count) nodes" : "\(delta > 0 ? "+" : "")\(delta) nodes · \(nodes.count) in all")
    }

    func remove(_ removal: Removal) async {
        self.removal = nil
        busy = "Removing…"
        defer { busy = nil }
        do {
            switch removal {
            case .node(let name):
                _ = try await cli.run(["nodes", "remove", name], as: Ignored.self)
                say("Removed \(name)")
            case .subscription(let name):
                _ = try await cli.run(["subs", "remove", name], as: Ignored.self)
                say("Removed \(name)")
            }
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

    var opensAtLogin: Bool {
        get { SMAppService.mainApp.status == .enabled }
        set {
            do {
                if newValue { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
            } catch {
                self.error = CLI.Failure(code: "login_item", message: "Couldn't change Open at Login",
                                         hint: "Allow Celare in System Settings → General → Login Items.")
            }
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

/// Subscription URLs and share links in pasted text, in order.
enum Links {
    static let schemes = ["https", "http", "vless", "vmess", "trojan", "ss", "hysteria2", "hy2", "wireguard"]

    static func find(in text: String) -> [String] {
        var seen = Set<String>()
        return text.split(whereSeparator: \.isWhitespace).map(String.init).filter { token in
            guard let colon = token.range(of: "://"), seen.insert(token).inserted else { return false }
            return schemes.contains(token[..<colon.lowerBound].lowercased()) && token[colon.upperBound...].count > 3
        }
    }

    /// The links on the clipboard, if it holds any. Only on request: macOS
    /// asks the user before an app reads the clipboard by itself.
    static func onClipboard() -> [String] {
        find(in: NSPasteboard.general.string(forType: .string) ?? "")
    }
}

/// Decodes any JSON value and keeps nothing.
struct Ignored: Decodable, Sendable {
    init(from decoder: Decoder) throws {}
}
