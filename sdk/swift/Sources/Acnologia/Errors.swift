import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

/// Every error the SDK throws, and the errors you throw from a ``Acnologia/withKey(_:options:maxAttempts:_:)``
/// body to rotate keys.
///
/// Throw ``rateLimited(retryAfter:statusCode:message:)``, ``quotaExhausted(statusCode:message:)`` or
/// ``invalidKey(statusCode:message:)`` from a `withKey` body to report the key and retry with the next one.
/// The server answers with the other cases.
public enum AcnologiaError: Error, Sendable, Equatable {
    /// The provider rate limited the key. `retryAfter` is how long the key should cool down, in seconds.
    case rateLimited(retryAfter: TimeInterval? = nil, statusCode: Int? = nil, message: String? = nil)
    /// The key used up its quota.
    case quotaExhausted(statusCode: Int? = nil, message: String? = nil)
    /// The provider rejected the key.
    case invalidKey(statusCode: Int? = nil, message: String? = nil)
    /// HTTP 503 `no_key_available`: every key is cooling down, exhausted, disabled or excluded.
    case noKeyAvailable(retryAfter: TimeInterval?, message: String)
    /// HTTP 503 `all_keys_busy`: usable keys exist but every one is at its concurrency limit.
    /// Retry after `retryAfter` (usually 1 second) or lease with ``LeaseOptions/wait``.
    case allKeysBusy(retryAfter: TimeInterval?, message: String)
    /// HTTP 503 `service_unavailable`: the provider keeps failing and its circuit breaker is open.
    case serviceUnavailable(retryAfter: TimeInterval?, message: String)
    /// HTTP 429 `app_limit_reached`: the app used up a daily or monthly request or budget limit.
    /// `limit` is `daily_requests`, `monthly_requests`, `daily_budget` or `monthly_budget`, and it
    /// resets at `resetAt`.
    case appLimitReached(limit: String, resetAt: Date?, retryAfter: TimeInterval?, message: String)
    /// HTTP 403 `share_revoked`, `share_paused` or `share_expired`: the share behind this token
    /// stopped. `status` is `revoked`, `paused` or `expired`.
    case shareUnavailable(status: String, message: String)
    /// HTTP 401: the app token is missing, wrong, revoked or expired.
    case unauthenticated(message: String)
    /// HTTP 403: the app is disabled or may not use this service or mode.
    /// `code` is for example `service_not_allowed`, `lease_not_allowed` or `proxy_not_allowed`.
    case forbidden(code: String, message: String)
    /// Any other non-2xx answer from the consumer API.
    case api(statusCode: Int, code: String, message: String)
    /// A bad argument or missing configuration, caught before any request is sent.
    case invalidArgument(String)
    /// The server answered with a body the SDK could not decode.
    case decoding(String)

    /// Seconds to wait before retrying, when the server or the thrower gave one.
    public var retryAfter: TimeInterval? {
        switch self {
        case let .rateLimited(retryAfter, _, _),
             let .noKeyAvailable(retryAfter, _),
             let .allKeysBusy(retryAfter, _),
             let .serviceUnavailable(retryAfter, _),
             let .appLimitReached(_, _, retryAfter, _):
            return retryAfter
        default:
            return nil
        }
    }

    /// The HTTP status code, when there is one.
    public var statusCode: Int? {
        switch self {
        case let .rateLimited(_, status, _), let .quotaExhausted(status, _), let .invalidKey(status, _):
            return status
        case .noKeyAvailable, .allKeysBusy, .serviceUnavailable:
            return 503
        case .appLimitReached:
            return 429
        case .unauthenticated:
            return 401
        case .forbidden, .shareUnavailable:
            return 403
        case let .api(status, _, _):
            return status
        case .invalidArgument, .decoding:
            return nil
        }
    }

    /// The machine readable error code from the server, or the outcome name for rotation errors.
    public var code: String {
        switch self {
        case .rateLimited: return "rate_limited"
        case .quotaExhausted: return "quota_exhausted"
        case .invalidKey: return "invalid"
        case .noKeyAvailable: return "no_key_available"
        case .allKeysBusy: return "all_keys_busy"
        case .serviceUnavailable: return "service_unavailable"
        case .appLimitReached: return "app_limit_reached"
        case let .shareUnavailable(status, _): return "share_" + status
        case .unauthenticated: return "unauthenticated"
        case let .forbidden(code, _): return code
        case let .api(_, code, _): return code
        case .invalidArgument: return "invalid_argument"
        case .decoding: return "decoding"
        }
    }

    /// The human readable message, if any.
    public var message: String? {
        switch self {
        case let .rateLimited(_, _, m), let .quotaExhausted(_, m), let .invalidKey(_, m):
            return m
        case let .noKeyAvailable(_, m), let .allKeysBusy(_, m), let .serviceUnavailable(_, m),
             let .appLimitReached(_, _, _, m), let .shareUnavailable(_, m),
             let .unauthenticated(m), let .forbidden(_, m), let .api(_, _, m),
             let .invalidArgument(m), let .decoding(m):
            return m
        }
    }

    /// True for ``noKeyAvailable(retryAfter:message:)`` and ``allKeysBusy(retryAfter:message:)``:
    /// no key can be leased right now.
    public var isNoKeyAvailable: Bool {
        switch self {
        case .noKeyAvailable, .allKeysBusy: return true
        default: return false
        }
    }

    /// True for the errors that make `withKey` report the key and rotate.
    public var rotatesKey: Bool {
        switch self {
        case .rateLimited, .quotaExhausted, .invalidKey: return true
        default: return false
        }
    }
}

extension AcnologiaError: CustomStringConvertible, LocalizedError {
    public var description: String {
        var text: String
        switch self {
        case .rateLimited: text = "acnologia: rate limited"
        case .quotaExhausted: text = "acnologia: quota exhausted"
        case .invalidKey: text = "acnologia: invalid key"
        case .noKeyAvailable: text = "acnologia: no key available"
        case .allKeysBusy: text = "acnologia: all keys busy"
        case .serviceUnavailable: text = "acnologia: service unavailable"
        case let .appLimitReached(limit, _, _, _):
            text = "acnologia: http 429: app_limit_reached"
            if !limit.isEmpty { text += " (" + limit + ")" }
        case let .shareUnavailable(status, _): text = "acnologia: http 403: share " + status
        case .unauthenticated: text = "acnologia: http 401: unauthenticated"
        case let .forbidden(code, _): text = "acnologia: http 403: \(code)"
        case let .api(status, code, _):
            text = code.isEmpty ? "acnologia: http \(status)" : "acnologia: http \(status): \(code)"
        case .invalidArgument: text = "acnologia: invalid argument"
        case .decoding: text = "acnologia: decode response"
        }
        if let retry = retryAfter, retry > 0 {
            text += " (retry after \(formatSeconds(retry)))"
        }
        if let m = message, !m.isEmpty {
            text += ": " + m
        }
        return text
    }

    public var errorDescription: String? { description }
}

private func formatSeconds(_ s: TimeInterval) -> String {
    s == s.rounded() ? "\(Int(s))s" : "\(s)s"
}

/// Builds the typed error for a non-2xx answer from Acnologia itself.
///
/// `shareHeader` is `X-Acnologia-Share`, which a stopped share's answer carries.
func parseAPIError(status: Int, data: Data, retryAfterHeader: String? = nil, shareHeader: String? = nil) -> AcnologiaError {
    var code = ""
    var message = ""
    var retryAfter: TimeInterval?
    var limit = ""
    var resetAt: Date?
    if let obj = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] {
        limit = obj["limit"] as? String ?? ""
        resetAt = parseTimestamp(obj["reset_at"] as? String)
        if let err = obj["error"] as? [String: Any] {
            code = err["code"] as? String ?? ""
            message = err["message"] as? String ?? ""
        } else if let err = obj["error"] as? String {
            message = err
        }
        if let n = obj["retry_after_sec"] as? NSNumber {
            retryAfter = n.doubleValue
        }
    }
    if retryAfter == nil, let h = retryAfterHeader, let n = Double(h.trimmingCharacters(in: .whitespaces)) {
        retryAfter = n
    }
    if message.isEmpty && code.isEmpty {
        message = String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
        if message.count > 200 { message = String(message.prefix(200)) }
        if message.isEmpty { message = HTTPURLResponse.localizedString(forStatusCode: status) }
    }
    switch code {
    case "all_keys_busy":
        return .allKeysBusy(retryAfter: retryAfter, message: message)
    case "no_key_available":
        return .noKeyAvailable(retryAfter: retryAfter, message: message)
    case "service_unavailable":
        return .serviceUnavailable(retryAfter: retryAfter, message: message)
    case "app_limit_reached":
        return .appLimitReached(limit: limit, resetAt: resetAt, retryAfter: retryAfter, message: message)
    case "share_revoked", "share_paused", "share_expired":
        return .shareUnavailable(status: String(code.dropFirst("share_".count)), message: message)
    case "" where status == 403 && ["revoked", "paused", "expired"].contains(shareHeader ?? ""):
        return .shareUnavailable(status: shareHeader ?? "", message: message)
    case "" where status == 503:
        return .noKeyAvailable(retryAfter: retryAfter, message: message)
    default:
        break
    }
    switch status {
    case 401: return .unauthenticated(message: message)
    case 403: return .forbidden(code: code.isEmpty ? "forbidden" : code, message: message)
    default: return .api(statusCode: status, code: code, message: message)
    }
}
