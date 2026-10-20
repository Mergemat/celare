// swift-tools-version:6.0
import PackageDescription

let package = Package(
    name: "Celare",
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(name: "Celare", path: "Sources/Celare")
    ]
)
