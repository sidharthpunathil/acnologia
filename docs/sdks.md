# SDKs

Acnologia has client libraries for twelve languages. They all speak the same consumer API (`/api/v1/...` and `/proxy/...`) with an app token, and they all have the same shape, so moving between them is mostly a matter of naming. None is on a package registry yet: the package column is the name each one will publish under, and the install column says how to get it today.

| Language | Folder | Package | Minimum | Install today |
|---|---|---|---|---|
| [Go](../sdk/go/README.md) | [`sdk/go`](../sdk/go) | `github.com/sidharthpunathil/acnologia/sdk/go` | Go 1.22 | `go get` (module path in the repo) |
| [Python](../sdk/python/README.md) | [`sdk/python`](../sdk/python) | `acnologia` (PyPI name) | Python 3.9 | pip from git |
| [TypeScript and JavaScript](../sdk/typescript/README.md) | [`sdk/typescript`](../sdk/typescript) | `@acnologia/keys` (npm name) | Node 18, Bun, Deno, browsers | build from a checkout |
| [Ruby](../sdk/ruby/README.md) | [`sdk/ruby`](../sdk/ruby) | `acnologia` (gem name) | Ruby 2.6 | Bundler from git |
| [PHP](../sdk/php/README.md) | [`sdk/php`](../sdk/php) | `acnologia/keys` (Composer name) | PHP 8.1 with curl | Composer path repository |
| [Rust](../sdk/rust/README.md) | [`sdk/rust`](../sdk/rust) | `acnologia` (crate name) | Rust 1.75 | Cargo git dependency |
| [Java](../sdk/java/README.md) | [`sdk/java`](../sdk/java) | `dev.acnologia:acnologia` | Java 17 | `mvn install` to the local repository |
| [Kotlin](../sdk/kotlin/README.md) | [`sdk/kotlin`](../sdk/kotlin) | `dev.acnologia:acnologia-kotlin` | JDK 17, Kotlin 2.0 | `gradle publishToMavenLocal` |
| [C#](../sdk/csharp/README.md) | [`sdk/csharp`](../sdk/csharp) | `Acnologia` (NuGet id) | .NET 8 | project reference or local feed |
| [Swift](../sdk/swift/README.md) | [`sdk/swift`](../sdk/swift) | `Acnologia` library | Swift 5.9, macOS 13, iOS 16 | SwiftPM repository URL |
| [Dart and Flutter](../sdk/dart/README.md) | [`sdk/dart`](../sdk/dart) | `acnologia` (pub name) | Dart 3.0 | pub git dependency |
| [C++](../sdk/cpp/README.md) | [`sdk/cpp`](../sdk/cpp) | `acnologia::acnologia` CMake target | C++17, CMake 3.16, libcurl | CMake FetchContent |

## What every SDK does

Each SDK builds a client from a base URL and an app token, or from the `ACNOLOGIA_URL` and `ACNOLOGIA_TOKEN` environment variables. **Proxy mode** sends a request to `/proxy/{service}/{path}`; Acnologia injects a key, rotates on rate limits and hands back the provider's raw response, so the secret never reaches your process. **Lease mode** borrows a key (`lease`, with optional excluded keys, a TTL, a `wait` of up to 60 seconds for a free slot, and `exclusive` to keep the key from every other caller), and the lease puts the secret where the provider expects it (query parameter, header or bearer). You then `report` the outcome, optionally with `rotate` to get the next key in the same call, and `renew` a lease that a long job still needs. The `withKey` helper (`with_key` in Python, Ruby, Rust and C++) runs that loop for you: it leases, calls your function, reports every lease it takes, and rotates when your function signals a rate limit, used up quota or invalid key. Errors are typed: no key available, all keys busy (a kind of no key available), service unavailable when the provider's circuit breaker is open (each with the server's retry after), unauthenticated, forbidden, and a generic API error carrying the status and error code such as `lease_not_found`. Secrets and tokens are never printed: string forms, debug output and report messages redact them.

## Install today

**Go**

```sh
go get github.com/sidharthpunathil/acnologia/sdk/go
```

**Python**

```sh
pip install "acnologia @ git+https://github.com/sidharthpunathil/acnologia.git#subdirectory=sdk/python"
```

**TypeScript and JavaScript**

```sh
git clone https://github.com/sidharthpunathil/acnologia.git
cd acnologia/sdk/typescript && pnpm install && pnpm build
cd /path/to/your/app && npm install /path/to/acnologia/sdk/typescript
```

**Ruby**

```ruby
# Gemfile
gem "acnologia", git: "https://github.com/sidharthpunathil/acnologia.git", glob: "sdk/ruby/*.gemspec"
```

**PHP**

```json
// composer.json, then: composer require acnologia/keys:@dev
"repositories": [{ "type": "path", "url": "path/to/acnologia/sdk/php" }]
```

**Rust**

```toml
[dependencies]
acnologia = { git = "https://github.com/sidharthpunathil/acnologia", package = "acnologia" }
```

**Java**

```sh
cd sdk/java && mvn install   # then depend on dev.acnologia:acnologia:0.1.0
```

**Kotlin**

```sh
cd sdk/kotlin && gradle publishToMavenLocal   # then mavenLocal() and dev.acnologia:acnologia-kotlin:0.1.0
```

**C#**

```sh
dotnet add YourApp.csproj reference path/to/acnologia/sdk/csharp/src/Acnologia/Acnologia.csproj
```

**Swift**

```swift
.package(url: "https://github.com/sidharthpunathil/acnologia", branch: "main"),
// target: .product(name: "Acnologia", package: "acnologia")
```

**Dart and Flutter**

```yaml
dependencies:
  acnologia:
    git:
      url: https://github.com/sidharthpunathil/acnologia.git
      path: sdk/dart
      ref: main
```

**C++**

```cmake
include(FetchContent)
FetchContent_Declare(acnologia
  GIT_REPOSITORY https://github.com/sidharthpunathil/acnologia.git
  GIT_TAG        main
  SOURCE_SUBDIR  sdk/cpp)
FetchContent_MakeAvailable(acnologia)
target_link_libraries(my_app PRIVATE acnologia::acnologia)
```

Each README has the full install notes, a quick start and the complete API.

## Running the tests

Every suite runs offline against a stub or fake server. Run each from its folder:

| SDK | From | Command |
|---|---|---|
| Go | `sdk/go` | `go test ./...` |
| Python | `sdk/python` | `python3 -m venv .venv && .venv/bin/pip install -e '.[test]' && .venv/bin/pytest` |
| TypeScript | `sdk/typescript` | `pnpm install && pnpm test && pnpm typecheck` |
| Ruby | `sdk/ruby` | `ruby -Ilib -Itest test/test_acnologia.rb` |
| PHP | `sdk/php` | `php tests/run.php` |
| Rust | `sdk/rust` | `cargo test --all-features` |
| Java | `sdk/java` | `mvn test` |
| Kotlin | `sdk/kotlin` | `gradle test` |
| C# | `sdk/csharp` | `dotnet test` |
| Swift | `sdk/swift` | `swift test` |
| Dart | `sdk/dart` | `dart pub get && dart test` |
| C++ | `sdk/cpp` | `cmake -S . -B build && cmake --build build && ctest --test-dir build --output-on-failure` |

Without a local toolchain, the Java, Kotlin, C#, Dart and PHP READMEs show how to run the same suite in Docker (`maven:3-eclipse-temurin-17`, `gradle:8-jdk17`, `mcr.microsoft.com/dotnet/sdk:8.0`, `dart:stable`, `php:8.1-cli`). The Swift suite also runs with `swift test` at the repository root, through the root `Package.swift`.
