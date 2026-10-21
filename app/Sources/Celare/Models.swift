import Foundation

struct Endpoint: Decodable, Sendable, Hashable {
    let node: String
}

struct Change: Decodable, Sendable, Identifiable, Hashable {
    let id: String
    let action: String
    let state: String
    let from: Endpoint?
    let to: Endpoint?
    let createdAt: Date?
    let deadline: Date?
    let reason: String?
}

struct SystemInfo: Decodable, Sendable, Hashable {
    let interface: String?
    let tun: String?
}

struct Tunnel: Decodable, Sendable, Hashable {
    let state: String
    let node: String?
    let since: Date
    let pending: Change?
    let version: String?
    let lastNode: String?
    let system: SystemInfo?

    var isUp: Bool { state == "up" }
}

struct NodeView: Decodable, Sendable, Identifiable, Hashable {
    let name: String
    let source: String
    let `protocol`: String
    let address: String
    let port: Int
    let transport: String?
    let security: String?

    var id: String { name }
}

struct TestResult: Decodable, Sendable, Hashable {
    let node: String
    let ok: Bool
    let latencyMs: Int?
    let error: String?
}

struct Empty: Decodable, Sendable {}

struct Usage: Decodable, Sendable, Hashable {
    let uploadBytes: Int64
    let downloadBytes: Int64
    let totalBytes: Int64
    /// Unix time; 0 means it never expires.
    let expire: Int64

    var usedBytes: Int64 { uploadBytes + downloadBytes }
    var expiresAt: Date? { expire > 0 ? Date(timeIntervalSince1970: TimeInterval(expire)) : nil }
}

struct Subscription: Decodable, Sendable, Identifiable, Hashable {
    let name: String
    let nodes: Int
    let updatedAt: Date?
    let usage: Usage?

    var id: String { name }
}

extension NodeView {
    /// The subscription this node came from, nil for nodes added by link.
    var subscription: String? {
        guard source.hasPrefix("subscription:") else { return nil }
        return String(source.dropFirst("subscription:".count))
    }

    /// "main/Tokyo" → "Tokyo", for rows already under their subscription.
    var shortName: String {
        guard let sub = subscription, name.hasPrefix(sub + "/") else { return name }
        return String(name.dropFirst(sub.count + 1))
    }

    /// "Trojan · TCP · TLS", the way people write them.
    var summary: String {
        [Names.protocol(`protocol`), transport.flatMap(Names.transport), security.flatMap(Names.security)]
            .compactMap { $0 }.joined(separator: " · ")
    }
}

/// Display names for the protocol vocabulary the CLI prints in lowercase.
enum Names {
    static func `protocol`(_ p: String) -> String {
        switch p {
        case "vless": "VLESS"
        case "vmess": "VMess"
        case "trojan": "Trojan"
        case "shadowsocks", "ss": "Shadowsocks"
        case "hysteria2", "hy2": "Hysteria2"
        case "wireguard": "WireGuard"
        default: p
        }
    }

    static func transport(_ t: String) -> String? {
        switch t {
        case "", "tcp", "raw": nil
        case "ws": "WebSocket"
        case "grpc": "gRPC"
        case "xhttp", "splithttp": "XHTTP"
        case "httpupgrade": "HTTPUpgrade"
        case "kcp", "mkcp": "mKCP"
        case "quic": "QUIC"
        default: t
        }
    }

    static func security(_ s: String) -> String? {
        switch s {
        case "", "none": nil
        case "tls": "TLS"
        case "reality": "REALITY"
        default: s
        }
    }
}
