#pragma once

#include <chrono>
#include <memory>
#include <string>
#include <vector>

#include <nlohmann/json.hpp>

#include "acnologia/client.hpp"

namespace acnologia::detail {

// Shared, immutable connection state. Client copies and every Lease and
// Proxy they hand out point at one ClientCore.
struct ClientCore : std::enable_shared_from_this<ClientCore> {
    std::string base_url;  // no trailing slash
    std::string token;
    std::string user_agent;
    std::string access_password;  // empty: no X-Acnologia-Access header
    std::shared_ptr<Transport> transport;
    std::chrono::milliseconds timeout{0};
    bool throw_proxy_errors = false;

    // The values scrubbed from every error message.
    std::vector<std::string> secrets() const { return {token, access_password}; }

    // Sends through the transport. A TransportError comes back with the
    // token and access password scrubbed.
    HttpResponse send(const HttpRequest& req) const;

    // Sends an API call and returns the response. Throws a typed ApiError
    // subclass on a non 2xx status.
    HttpResponse raw(const std::string& method, const std::string& path, const nlohmann::json* body,
                     std::chrono::milliseconds timeout_override, const std::string& accept) const;

    // Sends a JSON API call and returns the decoded body (null when empty).
    nlohmann::json call(const std::string& method, const std::string& path, const nlohmann::json* body,
                        std::chrono::milliseconds timeout_override = std::chrono::milliseconds{0}) const;

    Lease lease_from_json(const nlohmann::json& j) const;
};

// Maps a non 2xx response to the matching exception and throws it. The
// secrets are scrubbed from the message.
[[noreturn]] void throw_api_error(int status, const std::string& body, const Headers& headers = {},
                                  const std::vector<std::string>& secrets = {});

long long ceil_seconds(std::chrono::milliseconds d);

}  // namespace acnologia::detail
