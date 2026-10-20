import AppKit
import SwiftUI

/// `Celare --snapshot out.png` renders the menu with sample data, for
/// checking the design without clicking through the menu bar.
@MainActor
enum Snapshot {
    static func render(to path: String) {
        let model = AppModel()
        model.tunnel = Tunnel(
            state: "up", node: "main/Tokyo", since: Date().addingTimeInterval(-1800),
            pending: Change(id: "c1a2b3", action: "connect", state: "pending", from: Endpoint(node: "main/Osaka"),
                            to: Endpoint(node: "main/Tokyo"), deadline: Date().addingTimeInterval(24), reason: nil),
            version: "0.1.0",
            system: SystemInfo(interface: "en0", tun: "utun8", foreignTunnels: nil))
        model.nodes = [
            NodeView(name: "main/Tokyo", source: "subscription:main", protocol: "vless", address: "203.0.113.1", port: 443, transport: "xhttp", security: "reality"),
            NodeView(name: "main/Osaka", source: "subscription:main", protocol: "hysteria2", address: "203.0.113.2", port: 443, transport: nil, security: "tls"),
            NodeView(name: "home", source: "config", protocol: "trojan", address: "203.0.113.3", port: 8444, transport: "tcp", security: "tls"),
        ]
        model.latency = [
            "main/Tokyo": TestResult(node: "main/Tokyo", ok: true, latencyMs: 142, error: nil),
            "main/Osaka": TestResult(node: "main/Osaka", ok: true, latencyMs: 188, error: nil),
            "home": TestResult(node: "home", ok: false, latencyMs: nil, error: "timeout"),
        ]
        let dark = CommandLine.arguments.contains("--dark")
        let view = MenuView(model: model, live: false)
            .background(Color(nsColor: .windowBackgroundColor))
            .environment(\.colorScheme, dark ? .dark : .light)
        let renderer = ImageRenderer(content: view)
        renderer.scale = 2
        let appearance = NSAppearance(named: dark ? .darkAqua : .aqua)!
        var rendered: NSImage?
        appearance.performAsCurrentDrawingAppearance { rendered = renderer.nsImage }
        guard let image = rendered, let tiff = image.tiffRepresentation,
              let png = NSBitmapImageRep(data: tiff)?.representation(using: .png, properties: [:]) else {
            FileHandle.standardError.write("render failed\n".data(using: .utf8)!)
            return
        }
        try? png.write(to: URL(fileURLWithPath: path))
    }
}
