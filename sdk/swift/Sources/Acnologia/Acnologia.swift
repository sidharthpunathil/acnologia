import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

/// A client for Acnologia, a self-hosted API key pool.
///
/// Acnologia can be consumed in two ways:
///
/// - Lease mode: ``lease(_:options:)`` a key, call the provider yourself, then
///   ``Lease/report(_:statusCode:message:rotate:)`` the outcome. ``withKey(_:options:maxAttempts:_:)``
///   wraps this loop and rotates keys automatically.
/// - Proxy mode: send requests through ``proxy(_:)``. Acnologia injects a key and rotates on
///   rate limits without ever exposing the secret.
///
/// ```swift
/// let acn = try Acnologia.fromEnvironment()
/// let lease = try await acn.lease("serpapi")
/// ```
///
/// The app token (`acn_...`, or the older `cog_...`) and the access password never appear in
/// `description`, `debugDescription` or a `Mirror`.
public final class Acnologia: @unchecked Sendable {
    /// The SDK version.
    public static let version = "0.2.0"
    /// The default `User-Agent`.
    public static let defaultUserAgent = "acnologia-swift/\(version)"
    /// The default timeout for API calls, in seconds. It is longer than ``LeaseOptions/maxWait``
    /// so a lease that waits for a free slot can still succeed.
    public static let defaultTimeout: TimeInterval = 75

    /// The Acnologia server, without a trailing slash.
    public let baseURL: URL
    /// The `User-Agent` sent with every request.
    public let userAgent: String
    /// Timeout for API calls (lease, report, renew, services). Proxy calls use the session's own.
    public let timeout: TimeInterval

    let token: String
    private let accessPassword: String?
    let apiSession: URLSession
    let proxySession: URLSession

    /// Creates a client.
    ///
    /// - Parameters:
    ///   - baseURL: The Acnologia server, for example `http://localhost:8080`.
    ///   - token: The app token, `acn_...`.
    ///   - configuration: The session configuration. Pass one with custom `protocolClasses` in tests.
    ///   - userAgent: Overrides the `User-Agent` header.
    ///   - timeout: Timeout for API calls, in seconds.
    ///   - accessPassword: Sent as `X-Acnologia-Access` on every request, for a server behind
    ///     the access password.
    public init(
        baseURL: URL,
        token: String,
        configuration: URLSessionConfiguration = .default,
        userAgent: String = Acnologia.defaultUserAgent,
        timeout: TimeInterval = Acnologia.defaultTimeout,
        accessPassword: String? = nil
    ) {
        var s = baseURL.absoluteString
        while s.hasSuffix("/") { s.removeLast() }
        self.baseURL = URL(string: s) ?? baseURL
        self.token = token
        self.accessPassword = (accessPassword?.isEmpty ?? true) ? nil : accessPassword
        self.userAgent = userAgent
        self.timeout = timeout
        self.apiSession = URLSession(configuration: configuration)
        // Proxy calls never follow redirects, so the app token is not replayed to another host.
        self.proxySession = URLSession(configuration: configuration, delegate: NoRedirects(), delegateQueue: nil)
    }

    /// Creates a client from a base URL string.
    ///
    /// - Throws: ``AcnologiaError/invalidArgument(_:)`` when the URL or token is empty or invalid.
    public convenience init(
        baseURL: String,
        token: String,
        configuration: URLSessionConfiguration = .default,
        userAgent: String = Acnologia.defaultUserAgent,
        timeout: TimeInterval = Acnologia.defaultTimeout,
        accessPassword: String? = nil
    ) throws {
        guard !baseURL.isEmpty, let url = URL(string: baseURL), url.scheme != nil else {
            throw AcnologiaError.invalidArgument("baseURL must be an absolute URL such as http://localhost:8080")
        }
        guard !token.isEmpty else {
            throw AcnologiaError.invalidArgument("token is required")
        }
        self.init(baseURL: url, token: token, configuration: configuration, userAgent: userAgent, timeout: timeout,
                  accessPassword: accessPassword)
    }

    /// Creates a client from the `ACNOLOGIA_URL` and `ACNOLOGIA_TOKEN` environment variables, and
    /// `ACNOLOGIA_ACCESS_PASSWORD` when `accessPassword` is `nil`.
    ///
    /// - Parameter environment: The variables to read. Defaults to the process environment.
    /// - Throws: ``AcnologiaError/invalidArgument(_:)`` when either variable is missing.
    public static func fromEnvironment(
        _ environment: [String: String] = ProcessInfo.processInfo.environment,
        configuration: URLSessionConfiguration = .default,
        userAgent: String = Acnologia.defaultUserAgent,
        timeout: TimeInterval = Acnologia.defaultTimeout,
        accessPassword: String? = nil
    ) throws -> Acnologia {
        guard let url = environment["ACNOLOGIA_URL"], !url.isEmpty else {
            throw AcnologiaError.invalidArgument("ACNOLOGIA_URL is not set")
        }
        guard let token = environment["ACNOLOGIA_TOKEN"], !token.isEmpty else {
            throw AcnologiaError.invalidArgument("ACNOLOGIA_TOKEN is not set")
        }
        return try Acnologia(baseURL: url, token: token, configuration: configuration, userAgent: userAgent, timeout: timeout,
                             accessPassword: accessPassword ?? environment["ACNOLOGIA_ACCESS_PASSWORD"])
    }

    deinit {
        apiSession.finishTasksAndInvalidate()
        proxySession.finishTasksAndInvalidate()
    }

    // MARK: - Consumer API

    /// Lists the services this app token may use.
    public func services() async throws -> [Service] {
        let out: ServicesResponse = try await call("GET", "/api/v1/services", body: Optional<LeaseRequest>.none)
        return out.services ?? []
    }

    /// The token's own access: its app, limits and use, the services it may call and, for a
    /// share's token, the share. A share token whose share stopped still gets an answer, with
    /// ``MeShare/status`` saying so.
    public func me() async throws -> Me {
        try await call("GET", "/api/v1/me", body: Optional<LeaseRequest>.none)
    }

    /// A service's usage guide: its endpoints, parameters, limits and errors.
    public func guide(_ service: String) async throws -> Guide {
        try await call("GET", "/api/v1/services/" + strictEscape(service) + "/guide", body: Optional<LeaseRequest>.none)
    }

    /// A service's usage guide as Markdown (`Accept: text/markdown`).
    public func guideMarkdown(_ service: String) async throws -> String {
        let data = try await raw("GET", "/api/v1/services/" + strictEscape(service) + "/guide", accept: "text/markdown")
        return String(decoding: data, as: UTF8.self)
    }

    /// The capabilities the app may call: one job several providers can do, each with its params
    /// and provider routes. Empty for a share's token.
    public func capabilities() async throws -> [Capability] {
        let out: CapabilitiesResponse = try await call("GET", "/api/v1/capabilities", body: Optional<LeaseRequest>.none)
        return out.capabilities ?? []
    }

    /// Calls a capability: `POST /proxy/{slug}` with `params` as the JSON body. Acnologia picks a
    /// provider, translates the params and moves to the next provider when one fails.
    ///
    /// Like a proxy call, the answer comes back whatever its status; check
    /// ``ProxyResponse/acnologiaError`` and ``ProxyResponse/tried``.
    public func capability(
        _ slug: String,
        params: [String: JSONValue] = [:],
        options: CapabilityOptions = CapabilityOptions()
    ) async throws -> ProxyResponse {
        try await capabilityCall(slug, body: try JSONEncoder().encode(params), options: options)
    }

    /// Calls a capability with any `Encodable` value as its params.
    public func capability<Params: Encodable>(
        _ slug: String,
        params: Params,
        options: CapabilityOptions = CapabilityOptions()
    ) async throws -> ProxyResponse {
        try await capabilityCall(slug, body: try JSONEncoder().encode(params), options: options)
    }

    private func capabilityCall(_ slug: String, body: Data, options: CapabilityOptions) async throws -> ProxyResponse {
        var query: [URLQueryItem] = []
        if let svc = options.service, !svc.isEmpty { query.append(URLQueryItem(name: "_service", value: svc)) }
        if options.raw { query.append(URLQueryItem(name: "_raw", value: "1")) }
        var s = baseURL.absoluteString + "/proxy/" + strictEscape(slug)
        if !query.isEmpty { s += "?" + encodeQuery(query) }
        guard let url = URL(string: s) else { throw AcnologiaError.invalidArgument("bad capability \(slug)") }
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.httpBody = body
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        setHeaders(&req)
        options.proxy.apply(to: &req)
        let (data, resp) = try await perform(proxySession, req)
        return ProxyResponse(response: resp, data: data)
    }

    /// Leases a key from the pool.
    ///
    /// Always report the lease: on services with a concurrency limit it holds one of the key's
    /// slots until it is reported or its TTL expires.
    ///
    /// - Throws: ``AcnologiaError/noKeyAvailable(retryAfter:message:)`` when the pool is empty or
    ///   every key is cooling down, ``AcnologiaError/allKeysBusy(retryAfter:message:)`` when every
    ///   usable key is at its concurrency limit and ``LeaseOptions/wait`` ran out, and the other
    ///   ``AcnologiaError`` cases for other failures.
    public func lease(_ service: String, options: LeaseOptions = LeaseOptions()) async throws -> Lease {
        var waitSec: Int?
        if let wait = options.wait {
            guard wait >= 0, wait <= LeaseOptions.maxWait else {
                throw AcnologiaError.invalidArgument("lease wait must be between 0 and 60 seconds")
            }
            if wait > 0 { waitSec = ceilSeconds(wait) }
        }
        var ttlSec: Int?
        if let ttl = options.ttl, ttl > 0 { ttlSec = ceilSeconds(ttl) }
        let body = LeaseRequest(
            service: service,
            excludeKeyIDs: options.excludeKeyIDs.isEmpty ? nil : options.excludeKeyIDs,
            ttlSec: ttlSec,
            waitSec: waitSec,
            exclusive: options.exclusive ? true : nil
        )
        let wire: LeaseWire = try await call("POST", "/api/v1/lease", body: body)
        return Lease(wire: wire, client: self)
    }

    /// Returns a helper that sends requests through `/proxy/{service}`.
    public func proxy(_ service: String) -> Proxy {
        Proxy(service: service, client: self)
    }

    // MARK: - Transport

    func setHeaders(_ req: inout URLRequest) {
        setHeaders(&req, withToken: true)
    }

    func setHeaders(_ req: inout URLRequest, withToken: Bool) {
        if withToken {
            req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        if let accessPassword {
            req.setValue(accessPassword, forHTTPHeaderField: "X-Acnologia-Access")
        }
        if !userAgent.isEmpty {
            req.setValue(userAgent, forHTTPHeaderField: "User-Agent")
        }
    }

    /// Sends an API request and returns the raw body of a 2xx answer.
    func raw(_ method: String, _ path: String, accept: String, withToken: Bool = true) async throws -> Data {
        guard let url = URL(string: baseURL.absoluteString + path) else {
            throw AcnologiaError.invalidArgument("bad path \(path)")
        }
        var req = URLRequest(url: url)
        req.httpMethod = method
        if timeout > 0 { req.timeoutInterval = timeout }
        setHeaders(&req, withToken: withToken)
        req.setValue(accept, forHTTPHeaderField: "Accept")
        let (data, resp) = try await perform(apiSession, req)
        guard (200...299).contains(resp.statusCode) else {
            throw parseAPIError(status: resp.statusCode, data: data, retryAfterHeader: resp.value(forHTTPHeaderField: "Retry-After"),
                                shareHeader: resp.value(forHTTPHeaderField: "X-Acnologia-Share"))
        }
        return data
    }

    func call<Body: Encodable, Out: Decodable>(_ method: String, _ path: String, body: Body?) async throws -> Out {
        guard let url = URL(string: baseURL.absoluteString + path) else {
            throw AcnologiaError.invalidArgument("bad path \(path)")
        }
        var req = URLRequest(url: url)
        req.httpMethod = method
        if timeout > 0 { req.timeoutInterval = timeout }
        setHeaders(&req)
        req.setValue("application/json", forHTTPHeaderField: "Accept")
        if let body {
            req.httpBody = try JSONEncoder().encode(body)
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        let (data, resp) = try await perform(apiSession, req)
        guard (200...299).contains(resp.statusCode) else {
            throw parseAPIError(status: resp.statusCode, data: data, retryAfterHeader: resp.value(forHTTPHeaderField: "Retry-After"),
                                shareHeader: resp.value(forHTTPHeaderField: "X-Acnologia-Share"))
        }
        do {
            return try JSONDecoder().decode(Out.self, from: data.isEmpty ? Data("{}".utf8) : data)
        } catch {
            throw AcnologiaError.decoding(String(describing: error))
        }
    }
}

extension Acnologia: CustomStringConvertible, CustomDebugStringConvertible, CustomReflectable {
    public var description: String { "Acnologia(baseURL: \(baseURL.absoluteString), token: [redacted])" }
    public var debugDescription: String { description }
    public var customMirror: Mirror {
        Mirror(self, children: ["baseURL": baseURL, "token": "[redacted]", "userAgent": userAgent,
                               "accessPassword": accessPassword == nil ? "none" : "[redacted]"], displayStyle: .class)
    }
}

final class NoRedirects: NSObject, URLSessionTaskDelegate, @unchecked Sendable {
    func urlSession(
        _ session: URLSession,
        task: URLSessionTask,
        willPerformHTTPRedirection response: HTTPURLResponse,
        newRequest request: URLRequest,
        completionHandler: @escaping (URLRequest?) -> Void
    ) {
        completionHandler(nil)
    }
}

/// Holds a data task so a Swift task cancellation can cancel it.
private final class TaskHandle: @unchecked Sendable {
    private let lock = NSLock()
    private var task: URLSessionDataTask?
    private var cancelled = false

    func set(_ t: URLSessionDataTask) {
        lock.lock()
        task = t
        let c = cancelled
        lock.unlock()
        if c { t.cancel() }
    }

    func cancel() {
        lock.lock()
        cancelled = true
        let t = task
        lock.unlock()
        t?.cancel()
    }
}

/// Runs a request with a completion handler, which works the same on Apple platforms and on
/// Linux, and cancels it when the surrounding Swift task is cancelled.
func perform(_ session: URLSession, _ request: URLRequest) async throws -> (Data, HTTPURLResponse) {
    let handle = TaskHandle()
    return try await withTaskCancellationHandler {
        try await withCheckedThrowingContinuation { (cont: CheckedContinuation<(Data, HTTPURLResponse), Error>) in
            let task = session.dataTask(with: request) { data, response, error in
                if let error {
                    if (error as? URLError)?.code == .cancelled {
                        cont.resume(throwing: CancellationError())
                    } else {
                        cont.resume(throwing: error)
                    }
                    return
                }
                guard let http = response as? HTTPURLResponse else {
                    cont.resume(throwing: URLError(.badServerResponse))
                    return
                }
                cont.resume(returning: (data ?? Data(), http))
            }
            handle.set(task)
            task.resume()
        }
    } onCancel: {
        handle.cancel()
    }
}
