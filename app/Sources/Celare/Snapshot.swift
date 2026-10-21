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
            .environment(\.snapshot, true)
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
        var nodes = [
            NodeView(name: "home", source: "config", protocol: "trojan", address: "203.0.113.3", port: 8444, transport: "tcp", security: "tls"),
            NodeView(name: "main/Tokyo", source: "subscription:main", protocol: "vless", address: "203.0.113.1", port: 443, transport: "xhttp", security: "reality"),
            NodeView(name: "main/Osaka", source: "subscription:main", protocol: "hysteria2", address: "203.0.113.2", port: 443, transport: nil, security: "tls"),
        ]
        if scenario == "many" {
            for city in ["Frankfurt", "Amsterdam", "Helsinki", "Warsaw", "Istanbul", "Dubai", "Singapore", "Seoul", "Toronto"] {
                nodes.append(NodeView(name: "main/\(city)", source: "subscription:main", protocol: "vless", address: "203.0.113.9", port: 443, transport: "tcp", security: "reality"))
            }
        }
        model.subscriptions = [Subscription(name: "main", nodes: nodes.count - 1, updatedAt: Date().addingTimeInterval(-7200),
            usage: Usage(uploadBytes: 343_343_011, downloadBytes: 7_019_122_251, totalBytes: 50_000_000_000,
                         expire: Int64(Date().addingTimeInterval(12 * 86400).timeIntervalSince1970)))]
        func tunnel(up: Bool, last: String? = "main/Tokyo", pending: Change? = nil) -> Tunnel {
            Tunnel(state: up ? "up" : "down", node: up ? "main/Tokyo" : nil, since: Date().addingTimeInterval(-1940),
                   pending: pending, version: nil, lastNode: last, system: SystemInfo(interface: up ? "en0" : nil, tun: nil))
        }
        let measured = [
            "main/Tokyo": TestResult(node: "main/Tokyo", ok: true, latencyMs: 142, error: nil),
            "main/Osaka": TestResult(node: "main/Osaka", ok: true, latencyMs: 388, error: nil),
            "home": TestResult(node: "home", ok: false, latencyMs: nil, error: "timeout"),
        ]
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
            model.tunnel = tunnel(up: false, last: nil)
        case "down":
            model.tunnel = tunnel(up: false)
            model.nodes = nodes
        case "fresh":
            model.tunnel = tunnel(up: false, last: nil)
            model.nodes = nodes
        case "fastest":
            model.tunnel = tunnel(up: false, last: nil)
            model.nodes = nodes
            model.latency = measured
            model.sortBySpeed = true
            model.notice = "2 of 3 answered"
        case "measuring":
            model.tunnel = tunnel(up: false, last: nil)
            model.nodes = nodes
            model.findingFastest = true
            model.latency = ["main/Tokyo": measured["main/Tokyo"]!]
            model.testingNodes = ["main/Osaka", "home"]
        case "remove":
            model.tunnel = tunnel(up: true)
            model.nodes = nodes
            model.removal = .subscription("main")
        case "error":
            model.tunnel = tunnel(up: false)
            model.nodes = nodes
            model.error = CLI.Failure(code: "probe_failed", message: "main/Osaka didn't pass traffic, so celare went back",
                                      hint: "Try another node, or measure them all.")
        case "pending":
            model.tunnel = tunnel(up: true, pending: Change(id: "c1a2b3", action: "connect", state: "pending",
                from: Endpoint(node: "main/Osaka"), to: Endpoint(node: "main/Tokyo"), createdAt: Date().addingTimeInterval(-36), deadline: Date().addingTimeInterval(24), reason: nil))
            model.nodes = nodes
        default:
            model.tunnel = tunnel(up: true)
            model.nodes = nodes
            model.latency = measured
        }
    }
}
