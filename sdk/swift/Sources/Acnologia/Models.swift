import Foundation

/// Where a provider expects the key.
///
/// `type` is `query`, `header` or `bearer`; `name` is the query parameter or header name.
/// A non empty `prefix` goes in front of the secret for every type, and for `bearer` it
/// replaces the default `Bearer ` prefix.
public struct Auth: Codable, Sendable, Equatable {
    public var type: String
    public var name: String
    public var prefix: String

    public init(type: String, name: String, prefix: String = "") {
        self.type = type
        self.name = name
        self.prefix = prefix
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        type = try c.decodeIfPresent(String.self, forKey: .type) ?? "header"
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        prefix = try c.decodeIfPresent(String.self, forKey: .prefix) ?? ""
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(type, forKey: .type)
        try c.encode(name, forKey: .name)
        if !prefix.isEmpty { try c.encode(prefix, forKey: .prefix) }
    }

    enum CodingKeys: String, CodingKey { case type, name, prefix }
}

/// A service from `GET /api/v1/services`.
public struct Service: Codable, Sendable, Equatable {
    public var slug: String
    public var name: String
    public var provider: String
    public var category: String
    /// Whether a key can be leased right now. The server never says how many keys exist.
    public var available: Bool
    public var baseURL: String
    public var auth: Auth

    enum CodingKeys: String, CodingKey {
        case slug, name, provider, category, available, auth
        case baseURL = "base_url"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        slug = try c.decode(String.self, forKey: .slug)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? slug
        provider = try c.decodeIfPresent(String.self, forKey: .provider) ?? ""
        category = try c.decodeIfPresent(String.self, forKey: .category) ?? ""
        available = try c.decodeIfPresent(Bool.self, forKey: .available) ?? false
        baseURL = try c.decodeIfPresent(String.self, forKey: .baseURL) ?? ""
        auth = try c.decodeIfPresent(Auth.self, forKey: .auth) ?? Auth(type: "header", name: "")
    }
}

/// The result of using a leased key, sent with ``Lease/report(_:statusCode:message:rotate:)``.
public enum Outcome: Sendable, Equatable {
    /// The call worked.
    case ok
    /// The provider rate limited the key. `retryAfter` (seconds) sets its cooldown.
    case rateLimited(retryAfter: TimeInterval? = nil)
    /// The key used up its quota.
    case quotaExhausted
    /// The provider rejected the key.
    case invalid
    /// Something else failed. The key is not penalized much.
    case error
    /// The account is at its concurrency cap; the key steps aside briefly.
    case busy
    /// The provider suspended the account; the key leaves rotation.
    case banned

    /// The value sent on the wire.
    public var wireValue: String {
        switch self {
        case .ok: return "ok"
        case .rateLimited: return "rate_limited"
        case .quotaExhausted: return "quota_exhausted"
        case .invalid: return "invalid"
        case .error: return "error"
        case .busy: return "busy"
        case .banned: return "banned"
        }
    }
}

/// Options for ``Acnologia/lease(_:options:)``.
public struct LeaseOptions: Sendable, Equatable {
    /// The longest ``wait`` the server accepts, in seconds.
    public static let maxWait: TimeInterval = 60

    /// Keys that must not be returned, for example keys you already tried.
    public var excludeKeyIDs: [Int64]
    /// Lease lifetime in seconds. `nil` uses the server default (120 on services with a
    /// concurrency limit, 300 otherwise). Sent rounded up to whole seconds.
    public var ttl: TimeInterval?
    /// How long the server may wait for a free slot when every usable key is at its
    /// concurrency limit, 0 to ``maxWait`` seconds. `nil` or 0 fails fast with
    /// ``AcnologiaError/allKeysBusy(retryAfter:message:)``.
    public var wait: TimeInterval?
    /// Keep the key from every other caller until you report or the lease expires.
    public var exclusive: Bool

    public init(excludeKeyIDs: [Int64] = [], ttl: TimeInterval? = nil, wait: TimeInterval? = nil, exclusive: Bool = false) {
        self.excludeKeyIDs = excludeKeyIDs
        self.ttl = ttl
        self.wait = wait
        self.exclusive = exclusive
    }
}

// MARK: - Wire types

struct LeaseWire: Decodable, Sendable {
    let leaseID: String
    let keyID: Int64
    let service: String
    let secret: String
    let auth: Auth?
    let baseURL: String?
    let expiresAt: String?
    let exclusive: Bool?

    enum CodingKeys: String, CodingKey {
        case leaseID = "lease_id"
        case keyID = "key_id"
        case service, secret, auth, exclusive
        case baseURL = "base_url"
        case expiresAt = "expires_at"
    }
}

struct LeaseRequest: Encodable {
    let service: String
    let excludeKeyIDs: [Int64]?
    let ttlSec: Int?
    let waitSec: Int?
    let exclusive: Bool?

    enum CodingKeys: String, CodingKey {
        case service, exclusive
        case excludeKeyIDs = "exclude_key_ids"
        case ttlSec = "ttl_sec"
        case waitSec = "wait_sec"
    }
}

struct ReportRequest: Encodable {
    let outcome: String
    let statusCode: Int?
    let retryAfterSec: Int?
    let message: String?
    let rotate: Bool?

    enum CodingKeys: String, CodingKey {
        case outcome, message, rotate
        case statusCode = "status_code"
        case retryAfterSec = "retry_after_sec"
    }
}

struct ReportResponse: Decodable {
    let ok: Bool?
    let next: LeaseWire?
}

struct RenewRequest: Encodable {
    let ttlSec: Int?
    enum CodingKeys: String, CodingKey { case ttlSec = "ttl_sec" }
}

struct RenewResponse: Decodable {
    let ok: Bool?
    let expiresAt: String?
    enum CodingKeys: String, CodingKey {
        case ok
        case expiresAt = "expires_at"
    }
}

struct ServicesResponse: Decodable {
    let services: [Service]?
}

func ceilSeconds(_ t: TimeInterval) -> Int {
    Int(t.rounded(.up))
}

/// Parses RFC 3339 timestamps, with or without fractional seconds (Go sends up to nine digits).
func parseTimestamp(_ s: String?) -> Date? {
    guard var s, !s.isEmpty else { return nil }
    // ISO8601DateFormatter accepts at most millisecond precision, so trim longer fractions.
    if let dot = s.firstIndex(of: "."),
       let end = s[dot...].firstIndex(where: { $0 == "Z" || $0 == "+" || $0 == "-" }) {
        let digits = s[s.index(after: dot)..<end]
        if digits.count > 3 {
            s = String(s[..<s.index(after: dot)]) + digits.prefix(3) + s[end...]
        }
    }
    let f = ISO8601DateFormatter()
    f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    if let d = f.date(from: s) { return d }
    f.formatOptions = [.withInternetDateTime]
    return f.date(from: s)
}

/// Percent encodes everything except RFC 3986 unreserved characters.
func strictEscape(_ s: String) -> String {
    var allowed = CharacterSet(charactersIn: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
    allowed.insert(charactersIn: "-._~")
    return s.addingPercentEncoding(withAllowedCharacters: allowed) ?? s
}

/// Encodes query items strictly (so `+` and `&` in values survive) and joins them.
func encodeQuery(_ items: [URLQueryItem]) -> String {
    items.map { item in
        let name = strictEscape(item.name)
        guard let value = item.value else { return name }
        return name + "=" + strictEscape(value)
    }.joined(separator: "&")
}
