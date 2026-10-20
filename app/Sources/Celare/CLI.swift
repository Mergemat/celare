import Foundation

/// The app drives the bundled `celare` binary with `--json`, exactly as an
/// agent does, so it can never do something the CLI can't.
struct CLI: Sendable {
    let binary: URL

    static func locate() -> CLI {
        if let bundled = Bundle.main.url(forResource: "celare", withExtension: nil) {
            return CLI(binary: bundled)
        }
        for path in ["/opt/homebrew/bin/celare", "/usr/local/bin/celare"] where FileManager.default.isExecutableFile(atPath: path) {
            return CLI(binary: URL(fileURLWithPath: path))
        }
        return CLI(binary: URL(fileURLWithPath: "/usr/local/bin/celare"))
    }

    struct Failure: Error, Decodable, Sendable {
        let code: String
        let message: String
        let hint: String?
    }

    private struct Envelope<T: Decodable>: Decodable {
        let ok: Bool
        let data: T?
        let error: Failure?
    }

    func run<T: Decodable & Sendable>(_ args: [String], as: T.Type = T.self) async throws -> T {
        let binary = self.binary
        return try await Task.detached {
            let process = Process()
            process.executableURL = binary
            process.arguments = args + ["--json"]
            let out = Pipe()
            process.standardOutput = out
            process.standardError = FileHandle.nullDevice
            try process.run()
            let data = out.fileHandleForReading.readDataToEndOfFile()
            process.waitUntilExit()
            let envelope = try JSONDecoder.celare.decode(Envelope<T>.self, from: data)
            if let error = envelope.error { throw error }
            guard let value = envelope.data else {
                throw Failure(code: "empty", message: "celare returned no data", hint: nil)
            }
            return value
        }.value
    }

    /// Runs `celare service install` as root through the system's admin
    /// password prompt.
    func installService() async throws {
        let command = "'\(binary.path)' service install --uid \(getuid()) --json"
        let script = "do shell script \"\(command)\" with administrator privileges"
        try await Task.detached {
            var error: NSDictionary?
            NSAppleScript(source: script)?.executeAndReturnError(&error)
            if let error {
                throw Failure(code: "install_failed", message: error[NSAppleScript.errorMessage] as? String ?? "install failed", hint: nil)
            }
        }.value
    }
}

extension JSONDecoder {
    static let celare: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .custom { decoder in
            let s = try decoder.singleValueContainer().decode(String.self)
            let f = ISO8601DateFormatter()
            f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            if let date = f.date(from: s) { return date }
            f.formatOptions = [.withInternetDateTime]
            if let date = f.date(from: s) { return date }
            throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "bad date \(s)"))
        }
        return d
    }()
}
