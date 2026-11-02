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
class ApiError : public Error {
public:
    ApiError(int status, std::string code, std::string message);

    int status() const noexcept { return status_; }
    const std::string& code() const noexcept { return code_; }
    const std::string& message() const noexcept { return message_; }

protected:
    ApiError(int status, std::string code, std::string message, const std::string& what);

private:
    int status_;
    std::string code_;
    std::string message_;
};

/// No key can be leased right now (HTTP 503 no_key_available): every key is
/// cooling down, exhausted, disabled or excluded. retry_after is zero when
/// the server did not say.
class NoKeyAvailable : public ApiError {
public:
    NoKeyAvailable(std::string message, std::chrono::milliseconds retry_after,
                   std::string code = "no_key_available", int status = 503);

    std::chrono::milliseconds retry_after() const noexcept { return retry_after_; }
    /// True for all_keys_busy (the AllKeysBusy subclass).
    bool busy() const noexcept { return code() == "all_keys_busy"; }

private:
    std::chrono::milliseconds retry_after_;
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
    std::chrono::milliseconds retry_after() const noexcept { return retry_after_; }

private:
    std::chrono::milliseconds retry_after_;
};

/// HTTP 401: the app token is missing or wrong.
class Unauthenticated : public ApiError {
public:
    Unauthenticated(std::string code, std::string message, int status = 401);
};

/// HTTP 403: the app is disabled, or not allowed this service or mode
/// (service_not_allowed, lease_not_allowed, proxy_not_allowed).
class Forbidden : public ApiError {
public:
    Forbidden(std::string code, std::string message, int status = 403);
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
