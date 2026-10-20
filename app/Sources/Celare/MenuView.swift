import SwiftUI

struct MenuView: View {
    @Bindable var model: AppModel
    /// False for snapshots: don't start polling the daemon.
    var live = true
    @State private var adding = false
    @State private var input = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            topLine.padding(.bottom, 14)
            hero.padding(.bottom, 14)
            action
            if let error = model.error {
                ErrorNote(failure: error) { model.error = nil }.padding(.top, 10)
            }
            if model.phase == .ready {
                nodes.padding(.top, 16)
            }
            footer.padding(.top, 12)
        }
        .padding(14)
        .frame(width: 284)
        .onAppear { if live { model.start() } }
    }

    // MARK: top line: wordmark and state

    private var topLine: some View {
        HStack(spacing: 6) {
            MarkView(dot: isUp)
                .foregroundStyle(isUp ? AnyShapeStyle(Palette.blueDot) : AnyShapeStyle(.secondary))
                .frame(width: 12, height: 12)
            Text("celare").font(.system(size: 11, weight: .semibold)).foregroundStyle(.secondary)
            Spacer()
            Circle().fill(stateColor).frame(width: 6, height: 6)
            Text(stateLabel).font(.system(size: 11)).foregroundStyle(.secondary)
        }
    }

    private var isUp: Bool { model.tunnel?.isUp == true }

    private var stateLabel: String {
        if let busy = model.busy { return busy }
        switch model.phase {
        case .loading: return "Checking"
        case .needsService: return "Not set up"
        case .broken: return "Error"
        case .needsNodes, .ready:
            if model.tunnel?.pending != nil { return "Waiting for you" }
            return isUp ? "Connected" : "Off"
        }
    }

    private var stateColor: Color {
        switch model.phase {
        case .broken: return .red
        case .needsService: return .orange
        default:
            if model.tunnel?.pending != nil { return .orange }
            return isUp ? Palette.blueDot : Color.secondary.opacity(0.5)
        }
    }

    // MARK: hero: what is happening, once

    @ViewBuilder
    private var hero: some View {
        VStack(alignment: .leading, spacing: 3) {
            switch model.phase {
            case .loading:
                Text(" ").font(.system(size: 17, weight: .semibold))
            case .needsService:
                Text("Set up celare").font(.system(size: 17, weight: .semibold))
                Text("A background service creates the tunnel and sets routes and DNS. Installing it asks for your password once.")
                    .font(.system(size: 11.5)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            case .needsNodes:
                Text("Add a node").font(.system(size: 17, weight: .semibold))
                Text("Paste a subscription URL from your panel, or a single share link.")
                    .font(.system(size: 11.5)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            case .broken(let f):
                Text("celare can't run").font(.system(size: 17, weight: .semibold))
                Text(f.message + (f.hint.map { ". " + $0 } ?? ""))
                    .font(.system(size: 11.5)).foregroundStyle(.secondary).textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            case .ready:
                if let t = model.tunnel, t.isUp, let name = t.node {
                    NodeName(name: name, size: 17)
                    Text(detail(for: name)).font(.mono).foregroundStyle(.secondary)
                } else {
                    Text("Not connected").font(.system(size: 17, weight: .semibold))
                    Text(model.connectTarget == nil ? "Pick a node below." : "Connect, or pick another node below.")
                        .font(.system(size: 11.5)).foregroundStyle(.secondary)
                }
            }
        }
    }

    /// "main/Tokyo" → "Tokyo": the bar has no room for the prefix.
    private func shortName(_ name: String) -> String {
        name.split(separator: "/", maxSplits: 1).last.map(String.init) ?? name
    }

    private func detail(for name: String) -> String {
        var parts: [String] = []
        if let n = model.nodes.first(where: { $0.name == name }) {
            parts += [n.protocol, n.transport, n.security].compactMap { $0 }
        }
        if let iface = model.tunnel?.system?.interface { parts.append(iface) }
        return parts.joined(separator: " · ")
    }

    // MARK: the one loud control

    @ViewBuilder
    private var action: some View {
        switch model.phase {
        case .loading, .broken:
            EmptyView()
        case .needsService:
            Button { Task { await model.installService() } } label: { Label("Install background service", systemImage: "lock.shield") }
                .buttonStyle(PrimaryBarStyle())
                .disabled(model.busy != nil)
        case .needsNodes:
            addField(prominent: true)
        case .ready:
            if let pending = model.tunnel?.pending {
                PendingBar(change: pending, model: model)
            } else if isUp {
                Button { Task { await model.disconnect() } } label: { Label("Disconnect", systemImage: "power") }
                    .buttonStyle(PrimaryBarStyle())
                    .disabled(model.busy != nil)
            } else {
                let target = model.connectTarget
                Button { Task { await model.connect(target) } } label: {
                    Label(target.map { "Connect to \(shortName($0))" } ?? "Connect", systemImage: "power")
                }
                .buttonStyle(PrimaryBarStyle())
                .disabled(target == nil || model.busy != nil)
            }
            if model.serviceOutdated {
                quietNote("The background service is older than this app.", action: "Update") {
                    Task { await model.installService() }
                }
            }
            if let foreign = model.tunnel?.system?.foreignTunnels, !foreign.isEmpty {
                Text("Another VPN is on (\(foreign.joined(separator: ", "))). Turn it off before connecting.")
                    .font(.system(size: 11)).foregroundStyle(.orange).padding(.top, 8)
            }
        }
    }

    private func addField(prominent: Bool) -> some View {
        VStack(spacing: 8) {
            TextField("https://… or vless://…", text: $input)
                .textFieldStyle(.plain)
                .font(.mono)
                .padding(.horizontal, 9)
                .frame(height: 28)
                .background(RoundedRectangle(cornerRadius: 7).fill(Color.primary.opacity(0.06)))
                .overlay(RoundedRectangle(cornerRadius: 7).strokeBorder(Color.primary.opacity(0.1), lineWidth: 0.5))
                .onSubmit(submit)
            if prominent {
                Button(action: submit) { Label("Add", systemImage: "plus") }
                    .buttonStyle(PrimaryBarStyle())
                    .disabled(input.trimmingCharacters(in: .whitespaces).isEmpty || model.busy != nil)
            }
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

    private func quietNote(_ text: String, action: String, perform: @escaping () -> Void) -> some View {
        HStack {
            Text(text).font(.system(size: 11)).foregroundStyle(.secondary)
            Spacer(minLength: 4)
            Button(action, action: perform).buttonStyle(HoverRaiseStyle()).font(.system(size: 11, weight: .medium))
                .foregroundStyle(Palette.blueDot)
        }
        .padding(.top, 8)
    }

    // MARK: nodes

    private var nodes: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("Nodes").font(.system(size: 11, weight: .semibold)).foregroundStyle(.secondary)
                .padding(.bottom, 2)
            if adding { addField(prominent: false).padding(.bottom, 4) }
            Group {
                if model.nodes.count > 8 {
                    ScrollView { rows }.frame(maxHeight: 240)
                } else {
                    rows
                }
            }
            // Rows bleed into the margin so their dots line up with the
            // text above; the hover surface still has room around them.
            .padding(.horizontal, -8)
        }
    }

    private var rows: some View {
        VStack(spacing: 1) {
            ForEach(model.nodes) { node in
                NodeRow(node: node,
                        active: isUp && model.tunnel?.node == node.name,
                        result: model.latency[node.name]) {
                    Task { await model.connect(node.name) }
                }
                .disabled(model.busy != nil)
            }
        }
    }

    // MARK: footer

    private var footer: some View {
        HStack(spacing: 2) {
            if model.phase == .ready {
                footerIcon(adding ? "xmark" : "plus", help: adding ? "Cancel" : "Add a subscription or node") { adding.toggle() }
                footerIcon("arrow.clockwise", help: "Update subscriptions") { Task { await model.updateSubscriptions() } }
                footerIcon("gauge.with.needle", help: "Measure every node") { Task { await model.testNodes() } }
            }
            Spacer()
            if live {
                Menu {
                    Button("Open config.toml") { Task { await model.openConfig() } }
                    Button("Copy CLI path") {
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(model.cli.binary.path, forType: .string)
                    }
                    Divider()
                    Button("Quit celare") { NSApplication.shared.terminate(nil) }
                } label: {
                    footerGlyph("ellipsis")
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
                .help("More")
            } else {
                // ImageRenderer can't draw an AppKit menu; show its icon.
                footerGlyph("ellipsis").padding(.horizontal, 7).padding(.vertical, 5)
            }
        }
        .padding(.horizontal, -7)
        .disabled(model.busy != nil)
    }

    private func footerGlyph(_ symbol: String) -> some View {
        Image(systemName: symbol)
            .font(.system(size: 12, weight: .medium))
            .foregroundStyle(.secondary)
            .frame(width: 16, height: 16)
    }

    private func footerIcon(_ symbol: String, help: String, action: @escaping () -> Void) -> some View {
        Button(action: action) { footerGlyph(symbol) }
            .buttonStyle(HoverRaiseStyle(horizontal: 7, vertical: 5))
            .help(help)
    }
}

/// "main/Tokyo" with the subscription part dimmed.
struct NodeName: View {
    let name: String
    var size: CGFloat = 12.5

    var body: some View {
        let parts = name.split(separator: "/", maxSplits: 1).map(String.init)
        if parts.count == 2 {
            (Text(parts[0] + "/").foregroundStyle(.secondary) + Text(parts[1]))
                .font(.system(size: size, weight: size > 14 ? .semibold : .medium))
                .lineLimit(1)
        } else {
            Text(name).font(.system(size: size, weight: size > 14 ? .semibold : .medium)).lineLimit(1)
        }
    }
}

struct NodeRow: View {
    let node: NodeView
    let active: Bool
    let result: TestResult?
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 8) {
                Circle()
                    .fill(active ? Palette.blueDot : Color.clear)
                    .frame(width: 6, height: 6)
                NodeName(name: node.name)
                Spacer(minLength: 6)
                Text(trailing).font(.mono).foregroundStyle(failed ? AnyShapeStyle(Color.red.opacity(0.85)) : AnyShapeStyle(.secondary))
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .buttonStyle(HoverRaiseStyle(radius: 7, horizontal: 8, vertical: 6))
    }

    private var failed: Bool { result.map { !$0.ok } ?? false }

    private var trailing: String {
        guard let result else { return node.protocol }
        return result.ok ? "\(result.latencyMs ?? 0) ms" : "failed"
    }
}

/// A pending change: the bar splits into Keep and Revert, and Keep drains
/// as the deadline approaches.
struct PendingBar: View {
    let change: Change
    let model: AppModel

    var body: some View {
        TimelineView(.periodic(from: .now, by: 0.25)) { context in
            let left = remaining(at: context.date)
            VStack(alignment: .leading, spacing: 6) {
                HStack(spacing: 6) {
                    Button { Task { await model.confirm(change.id) } } label: {
                        HStack(spacing: 6) {
                            Text("Keep")
                            Text("\(Int(left.seconds.rounded(.up)))s").font(.mono).opacity(0.75)
                        }
                    }
                    .buttonStyle(PrimaryBarStyle())
                    .overlay(alignment: .leading) {
                        // The time left, as a lit band that shrinks.
                        GeometryReader { geo in
                            RoundedRectangle(cornerRadius: 8, style: .continuous)
                                .fill(Color.white.opacity(0.12))
                                .frame(width: geo.size.width * left.fraction)
                        }
                        .allowsHitTesting(false)
                    }
                    Button("Revert") { Task { await model.revert(change.id) } }
                        .buttonStyle(RaisedButtonStyle())
                }
                Text("Goes back to \(change.from?.node ?? "disconnected") unless you keep it.")
                    .font(.system(size: 11)).foregroundStyle(.secondary)
            }
        }
    }

    private func remaining(at now: Date) -> (seconds: Double, fraction: Double) {
        guard let deadline = change.deadline else { return (0, 0) }
        let left = max(0, deadline.timeIntervalSince(now))
        let total = change.createdAt.map { deadline.timeIntervalSince($0) } ?? 30
        return (left, total > 0 ? min(1, left / total) : 0)
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
                    Text(hint).font(.system(size: 10.5)).foregroundStyle(.secondary)
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
