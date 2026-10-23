import SwiftUI

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

/// A subscription's name with what the panel reports about it: traffic
/// used, the quota and when it runs out.
struct GroupHeader: View {
    let group: NodeGroup
    let subscription: Subscription?
    let model: AppModel

    var body: some View {
        HStack(spacing: 6) {
            Text(group.subscription ?? "")
                .font(.system(size: 11, weight: .semibold))
                .foregroundStyle(.secondary)
                .lineLimit(1)
            Spacer(minLength: 4)
            if let usage = subscription?.usage {
                Text(Self.usage(usage, now: .now))
                    .font(.system(size: 10.5))
                    .foregroundStyle(Self.runningOut(usage, now: .now) ? AnyShapeStyle(Color.orange) : AnyShapeStyle(.tertiary))
                    .lineLimit(1)
            }
        }
        .padding(.horizontal, 8)
        .padding(.bottom, 2)
        .help(details)
        .contextMenu {
            if let name = group.subscription {
                Button("Update \(name)") { Task { await model.updateSubscriptions([name]) } }
                Divider()
                Button("Remove \(name)…", role: .destructive) { model.removal = .subscription(name) }
            }
        }
    }

    private var details: String {
        guard let sub = subscription else { return "" }
        var lines = ["\(sub.nodes) node\(sub.nodes == 1 ? "" : "s")"]
        if let at = sub.updatedAt {
            lines.append("Updated " + at.formatted(.relative(presentation: .named)))
        }
        if let usage = sub.usage {
            lines.append("↑ \(Self.bytes(usage.uploadBytes))  ↓ \(Self.bytes(usage.downloadBytes))")
            if let end = usage.expiresAt { lines.append("Expires " + end.formatted(date: .abbreviated, time: .omitted)) }
        }
        return lines.joined(separator: "\n")
    }

    /// "7.3 GB used", "7.3 of 50 GB · 12 days left", "expired".
    static func usage(_ u: Usage, now: Date) -> String {
        var parts = [u.totalBytes > 0 ? "\(bytes(u.usedBytes)) of \(bytes(u.totalBytes))" : "\(bytes(u.usedBytes)) used"]
        if let end = u.expiresAt {
            let days = Int((end.timeIntervalSince(now) / 86400).rounded(.up))
            parts.append(end < now ? "expired" : days <= 1 ? "ends today" : "\(days) day\(days == 1 ? "" : "s") left")
        }
        return parts.joined(separator: " · ")
    }

    /// Under a tenth of the quota or under three days left.
    static func runningOut(_ u: Usage, now: Date) -> Bool {
        if u.totalBytes > 0, Double(u.usedBytes) > Double(u.totalBytes) * 0.9 { return true }
        if let end = u.expiresAt, end.timeIntervalSince(now) < 3 * 86400 { return true }
        return false
    }

    static func bytes(_ n: Int64) -> String {
        n.formatted(.byteCount(style: .decimal, spellsOutZero: false))
    }
}

/// A node: click to choose it, and while connected the tunnel follows.
/// The trailing column says how fast it was.
struct NodeRow: View {
    let node: NodeView
    /// Under its subscription's heading, so the prefix goes.
    let shortened: Bool
    /// The node the hero names.
    let chosen: Bool
    let connected: Bool
    let connecting: Bool
    let testing: Bool
    let result: TestResult?
    let model: AppModel

    var body: some View {
        Button { Task { await model.choose(node.name) } } label: {
            HStack(spacing: 8) {
                leading.frame(width: 8, height: 8)
                if shortened {
                    Text(node.shortName).font(.system(size: 12.5, weight: .medium)).lineLimit(1)
                } else {
                    NodeName(name: node.name)
                }
                Spacer(minLength: 6)
                trailing
            }
            // Fixed, so swapping what's on the right never moves a row.
            .frame(maxWidth: .infinity, minHeight: 16, maxHeight: 16, alignment: .leading)
        }
        .buttonStyle(HoverRaiseStyle(radius: 7, horizontal: 8, vertical: 6))
        .help(tooltip)
        .contextMenu {
            Button(chosen && connected ? "Reconnect" : "Connect") { Task { await model.connect(node.name) } }
            Button("Measure") { Task { await model.testNodes([node.name]) } }
                .disabled(!model.testingNodes.isEmpty)
            Divider()
            Button("Copy Name") { copy(node.name) }
            Button("Copy Address") { copy("\(node.address):\(node.port)") }
            Divider()
            if let sub = node.subscription {
                Button("Remove Subscription \(sub)…", role: .destructive) { model.removal = .subscription(sub) }
            } else {
                Button("Remove \(node.name)…", role: .destructive) { model.removal = .node(node.name) }
            }
        }
    }

    @ViewBuilder
    private var leading: some View {
        if connecting {
            Spinner().scaleEffect(0.7)
        } else {
            Circle().fill(chosen ? (connected ? Palette.blueDot : Color.secondary.opacity(0.5)) : Color.clear)
                .frame(width: 6, height: 6)
        }
    }

    @ViewBuilder
    private var trailing: some View {
        if testing {
            Spinner().scaleEffect(0.8).frame(height: 12)
        } else if let result, result.ok {
            Text("\(result.latencyMs ?? 0) ms").font(.mono).foregroundStyle(.secondary)
        } else if result != nil {
            Text("no answer").font(.mono).foregroundStyle(Color.red.opacity(0.85))
        } else {
            Text(Names.protocol(node.protocol)).font(.system(size: 11)).foregroundStyle(.tertiary)
        }
    }

    private var tooltip: String {
        var lines = [node.name, node.summary, "\(node.address):\(node.port)"]
        if let error = result?.error, result?.ok == false { lines.append("Last test: \(error)") }
        return lines.joined(separator: "\n")
    }

    private func copy(_ text: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
        model.say("Copied \(text)")
    }
}
