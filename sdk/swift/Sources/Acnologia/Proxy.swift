import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

/// Sends requests through Acnologia's proxy for one service.
///
/// Acnologia injects a key, retries on rate limits with the next key, and never returns the
/// secret. Upstream responses come back as they are: a non-2xx status is not thrown. Redirects
/// are not followed, so the app token is never replayed to another host.
public struct Proxy: Sendable {
    /// The service slug (or service group).
    public let service: String
    let client: Acnologia

    private var prefix: String {
        client.baseURL.absoluteString + "/proxy/" + strictEscape(service)
    }

    /// The full proxy URL for a provider path and query.
    public func url(_ path: String, query: [URLQueryItem] = []) throws -> URL {
        var p = path
        while p.hasPrefix("/") { p.removeFirst() }
        var s = prefix + "/" + p
        if !query.isEmpty { s += (s.contains("?") ? "&" : "?") + encodeQuery(query) }
        guard let u = URL(string: s) else { throw AcnologiaError.invalidArgument("bad proxy path \(path)") }
        return u
    }

    /// Sends `request` through the proxy.
    ///
    /// The request URL may be relative (`/search.json?q=x`) or absolute; only its path and
    /// query are used, and the path is resolved under `/proxy/{service}/`. A path that already
    /// starts with `/proxy/{service}/` is kept as is. Method, headers and body are kept.
    public func send(_ request: URLRequest) async throws -> ProxyResponse {
        var path = "/"
        var query: String?
        if let u = request.url, let comps = URLComponents(url: u, resolvingAgainstBaseURL: false) {
            path = comps.percentEncodedPath
            query = comps.percentEncodedQuery
        }
        let svcPrefix = "/proxy/" + strictEscape(service)
        if path == svcPrefix || path.hasPrefix(svcPrefix + "/") {
            path.removeFirst(svcPrefix.count)
        }
        while path.hasPrefix("/") { path.removeFirst() }
        var s = prefix + "/" + path
        if let query, !query.isEmpty { s += "?" + query }
        guard let full = URL(string: s) else { throw AcnologiaError.invalidArgument("bad proxy path \(path)") }
        var out = request
        out.url = full
        client.setHeaders(&out)
        let (data, resp) = try await perform(client.proxySession, out)
        return ProxyResponse(response: resp, data: data)
    }

    /// Builds and sends a request through the proxy.
    public func request(
        _ method: String,
        _ path: String,
        query: [URLQueryItem] = [],
        headers: [String: String] = [:],
        body: Data? = nil
    ) async throws -> ProxyResponse {
        var req = URLRequest(url: try url(path, query: query))
        req.httpMethod = method.uppercased()
        for (k, v) in headers { req.setValue(v, forHTTPHeaderField: k) }
        req.httpBody = body
        return try await send(req)
    }

    /// Sends a GET with optional query parameters.
    public func get(_ path: String, query: [URLQueryItem] = [], headers: [String: String] = [:]) async throws -> ProxyResponse {
        try await request("GET", path, query: query, headers: headers)
    }

    /// Sends a DELETE.
    public func delete(_ path: String, query: [URLQueryItem] = [], headers: [String: String] = [:]) async throws -> ProxyResponse {
        try await request("DELETE", path, query: query, headers: headers)
    }

    /// Sends a POST with the given body and content type.
    public func post(_ path: String, body: Data?, contentType: String? = nil, headers: [String: String] = [:]) async throws -> ProxyResponse {
        try await request("POST", path, headers: withType(headers, contentType), body: body)
    }

    /// Sends a PUT with the given body and content type.
    public func put(_ path: String, body: Data?, contentType: String? = nil, headers: [String: String] = [:]) async throws -> ProxyResponse {
        try await request("PUT", path, headers: withType(headers, contentType), body: body)
    }

    /// Sends a PATCH with the given body and content type.
    public func patch(_ path: String, body: Data?, contentType: String? = nil, headers: [String: String] = [:]) async throws -> ProxyResponse {
        try await request("PATCH", path, headers: withType(headers, contentType), body: body)
    }

    /// Encodes `value` as JSON and POSTs it.
    public func postJSON<Body: Encodable>(_ path: String, _ value: Body, headers: [String: String] = [:]) async throws -> ProxyResponse {
        try await post(path, body: try JSONEncoder().encode(value), contentType: "application/json", headers: headers)
    }

    private func withType(_ headers: [String: String], _ contentType: String?) -> [String: String] {
        guard let contentType else { return headers }
        var h = headers
        if !h.keys.contains(where: { $0.lowercased() == "content-type" }) { h["Content-Type"] = contentType }
        return h
    }
}

/// A response relayed by the proxy, with Acnologia's own headers parsed out.
public struct ProxyResponse: Sendable {
    /// The HTTP status code.
    public let statusCode: Int
    /// Response headers. Use ``header(_:)`` for a case insensitive lookup.
    public let headers: [String: String]
    /// The response body.
    public let data: Data

    init(response: HTTPURLResponse, data: Data) {
        statusCode = response.statusCode
        var h: [String: String] = [:]
        for (k, v) in response.allHeaderFields {
            if let k = k as? String { h[k] = "\(v)" }
        }
        headers = h
        self.data = data
    }

    /// True for a 2xx status.
    public var isSuccess: Bool { (200...299).contains(statusCode) }

    /// Returns a header value, ignoring case.
    public func header(_ name: String) -> String? {
        let lower = name.lowercased()
        return headers.first { $0.key.lowercased() == lower }?.value
    }

    /// The pool key that served the request (`X-Acnologia-Key-Id`).
    public var keyID: Int64? { header("X-Acnologia-Key-Id").flatMap { Int64($0) } }
    /// How many keys were tried (`X-Acnologia-Attempts`).
    public var attempts: Int? { header("X-Acnologia-Attempts").flatMap { Int($0) } }
    /// The service that answered, which matters for service groups (`X-Acnologia-Service`).
    public var servedBy: String? { header("X-Acnologia-Service") }
    /// The cache status (`X-Acnologia-Cache`), when caching applies.
    public var cache: String? { header("X-Acnologia-Cache") }

    /// The body as UTF-8 text.
    public var text: String { String(decoding: data, as: UTF8.self) }

    /// Decodes the body as JSON.
    public func decode<T: Decodable>(_ type: T.Type = T.self, decoder: JSONDecoder = JSONDecoder()) throws -> T {
        try decoder.decode(type, from: data)
    }

    /// The typed error when Acnologia itself refused the request (no key, all keys busy, service
    /// unavailable, unauthenticated, forbidden and so on). `nil` for a 2xx and for any answer
    /// that came from the provider (those carry `X-Acnologia-Key-Id`).
    public var acnologiaError: AcnologiaError? {
        if isSuccess || header("X-Acnologia-Key-Id") != nil { return nil }
        guard let obj = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any],
              obj["error"] is [String: Any] else { return nil }
        return parseAPIError(status: statusCode, data: data, retryAfterHeader: header("Retry-After"))
    }

    /// Throws ``acnologiaError`` when there is one, and returns `self` otherwise.
    @discardableResult
    public func throwingAcnologiaError() throws -> ProxyResponse {
        if let e = acnologiaError { throw e }
        return self
    }
}
