// swift-tools-version:5.9
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
        .target(name: "Acnologia"),
        .testTarget(name: "AcnologiaTests", dependencies: ["Acnologia"]),
    ]
)
