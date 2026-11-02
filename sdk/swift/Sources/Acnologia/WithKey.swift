import Foundation

/// The longest message sent with a report built from an error.
let maxReportMessage = 500

extension Acnologia {
    /// Leases a key for `service` and runs `body` with it, rotating keys on key problems.
    ///
    /// - `body` returns: the lease is reported `ok` and the value is returned. A failed report
    ///   is thrown.
    /// - `body` throws ``AcnologiaError/rateLimited(retryAfter:statusCode:message:)``,
    ///   ``AcnologiaError/quotaExhausted(statusCode:message:)`` or
    ///   ``AcnologiaError/invalidKey(statusCode:message:)``: the outcome is reported with
    ///   `rotate` and `body` runs again with the next key, up to `maxAttempts` keys. The final
    ///   attempt is reported without asking for a next key, and its error is thrown.
    /// - `body` throws anything else: the lease is reported `error` and the error is rethrown
    ///   unchanged.
    ///
    /// When the pool has no further key, ``AcnologiaError/noKeyAvailable(retryAfter:message:)``
    /// or ``AcnologiaError/allKeysBusy(retryAfter:message:)`` is thrown.
    ///
    /// Every lease is reported, even when the surrounding task is cancelled while `body` runs
    /// (the report is sent from a detached task). This matters on services with a concurrency
    /// limit, where an unreported lease keeps one of the key's slots busy until its TTL expires.
    ///
    /// - Parameters:
    ///   - service: The service slug.
    ///   - options: Options for the first lease. ``LeaseOptions/ttl``, ``LeaseOptions/wait`` and
    ///     ``LeaseOptions/exclusive`` also apply to every later lease.
    ///   - maxAttempts: How many keys to try before giving up. Values below 1 mean 5.
    ///   - body: Calls the provider with the lease.
    public func withKey<T>(
        _ service: String,
        options: LeaseOptions = LeaseOptions(),
        maxAttempts: Int = 5,
        _ body: (Lease) async throws -> T
    ) async throws -> T {
        let attempts = maxAttempts > 0 ? maxAttempts : 5
        var tried = options.excludeKeyIDs
        var lease = try await self.lease(service, options: options)
        var lastError: Error = AcnologiaError.invalidArgument("maxAttempts must be at least 1")

        for attempt in 1...attempts {
            let result: T
            do {
                result = try await body(lease)
            } catch {
                guard let rotation = rotationOutcome(error, secret: lease.secret) else {
                    _ = try? await reportDetached(lease, .error, message: scrubMessage(describe(error), secret: lease.secret))
                    throw error
                }
                lastError = error
                tried.append(lease.keyID)
                if attempt == attempts || Task.isCancelled {
                    _ = try? await reportDetached(lease, rotation.outcome, statusCode: rotation.statusCode, message: rotation.message)
                    if attempt < attempts { throw CancellationError() }
                    break
                }
                let next = try await lease.report(rotation.outcome, statusCode: rotation.statusCode, message: rotation.message, rotate: true)
                if let next {
                    lease = next
                } else {
                    lease = try await self.lease(service, options: LeaseOptions(
                        excludeKeyIDs: tried, ttl: options.ttl, wait: options.wait, exclusive: options.exclusive))
                }
                continue
            }
            try await reportDetached(lease, .ok)
            return result
        }
        throw lastError
    }

    /// Reports from an unstructured task, so the report still goes out when the caller's task
    /// is cancelled.
    @discardableResult
    func reportDetached(_ lease: Lease, _ outcome: Outcome, statusCode: Int? = nil, message: String? = nil) async throws -> Lease? {
        guard Task.isCancelled else {
            return try await lease.report(outcome, statusCode: statusCode, message: message)
        }
        return try await Task.detached {
            try await lease.report(outcome, statusCode: statusCode, message: message)
        }.value
    }
}

struct Rotation {
    let outcome: Outcome
    let statusCode: Int?
    let message: String?
}

func rotationOutcome(_ error: Error, secret: String) -> Rotation? {
    guard let e = error as? AcnologiaError else { return nil }
    switch e {
    case let .rateLimited(retryAfter, status, message):
        return Rotation(outcome: .rateLimited(retryAfter: retryAfter), statusCode: status,
                        message: message.map { scrubMessage($0, secret: secret) })
    case let .quotaExhausted(status, message):
        return Rotation(outcome: .quotaExhausted, statusCode: status, message: message.map { scrubMessage($0, secret: secret) })
    case let .invalidKey(status, message):
        return Rotation(outcome: .invalid, statusCode: status, message: message.map { scrubMessage($0, secret: secret) })
    default:
        return nil
    }
}

func describe(_ error: Error) -> String {
    if let e = error as? LocalizedError, let d = e.errorDescription { return d }
    return String(describing: error)
}

/// Removes the lease secret (raw and percent encoded) from `message` and truncates it. Errors
/// from query auth services often carry the full request URL, key included, and the report
/// must not copy it to the server.
public func scrubMessage(_ message: String, secret: String) -> String {
    var out = message
    if !secret.isEmpty {
        out = out.replacingOccurrences(of: secret, with: "[key]")
        out = out.replacingOccurrences(of: strictEscape(secret), with: "[key]")
        let plus = strictEscape(secret).replacingOccurrences(of: "%20", with: "+")
        out = out.replacingOccurrences(of: plus, with: "[key]")
    }
    if out.count > maxReportMessage { out = String(out.prefix(maxReportMessage)) }
    return out
}
