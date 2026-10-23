import SwiftUI

struct MenuView: View {
    @Bindable var model: AppModel
    /// False for snapshots: don't start polling the daemon.
    var live = true
    @State private var adding = false
    @State private var input = ""
    @State private var filter = ""
    /// The footer icon under the pointer, named in the footer.
    @State private var hint: String?
    @State private var fitter = WindowFitter()
    @FocusState private var fieldFocused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            hero.padding(.bottom, 14)
            action
            if let removal = model.removal {
                RemovalNote(removal: removal, model: model).padding(.top, 10)
            }
            if let error = model.error {
                ErrorNote(failure: error) { model.error = nil }.padding(.top, 10)
            }
            if model.phase == .ready {
                nodes.padding(.top, 14)
            }
            footer.padding(.top, 10)
        }
        .padding(14)
        .frame(width: 300)
        .fixedSize(horizontal: false, vertical: true)
        // Height changes are instant: the window can't animate in step
        // with SwiftUI, so an animated change would show as a flicker.
        .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { fitter.fit(height: $0) }
        .background { if live { WindowReader(fitter: fitter) } }
        // Pinned to the top, so a window a pass behind its content shows
        // still space at the bottom rather than everything sliding.
        .frame(maxHeight: live ? .infinity : nil, alignment: .top)
        .onAppear { if live { model.start() } }
        // The window is kept between openings; becoming key is the menu opening.
        .onReceive(NotificationCenter.default.publisher(for: NSWindow.didBecomeKeyNotification)) { note in
            guard live, let window = fitter.window, note.object as? NSWindow === window else { return }
            Task { await model.measureIfStale() }
        }
    }

    // MARK: hero: the chosen node and its state

    private var isUp: Bool { model.tunnel?.isUp == true }

    /// The tunnel's state, under the node's name.
    private var stateLabel: String {
        if model.connecting != nil { return "Connecting…" }
        if let busy = model.busy, ["Disconnecting…", "Keeping…", "Reverting…"].contains(busy) { return busy }
        if model.tunnel?.pending != nil { return "Waiting for you" }
        return isUp ? "Connected" : "Not connected"
    }

    private var stateColor: Color {
        if model.connecting != nil || model.tunnel?.pending != nil { return .orange }
        return isUp ? Palette.blueDot : Color.secondary.opacity(0.5)
    }

    @ViewBuilder
    private var hero: some View {
        VStack(alignment: .leading, spacing: 3) {
            switch model.phase {
            case .loading:
                Text(" ").font(.system(size: 17, weight: .semibold))
                Text(" ").font(.system(size: 11.5))
            case .needsService:
                title("Set up celare")
                caption("A background service creates the tunnel and sets routes and DNS. Installing it asks for your password once.")
            case .needsNodes:
                title("Add your first node")
                caption("Paste the subscription URL your panel gives you, or share links. Several at once work too.")
            case .broken(let f):
                title("celare can't run")
                caption(f.message + (f.hint.map { ". " + $0 } ?? "")).textSelection(.enabled)
            case .ready:
                // Without its subscription: the list already groups by it.
                if let name = shownNode {
                    title(model.nodes.first { $0.name == name }?.shortName ?? name).lineLimit(1)
                } else {
                    title("Fastest node")
                }
                status
            }
        }
    }

    /// The node the hero names: the one in use, else the one Connect
    /// connects to. nil means Connect measures every node first.
    private var shownNode: String? {
        if let name = model.connecting { return name }
        if measuringToConnect { return nil }
        if isUp, let name = model.tunnel?.node { return name }
        return model.connectTarget
    }

    private var status: some View {
        HStack(spacing: 6) {
            Circle().fill(stateColor).frame(width: 6, height: 6)
            if stateLabel == "Connected", let t = model.tunnel {
                TimelineView(.periodic(from: .now, by: 15)) { context in
                    let ms = t.node.flatMap { model.latency[$0] }.flatMap { $0.ok ? $0.latencyMs : nil }
                    Text("Connected " + Self.duration(from: t.since, to: context.date) + (ms.map { " · \($0) ms" } ?? ""))
                }
                .help(t.system?.interface.map { "Leaves through \($0)" } ?? "")
            } else {
                Text(stateLabel)
            }
        }
        .font(.system(size: 11.5))
        .foregroundStyle(.secondary)
    }

    private func title(_ text: String) -> some View {
        Text(text).font(.system(size: 17, weight: .semibold))
    }

    private func caption(_ text: String) -> some View {
        Text(text).font(.system(size: 11.5)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
    }

    /// "just now", "for 4 min", "for 2 h 5 min".
    static func duration(from start: Date, to now: Date) -> String {
        let seconds = now.timeIntervalSince(start)
        guard seconds >= 60 else { return "just now" }
        let f = DateComponentsFormatter()
        f.unitsStyle = .short
        f.allowedUnits = seconds >= 86400 ? [.day, .hour] : [.hour, .minute]
        f.maximumUnitCount = 2
        return "for " + (f.string(from: seconds) ?? "")
    }

    /// Connect has to wait for measurements to know which node is fastest.
    private var measuringToConnect: Bool {
        model.findingFastest || (!isUp && model.connectTarget == nil && !model.testingNodes.isEmpty)
    }

    // MARK: the one loud control

    @ViewBuilder
    private var action: some View {
        switch model.phase {
        case .loading, .broken:
            EmptyView()
        case .needsService:
            Button { Task { await model.installService() } } label: { Label("Install background service", systemImage: "lock") }
                .buttonStyle(PrimaryBarStyle())
                .disabled(model.busy != nil)
        case .needsNodes:
            addField(prominent: true)
        case .ready:
            if model.connecting != nil {
                // The status line says what's happening.
                Button {} label: { Spinner(size: .small).tint(.white) }
                    .buttonStyle(PrimaryBarStyle())
                    .allowsHitTesting(false)
            } else if measuringToConnect {
                let done = model.testingTotal - model.testingNodes.count
                Button {} label: {
                    HStack(spacing: 6) {
                        Text("Measuring")
                        Text("\(done) of \(model.testingTotal)").font(.mono).opacity(0.75)
                    }
                }
                .buttonStyle(PrimaryBarStyle())
                .overlay(alignment: .leading) { BarFill(fraction: Double(done) / Double(max(model.testingTotal, 1))) }
                .allowsHitTesting(false)
            } else if let pending = model.tunnel?.pending {
                PendingBar(change: pending, model: model)
            } else if isUp {
                Button { Task { await model.disconnect() } } label: { Label("Disconnect", systemImage: "power") }
                    .buttonStyle(PrimaryBarStyle())
                    .disabled(model.busy != nil)
            } else {
                Button {
                    Task {
                        if let target = model.connectTarget { await model.connect(target) } else { await model.connectFastest() }
                    }
                } label: { Label("Connect", systemImage: "power") }
                    .buttonStyle(PrimaryBarStyle())
                    .disabled(model.busy != nil)
            }
            if model.serviceOutdated {
                quietNote("The background service is older than this app.", action: "Update") {
                    Task { await model.installService() }
                }
            }
        }
    }

    private func addField(prominent: Bool) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 4) {
                TextField("Subscription URL or share link", text: $input)
                    .textFieldStyle(.plain)
                    .font(.mono)
                    .focused($fieldFocused)
                    .onSubmit(submit)
                if input.isEmpty {
                    Button(action: pasteFromClipboard) { Image(systemName: "doc.on.clipboard") }
                        .buttonStyle(.plain)
                        .foregroundStyle(.secondary)
                        .help("Paste")
                } else {
                    Button { input = "" } label: { Image(systemName: "xmark.circle.fill") }
                        .buttonStyle(.plain)
                        .foregroundStyle(.tertiary)
                        .help("Clear")
                }
            }
            .padding(.horizontal, 9)
            .frame(height: 28)
            .background(RoundedRectangle(cornerRadius: 7).fill(Color.primary.opacity(0.06)))
            .overlay(RoundedRectangle(cornerRadius: 7).strokeBorder(Color.primary.opacity(fieldFocused ? 0.25 : 0.1), lineWidth: 0.5))
            Text(addCaption)
                .font(.system(size: 10.5)).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            if prominent {
                Button(action: submit) { Label(addTitle, systemImage: "plus") }
                    .buttonStyle(PrimaryBarStyle())
                    .disabled(Links.find(in: input).isEmpty || model.busy != nil)
                    .padding(.top, 2)
            } else {
                HStack {
                    Spacer()
                    Button("Cancel") { closeAdding() }
                        .buttonStyle(HoverRaiseStyle()).font(.system(size: 11, weight: .medium))
                    Button(addTitle, action: submit)
                        .buttonStyle(HoverRaiseStyle()).font(.system(size: 11, weight: .semibold))
                        .foregroundStyle(Palette.blueDot)
                        .disabled(Links.find(in: input).isEmpty || model.busy != nil)
                }
                .padding(.horizontal, -8)
            }
        }
    }

    /// Says what Add will do with what's in the field.
    private var addCaption: String {
        let links = Links.find(in: input)
        if links.count > 1 { return "\(links.count) links." }
        if let link = links.first {
            if link.hasPrefix("http") { return "A subscription from \(URL(string: link)?.host() ?? "your panel"). Its nodes stay up to date." }
            let scheme = String(link.prefix { $0 != ":" })
            return "One \(Names.protocol(scheme == "hy2" ? "hysteria2" : scheme)) node."
        }
        return "https:// from your panel, or vless://, vmess://, trojan://, ss://, hysteria2://, wireguard://"
    }

    private var addTitle: String {
        let links = Links.find(in: input)
        if links.count > 1 { return "Add \(links.count)" }
        return links.first?.hasPrefix("http") == true ? "Add subscription" : "Add"
    }

    private func pasteFromClipboard() {
        let links = Links.onClipboard()
        guard !links.isEmpty else { return }
        input = links.joined(separator: " ")
    }

    private func openAdding() {
        adding = true
        fieldFocused = true
    }

    private func closeAdding() {
        adding = false
        input = ""
    }

    private func submit() {
        let text = input
        Task {
            if await model.add(text) { closeAdding() }
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
            if adding { addField(prominent: false).padding(.bottom, 8) }
            if model.nodes.count > 8 { search.padding(.bottom, 2) }
            let groups = model.groups(matching: filter)
            Group {
                if groups.isEmpty {
                    Text("Nothing matches “\(filter)”.").font(.system(size: 11.5)).foregroundStyle(.secondary)
                        .padding(.horizontal, 8).padding(.vertical, 6)
                } else if model.nodes.count > 8 {
                    if live {
                        ScrollView { rows(groups) }.frame(maxHeight: 300)
                    } else {
                        // ImageRenderer draws a ScrollView empty.
                        rows(groups).frame(maxHeight: 300, alignment: .top).clipped()
                    }
                } else {
                    rows(groups)
                }
            }
            // Rows bleed into the margin so their dots line up with the
            // text above; the hover surface still has room around them.
            .padding(.horizontal, -8)
        }
    }

    private var search: some View {
        HStack(spacing: 6) {
            Image(systemName: "magnifyingglass").font(.system(size: 10.5, weight: .medium)).foregroundStyle(.tertiary)
            TextField("Filter \(model.nodes.count) nodes", text: $filter)
                .textFieldStyle(.plain).font(.system(size: 11.5))
            if !filter.isEmpty {
                Button { filter = "" } label: { Image(systemName: "xmark.circle.fill") }
                    .buttonStyle(.plain).foregroundStyle(.tertiary)
            }
        }
        .padding(.horizontal, 8)
        .frame(height: 24)
        .background(RoundedRectangle(cornerRadius: 6).fill(Color.primary.opacity(0.05)))
    }

    /// Nodes added by link come first; each subscription gets
    /// a heading with what its panel reports.
    private func rows(_ groups: [NodeGroup]) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            ForEach(groups) { group in
                if group.subscription != nil {
                    GroupHeader(group: group, subscription: group.subscription.flatMap(model.subscription(named:)), model: model)
                        .padding(.top, group == groups.first ? 0 : 8)
                }
                ForEach(group.nodes) { node in
                    NodeRow(node: node,
                            chosen: shownNode == node.name,
                            connected: isUp,
                            connecting: model.connecting == node.name,
                            testing: model.testingNodes.contains(node.name),
                            result: model.latency[node.name],
                            fastest: model.fastest?.node == node.name,
                            model: model)
                        .disabled(model.busy != nil || model.connecting != nil)
                }
            }
        }
    }

    // MARK: footer

    private var footer: some View {
        HStack(spacing: 2) {
            if model.phase == .ready {
                footerIcon(adding ? "xmark" : "plus", help: adding ? "Cancel" : "Add nodes") {
                    adding ? closeAdding() : openAdding()
                }
                if !model.subscriptions.isEmpty {
                    footerIcon("arrow.clockwise", help: "Update subscriptions", working: model.busy == "Updating…") {
                        Task { await model.updateSubscriptions() }
                    }
                }
                footerIcon("gauge.with.needle", help: "Measure every node", working: !model.testingNodes.isEmpty) {
                    Task { await model.testNodes() }
                }
                .disabled(!model.testingNodes.isEmpty)
            }
            Text(hint ?? model.notice ?? "")
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.tail)
                .padding(.leading, 6)
                .animation(nil, value: hint)
                .contentTransition(.opacity)
            Spacer(minLength: 4)
            if live {
                Menu {
                    Button("Open config.toml") { Task { await model.openConfig() } }
                    Button("Copy CLI path") {
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(model.cli.binary.path, forType: .string)
                        model.say("Copied \(model.cli.binary.path)")
                    }
                    Toggle("Sort by Speed", isOn: $model.sortBySpeed)
                    Toggle("Open at Login", isOn: $model.opensAtLogin)
                    Divider()
                    Button("Quit celare") { NSApplication.shared.terminate(nil) }
                } label: {
                    footerGlyph("ellipsis")
                }
                // A button-styled menu takes the label as drawn, so it
                // matches the icons beside it instead of AppKit's tint.
                .menuStyle(.button)
                .buttonStyle(HoverRaiseStyle(horizontal: 7, vertical: 5))
                .menuIndicator(.hidden)
                .fixedSize()
                .onHover { hint = $0 ? "More" : nil }
            } else {
                // ImageRenderer can't draw an AppKit menu; show its icon.
                footerGlyph("ellipsis").padding(.horizontal, 7).padding(.vertical, 5)
            }
        }
        .frame(height: 26)
        .padding(.horizontal, -7)
        .disabled(model.busy != nil)
    }

    private func footerGlyph(_ symbol: String) -> some View {
        Image(systemName: symbol)
            .font(.system(size: 12, weight: .medium))
            .foregroundStyle(.secondary)
            .frame(width: 16, height: 16)
    }

    private func footerIcon(_ symbol: String, help: String, working: Bool = false,
                            action: @escaping () -> Void) -> some View {
        Button(action: action) {
            if working {
                Spinner().frame(width: 16, height: 16)
            } else {
                footerGlyph(symbol)
            }
        }
        .buttonStyle(HoverRaiseStyle(horizontal: 7, vertical: 5))
        .onHover { hint = $0 ? help : nil }
    }
}

/// The menu bar window grows with its content but never shrinks back on
/// its own. This keeps it exactly the content's height, top edge under the
/// bar, resizing in the same pass that measured the content so the two
/// never draw out of step.
@MainActor
final class WindowFitter {
    weak var window: NSWindow?
    private var height: CGFloat = 0

    func fit(height: CGFloat) {
        self.height = height
        fit()
    }

    func fit() {
        guard height > 0, let window else { return }
        let content = window.contentRect(forFrameRect: window.frame)
        guard abs(content.height - height) > 0.5 else { return }
        let fitted = NSRect(x: content.minX, y: content.maxY - height, width: content.width, height: height)
        window.setFrame(window.frameRect(forContentRect: fitted), display: true, animate: false)
    }
}

/// Hands the fitter its window the moment the view lands in one.
struct WindowReader: NSViewRepresentable {
    let fitter: WindowFitter

    func makeNSView(context: Context) -> NSView { Reader(fitter: fitter) }
    func updateNSView(_ view: NSView, context: Context) {}

    private final class Reader: NSView {
        let fitter: WindowFitter

        init(fitter: WindowFitter) {
            self.fitter = fitter
            super.init(frame: .zero)
        }

        required init?(coder: NSCoder) { fatalError() }

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            fitter.window = window
            fitter.fit()
        }
    }
}
