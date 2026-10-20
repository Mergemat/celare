import AppKit
import SwiftUI

/// `Celare --snapshot out.png [--dark] [--scenario name | --live]`
/// renders the menu to a PNG, for checking the design without clicking
/// through the menu bar. --live shows this Mac's real state.
@MainActor
enum Snapshot {
    static func render(to path: String) {
        let args = CommandLine.arguments
        let model = AppModel()
        if args.contains("--live") {
            model.loadSync()
        } else {
            let scenario = args.firstIndex(of: "--scenario").map { args[$0 + 1] } ?? "connected"
            fill(model, scenario: scenario)
        }
        let dark = args.contains("--dark")
        let view = MenuView(model: model, live: false)
            // Stands in for the popover's frosted material.
            .background(dark ? Color(white: 0.16) : Color(white: 0.93))
            .environment(\.colorScheme, dark ? .dark : .light)
        let renderer = ImageRenderer(content: view)
        renderer.scale = 2
        var rendered: NSImage?
        NSAppearance(named: dark ? .darkAqua : .aqua)!.performAsCurrentDrawingAppearance { rendered = renderer.nsImage }
        guard let image = rendered, let tiff = image.tiffRepresentation,
              let png = NSBitmapImageRep(data: tiff)?.representation(using: .png, properties: [:]) else {
            FileHandle.standardError.write("render failed\n".data(using: .utf8)!)
            return
        }
        try? png.write(to: URL(fileURLWithPath: path))
    }

    private static func fill(_ model: AppModel, scenario: String) {
        let nodes = [
            NodeView(name: "main/Tokyo", source: "subscription:main", protocol: "vless", address: "203.0.113.1", port: 443, transport: "xhttp", security: "reality"),
            NodeView(name: "main/Osaka", source: "subscription:main", protocol: "hysteria2", address: "203.0.113.2", port: 443, transport: nil, security: "tls"),
            NodeView(name: "home", source: "config", protocol: "trojan", address: "203.0.113.3", port: 8444, transport: "tcp", security: "tls"),
        ]
        func tunnel(up: Bool, pending: Change? = nil) -> Tunnel {
            Tunnel(state: up ? "up" : "down", node: up ? "main/Tokyo" : nil, since: Date().addingTimeInterval(-1800),
                   pending: pending, version: nil, lastNode: up ? "main/Tokyo" : nil, system: SystemInfo(interface: up ? "en0" : nil, tun: nil, foreignTunnels: nil))
        }
        model.nodesLoaded = true
        switch scenario {
        case "setup":
            model.serviceMissing = true
        case "connecting":
            model.tunnel = tunnel(up: false)
            model.nodes = nodes
            model.connecting = "main/Osaka"
            model.busy = "Connecting…"
        case "empty":
            model.tunnel = tunnel(up: false)
        case "down":
            model.tunnel = tunnel(up: false)
            model.nodes = nodes
        case "pending":
            model.tunnel = tunnel(up: true, pending: Change(id: "c1a2b3", action: "connect", state: "pending",
                from: Endpoint(node: "main/Osaka"), to: Endpoint(node: "main/Tokyo"), createdAt: Date().addingTimeInterval(-36), deadline: Date().addingTimeInterval(24), reason: nil))
            model.nodes = nodes
        default:
            model.tunnel = tunnel(up: true)
            model.nodes = nodes
            model.latency = [
                "main/Tokyo": TestResult(node: "main/Tokyo", ok: true, latencyMs: 142, error: nil),
                "main/Osaka": TestResult(node: "main/Osaka", ok: true, latencyMs: 188, error: nil),
                "home": TestResult(node: "home", ok: false, latencyMs: nil, error: "timeout"),
            ]
        }
    }
}
