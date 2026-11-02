#pragma once

#include <chrono>
#include <cstdint>
#include <functional>
#include <iosfwd>
#include <memory>
#include <optional>
#include <string>
#include <type_traits>
#include <utility>
#include <vector>

#include "acnologia/errors.hpp"
#include "acnologia/transport.hpp"
#include "acnologia/types.hpp"

namespace acnologia {

namespace detail {
struct ClientCore;
}

class Client;

/// Options for a Client.
struct ClientOptions {
    /// HTTP transport. Null means the libcurl transport.
    std::shared_ptr<Transport> transport;
    /// User-Agent header. Empty means acnologia-cpp/<version>.
    std::string user_agent;
    /// Timeout for API calls (services, lease, report, renew). It is longer
    /// than max_wait so a lease that waits for a slot can still succeed, and a
    /// lease with a longer wait stretches it. Zero disables it. Proxy calls
    /// have no timeout because proxied bodies may take long.
    std::chrono::milliseconds timeout{75000};
};

/// A key handed out by the pool. Report its outcome when done: on services
/// with a concurrency limit an unreported lease holds one of the key's slots
/// until its TTL expires.
///
/// A Lease keeps the client's connection settings alive, so it stays usable
/// after the Client that issued it is destroyed. It is not synchronized: use
/// one Lease from one thread at a time.
class Lease {
public:
    const std::string& lease_id() const noexcept { return lease_id_; }
    std::int64_t key_id() const noexcept { return key_id_; }
    const std::string& service() const noexcept { return service_; }
    /// The plaintext provider key. Never printed by operator<< or to_string.
    const std::string& secret() const noexcept { return secret_; }
    const Auth& auth() const noexcept { return auth_; }
    const std::string& base_url() const noexcept { return base_url_; }
    /// RFC 3339 expiry as sent by the server; updated by renew().
    const std::string& expires_at() const noexcept { return expires_at_; }
    bool exclusive() const noexcept { return exclusive_; }
    /// True once report() succeeded for this lease.
    bool reported() const noexcept { return reported_; }

    /// Sends the outcome. When report.rotate is true and another key is
    /// available, the next lease is returned; otherwise std::nullopt.
    std::optional<Lease> report(const Report& report);
    /// Shorthand for report(Report{outcome, status_code, {}, {}, rotate}).
    std::optional<Lease> report(Outcome outcome, int status_code = 0, bool rotate = false);

    /// Extends the open lease (POST /api/v1/leases/{id}/renew). Zero ttl uses
    /// the server default of 120s. Returns and stores the new expires_at.
    /// Throws ApiError with code lease_not_found when the lease is not open.
    std::string renew(std::chrono::milliseconds ttl = std::chrono::milliseconds{0});

    /// Where to put the secret: a query parameter for "query" auth, otherwise
    /// a header (Authorization when auth().name is empty). A non empty
    /// auth().prefix goes in front of the secret for every type; for "bearer"
    /// it replaces the default "Bearer ".
    AppliedAuth apply() const;

    /// Joins base_url() with path and query. The secret is not added; use
    /// apply() or signed_url().
    std::string url(const std::string& path, const Query& query = {}) const;
    /// Like url(), and for "query" auth also appends the key parameter.
    /// The result contains the secret: do not log it.
    std::string signed_url(const std::string& path, const Query& query = {}) const;

private:
    friend struct detail::ClientCore;
    Lease() = default;

    std::shared_ptr<const detail::ClientCore> core_;
    std::string lease_id_;
    std::int64_t key_id_ = 0;
    std::string service_;
    std::string secret_;
    Auth auth_;
    std::string base_url_;
    std::string expires_at_;
    bool exclusive_ = false;
    bool reported_ = false;
};

/// Redacted: the secret is printed as [redacted].
std::ostream& operator<<(std::ostream& os, const Lease& lease);
std::string to_string(const Lease& lease);

/// Sends requests through Acnologia's proxy for one service. Acnologia
/// injects a key, retries on rate limits with the next key, and never returns
/// the secret. Upstream responses come back as they are (a non 2xx status is
/// not an exception) and carry X-Acnologia-Key-Id and X-Acnologia-Attempts.
/// The body is read fully into memory.
class Proxy {
public:
    const std::string& service() const noexcept { return service_; }

    /// Full proxy URL: {base}/proxy/{service}/{path}?{query}.
    std::string url(const std::string& path, const Query& query = {}) const;

    /// Sends any method. path may carry its own query string, and a path that
    /// already starts with /proxy/{service}/ is kept as is. Your headers are
    /// sent too, except that Authorization and User-Agent are set by the SDK.
    HttpResponse request(const std::string& method, const std::string& path,
                         const std::string& body = {}, const Headers& headers = {},
                         const Query& query = {}) const;
    HttpResponse get(const std::string& path, const Query& query = {},
                     const Headers& headers = {}) const;
    /// POST with a Content-Type (application/json by default).
    HttpResponse post(const std::string& path, const std::string& body,
                      const std::string& content_type = "application/json",
                      const Headers& headers = {}) const;

private:
    friend class Client;
    Proxy(std::shared_ptr<const detail::ClientCore> core, std::string service)
        : core_(std::move(core)), service_(std::move(service)) {}

    std::shared_ptr<const detail::ClientCore> core_;
    std::string service_;
};

std::ostream& operator<<(std::ostream& os, const Proxy& proxy);

namespace detail {
void run_with_key(const Client& client, const std::string& service,
                  const std::function<void(Lease&)>& fn, const WithKeyOptions& options);
std::string scrub_message(const std::string& message, const std::string& secret);
}  // namespace detail

/// Client for an Acnologia key pool, authenticated with an app token
/// (acn_..., the older cog_ prefix is still accepted by the server).
///
/// Thread safety: a Client is immutable after construction, so one Client
/// (or copies of it, which share state) may be used from many threads at
/// once, provided the Transport is thread safe (the libcurl one is). Leases
/// are not synchronized.
class Client {
public:
    /// base_url is the Acnologia server, for example http://localhost:8080.
    /// Throws std::invalid_argument when either argument is empty.
    Client(std::string base_url, std::string token, ClientOptions options = {});

    /// Reads ACNOLOGIA_URL and ACNOLOGIA_TOKEN. Throws std::invalid_argument
    /// when either is unset or empty.
    static Client from_env(ClientOptions options = {});

    const std::string& base_url() const noexcept;
    const std::string& user_agent() const noexcept;

    /// GET /api/v1/services: the services this app token may use.
    std::vector<Service> services() const;

    /// POST /api/v1/lease. Throws NoKeyAvailable when the pool is empty or
    /// every key is cooling down, and AllKeysBusy (a subclass) when every
    /// usable key is at its concurrency limit and options.wait ran out.
    /// Throws std::invalid_argument when options.wait is outside 0 to max_wait.
    Lease lease(const std::string& service, const LeaseOptions& options = {}) const;

    /// Same as lease.report(report).
    std::optional<Lease> report(Lease& lease, const Report& report) const { return lease.report(report); }
    /// Same as lease.renew(ttl).
    std::string renew(Lease& lease, std::chrono::milliseconds ttl = std::chrono::milliseconds{0}) const {
        return lease.renew(ttl);
    }

    /// Leases a key for service and calls fn(Lease&).
    ///
    /// - fn returns: the lease is reported ok and fn's result is returned.
    /// - fn throws RateLimited, QuotaExhausted or InvalidKey: the outcome is
    ///   reported with rotate=true and fn is called again with the next key
    ///   (or a fresh lease that excludes every key tried), up to
    ///   options.max_attempts. The last attempt is reported without asking
    ///   for a next key, and its exception is rethrown.
    /// - fn throws anything else: the lease is reported error (the message
    ///   with the secret scrubbed) and the exception is rethrown unchanged.
    ///
    /// Every lease is reported, whatever fn does. NoKeyAvailable or
    /// AllKeysBusy is thrown when the pool has no further key.
    template <typename F>
    auto with_key(const std::string& service, F&& fn, const WithKeyOptions& options = {}) const
        -> std::decay_t<std::invoke_result_t<F&, Lease&>> {
        using R = std::decay_t<std::invoke_result_t<F&, Lease&>>;
        if constexpr (std::is_void_v<R>) {
            detail::run_with_key(*this, service, [&fn](Lease& l) { fn(l); }, options);
        } else {
            std::optional<R> out;
            detail::run_with_key(*this, service, [&fn, &out](Lease& l) { out.emplace(fn(l)); }, options);
            return std::move(*out);
        }
    }

    /// A proxy helper for service.
    Proxy proxy(std::string service) const;

    /// Shorthands for proxy(service).url / get / request.
    std::string proxy_url(const std::string& service, const std::string& path, const Query& query = {}) const {
        return proxy(service).url(path, query);
    }
    HttpResponse proxy_get(const std::string& service, const std::string& path, const Query& query = {},
                           const Headers& headers = {}) const {
        return proxy(service).get(path, query, headers);
    }
    HttpResponse proxy_request(const std::string& service, const std::string& method, const std::string& path,
                               const std::string& body = {}, const Headers& headers = {},
                               const Query& query = {}) const {
        return proxy(service).request(method, path, body, headers, query);
    }

private:
    std::shared_ptr<const detail::ClientCore> core_;
};

/// Redacted: the token is printed as [redacted].
std::ostream& operator<<(std::ostream& os, const Client& client);
std::string to_string(const Client& client);

}  // namespace acnologia
