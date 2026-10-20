import AppKit
import Foundation

/// The app drives the bundled `celare` binary with `--json`, exactly as an
/// agent does, so it can never do something the CLI can't.
struct CLI: Sendable {
    let binary: URL

    static func locate() -> CLI {
        if let path = ProcessInfo.processInfo.environment["CELARE_CLI"] {
            return CLI(binary: URL(fileURLWithPath: path))
        }
        if let bundled = Bundle.main.url(forResource: "celare", withExtension: nil) {
            return CLI(binary: bundled)
        }
        for path in ["/opt/homebrew/bin/celare", "/usr/local/bin/celare"] where FileManager.default.isExecutableFile(atPath: path) {
            return CLI(binary: URL(fileURLWithPath: path))
        }
        return CLI(binary: URL(fileURLWithPath: "/usr/local/bin/celare"))
    }

    struct Failure: Error, Decodable, Sendable, Equatable {
        let code: String
        let message: String
        let hint: String?
    }

    private struct Envelope<T: Decodable>: Decodable {
        let data: T?
        let error: Failure?
    }

    func run<T: Decodable & Sendable>(_ args: [String], as: T.Type = T.self) async throws -> T {
        try await Task.detached { try runSync(args, as: T.self) }.value
    }

    func runSync<T: Decodable>(_ args: [String], as: T.Type = T.self) throws -> T {
        let process = Process()
        process.executableURL = binary
        process.arguments = args + ["--json"]
        let out = Pipe()
        process.standardOutput = out
        process.standardError = FileHandle.nullDevice
        do {
            try process.run()
        } catch {
            throw Failure(code: "cli_missing", message: "Can't run \(binary.path)", hint: "Reinstall Celare.")
        }
        let data = out.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()
        let envelope: Envelope<T>
        do {
            envelope = try JSONDecoder.celare.decode(Envelope<T>.self, from: data)
        } catch {
            throw Failure(code: "bad_output", message: "celare \(args.first ?? "") returned something unexpected", hint: nil)
        }
        if let error = envelope.error { throw error }
        guard let value = envelope.data else {
            throw Failure(code: "empty", message: "celare returned no data", hint: nil)
        }
        return value
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
                let message = error[NSAppleScript.errorMessage] as? String ?? "Install failed"
                throw Failure(code: "install_failed", message: message, hint: nil)
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
