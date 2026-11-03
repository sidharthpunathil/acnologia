#pragma once

#include <chrono>
#include <stdexcept>
#include <string>

#include "acnologia/types.hpp"

namespace acnologia {

/// Base class of every exception the SDK throws (argument checks aside,
/// which throw std::invalid_argument).
class Error : public std::runtime_error {
public:
    explicit Error(const std::string& what) : std::runtime_error(what) {}
};

/// The request did not complete: connection refused, DNS failure, timeout.
class TransportError : public Error {
public:
    explicit TransportError(const std::string& what) : Error(what) {}
};

/// A non 2xx answer from the consumer API that has no more specific type.
/// The server sends {"error": {"code", "message"}} with a matching status.
/// The token and the access password are scrubbed from the message.
class ApiError : public Error {
public:
    ApiError(int status, std::string code, std::string message);
    ApiError(int status, std::string code, std::string message, std::chrono::milliseconds retry_after);

    int status() const noexcept { return status_; }
    const std::string& code() const noexcept { return code_; }
    const std::string& message() const noexcept { return message_; }
    /// How long to wait before trying again: retry_after_sec from the body,
    /// else the Retry-After header. Zero when the server did not say.
    std::chrono::milliseconds retry_after() const noexcept { return retry_after_; }

protected:
    ApiError(int status, std::string code, std::string message, const std::string& what,
             std::chrono::milliseconds retry_after = {});

private:
    int status_;
    std::string code_;
    std::string message_;
    std::chrono::milliseconds retry_after_;
};

/// No key can be leased right now (HTTP 503 no_key_available): every key is
/// cooling down, exhausted, disabled or excluded. retry_after is zero when
/// the server did not say.
class NoKeyAvailable : public ApiError {
public:
    NoKeyAvailable(std::string message, std::chrono::milliseconds retry_after,
                   std::string code = "no_key_available", int status = 503);

    /// True for all_keys_busy (the AllKeysBusy subclass).
    bool busy() const noexcept { return code() == "all_keys_busy"; }
};

/// Usable keys exist but every one is at its concurrency limit, or at a rate
/// limit its provider publishes (HTTP 503 all_keys_busy). It clears as soon as
/// another caller reports, so retry after retry_after (usually 1s) or lease
/// with LeaseOptions::wait. Derives from NoKeyAvailable, so handlers for that
/// still catch it.
class AllKeysBusy : public NoKeyAvailable {
public:
    AllKeysBusy(std::string message, std::chrono::milliseconds retry_after, int status = 503);
};

/// The provider keeps failing and its circuit breaker is open (HTTP 503
/// service_unavailable). Wait retry_after.
class ServiceUnavailable : public ApiError {
public:
    ServiceUnavailable(std::string message, std::chrono::milliseconds retry_after, int status = 503);
};

/// The app used up a daily or monthly request or budget limit (HTTP 429
/// app_limit_reached). limit() says which, reset_at() (RFC 3339) until when,
/// and retry_after() how long that is from now.
class AppLimitReached : public ApiError {
public:
    AppLimitReached(std::string message, std::string limit, std::string reset_at,
                    std::chrono::milliseconds retry_after, int status = 429);

    /// daily_requests, monthly_requests, daily_budget or monthly_budget.
    const std::string& limit() const noexcept { return limit_; }
    const std::string& reset_at() const noexcept { return reset_at_; }

private:
    std::string limit_;
    std::string reset_at_;
};

/// HTTP 401: the app token is missing or wrong.
class Unauthenticated : public ApiError {
public:
    Unauthenticated(std::string code, std::string message, int status = 401);
};

/// HTTP 403: the app is disabled, or not allowed this service or mode
/// (service_not_allowed, lease_not_allowed, proxy_not_allowed). The more
/// specific 403s below derive from it.
class Forbidden : public ApiError {
public:
    Forbidden(std::string code, std::string message, int status = 403);
};

/// HTTP 403 lease_not_allowed: leasing is not open to this token. The app's
/// mode is proxy, or the service is proxy only (a service another server
/// shares, one holding handed over keys, or any service for a person's
/// token). Call it through Client::proxy instead.
class LeaseNotAllowed : public Forbidden {
public:
    explicit LeaseNotAllowed(std::string message, int status = 403);
};

/// HTTP 403 lease_revoked from renew: the app may no longer hold the key, so
/// the lease has ended. Lease a new key.
class LeaseRevoked : public Forbidden {
public:
    explicit LeaseRevoked(std::string message, int status = 403);
};

/// HTTP 403 for a share's token whose share has ended or stopped:
/// share_revoked, share_paused or share_expired. Nothing on this side can fix
/// it; the owner has to resume or renew the share.
class ShareUnavailable : public Forbidden {
public:
    ShareUnavailable(std::string code, std::string message, int status = 403);
    /// revoked, paused or expired.
    std::string share_status() const;
};

/// HTTP 403 share_revoked: the owner revoked the share.
class ShareRevoked : public ShareUnavailable {
public:
    explicit ShareRevoked(std::string message, int status = 403);
};

/// HTTP 403 share_paused: the owner paused the share. It may resume.
class SharePaused : public ShareUnavailable {
public:
    explicit SharePaused(std::string message, int status = 403);
};

/// HTTP 403 share_expired: the share's timer ran out.
class ShareExpired : public ShareUnavailable {
public:
    explicit ShareExpired(std::string message, int status = 403);
};

/// HTTP 502 upstream_unreachable: the proxy could not reach the provider.
class UpstreamUnreachable : public ApiError {
public:
    explicit UpstreamUnreachable(std::string message, int status = 502);
};

/// HTTP 502 upstream_blocked: the service's address points at a private or
/// metadata address the server refuses to call.
class UpstreamBlocked : public ApiError {
public:
    explicit UpstreamBlocked(std::string message, int status = 502);
};

/// HTTP 508 loop_detected: the request already passed through more than 3
/// Acnologia servers.
class LoopDetected : public ApiError {
public:
    explicit LoopDetected(std::string message, int status = 508);
};

/// Base of the exceptions you throw from a with_key callback to report the
/// key and rotate to the next one.
class KeyRotationError : public Error {
public:
    virtual Outcome outcome() const noexcept = 0;
    virtual std::chrono::milliseconds retry_after() const noexcept { return {}; }
    int status_code() const noexcept { return status_code_; }
    /// The message given at construction (sent with the report, secret scrubbed).
    const std::string& message() const noexcept { return message_; }

protected:
    KeyRotationError(const std::string& what, int status_code, std::string message);

private:
    int status_code_;
    std::string message_;
};

/// The provider rate limited the key. Reported as rate_limited with
/// retry_after as the cooldown.
class RateLimited : public KeyRotationError {
public:
    explicit RateLimited(std::chrono::milliseconds retry_after = {}, int status_code = 0,
                         std::string message = {});
    Outcome outcome() const noexcept override { return Outcome::rate_limited; }
    std::chrono::milliseconds retry_after() const noexcept override { return retry_after_; }

private:
    std::chrono::milliseconds retry_after_;
};

/// The key used up its quota. Reported as quota_exhausted.
class QuotaExhausted : public KeyRotationError {
public:
    explicit QuotaExhausted(int status_code = 0, std::string message = {});
    Outcome outcome() const noexcept override { return Outcome::quota_exhausted; }
};

/// The provider rejected the key. Reported as invalid.
class InvalidKey : public KeyRotationError {
public:
    explicit InvalidKey(int status_code = 0, std::string message = {});
    Outcome outcome() const noexcept override { return Outcome::invalid; }
};

}  // namespace acnologia
