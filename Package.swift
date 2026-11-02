// swift-tools-version:5.9
// Swift Package Manager entry point for the Swift SDK, so the repository URL
// works as a dependency:
//
//     .package(url: "https://github.com/sidharthpunathil/acnologia", branch: "main")
//
// The sources live in sdk/swift; sdk/swift/Package.swift builds the same
// library when you work inside that folder.
import PackageDescription

let package = Package(
    name: "Acnologia",
    platforms: [
        .macOS(.v13),
        .iOS(.v16),
        .tvOS(.v16),
        .watchOS(.v9),
    ],
    products: [
        .library(name: "Acnologia", targets: ["Acnologia"]),
    ],
    targets: [
        .target(name: "Acnologia", path: "sdk/swift/Sources/Acnologia"),
        .testTarget(
            name: "AcnologiaTests",
            dependencies: ["Acnologia"],
            path: "sdk/swift/Tests/AcnologiaTests"
        ),
    ]
)
