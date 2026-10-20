import SwiftUI

struct MenuView: View {
    @Bindable var model: AppModel
    /// False for snapshots: don't start polling the daemon.
    var live = true

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            header
            if let pending = model.tunnel?.pending {
                PendingBanner(change: pending, model: model)
            }
            if let foreign = model.tunnel?.system?.foreignTunnels, !foreign.isEmpty {
                Text("Another VPN holds the default route (\(foreign.joined(separator: ", "))). Turn it off.")
                    .font(.caption)
                    .foregroundStyle(.orange)
            }
            if model.daemonMissing {
                installPrompt
            } else {
                nodeList
            }
            if let error = model.error {
                VStack(alignment: .leading, spacing: 2) {
                    Text(error.message).font(.caption).foregroundStyle(.red)
                    if let hint = error.hint { Text(hint).font(.caption2).foregroundStyle(.secondary) }
                }
                .textSelection(.enabled)
            }
            Divider()
            footer
        }
        .padding(14)
        .frame(width: 320)
        .onAppear { if live { model.start() } }
    }

    private var header: some View {
        HStack(spacing: 12) {
            Button {
                Task { model.tunnel?.isUp == true ? await model.disconnect() : await model.connect(nil) }
            } label: {
                Image(systemName: "power")
            }
            .buttonStyle(PowerButtonStyle(on: model.tunnel?.isUp == true))
            .disabled(model.daemonMissing || model.busy != nil)

            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.system(size: 15, weight: .semibold))
                Text(subtitle).font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
        }
    }

    private var title: String {
        if let busy = model.busy { return busy }
        guard let tunnel = model.tunnel else { return model.daemonMissing ? "Not installed" : "celare" }
        return tunnel.isUp ? (tunnel.node ?? "Connected") : "Disconnected"
    }

    private var subtitle: String {
        guard let tunnel = model.tunnel else { return "" }
        let since = tunnel.since.formatted(date: .omitted, time: .shortened)
        if tunnel.isUp, let iface = tunnel.system?.interface {
            return "since \(since) · via \(iface)"
        }
        return "since \(since)"
    }

    private var nodeList: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("NODES").font(.caption2.weight(.semibold)).foregroundStyle(.secondary)
            if model.nodes.isEmpty {
                Text("No nodes yet. Add one with `celare nodes add` or a subscription with `celare subs add`.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            if model.nodes.count > 6 {
                ScrollView { rows.padding(.vertical, 2) }
                    .frame(maxHeight: 260)
            } else {
                rows
            }
        }
    }

    private var rows: some View {
        VStack(spacing: 6) {
            ForEach(model.nodes) { node in
                NodeRow(node: node, active: model.tunnel?.isUp == true && model.tunnel?.node == node.name,
                        result: model.latency[node.name]) {
                    Task { await model.connect(node.name) }
                }
                .disabled(model.busy != nil)
            }
        }
    }

    private var installPrompt: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("The background service isn't installed. It needs your password once, to create the tunnel and set routes and DNS.")
                .font(.caption)
                .foregroundStyle(.secondary)
            Button("Install background service") { Task { await model.installService() } }
                .buttonStyle(ConvexButtonStyle(prominent: true))
        }
    }

    private var footer: some View {
        HStack(spacing: 6) {
            Button("Test") { Task { await model.testNodes() } }
            Button("Update") { Task { await model.updateSubscriptions() } }
            Button("Config") { Task { await model.openConfig() } }
            Spacer()
            Button("Quit") { NSApplication.shared.terminate(nil) }
        }
        .buttonStyle(ConvexButtonStyle())
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
            HStack {
                VStack(alignment: .leading, spacing: 1) {
                    Text(node.name).font(.system(size: 12, weight: .medium)).lineLimit(1)
                    Text([node.protocol, node.transport, node.security].compactMap { $0 }.joined(separator: " · "))
                        .font(.caption2)
                        .opacity(0.7)
                }
                Spacer()
                if let result {
                    Text(result.ok ? "\(result.latencyMs ?? 0) ms" : "failed")
                        .font(.caption2.monospacedDigit())
                        .foregroundStyle(result.ok ? AnyShapeStyle(.primary.opacity(0.8)) : AnyShapeStyle(Color.red.opacity(0.85)))
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
            HStack(spacing: 8) {
                VStack(alignment: .leading, spacing: 1) {
                    Text("Change \(change.id) pending").font(.caption.weight(.semibold))
                    if let deadline = change.deadline {
                        let left = max(0, Int(deadline.timeIntervalSince(context.date)))
                        Text("reverts to \(change.from?.node ?? "down") in \(left)s").font(.caption2).foregroundStyle(.secondary)
                    }
                }
                Spacer()
                Button("Revert") { Task { await model.revert(change.id) } }
                    .buttonStyle(ConvexButtonStyle())
                Button("Keep") { Task { await model.confirm(change.id) } }
                    .buttonStyle(ConvexButtonStyle(prominent: true))
            }
        }
    }
}
