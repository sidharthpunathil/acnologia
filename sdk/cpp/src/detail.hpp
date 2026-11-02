#pragma once

#include <chrono>
#include <memory>
#include <string>

#include <nlohmann/json.hpp>

#include "acnologia/client.hpp"

namespace acnologia::detail {

// Shared, immutable connection state. Client copies and every Lease and
// Proxy they hand out point at one ClientCore.
struct ClientCore : std::enable_shared_from_this<ClientCore> {
    std::string base_url;  // no trailing slash
    std::string token;
    std::string user_agent;
    std::shared_ptr<Transport> transport;
    std::chrono::milliseconds timeout{0};

    // Sends a JSON API call and returns the decoded body (null when empty).
    // Throws a typed ApiError subclass on a non 2xx status.
    nlohmann::json call(const std::string& method, const std::string& path, const nlohmann::json* body,
                        std::chrono::milliseconds timeout_override = std::chrono::milliseconds{0}) const;

    Lease lease_from_json(const nlohmann::json& j) const;
};

// Maps a non 2xx response to the matching exception and throws it.
[[noreturn]] void throw_api_error(int status, const std::string& body);

long long ceil_seconds(std::chrono::milliseconds d);

}  // namespace acnologia::detail
