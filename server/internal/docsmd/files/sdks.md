# SDKs

The SDKs call Acnologia's consumer API (`/proxy/...` and `/api/v1/...`) with an app token, so your code never has to store a provider key. There are seventeen, one per folder under [`sdk/`](../sdk), and they all have the same shape: once you know one, the others differ mostly in naming.

None is on a package registry yet. The package column below is the name each one will publish under; [Install today](#install-today) shows how to add each one now.

## Pick an SDK

| Language | Folder | Package | Needs | Lease helper |
|---|---|---|---|---|
| [Go](../sdk/go/README.md) | [`sdk/go`](../sdk/go) | `github.com/sidharthpunathil/acnologia/sdk/go` | Go 1.22 | `WithKey` |
| [Python](../sdk/python/README.md) | [`sdk/python`](../sdk/python) | `acnologia` (PyPI) | Python 3.9 | `with_key` |
| [TypeScript and JavaScript](../sdk/typescript/README.md) | [`sdk/typescript`](../sdk/typescript) | `@acnologia/keys` (npm) | Node 18, Bun, Deno, browsers | `withKey` |
| [Ruby](../sdk/ruby/README.md) | [`sdk/ruby`](../sdk/ruby) | `acnologia` (gem) | Ruby 3.1 | `with_key` |
| [PHP](../sdk/php/README.md) | [`sdk/php`](../sdk/php) | `acnologia/keys` (Composer) | PHP 8.1 with curl | `withKey` |
| [Rust](../sdk/rust/README.md) | [`sdk/rust`](../sdk/rust) | `acnologia` (crate) | Rust 1.75 | `with_key` |
| [Java](../sdk/java/README.md) | [`sdk/java`](../sdk/java) | `dev.acnologia:acnologia` | Java 17 | `withKey` |
| [Kotlin](../sdk/kotlin/README.md) | [`sdk/kotlin`](../sdk/kotlin) | `dev.acnologia:acnologia-kotlin` | JDK 17, Kotlin 2.0 | `withKey` |
| [C#](../sdk/csharp/README.md) | [`sdk/csharp`](../sdk/csharp) | `Acnologia` (NuGet) | .NET 8 | `WithKeyAsync` |
| [Swift](../sdk/swift/README.md) | [`sdk/swift`](../sdk/swift) | `Acnologia` library | Swift 5.9, macOS 13, iOS 16 | `withKey` |
| [Dart and Flutter](../sdk/dart/README.md) | [`sdk/dart`](../sdk/dart) | `acnologia` (pub) | Dart 3.0 | `withKey` |
| [C++](../sdk/cpp/README.md) | [`sdk/cpp`](../sdk/cpp) | `acnologia::acnologia` CMake target | C++17, CMake 3.16, libcurl | `with_key` |
| [C](../sdk/c/README.md) | [`sdk/c`](../sdk/c) | `acnologia::acnologia` CMake target | C11, libcurl, CMake 3.16 or make | `acn_with_key` |
| [Zig](../sdk/zig/README.md) | [`sdk/zig`](../sdk/zig) | `acnologia` Zig module | Zig 0.17.0 | none |
| [Elixir](../sdk/elixir/README.md) | [`sdk/elixir`](../sdk/elixir) | `acnologia` (Hex) | Elixir 1.18, OTP 25 | `with_key` |
| [Haskell](../sdk/haskell/README.md) | [`sdk/haskell`](../sdk/haskell) | `acnologia` (Hackage) | GHC 9.4, cabal 3.0 | `withKey` |
| [Lua](../sdk/lua/README.md) | [`sdk/lua`](../sdk/lua) | `acnologia` (luarocks) | Lua 5.1 to 5.5 or LuaJIT, with luasocket and luasec or lua-curl | `with_key` |

Each README opens with a quick start, then covers install, the full API, errors and tests.

## What every SDK covers

- **A client** built from the server URL and an app token (`acn_...`).
- **Proxy mode.** Send the request to `/proxy/{service}/{path}`. Acnologia attaches a key, moves to the next key when one is rate limited or out of quota, and returns the provider's response as it is. The secret never reaches your process.
- **Lease mode**, for code that has to hold the raw key. `lease` borrows one, with optional excluded key ids, a TTL, a `wait` of up to 60 seconds for a free slot, and `exclusive` to keep the key from every other caller. The lease puts the secret where the provider expects it: a query parameter, a header or a bearer token.
- **Report, rotate and renew.** `report` sends the outcome (`ok`, `rate_limited`, `busy`, `quota_exhausted`, `invalid`, `banned` or `error`). With `rotate` it returns the next key in the same call. `renew` extends a lease that a long job still needs.
- **The lease helper** (the last column above) runs that loop for you. It leases a key, calls your function and reports every lease it takes, even when your function throws. Signal a rate limit, used up quota or invalid key from your function and it rotates to the next key.
- **Typed errors** for no key available, all keys busy (a kind of no key available) and service unavailable when the provider's circuit breaker is open, each with the server's retry after. Other failures are an API error carrying the HTTP status and the error code, such as `lease_not_found`.
- **Redaction.** String forms, debug output and report messages hide the token and the secret.

You may not need an SDK at all. With the [forward proxy](api.md#forward-proxy), any plain HTTP client calls provider URLs as they are and Acnologia puts the key on: set `HTTPS_PROXY` to `http://app:acn_...@your-server:8888`, trust the server's CA (`GET /api/v1/proxy/ca.pem`), and keep your code unchanged. The SDKs add leases, typed errors and the helpers above.

A service that another Acnologia server shares with yours is proxy only. A lease on it fails with a 403 and the code `lease_not_allowed`, so call it in proxy mode.

## Where they differ

- **Environment variables.** Most clients can be built from `ACNOLOGIA_URL` and `ACNOLOGIA_TOKEN` (for example `Client.new` in Ruby, `Acnologia.fromEnvironment()` in Dart, `Client::from_env()` in C++). The Go, TypeScript and PHP clients do not read them: pass the values to the constructor yourself.
- **Auth errors.** Rust, Java, Kotlin, C#, Swift, Dart, C++ and Ruby have their own types for 401 (unauthenticated) and 403 (forbidden). Go, Python, TypeScript and PHP raise their generic API error, so check its status and code.
- **The newer API (Ruby, C++, C, Zig, Elixir, Haskell and Lua).** These seven also have `me` for `GET /api/v1/me`, their own errors for `app_limit_reached` (with `limit`, `reset_at` and `retry_after`), `share_revoked`, `share_paused`, `share_expired`, `lease_not_allowed`, `upstream_blocked` and `loop_detected`, an `Idempotency-Key` on proxied POSTs, `Retry-After` from the header when the body has none, and the `X-Acnologia-Access` header for a deploy behind the access password. They also tell Acnologia's own refusals on the proxy apart from the provider's answers. The other SDKs report those errors as their generic API error. Ruby adds opt in retries that sleep the server's `Retry-After`.

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

**C**

```cmake
add_subdirectory(path/to/acnologia/sdk/c)
target_link_libraries(my_app PRIVATE acnologia::acnologia)
```

Or FetchContent as for C++, with `SOURCE_SUBDIR sdk/c`.

**Zig**

```sh
zig fetch --save=acnologia git+https://github.com/sidharthpunathil/acnologia#main
```

Then build a module from `dep.path("sdk/zig/src/acnologia.zig")` in `build.zig`. The [Zig README](../sdk/zig/README.md) shows the lines.

**Elixir**

```elixir
{:acnologia, git: "https://github.com/sidharthpunathil/acnologia.git", sparse: "sdk/elixir", branch: "main"}
```

**Haskell**

In `cabal.project`:

```
source-repository-package
  type: git
  location: https://github.com/sidharthpunathil/acnologia.git
  tag: main
  subdir: sdk/haskell
```

Then depend on `acnologia`.

**Lua**

```sh
luarocks install --local https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/sdk/lua/acnologia-scm-1.rockspec
```

## Run the tests

Every suite runs offline against a stub or fake server. Run each one from its folder:

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
| C | `sdk/c` | `cmake -S . -B build -DACN_SANITIZE=ON && cmake --build build && ctest --test-dir build --output-on-failure` |
| Zig | `sdk/zig` | `zig build test` |
| Elixir | `sdk/elixir` | `mix test` (add `ACNOLOGIA_TEST_HTTP=raw` to test the built in HTTP client used before inets 9.6) |
| Haskell | `sdk/haskell` | `cabal update && cabal build all && cabal test` |
| Lua | `sdk/lua` | `luarocks install --local busted && busted` (or `lua spec/run.lua`) |

Without a local toolchain, the Java, Kotlin, C#, Dart and PHP READMEs show how to run the same suite in Docker (`maven:3-eclipse-temurin-17`, `gradle:8-jdk17`, `mcr.microsoft.com/dotnet/sdk:8.0`, `dart:stable`, `php:8.1-cli`). The Swift suite also runs with `swift test` at the repository root, through the root `Package.swift`.

### On Windows

The SDKs read no files and keep no config of their own, so they behave the same on Windows. CI runs the Go, Python, Ruby and TypeScript suites on `windows-latest` as well as Linux and macOS.

For the SDKs that read the environment, set the token for one PowerShell session with `$env:ACNOLOGIA_TOKEN = "acn_..."`, or for your account with `setx ACNOLOGIA_TOKEN acn_...` (new terminals only).

The Python venv lives in `.venv\Scripts` on Windows, so the Python line becomes:

```powershell
cd sdk\python
python -m venv .venv
.venv\Scripts\pip install -e ".[test]"
.venv\Scripts\pytest
```

The other commands in the table run as they are in PowerShell 7. Windows PowerShell 5.1 has no `&&`: run the steps one by one, or join them with `;`.

## See also

- [HTTP API](api.md): every consumer route and error code
- [Request flows](flows.md): what the proxy and leases do on the server
