# SDKs

Client libraries for seventeen languages. Each one calls Acnologia with an app token (`acn_...`), so your code gets a working key, or a proxied response, without holding the key pool itself. They all speak the same consumer API (`/api/v1/...` and `/proxy/...`) and, at version 0.2.0, share one surface, written down in [SPEC.md](SPEC.md), so moving between them is mostly a matter of naming.

None is on a package registry yet. The package column is the name each one will publish under, and the install column says how to get it today.

| Language | Folder | Package | Minimum | Install today |
|---|---|---|---|---|
| [Go](go/README.md) | [`sdk/go`](go) | `github.com/sidharthpunathil/acnologia/sdk/go` | Go 1.22 | `go get` (module path in the repo) |
| [Python](python/README.md) | [`sdk/python`](python) | `acnologia` (PyPI name) | Python 3.9 | pip from git |
| [TypeScript and JavaScript](typescript/README.md) | [`sdk/typescript`](typescript) | `@acnologia/keys` (npm name) | Node 18, Bun, Deno, browsers | build from a checkout |
| [Ruby](ruby/README.md) | [`sdk/ruby`](ruby) | `acnologia` (gem name) | Ruby 3.1 | Bundler from git |
| [PHP](php/README.md) | [`sdk/php`](php) | `acnologia/keys` (Composer name) | PHP 8.1 with curl | Composer path repository |
| [Rust](rust/README.md) | [`sdk/rust`](rust) | `acnologia` (crate name) | Rust 1.75 | Cargo git dependency |
| [Java](java/README.md) | [`sdk/java`](java) | `dev.acnologia:acnologia` | Java 17 | `mvn install` to the local repository |
| [Kotlin](kotlin/README.md) | [`sdk/kotlin`](kotlin) | `dev.acnologia:acnologia-kotlin` | JDK 17, Kotlin 2.0 | `gradle publishToMavenLocal` |
| [C#](csharp/README.md) | [`sdk/csharp`](csharp) | `Acnologia` (NuGet id) | .NET 8 | project reference or local feed |
| [Swift](swift/README.md) | [`sdk/swift`](swift) | `Acnologia` library | Swift 5.9, macOS 13, iOS 16 | SwiftPM repository URL |
| [Dart and Flutter](dart/README.md) | [`sdk/dart`](dart) | `acnologia` (pub name) | Dart 3.0 | pub git dependency |
| [C++](cpp/README.md) | [`sdk/cpp`](cpp) | `acnologia::acnologia` CMake target | C++17, CMake 3.16, libcurl | CMake FetchContent |
| [C](c/README.md) | [`sdk/c`](c) | `acnologia::acnologia` CMake target | C11, libcurl | CMake `add_subdirectory` or FetchContent |
| [Zig](zig/README.md) | [`sdk/zig`](zig) | `acnologia` Zig module | Zig 0.17.0 | `zig fetch` |
| [Elixir](elixir/README.md) | [`sdk/elixir`](elixir) | `acnologia` (Hex name) | Elixir 1.18, OTP 25 | Mix git dependency |
| [Haskell](haskell/README.md) | [`sdk/haskell`](haskell) | `acnologia` (Hackage name) | GHC 9.4 | cabal `source-repository-package` |
| [Lua](lua/README.md) | [`sdk/lua`](lua) | `acnologia` (luarocks name) | Lua 5.1 to 5.5, LuaJIT | luarocks from the rockspec URL |

## What every SDK does

Every SDK follows the contract in [SPEC.md](SPEC.md). Names follow each language's idiom (`forwardProxy` in TypeScript, `forward_proxy` in Python and Ruby, `ForwardProxy` in Go and C#); each README has the exact ones.

**A client from a URL and a token.** Every SDK takes the server's base URL and an app token in its constructor. All except Go, PHP and TypeScript also fall back to the `ACNOLOGIA_URL` and `ACNOLOGIA_TOKEN` environment variables; in those three, read the variables yourself and pass them in. An `accessPassword` option (`access_password`, `AccessPassword` and so on) sends `X-Acnologia-Access` on every request, for a deploy behind the access password; where the SDK reads the environment it falls back to `ACNOLOGIA_ACCESS_PASSWORD`.

**Proxy mode.** A request goes to `/proxy/{service}/{path}`. Acnologia attaches a key, rotates on rate limits and hands back the provider's raw response, so the secret never reaches your process. `{service}` may also be a service group or a capability slug. Per call options `idempotencyKey` (sent as `Idempotency-Key`) and `noCache` (`Cache-Control: no-cache`) are in every SDK.

**Response metadata.** Every SDK reads Acnologia's answer headers into `keyId`, `attempts`, `service`, `cache`, `replayed`, `capability` and `tried` (the routes a capability or group call tried, as `{service, status}`). Some put them on the response itself; others use a helper: `ProxyInfo(resp)` in Go, `proxy_info(resp)` in Python, `proxyInfo(res)` in TypeScript, `ProxyResponse.of(res)` in Java, `res.acnologia` in Kotlin, `res.GetProxyInfo()` in C# and the `ProxyResponseExt` trait in Rust.

**Discovery.** `me` reads `GET /api/v1/me` (the app, its limits and usage, its services, and the share behind a share's token). `services` lists the services with their `guide_url`. `guide` and `guideMarkdown` return a service's usage guide, structured or as Markdown (in Ruby `guide` returns the Markdown and `guide_doc` the structured guide; in C++ `guide` returns the Markdown and `guide_details` the structured one). `capabilities` lists the capabilities and `capability` calls one with its params.

**The forward proxy.** `forwardProxy` reads `GET /api/v1/proxy`; `forwardProxyCA` fetches the CA (public, no token), with a variant that saves it to a file where there is a file system; `forwardProxyURL` builds the proxy URL with the token in it; `forwardProxyEnv` returns the variables (`HTTPS_PROXY`, `HTTP_PROXY`, `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`, `SSL_CERT_FILE`, ...) to hand to any program. Java and Kotlin spell two of them `forwardProxyUrl` and (Java) `forwardProxyCa`. Some SDKs also build a ready HTTP client that goes through the proxy and trusts the Acnologia CA:

| SDK | Forward proxy client |
|---|---|
| Go | `ForwardProxyTransport(ctx)`, an `*http.Transport` |
| Python | `forward_proxy_client()`, an `httpx.Client` (`httpx.AsyncClient` on `AsyncAcnologia`) |
| Rust | `forward_proxy_client()`, a `reqwest::Client` |
| Kotlin | `forwardProxyClient()`, an OkHttp client |
| C# | `CreateForwardProxyHandlerAsync()`, an `HttpClientHandler` |
| Ruby | `forward_proxy_http(host)`, a `Net::HTTP` |

`forwardProxyURL`, `forwardProxyEnv` and these clients carry the token, so keep them out of logs.

**Lease mode.** `lease` borrows a key, with optional excluded keys, a TTL, a wait of up to 60 seconds for a free slot, and `exclusive` to keep the key from every other caller. The lease puts the secret where the provider expects it: a query parameter, a header or a bearer token. You then `report` the outcome, optionally with `rotate` to get the next key in the same call, and `renew` a lease that a long job still needs.

**A retry loop.** `withKey` (`with_key` in Python, Ruby, Rust and C++) leases a key, calls your function, reports every lease it takes, and rotates when your function signals a rate limit, used up quota or invalid key.

**Typed errors.** Every SDK has distinct errors for no key available, all keys busy (a kind of no key available) and service unavailable when the provider's circuit breaker is open, each carrying the server's retry after. Every SDK also has app limit reached (429 `app_limit_reached`, with the `limit`, `reset_at` and retry after) and share unavailable (403 `share_revoked`, `share_paused` or `share_expired`, with the status). Each models them the way it models other errors: C has error codes (`ACN_ERR_APP_LIMIT_REACHED`, `ACN_ERR_SHARE_REVOKED` and so on, with `acn_is_share_unavailable`), Elixir has reasons with the helpers `Acnologia.Error.app_limit_reached?/1` and `share_unavailable?/1`, Zig has error values with `isShareUnavailable`, and Lua sets `app_limit` and `share_unavailable` on the error. Anything else is a generic API error with the HTTP status and the error code, such as `lease_not_found`. The C++, C#, Dart, Java, Kotlin, Ruby, Rust and Swift SDKs also have their own unauthenticated (401) and forbidden (403) errors; the others report those as the generic API error. Ruby can also retry after the server's `Retry-After` when you set `max_retries:`.

**Redacted secrets.** String forms, debug output and report messages never print the key or the app token.

A lease fails with 403 `lease_not_allowed` when the app is proxy only, or when the service is one another Acnologia server shares with yours. Call those services in proxy mode.

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
cd sdk/java && mvn install   # then depend on dev.acnologia:acnologia:0.2.0
```

**Kotlin**

```sh
cd sdk/kotlin && gradle publishToMavenLocal   # then mavenLocal() and dev.acnologia:acnologia-kotlin:0.2.0
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

Each README has the full install notes, a quick start and the complete API. For how requests flow through the server, see [docs/flows.md](../docs/flows.md); for every error code, [docs/api.md](../docs/api.md).

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

### Windows

The SDKs keep no config of their own (the only file one writes is the forward proxy CA, when you ask it to): the base URL and the token come from the constructor or the `ACNOLOGIA_URL` and `ACNOLOGIA_TOKEN` environment variables, so they behave the same on Windows. CI runs the Go, Python, Ruby and TypeScript suites on `windows-latest` as well as Linux and macOS. Set the variables for one PowerShell session with `$env:ACNOLOGIA_TOKEN = "acn_..."`, or for your account with `setx ACNOLOGIA_TOKEN acn_...` (new terminals only). The Python venv lives in `.venv\Scripts` on Windows, so the Python line becomes:

```powershell
cd sdk\python
python -m venv .venv
.venv\Scripts\pip install -e ".[test]"
.venv\Scripts\pytest
```

The other commands in the table run as they are in PowerShell, except that `&&` needs PowerShell 7; in Windows PowerShell 5.1 run the steps one by one or join them with `;`.
