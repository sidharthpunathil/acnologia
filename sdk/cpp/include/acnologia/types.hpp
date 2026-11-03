#pragma once

#include <chrono>
#include <cstddef>
#include <cstdint>
#include <iosfwd>
#include <optional>
#include <string>
#include <utility>
#include <vector>

namespace acnologia {

/// The SDK version, sent in the User-Agent header as acnologia-cpp/<version>.
inline constexpr const char* version = "0.1.0";

/// Ordered name and value pairs, used for query parameters and headers.
using Query = std::vector<std::pair<std::string, std::string>>;
using Headers = std::vector<std::pair<std::string, std::string>>;

/// Where a provider expects the key. type is "query", "header" or "bearer";
/// name is the parameter or header name. A non empty prefix goes in front of
/// the secret for every type (for bearer it replaces the default "Bearer ").
struct Auth {
    std::string type;
    std::string name;
    std::string prefix;
};

/// An entry from GET /api/v1/services.
struct Service {
    std::string slug;
    std::string name;
    std::string provider;
    std::string category;
    bool available = false;
    std::string base_url;
    Auth auth;
    /// Where the service's usage guide is served. Client::guide fetches it.
    std::string guide_url;
};

/// The app behind a token, from GET /api/v1/me. For a share's token, name is
/// the share's name.
struct AppInfo {
    std::int64_t id = 0;
    std::string name;
};

/// An app's request and budget limits. An empty optional means no limit.
struct AppLimits {
    std::optional<std::int64_t> daily_requests;
    std::optional<std::int64_t> monthly_requests;
    std::optional<double> daily_budget;
    std::optional<double> monthly_budget;
};

/// A limit the app has reached. limit is daily_requests, monthly_requests,
/// daily_budget or monthly_budget, and reset_at (RFC 3339) is when it resets.
struct LimitHit {
    std::string limit;
    std::string reset_at;
};

/// What the app used today and this month (UTC).
struct AppUsage {
    std::int64_t today_requests = 0;
    double today_cost = 0;
    std::int64_t month_requests = 0;
    double month_cost = 0;
    /// RFC 3339, empty when the app was never used.
    std::string last_used_at;
    std::optional<LimitHit> limit_hit;
};

/// A service in GET /api/v1/me. available counts keys free right now for a
/// share's token. For an ordinary app it is 1 or 0.
struct MeService {
    std::string slug;
    std::string name;
    std::string provider;
    std::string category;
    int available = 0;
    /// The logo as a data: URI, or empty.
    std::string logo_url;
};

/// The server that owns a share.
struct ShareOwner {
    std::string name;
    std::string id;
};

/// The share behind a share's token (another server's, or a person's from a
/// claim link).
struct ShareInfo {
    std::string id;
    std::string name;
    ShareOwner owner;
    std::string recipient;  ///< "server" or "person"
    std::string label;
    /// pending, active, paused, expired or revoked.
    std::string status;
    std::string mode;
    /// RFC 3339, empty when the share has no end.
    std::string ends_at;

    bool active() const noexcept { return status == "active"; }
};

/// GET /api/v1/me: the token's own access.
struct Me {
    AppInfo app;
    AppLimits limits;
    AppUsage usage;
    /// The first limit reached, if any (the same as usage.limit_hit).
    std::optional<LimitHit> limit_hit;
    std::vector<MeService> services;
    /// Set only for a share's token. A paused, expired or revoked share still
    /// answers, with that status and no services.
    std::optional<ShareInfo> share;
};

/// The result of using a leased key.
enum class Outcome {
    ok,
    rate_limited,
    quota_exhausted,
    invalid,
    error,
    busy,    ///< the account is at its concurrency cap; the key steps aside briefly
    banned,  ///< the provider suspended the account; the key leaves rotation
};

/// The wire name of an outcome ("ok", "rate_limited", ...).
const char* to_string(Outcome outcome) noexcept;
std::ostream& operator<<(std::ostream& os, Outcome outcome);

/// The longest LeaseOptions::wait the server accepts.
inline constexpr std::chrono::seconds max_wait{60};

/// Optional parameters for Client::lease. Durations are rounded up to whole
/// seconds on the wire.
struct LeaseOptions {
    /// Keys that must not be returned.
    std::vector<std::int64_t> exclude_key_ids;
    /// Lease lifetime; zero uses the server default (120s on services with a
    /// concurrency limit, 300s otherwise).
    std::chrono::milliseconds ttl{0};
    /// How long the server may wait (at most max_wait) for a free slot when
    /// every usable key is at its concurrency limit. Zero fails fast with
    /// AllKeysBusy. The request timeout is stretched to outlast it.
    std::chrono::milliseconds wait{0};
    /// Keep the key from every other caller until it is reported or expires.
    bool exclusive = false;
};

/// How a leased key behaved. Set rotate to receive the next key in the same
/// round trip.
struct Report {
    Outcome outcome = Outcome::ok;
    int status_code = 0;                      ///< upstream status, 0 to omit
    std::chrono::milliseconds retry_after{0}; ///< cooldown, sent rounded up to seconds
    std::string message;                      ///< optional detail, keep secrets out of it
    bool rotate = false;
};

/// Options for Client::with_key.
struct WithKeyOptions {
    /// How many keys to try before giving up (default 5).
    int max_attempts = 5;
    /// Used for the first lease. ttl, wait and exclusive also apply to every
    /// later lease; exclude_key_ids grows with each key tried.
    LeaseOptions lease;
};

/// Where and how to put a lease's secret on an outgoing request. Works with
/// any HTTP library: set the header, or add the query parameter.
struct AppliedAuth {
    enum class Location { header, query };
    Location location = Location::header;
    std::string name;   ///< header name or query parameter name
    std::string value;  ///< the full value, secret included

    bool is_header() const noexcept { return location == Location::header; }
    bool is_query() const noexcept { return location == Location::query; }
    /// "Name: value", ready for a curl_slist. Contains the secret.
    std::string header_line() const { return name + ": " + value; }
};

/// Redacted: prints the location and name, never the value.
std::ostream& operator<<(std::ostream& os, const AppliedAuth& auth);
std::string to_string(const AppliedAuth& auth);

/// The longest Idempotency-Key the proxy accepts.
inline constexpr std::size_t max_idempotency_key = 200;

/// A random Idempotency-Key (32 hex characters) for a proxied request.
std::string new_idempotency_key();

/// Parses a Retry-After value: delay seconds, or an HTTP date (IMF-fixdate)
/// counted from now. Zero when it is empty, invalid or in the past.
std::chrono::milliseconds parse_retry_after(const std::string& value);

/// Percent encodes everything but the unreserved characters (A-Z a-z 0-9 - . _ ~).
std::string url_encode(const std::string& s);
/// Encodes query pairs as k=v&k=v in the given order.
std::string encode_query(const Query& q);

}  // namespace acnologia
