import SwiftUI

@main
struct CelareApp: App {
    @State private var model: AppModel

    init() {
        let model = AppModel()
        _model = State(initialValue: model)
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
            Image(systemName: icon)
        }
        .menuBarExtraStyle(.window)
    }

    private var icon: String {
        if model.tunnel?.pending != nil { return "shield.lefthalf.filled.badge.checkmark" }
        return model.tunnel?.isUp == true ? "shield.fill" : "shield"
    }
}
