import Foundation

/// Any JSON value, for the parts of an answer whose shape the server owns (a service's
/// structured guide, a capability's params) and for capability params you send.
public enum JSONValue: Codable, Sendable, Equatable, Hashable {
    case null
    case bool(Bool)
    case number(Double)
    case string(String)
    case array([JSONValue])
    case object([String: JSONValue])

    public init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() {
            self = .null
        } else if let b = try? c.decode(Bool.self) {
            self = .bool(b)
        } else if let n = try? c.decode(Double.self) {
            self = .number(n)
        } else if let s = try? c.decode(String.self) {
            self = .string(s)
        } else if let a = try? c.decode([JSONValue].self) {
            self = .array(a)
        } else {
            self = .object(try c.decode([String: JSONValue].self))
        }
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .null: try c.encodeNil()
        case let .bool(b): try c.encode(b)
        case let .number(n):
            if n == n.rounded(), abs(n) < 1e15 { try c.encode(Int64(n)) } else { try c.encode(n) }
        case let .string(s): try c.encode(s)
        case let .array(a): try c.encode(a)
        case let .object(o): try c.encode(o)
        }
    }

    /// The value for `key` when this is an object.
    public subscript(key: String) -> JSONValue? {
        if case let .object(o) = self { return o[key] }
        return nil
    }

    /// The string, when this is one.
    public var stringValue: String? {
        if case let .string(s) = self { return s }
        return nil
    }

    /// The number, when this is one.
    public var doubleValue: Double? {
        if case let .number(n) = self { return n }
        return nil
    }

    /// The bool, when this is one.
    public var boolValue: Bool? {
        if case let .bool(b) = self { return b }
        return nil
    }
}

extension JSONValue: ExpressibleByNilLiteral, ExpressibleByBooleanLiteral, ExpressibleByIntegerLiteral,
    ExpressibleByFloatLiteral, ExpressibleByStringLiteral, ExpressibleByArrayLiteral, ExpressibleByDictionaryLiteral {
    public init(nilLiteral: ()) { self = .null }
    public init(booleanLiteral value: Bool) { self = .bool(value) }
    public init(integerLiteral value: Int) { self = .number(Double(value)) }
    public init(floatLiteral value: Double) { self = .number(value) }
    public init(stringLiteral value: String) { self = .string(value) }
    public init(arrayLiteral elements: JSONValue...) { self = .array(elements) }
    public init(dictionaryLiteral elements: (String, JSONValue)...) {
        self = .object(Dictionary(elements, uniquingKeysWith: { _, last in last }))
    }
}

// MARK: - me

/// The token's own access, from ``Acnologia/me()``.
public struct Me: Decodable, Sendable, Equatable {
    /// The app (for a share's token, the share's name).
    public var app: MeApp
    /// The app's request and budget limits. Each is `nil` when not set.
    public var limits: AppLimits
    /// Use so far today and this month.
    public var usage: AppUsage
    /// The limit the app has reached, or `nil`.
    public var limitHit: LimitHit?
    /// The services the token may use.
    public var services: [MeService]
    /// The share behind the token, or `nil` for an ordinary app.
    public var share: MeShare?

    enum CodingKeys: String, CodingKey {
        case app, limits, usage, services, share
        case limitHit = "limit_hit"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        app = try c.decodeIfPresent(MeApp.self, forKey: .app) ?? MeApp(id: 0, name: "")
        limits = try c.decodeIfPresent(AppLimits.self, forKey: .limits) ?? AppLimits()
        usage = try c.decodeIfPresent(AppUsage.self, forKey: .usage) ?? AppUsage()
        limitHit = try c.decodeIfPresent(LimitHit.self, forKey: .limitHit)
        services = try c.decodeIfPresent([MeService].self, forKey: .services) ?? []
        share = try c.decodeIfPresent(MeShare.self, forKey: .share)
    }
}

/// The app in ``Me``.
public struct MeApp: Codable, Sendable, Equatable {
    public var id: Int64
    public var name: String

    public init(id: Int64, name: String) {
        self.id = id
        self.name = name
    }
}

/// An app's limits. Each is `nil` when that limit is not set.
public struct AppLimits: Codable, Sendable, Equatable {
    public var dailyRequests: Int64?
    public var monthlyRequests: Int64?
    public var dailyBudget: Double?
    public var monthlyBudget: Double?

    public init(dailyRequests: Int64? = nil, monthlyRequests: Int64? = nil, dailyBudget: Double? = nil, monthlyBudget: Double? = nil) {
        self.dailyRequests = dailyRequests
        self.monthlyRequests = monthlyRequests
        self.dailyBudget = dailyBudget
        self.monthlyBudget = monthlyBudget
    }

    enum CodingKeys: String, CodingKey {
        case dailyRequests = "daily_requests"
        case monthlyRequests = "monthly_requests"
        case dailyBudget = "daily_budget"
        case monthlyBudget = "monthly_budget"
    }
}

/// An app's use so far.
public struct AppUsage: Decodable, Sendable, Equatable {
    public var todayRequests: Int64 = 0
    public var todayCost: Double = 0
    public var monthRequests: Int64 = 0
    public var monthCost: Double = 0
    public var lastUsedAt: Date?
    public var limitHit: LimitHit?

    public init() {}

    enum CodingKeys: String, CodingKey {
        case todayRequests = "today_requests"
        case todayCost = "today_cost"
        case monthRequests = "month_requests"
        case monthCost = "month_cost"
        case lastUsedAt = "last_used_at"
        case limitHit = "limit_hit"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        todayRequests = try c.decodeIfPresent(Int64.self, forKey: .todayRequests) ?? 0
        todayCost = try c.decodeIfPresent(Double.self, forKey: .todayCost) ?? 0
        monthRequests = try c.decodeIfPresent(Int64.self, forKey: .monthRequests) ?? 0
        monthCost = try c.decodeIfPresent(Double.self, forKey: .monthCost) ?? 0
        lastUsedAt = parseTimestamp(try c.decodeIfPresent(String.self, forKey: .lastUsedAt))
        limitHit = try c.decodeIfPresent(LimitHit.self, forKey: .limitHit)
    }
}

/// A limit the app reached: `limit` is `daily_requests`, `monthly_requests`, `daily_budget`
/// or `monthly_budget`, and it resets at `resetAt`.
public struct LimitHit: Decodable, Sendable, Equatable {
    public var limit: String
    public var resetAt: Date?

    enum CodingKeys: String, CodingKey {
        case limit
        case resetAt = "reset_at"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        limit = try c.decodeIfPresent(String.self, forKey: .limit) ?? ""
        resetAt = parseTimestamp(try c.decodeIfPresent(String.self, forKey: .resetAt))
    }
}

/// A service in ``Me``.
public struct MeService: Decodable, Sendable, Equatable {
    public var slug: String
    public var name: String
    public var provider: String
    public var category: String
    /// For an ordinary app 1 or 0 (apps never learn pool sizes); for a share's token the keys
    /// free right now.
    public var available: Int
    /// The service logo as a `data:` URI, or `nil`.
    public var logoURL: String?

    enum CodingKeys: String, CodingKey {
        case slug, name, provider, category, available
        case logoURL = "logo_url"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        slug = try c.decode(String.self, forKey: .slug)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? slug
        provider = try c.decodeIfPresent(String.self, forKey: .provider) ?? ""
        category = try c.decodeIfPresent(String.self, forKey: .category) ?? ""
        if let n = try? c.decodeIfPresent(Int.self, forKey: .available) {
            available = n
        } else {
            available = (try? c.decodeIfPresent(Bool.self, forKey: .available)) == true ? 1 : 0
        }
        logoURL = try c.decodeIfPresent(String.self, forKey: .logoURL)
    }
}

/// The share behind a share's token. `status` is `active`, `paused`, `expired` or `revoked`.
public struct MeShare: Decodable, Sendable, Equatable {
    public var id: String
    public var name: String
    public var ownerName: String
    public var recipient: String
    public var label: String
    public var status: String
    public var mode: String
    public var endsAt: Date?

    enum CodingKeys: String, CodingKey {
        case id, name, recipient, label, status, mode
        case ownerName = "owner_name"
        case endsAt = "ends_at"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id) ?? ""
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        ownerName = try c.decodeIfPresent(String.self, forKey: .ownerName) ?? ""
        recipient = try c.decodeIfPresent(String.self, forKey: .recipient) ?? ""
        label = try c.decodeIfPresent(String.self, forKey: .label) ?? ""
        status = try c.decodeIfPresent(String.self, forKey: .status) ?? ""
        mode = try c.decodeIfPresent(String.self, forKey: .mode) ?? ""
        endsAt = parseTimestamp(try c.decodeIfPresent(String.self, forKey: .endsAt))
    }
}

// MARK: - guides and capabilities

/// A service's usage guide, from ``Acnologia/guide(_:)``.
public struct Guide: Decodable, Sendable, Equatable {
    /// The guide rendered as Markdown.
    public var markdown: String
    /// True when the server generated the guide from the provider's spec.
    public var generated: Bool
    /// The structured guide, as the server sent it.
    public var guide: JSONValue

    enum CodingKeys: String, CodingKey { case markdown, generated, guide }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        markdown = try c.decodeIfPresent(String.self, forKey: .markdown) ?? ""
        generated = try c.decodeIfPresent(Bool.self, forKey: .generated) ?? false
        guide = try c.decodeIfPresent(JSONValue.self, forKey: .guide) ?? .null
    }
}

/// A capability: one job several providers can do, from ``Acnologia/capabilities()``.
public struct Capability: Decodable, Sendable, Equatable {
    public var slug: String
    public var name: String
    public var description: String
    public var strategy: String?
    /// The params the capability takes, as the server describes them.
    public var params: JSONValue
    public var routes: [CapabilityRoute]
    /// Where to call it (`/proxy/{slug}`).
    public var url: String

    enum CodingKeys: String, CodingKey { case slug, name, description, strategy, params, routes, url }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        slug = try c.decode(String.self, forKey: .slug)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? slug
        description = try c.decodeIfPresent(String.self, forKey: .description) ?? ""
        strategy = try c.decodeIfPresent(String.self, forKey: .strategy)
        params = try c.decodeIfPresent(JSONValue.self, forKey: .params) ?? .null
        routes = try c.decodeIfPresent([CapabilityRoute].self, forKey: .routes) ?? []
        url = try c.decodeIfPresent(String.self, forKey: .url) ?? ""
    }
}

/// One provider route of a ``Capability``. `mapped` routes answer in the capability's shape.
public struct CapabilityRoute: Codable, Sendable, Equatable {
    public var service: String
    public var mapped: Bool

    public init(service: String, mapped: Bool) {
        self.service = service
        self.mapped = mapped
    }
}

/// Options for ``Acnologia/capability(_:params:options:)``.
public struct CapabilityOptions: Sendable, Equatable {
    /// Use only this provider's route (`?_service=`).
    public var service: String?
    /// Return the provider's own body instead of the mapped shape (`?_raw=1`).
    public var raw: Bool
    /// Proxy options such as an idempotency key.
    public var proxy: ProxyOptions

    public init(service: String? = nil, raw: Bool = false, proxy: ProxyOptions = ProxyOptions()) {
        self.service = service
        self.raw = raw
        self.proxy = proxy
    }
}

// MARK: - forward proxy

/// How to use the forward proxy, from ``Acnologia/forwardProxy()``.
///
/// None of its fields hold the app token: `proxyURLWithToken` and `env` carry a
/// `<app token>` placeholder. ``Acnologia/forwardProxyURL()`` and
/// ``Acnologia/forwardProxyEnv(caPath:)`` fill the token in.
public struct ForwardProxy: Decodable, Sendable, Equatable {
    /// The proxy address, without credentials, such as `http://host:8888`.
    public var proxyURL: String
    /// The proxy address with `app:<app token>` as a placeholder.
    public var proxyURLWithToken: String
    /// Where to download the CA certificate.
    public var caURL: String
    public var caFingerprint: String
    /// Whether HTTPS to hosts that are no service is tunnelled through.
    public var passthrough: Bool
    /// The hosts of the services the token may use.
    public var hosts: [ForwardProxyHost]
    /// The environment the server suggests, with the token as a placeholder.
    public var env: [String: String]
    /// The request header that picks a service when several match (`X-Acnologia-Service`).
    public var serviceHeader: String

    enum CodingKeys: String, CodingKey {
        case passthrough, hosts, env
        case proxyURL = "proxy_url"
        case proxyURLWithToken = "proxy_url_with_token"
        case caURL = "ca_url"
        case caFingerprint = "ca_fingerprint"
        case serviceHeader = "service_header"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        proxyURL = try c.decodeIfPresent(String.self, forKey: .proxyURL) ?? ""
        proxyURLWithToken = try c.decodeIfPresent(String.self, forKey: .proxyURLWithToken) ?? ""
        caURL = try c.decodeIfPresent(String.self, forKey: .caURL) ?? ""
        caFingerprint = try c.decodeIfPresent(String.self, forKey: .caFingerprint) ?? ""
        passthrough = try c.decodeIfPresent(Bool.self, forKey: .passthrough) ?? false
        hosts = try c.decodeIfPresent([ForwardProxyHost].self, forKey: .hosts) ?? []
        env = try c.decodeIfPresent([String: String].self, forKey: .env) ?? [:]
        serviceHeader = try c.decodeIfPresent(String.self, forKey: .serviceHeader) ?? "X-Acnologia-Service"
    }
}

/// A host the forward proxy routes to a service. `path` is set for a provider host that maps
/// to a path under the service, such as `/~name`.
public struct ForwardProxyHost: Codable, Sendable, Equatable {
    public var host: String
    public var service: String
    public var name: String
    public var path: String?

    public init(host: String, service: String, name: String, path: String? = nil) {
        self.host = host
        self.service = service
        self.name = name
        self.path = path
    }
}

/// One route a capability call or a service group tried, from `X-Acnologia-Tried`.
public struct TriedRoute: Sendable, Equatable {
    public var service: String
    public var status: Int

    public init(service: String, status: Int) {
        self.service = service
        self.status = status
    }
}

/// Parses `X-Acnologia-Tried` (`svc=200, other=503`).
func parseTried(_ header: String?) -> [TriedRoute] {
    guard let header else { return [] }
    return header.split(separator: ",").compactMap { part in
        let p = part.trimmingCharacters(in: .whitespaces)
        guard let eq = p.lastIndex(of: "=") else { return nil }
        let svc = String(p[..<eq])
        guard !svc.isEmpty, let status = Int(p[p.index(after: eq)...]) else { return nil }
        return TriedRoute(service: svc, status: status)
    }
}

struct CapabilitiesResponse: Decodable {
    let capabilities: [Capability]?
}
