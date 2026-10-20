import Foundation

extension CLI {
    /// Deliver completed probes immediately, with at most six CLI processes
    /// running at once. Each command keeps the usual single-document contract.
    func testNodes(_ names: [String], receive: @escaping @MainActor @Sendable (TestResult) -> Void) async {
        var remaining = names.makeIterator()
        await withTaskGroup(of: TestResult.self) { group in
            for _ in 0..<min(6, names.count) {
                if let name = remaining.next() {
                    group.addTask { await testNode(name) }
                }
            }
            for await result in group {
                await receive(result)
                if let name = remaining.next() {
                    group.addTask { await testNode(name) }
                }
            }
        }
    }

    private func testNode(_ name: String) async -> TestResult {
        struct Tests: Decodable, Sendable { let results: [TestResult] }
        do {
            let tests: Tests = try await run(["nodes", "test", name])
            guard let result = tests.results.first else {
                throw Failure(code: "empty", message: "No test result for \(name)", hint: nil)
            }
            return result
        } catch let failure as Failure {
            return TestResult(node: name, ok: false, latencyMs: nil, error: failure.message)
        } catch {
            return TestResult(node: name, ok: false, latencyMs: nil, error: error.localizedDescription)
        }
    }
}
