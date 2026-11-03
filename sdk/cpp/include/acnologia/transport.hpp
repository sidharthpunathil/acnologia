#pragma once

#include <chrono>
#include <memory>
#include <string>

#include "acnologia/types.hpp"

namespace acnologia {

/// One HTTP request as the client hands it to a Transport.
struct HttpRequest {
    std::string method;   ///< GET, POST, ...
    std::string url;      ///< absolute URL
    Headers headers;      ///< carries the app token; do not log
    std::string body;     ///< empty means no body
    /// Total time allowed; zero means no limit (used for proxy calls).
    std::chrono::milliseconds timeout{0};
};

/// The response. Redirects are never followed, so the app token is not
/// replayed to another host.
struct HttpResponse {
    int status = 0;
    Headers headers;
    std::string body;

    /// First header with this name, compared case insensitively, or "".
    std::string header(const std::string& name) const;
    /// The Retry-After header as a duration, zero when absent or invalid.
    std::chrono::milliseconds retry_after() const { return parse_retry_after(header("Retry-After")); }
    /// True when the proxy replayed a stored answer for a repeated
    /// Idempotency-Key (Idempotent-Replayed: true).
    bool idempotent_replayed() const { return header("Idempotent-Replayed") == "true"; }
};

/// Sends HTTP requests. Implementations must be safe to call from several
/// threads at once, since one Client may be shared between threads. Throw
/// TransportError when the request does not complete; return any HTTP status
/// as a response.
class Transport {
public:
    virtual ~Transport() = default;
    virtual HttpResponse send(const HttpRequest& request) = 0;
};

/// The default libcurl transport. Each send uses its own easy handle, so it is
/// thread safe. curl_global_init runs once, the first time one is created.
std::shared_ptr<Transport> make_curl_transport();

}  // namespace acnologia
