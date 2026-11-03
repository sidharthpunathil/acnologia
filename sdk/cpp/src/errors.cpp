#include "acnologia/errors.hpp"

#include <sstream>
#include <utility>

namespace acnologia {
namespace {

std::string format_duration(std::chrono::milliseconds d) {
    std::ostringstream os;
    if (d.count() % 1000 == 0) {
        os << d.count() / 1000 << "s";
    } else {
        os << static_cast<double>(d.count()) / 1000.0 << "s";
    }
    return os.str();
}

std::string api_what(int status, const std::string& code, const std::string& message) {
    std::string w = "acnologia: http " + std::to_string(status) + ": ";
    if (!code.empty()) w += code + ": ";
    return w + message;
}

std::string retry_what(const std::string& head, std::chrono::milliseconds retry_after, const std::string& message) {
    std::string w = head;
    if (retry_after.count() > 0) w += " (retry after " + format_duration(retry_after) + ")";
    if (!message.empty()) w += ": " + message;
    return w;
}

std::string simple_what(const std::string& head, const std::string& message) {
    return message.empty() ? head : head + ": " + message;
}

std::string limit_what(const std::string& limit, const std::string& reset_at, std::chrono::milliseconds retry_after,
                       const std::string& message) {
    std::string head = "acnologia: app limit reached";
    if (!limit.empty()) head += " (" + limit + (reset_at.empty() ? "" : ", resets " + reset_at) + ")";
    return retry_what(head, retry_after, message);
}

}  // namespace

ApiError::ApiError(int status, std::string code, std::string message)
    : ApiError(status, code, message, api_what(status, code, message)) {}

ApiError::ApiError(int status, std::string code, std::string message, std::chrono::milliseconds retry_after)
    : ApiError(status, code, message, api_what(status, code, message), retry_after) {}

ApiError::ApiError(int status, std::string code, std::string message, const std::string& what,
                   std::chrono::milliseconds retry_after)
    : Error(what),
      status_(status),
      code_(std::move(code)),
      message_(std::move(message)),
      retry_after_(retry_after) {}

NoKeyAvailable::NoKeyAvailable(std::string message, std::chrono::milliseconds retry_after, std::string code,
                               int status)
    : ApiError(status, code, message,
               retry_what(code == "all_keys_busy" ? "acnologia: all keys busy" : "acnologia: no key available",
                          retry_after, message),
               retry_after) {}

AllKeysBusy::AllKeysBusy(std::string message, std::chrono::milliseconds retry_after, int status)
    : NoKeyAvailable(std::move(message), retry_after, "all_keys_busy", status) {}

ServiceUnavailable::ServiceUnavailable(std::string message, std::chrono::milliseconds retry_after, int status)
    : ApiError(status, "service_unavailable", message,
               retry_what("acnologia: service unavailable", retry_after, message), retry_after) {}

AppLimitReached::AppLimitReached(std::string message, std::string limit, std::string reset_at,
                                 std::chrono::milliseconds retry_after, int status)
    : ApiError(status, "app_limit_reached", message, limit_what(limit, reset_at, retry_after, message), retry_after),
      limit_(std::move(limit)),
      reset_at_(std::move(reset_at)) {}

Unauthenticated::Unauthenticated(std::string code, std::string message, int status)
    : ApiError(status, code, message, api_what(status, code, message)) {}

Forbidden::Forbidden(std::string code, std::string message, int status)
    : ApiError(status, code, message, api_what(status, code, message)) {}

LeaseNotAllowed::LeaseNotAllowed(std::string message, int status)
    : Forbidden("lease_not_allowed", std::move(message), status) {}

LeaseRevoked::LeaseRevoked(std::string message, int status) : Forbidden("lease_revoked", std::move(message), status) {}

ShareUnavailable::ShareUnavailable(std::string code, std::string message, int status)
    : Forbidden(std::move(code), std::move(message), status) {}

std::string ShareUnavailable::share_status() const {
    const std::string& c = code();
    return c.rfind("share_", 0) == 0 ? c.substr(6) : c;
}

ShareRevoked::ShareRevoked(std::string message, int status)
    : ShareUnavailable("share_revoked", std::move(message), status) {}

SharePaused::SharePaused(std::string message, int status)
    : ShareUnavailable("share_paused", std::move(message), status) {}

ShareExpired::ShareExpired(std::string message, int status)
    : ShareUnavailable("share_expired", std::move(message), status) {}

UpstreamUnreachable::UpstreamUnreachable(std::string message, int status)
    : ApiError(status, "upstream_unreachable", message, api_what(status, "upstream_unreachable", message)) {}

UpstreamBlocked::UpstreamBlocked(std::string message, int status)
    : ApiError(status, "upstream_blocked", message, api_what(status, "upstream_blocked", message)) {}

LoopDetected::LoopDetected(std::string message, int status)
    : ApiError(status, "loop_detected", message, api_what(status, "loop_detected", message)) {}

KeyRotationError::KeyRotationError(const std::string& what, int status_code, std::string message)
    : Error(what), status_code_(status_code), message_(std::move(message)) {}

RateLimited::RateLimited(std::chrono::milliseconds retry_after, int status_code, std::string message)
    : KeyRotationError(retry_what("acnologia: rate limited", retry_after, message), status_code, message),
      retry_after_(retry_after) {}

QuotaExhausted::QuotaExhausted(int status_code, std::string message)
    : KeyRotationError(simple_what("acnologia: quota exhausted", message), status_code, message) {}

InvalidKey::InvalidKey(int status_code, std::string message)
    : KeyRotationError(simple_what("acnologia: invalid key", message), status_code, message) {}

}  // namespace acnologia
