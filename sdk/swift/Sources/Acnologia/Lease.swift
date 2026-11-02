import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

/// A key handed out by the pool. Report its outcome with ``report(_:statusCode:message:rotate:)``
/// when you are done.
///
/// The secret never appears in `description`, `debugDescription` or a `Mirror` (so `print`,
/// `dump` and string interpolation are safe). Read ``secret`` directly when you need it, or let
/// ``apply(to:)`` place it on a request.
public struct Lease: Sendable {
    public let leaseID: String
    public let keyID: Int64
    public let service: String
    /// The provider key. Never log it.
    public let secret: String
    public let auth: Auth
    /// The provider base URL, without a trailing slash.
    public let baseURL: String
    public let expiresAt: Date?
    /// True when the lease was taken with ``LeaseOptions/exclusive``.
    public let exclusive: Bool

    let client: Acnologia

    init(wire: LeaseWire, client: Acnologia) {
        leaseID = wire.leaseID
        keyID = wire.keyID
        service = wire.service
        secret = wire.secret
        auth = wire.auth ?? Auth(type: "header", name: "")
        var base = wire.baseURL ?? ""
        while base.hasSuffix("/") { base.removeLast() }
        baseURL = base
        expiresAt = parseTimestamp(wire.expiresAt)
        exclusive = wire.exclusive ?? false
        self.client = client
    }

    /// Reports how the key behaved.
    ///
    /// - Parameters:
    ///   - outcome: What happened. ``Outcome/rateLimited(retryAfter:)`` carries the cooldown.
    ///   - statusCode: The provider's HTTP status, if any.
    ///   - message: A short note shown on the dashboard. The server removes the secret from it.
    ///   - rotate: Ask for the next key in the same round trip.
    /// - Returns: The next lease when `rotate` is true and another key is available, else `nil`.
    @discardableResult
    public func report(_ outcome: Outcome = .ok, statusCode: Int? = nil, message: String? = nil, rotate: Bool = false) async throws -> Lease? {
        var retry: Int?
        if case let .rateLimited(retryAfter) = outcome, let r = retryAfter, r > 0 {
            retry = ceilSeconds(r)
        }
        let body = ReportRequest(
            outcome: outcome.wireValue,
            statusCode: (statusCode ?? 0) != 0 ? statusCode : nil,
            retryAfterSec: retry,
            message: (message ?? "").isEmpty ? nil : message,
            rotate: rotate ? true : nil
        )
        let path = "/api/v1/leases/\(strictEscape(leaseID))/report"
        let out: ReportResponse = try await client.call("POST", path, body: body)
        return out.next.map { Lease(wire: $0, client: client) }
    }

    /// Extends an open lease so a long job keeps its key.
    ///
    /// - Parameter ttl: The new lifetime from now, in seconds. `nil` uses the server default (120).
    ///   The server caps it at 24 hours.
    /// - Returns: The new expiry.
    /// - Throws: ``AcnologiaError/api(statusCode:code:message:)`` with code `lease_not_found` when the
    ///   lease is not yours or no longer open.
    @discardableResult
    public func renew(ttl: TimeInterval? = nil) async throws -> Date {
        let body = RenewRequest(ttlSec: ttl.flatMap { $0 > 0 ? ceilSeconds($0) : nil })
        let path = "/api/v1/leases/\(strictEscape(leaseID))/renew"
        let out: RenewResponse = try await client.call("POST", path, body: body)
        guard let date = parseTimestamp(out.expiresAt) else {
            throw AcnologiaError.decoding("renew response has no expires_at")
        }
        return date
    }

    /// Puts the secret on `request` where the provider expects it.
    ///
    /// `query` sets the query parameter ``Auth/name`` (default `api_key`), `header` sets header
    /// ``Auth/name``, and `bearer` sets ``Auth/name`` (default `Authorization`) to `Bearer <secret>`.
    /// A non empty ``Auth/prefix`` goes in front of the secret for every type; for `bearer` it
    /// replaces `Bearer `. Header names default to `Authorization` when ``Auth/name`` is empty.
    public func apply(to request: inout URLRequest) {
        let value = auth.prefix + secret
        switch auth.type {
        case "query":
            guard let url = request.url, var comps = URLComponents(url: url, resolvingAgainstBaseURL: false) else { return }
            let name = auth.name.isEmpty ? "api_key" : auth.name
            var items = (comps.percentEncodedQueryItems ?? []).filter { $0.name != strictEscape(name) && $0.name != name }
            items.append(URLQueryItem(name: strictEscape(name), value: strictEscape(value)))
            comps.percentEncodedQueryItems = items
            if let u = comps.url { request.url = u }
        case "bearer":
            let name = auth.name.isEmpty ? "Authorization" : auth.name
            request.setValue(auth.prefix.isEmpty ? "Bearer \(secret)" : value, forHTTPHeaderField: name)
        default:
            let name = auth.name.isEmpty ? "Authorization" : auth.name
            request.setValue(value, forHTTPHeaderField: name)
        }
    }

    /// Returns a copy of `request` with the secret applied. See ``apply(to:)``.
    public func applied(to request: URLRequest) -> URLRequest {
        var r = request
        apply(to: &r)
        return r
    }

    /// Joins ``baseURL`` with `path` and `query`. The secret is not added; call ``apply(to:)``
    /// on the request for that.
    public func url(_ path: String, query: [URLQueryItem] = []) throws -> URL {
        var p = path
        while p.hasPrefix("/") { p.removeFirst() }
        var s = baseURL + "/" + p
        if !query.isEmpty { s += (s.contains("?") ? "&" : "?") + encodeQuery(query) }
        guard let u = URL(string: s) else { throw AcnologiaError.invalidArgument("bad URL \(s)") }
        return u
    }
}

extension Lease: CustomStringConvertible, CustomDebugStringConvertible, CustomReflectable {
    public var description: String {
        let exp = expiresAt.map { ISO8601DateFormatter().string(from: $0) } ?? "nil"
        return "Lease(leaseID: \(leaseID), keyID: \(keyID), service: \(service), secret: [redacted], "
            + "auth: \(auth.type)/\(auth.name), baseURL: \(baseURL), expiresAt: \(exp))"
    }

    public var debugDescription: String { description }

    public var customMirror: Mirror {
        Mirror(self, children: [
            "leaseID": leaseID, "keyID": keyID, "service": service, "secret": "[redacted]",
            "auth": auth, "baseURL": baseURL, "expiresAt": expiresAt as Any, "exclusive": exclusive,
        ], displayStyle: .struct)
    }
}
