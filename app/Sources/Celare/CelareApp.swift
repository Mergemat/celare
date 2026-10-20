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
