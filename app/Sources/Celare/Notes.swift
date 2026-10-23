import SwiftUI

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
                    .overlay(alignment: .leading) { BarFill(fraction: left.fraction) }
                    Button("Revert") { Task { await model.revert(change.id) } }
                        .buttonStyle(RaisedButtonStyle())
                }
                Text(explanation)
                    .font(.system(size: 11)).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    private var explanation: String {
        let back = change.from.map { "goes back to \($0.node)" } ?? "disconnects"
        return "A trial change from the CLI. Unless you keep it, celare \(back)."
    }

    private func remaining(at now: Date) -> (seconds: Double, fraction: Double) {
        guard let deadline = change.deadline else { return (0, 0) }
        let left = max(0, deadline.timeIntervalSince(now))
        let total = change.createdAt.map { deadline.timeIntervalSince($0) } ?? 30
        return (left, total > 0 ? min(1, left / total) : 0)
    }
}

/// A soft tinted card, for things that need reading before moving on.
struct NoteCard<Content: View>: View {
    let tint: Color
    @ViewBuilder let content: Content

    var body: some View {
        content
            .padding(.horizontal, 10)
            .padding(.vertical, 8)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(RoundedRectangle(cornerRadius: 8, style: .continuous).fill(tint.opacity(0.09)))
            .overlay(RoundedRectangle(cornerRadius: 8, style: .continuous).strokeBorder(tint.opacity(0.18), lineWidth: 0.5))
    }
}

struct ErrorNote: View {
    let failure: CLI.Failure
    let dismiss: () -> Void

    var body: some View {
        NoteCard(tint: .red) {
            HStack(alignment: .top, spacing: 7) {
                Image(systemName: "exclamationmark.triangle.fill")
                    .font(.system(size: 10.5))
                    .foregroundStyle(.red)
                    .padding(.top, 1)
                VStack(alignment: .leading, spacing: 2) {
                    Text(failure.message).font(.system(size: 11.5, weight: .medium))
                    if let hint = failure.hint {
                        Text(hint).font(.system(size: 11)).foregroundStyle(.secondary)
                    }
                }
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
                Spacer(minLength: 0)
                Button(action: dismiss) { Image(systemName: "xmark") }
                    .buttonStyle(.plain)
                    .font(.system(size: 9, weight: .bold))
                    .foregroundStyle(.secondary)
                    .help("Dismiss")
            }
        }
    }
}

/// Asks before removing, in place, so the menu never loses focus to an alert.
struct RemovalNote: View {
    let removal: Removal
    let model: AppModel

    var body: some View {
        NoteCard(tint: .orange) {
            VStack(alignment: .leading, spacing: 8) {
                Text(question).font(.system(size: 11.5, weight: .medium)).fixedSize(horizontal: false, vertical: true)
                HStack(spacing: 6) {
                    Spacer()
                    Button("Cancel") { model.removal = nil }
                        .buttonStyle(HoverRaiseStyle()).font(.system(size: 11, weight: .medium))
                    Button("Remove") { Task { await model.remove(removal) } }
                        .buttonStyle(RaisedButtonStyle(height: 24)).font(.system(size: 11, weight: .semibold))
                        .foregroundStyle(.red)
                }
            }
        }
    }

    private var question: String {
        switch removal {
        case .node(let name):
            return "Remove \(name)?"
        case .subscription(let name):
            let count = model.nodes.filter { $0.subscription == name }.count
            return "Remove the subscription \(name) and its \(count) node\(count == 1 ? "" : "s")?"
        }
    }
}
