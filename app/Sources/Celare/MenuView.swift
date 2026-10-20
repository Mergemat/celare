import SwiftUI

struct MenuView: View {
    @Bindable var model: AppModel
    /// False for snapshots: don't start polling the daemon.
    var live = true
    @State private var adding = false
    @State private var input = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            header
            content
            if let error = model.error {
                ErrorNote(failure: error) { model.error = nil }
            }
            Divider()
            footer
        }
        .padding(12)
        .frame(width: 300)
        .onAppear { if live { model.start() } }
    }

    // MARK: header

    private var header: some View {
        HStack(spacing: 10) {
            Button {
                Task { model.tunnel?.isUp == true ? await model.disconnect() : await model.connect(nil) }
            } label: {
                Image(systemName: "power")
            }
            .buttonStyle(PowerButtonStyle(on: model.tunnel?.isUp == true))
            .disabled(model.phase != .ready || model.busy != nil)
            .help(model.tunnel?.isUp == true ? "Disconnect" : "Connect to the last node")

            VStack(alignment: .leading, spacing: 1) {
                Text(title).font(.system(size: 13, weight: .semibold)).lineLimit(1)
                Text(subtitle).font(.system(size: 11)).foregroundStyle(.secondary).lineLimit(1)
            }
            Spacer(minLength: 0)
        }
    }

    private var title: String {
        if let busy = model.busy { return busy }
        switch model.phase {
        case .loading: return "celare"
        case .needsService: return "Set up celare"
        case .needsNodes: return "Add a node"
        case .broken: return "Something's wrong"
        case .ready:
            guard let t = model.tunnel, t.isUp else { return "Not connected" }
            return t.node ?? "Connected"
        }
    }

    private var subtitle: String {
        switch model.phase {
        case .loading: return "Checking…"
        case .needsService: return "Install the background service once"
        case .needsNodes: return "Paste a subscription URL or a share link"
        case .broken(let f): return f.message
        case .ready:
            guard let t = model.tunnel, t.isUp else { return "Pick a node" }
            var parts = ["Connected since \(t.since.formatted(date: .omitted, time: .shortened))"]
            if let iface = t.system?.interface { parts.append("via \(iface)") }
            return parts.joined(separator: " · ")
        }
    }

    // MARK: content

    @ViewBuilder
    private var content: some View {
        switch model.phase {
        case .loading:
            EmptyView()
        case .needsService:
            VStack(alignment: .leading, spacing: 8) {
                Text("It creates the tunnel and sets routes and DNS, which takes your password the first time.")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                Button("Install") { Task { await model.installService() } }
                    .buttonStyle(ConvexButtonStyle(prominent: true, fullWidth: true))
                    .disabled(model.busy != nil)
            }
        case .needsNodes:
            addField
        case .broken(let f):
            if let hint = f.hint {
                Text(hint).font(.system(size: 11)).foregroundStyle(.secondary).textSelection(.enabled)
            }
        case .ready:
            ready
        }
    }

    @ViewBuilder
    private var ready: some View {
        if let pending = model.tunnel?.pending {
            PendingBanner(change: pending, model: model)
        }
        if model.serviceOutdated {
            Notice(text: "The background service is older than the app.", action: "Update") {
                Task { await model.installService() }
            }
        }
        if let foreign = model.tunnel?.system?.foreignTunnels, !foreign.isEmpty {
            Text("Another VPN is active (\(foreign.joined(separator: ", "))). Turn it off to connect.")
                .font(.system(size: 11))
                .foregroundStyle(.orange)
        }
        if adding { addField }
        VStack(spacing: 5) {
            if model.nodes.count > 7 {
                ScrollView { rows.padding(.vertical, 1) }.frame(maxHeight: 250)
            } else {
                rows
            }
        }
    }

    private var rows: some View {
        VStack(spacing: 5) {
            ForEach(model.nodes) { node in
                NodeRow(node: node,
                        active: model.tunnel?.isUp == true && model.tunnel?.node == node.name,
                        result: model.latency[node.name]) {
                    Task { await model.connect(node.name) }
                }
                .disabled(model.busy != nil)
            }
        }
    }

    private var addField: some View {
        HStack(spacing: 6) {
            TextField("https://… or vless://…", text: $input)
                .textFieldStyle(.plain)
                .font(.system(size: 12))
                .padding(.horizontal, 8)
                .frame(height: 24)
                .background(RoundedRectangle(cornerRadius: 6).fill(Color(nsColor: .textBackgroundColor)))
                .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(.separator, lineWidth: 0.75))
                .onSubmit(submit)
            Button("Add", action: submit)
                .buttonStyle(ConvexButtonStyle(prominent: true))
                .disabled(input.trimmingCharacters(in: .whitespaces).isEmpty || model.busy != nil)
        }
    }

    private func submit() {
        let text = input
        Task {
            if await model.add(text) {
                input = ""
                adding = false
            }
        }
    }

    // MARK: footer

    private var footer: some View {
        HStack(spacing: 5) {
            if model.phase == .ready {
                Button { adding.toggle() } label: { Image(systemName: "plus") }
                    .help("Add a subscription or a node")
                Button { Task { await model.updateSubscriptions() } } label: { Image(systemName: "arrow.clockwise") }
                    .help("Update subscriptions")
                Button { Task { await model.testNodes() } } label: { Image(systemName: "gauge.with.needle") }
                    .help("Measure every node")
            }
            Spacer()
            Menu {
                Button("Open config.toml") { Task { await model.openConfig() } }
                Button("Copy CLI path") {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(model.cli.binary.path, forType: .string)
                }
                Divider()
                Button("Quit celare") { NSApplication.shared.terminate(nil) }
            } label: {
                Image(systemName: "ellipsis")
            }
            .menuStyle(.button)
            .menuIndicator(.hidden)
            .fixedSize()
        }
        .buttonStyle(IconButtonStyle())
        .disabled(model.busy != nil)
    }
}

struct NodeRow: View {
    let node: NodeView
    let active: Bool
    let result: TestResult?
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 6) {
                VStack(alignment: .leading, spacing: 1) {
                    Text(node.name).font(.system(size: 12, weight: .medium)).lineLimit(1)
                    Text([node.protocol, node.transport, node.security].compactMap { $0 }.joined(separator: " · "))
                        .font(.system(size: 10))
                        .opacity(0.65)
                }
                Spacer(minLength: 0)
                if let result {
                    Text(result.ok ? "\(result.latencyMs ?? 0) ms" : "failed")
                        .font(.system(size: 10, weight: .medium).monospacedDigit())
                        .foregroundStyle(result.ok || active ? AnyShapeStyle(.primary.opacity(0.75)) : AnyShapeStyle(Color.red))
                }
            }
        }
        .buttonStyle(ConvexRowStyle(active: active))
    }
}

struct PendingBanner: View {
    let change: Change
    let model: AppModel

    var body: some View {
        TimelineView(.periodic(from: .now, by: 1)) { context in
            HStack(spacing: 6) {
                VStack(alignment: .leading, spacing: 1) {
                    Text("Keep this change?").font(.system(size: 11, weight: .semibold))
                    if let deadline = change.deadline {
                        let left = max(0, Int(deadline.timeIntervalSince(context.date)))
                        Text("Back to \(change.from?.node ?? "disconnected") in \(left)s")
                            .font(.system(size: 10)).foregroundStyle(.secondary)
                    }
                }
                Spacer(minLength: 0)
                Button("Revert") { Task { await model.revert(change.id) } }
                    .buttonStyle(ConvexButtonStyle())
                Button("Keep") { Task { await model.confirm(change.id) } }
                    .buttonStyle(ConvexButtonStyle(prominent: true))
            }
        }
    }
}

struct Notice: View {
    let text: String
    let action: String
    let perform: () -> Void

    var body: some View {
        HStack(spacing: 6) {
            Text(text).font(.system(size: 11)).foregroundStyle(.secondary)
            Spacer(minLength: 0)
            Button(action, action: perform).buttonStyle(ConvexButtonStyle(prominent: true))
        }
    }
}

struct ErrorNote: View {
    let failure: CLI.Failure
    let dismiss: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 6) {
            VStack(alignment: .leading, spacing: 2) {
                Text(failure.message).font(.system(size: 11)).foregroundStyle(.red)
                if let hint = failure.hint {
                    Text(hint).font(.system(size: 10)).foregroundStyle(.secondary)
                }
            }
            .textSelection(.enabled)
            .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 0)
            Button(action: dismiss) { Image(systemName: "xmark") }
                .buttonStyle(.plain)
                .font(.system(size: 9, weight: .bold))
                .foregroundStyle(.secondary)
        }
    }
}
