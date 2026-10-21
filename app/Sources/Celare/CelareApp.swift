import SwiftUI

@main
struct CelareApp: App {
    @State private var model: AppModel

    init() {
        let model = AppModel()
        _model = State(initialValue: model)
        if let i = CommandLine.arguments.firstIndex(of: "--render-icon"), i + 1 < CommandLine.arguments.count {
            exit(AppIconView.write(to: CommandLine.arguments[i + 1]) ? 0 : 1)
        }
        if let i = CommandLine.arguments.firstIndex(of: "--snapshot"), i + 1 < CommandLine.arguments.count {
            Snapshot.render(to: CommandLine.arguments[i + 1])
            exit(0)
        }
        Task { @MainActor in model.start() }
        // `Celare --open` opens the menu at launch, for checking the live
        // menu without clicking the menu bar.
        if CommandLine.arguments.contains("--open") {
            Task { @MainActor in
                for _ in 0..<40 {
                    try? await Task.sleep(for: .milliseconds(250))
                    if let button = NSApp.windows.lazy.compactMap({ $0.contentView?.firstSubview(of: NSStatusBarButton.self) }).first {
                        NSApp.activate()
                        button.performClick(nil)
                        return
                    }
                }
            }
        }
    }

    var body: some Scene {
        MenuBarExtra {
            MenuView(model: model)
        } label: {
            Image(nsImage: Mark.menuBarImage(connected: model.tunnel?.isUp == true))
        }
        .menuBarExtraStyle(.window)
    }
}

extension NSView {
    func firstSubview<T: NSView>(of type: T.Type) -> T? {
        for view in subviews {
            if let match = view as? T ?? view.firstSubview(of: type) { return match }
        }
        return nil
    }
}
