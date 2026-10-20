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
    let foreignTunnels: [String]?
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
