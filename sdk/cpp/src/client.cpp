#include <algorithm>
#include <cctype>
#include <cstdlib>
#include <ostream>
#include <sstream>
#include <stdexcept>
#include <utility>

#include "acnologia/client.hpp"
#include "detail.hpp"

namespace acnologia {

using nlohmann::json;

namespace detail {
namespace {

constexpr std::size_t max_report_message = 500;

std::string trim_right(std::string s, char c) {
    while (!s.empty() && s.back() == c) s.pop_back();
    return s;
}

std::string trim_left(const std::string& s, char c) {
    std::size_t i = 0;
    while (i < s.size() && s[i] == c) ++i;
    return s.substr(i);
}

std::string lower(std::string s) {
    for (auto& c : s) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
    return s;
}

std::string trim_space(const std::string& s) {
    std::size_t b = 0, e = s.size();
    while (b < e && std::isspace(static_cast<unsigned char>(s[b]))) ++b;
    while (e > b && std::isspace(static_cast<unsigned char>(s[e - 1]))) --e;
    return s.substr(b, e - b);
}

const char* status_text(int status) {
    switch (status) {
        case 400: return "Bad Request";
        case 401: return "Unauthorized";
        case 403: return "Forbidden";
        case 404: return "Not Found";
        case 405: return "Method Not Allowed";
        case 409: return "Conflict";
        case 410: return "Gone";
        case 413: return "Request Entity Too Large";
        case 429: return "Too Many Requests";
        case 500: return "Internal Server Error";
        case 502: return "Bad Gateway";
        case 503: return "Service Unavailable";
        case 504: return "Gateway Timeout";
        default: return "";
    }
}

std::string json_string(const json& j, const char* key) {
    auto it = j.find(key);
    if (it == j.end() || !it->is_string()) return {};
    return it->get<std::string>();
}

std::chrono::milliseconds json_seconds(const json& j, const char* key) {
    auto it = j.find(key);
    if (it == j.end() || !it->is_number()) return std::chrono::milliseconds{0};
    double sec = it->get<double>();
    if (sec <= 0) return std::chrono::milliseconds{0};
    return std::chrono::milliseconds{static_cast<long long>(sec * 1000.0 + 0.5)};
}

void replace_all(std::string& s, const std::string& from, const std::string& to) {
    if (from.empty()) return;
    std::size_t pos = 0;
    while ((pos = s.find(from, pos)) != std::string::npos) {
        s.replace(pos, from.size(), to);
        pos += to.size();
    }
}

}  // namespace

long long ceil_seconds(std::chrono::milliseconds d) {
    auto ms = d.count();
    if (ms <= 0) return 0;
    return (ms + 999) / 1000;
}

std::string scrub_message(const std::string& message, const std::string& secret) {
    std::string out = message;
    if (!secret.empty()) {
        replace_all(out, secret, "[key]");
        std::string enc = url_encode(secret);
        replace_all(out, enc, "[key]");
        std::string form = enc;
        replace_all(form, "%20", "+");
        replace_all(out, form, "[key]");
    }
    if (out.size() > max_report_message) {
        std::size_t cut = max_report_message;
        // Do not split a UTF-8 sequence.
        while (cut > 0 && (static_cast<unsigned char>(out[cut]) & 0xC0) == 0x80) --cut;
        out.resize(cut);
    }
    return out;
}

void throw_api_error(int status, const std::string& body) {
    json j = json::parse(body, nullptr, false);
    std::string code, message;
    std::chrono::milliseconds retry{0};
    if (j.is_object()) {
        auto e = j.find("error");
        if (e != j.end() && e->is_object()) {
            code = json_string(*e, "code");
            message = json_string(*e, "message");
        } else if (e != j.end() && e->is_string()) {
            message = e->get<std::string>();
        }
        retry = json_seconds(j, "retry_after_sec");
    }
    if (code == "all_keys_busy") throw AllKeysBusy(message, retry, status);
    if (code == "no_key_available" || (status == 503 && code.empty())) {
        throw NoKeyAvailable(message, retry, "no_key_available", status);
    }
    if (code == "service_unavailable") throw ServiceUnavailable(message, retry, status);
    if (message.empty() && code.empty()) {
        message = trim_space(body);
        if (message.size() > 200) message.resize(200);
        if (message.empty()) message = status_text(status);
        if (message.empty()) message = "HTTP " + std::to_string(status);
    }
    if (status == 401) throw Unauthenticated(code, message, status);
    if (status == 403) throw Forbidden(code, message, status);
    throw ApiError(status, code, message);
}

json ClientCore::call(const std::string& method, const std::string& path, const json* body,
                      std::chrono::milliseconds timeout_override) const {
    HttpRequest req;
    req.method = method;
    req.url = base_url + path;
    req.timeout = timeout;
    if (timeout.count() > 0 && timeout_override > timeout) req.timeout = timeout_override;
    req.headers = {{"Authorization", "Bearer " + token},
                   {"User-Agent", user_agent},
                   {"Accept", "application/json"}};
    if (body != nullptr) {
        req.headers.emplace_back("Content-Type", "application/json");
        req.body = body->dump();
    }
    HttpResponse resp = transport->send(req);
    if (resp.status < 200 || resp.status > 299) throw_api_error(resp.status, resp.body);
    if (resp.body.empty()) return json();
    json out = json::parse(resp.body, nullptr, false);
    if (out.is_discarded()) throw Error("acnologia: decode response: invalid JSON");
    return out;
}

Lease ClientCore::lease_from_json(const json& j) const {
    if (!j.is_object()) throw Error("acnologia: decode response: lease is not an object");
    Lease l;
    l.core_ = shared_from_this();
    l.lease_id_ = json_string(j, "lease_id");
    if (auto it = j.find("key_id"); it != j.end() && it->is_number_integer()) l.key_id_ = it->get<std::int64_t>();
    l.service_ = json_string(j, "service");
    l.secret_ = json_string(j, "secret");
    if (auto it = j.find("auth"); it != j.end() && it->is_object()) {
        l.auth_.type = json_string(*it, "type");
        l.auth_.name = json_string(*it, "name");
        l.auth_.prefix = json_string(*it, "prefix");
    }
    l.base_url_ = json_string(j, "base_url");
    l.expires_at_ = json_string(j, "expires_at");
    if (auto it = j.find("exclusive"); it != j.end() && it->is_boolean()) l.exclusive_ = it->get<bool>();
    return l;
}

void run_with_key(const Client& client, const std::string& service, const std::function<void(Lease&)>& fn,
                  const WithKeyOptions& options) {
    const int max_attempts = options.max_attempts > 0 ? options.max_attempts : 5;
    std::vector<std::int64_t> tried = options.lease.exclude_key_ids;
    Lease lease = client.lease(service, options.lease);
    std::exception_ptr last;
    for (int attempt = 1; attempt <= max_attempts; ++attempt) {
        std::exception_ptr err;
        Report report;
        bool rotate = false;
        try {
            fn(lease);
        } catch (const KeyRotationError& e) {
            err = std::current_exception();
            report.outcome = e.outcome();
            report.status_code = e.status_code();
            report.retry_after = e.retry_after();
            report.message = scrub_message(e.message(), lease.secret());
            report.rotate = true;
            rotate = true;
        } catch (const std::exception& e) {
            err = std::current_exception();
            report.outcome = Outcome::error;
            report.message = scrub_message(e.what(), lease.secret());
        } catch (...) {
            err = std::current_exception();
            report.outcome = Outcome::error;
            report.message = "unknown exception";
        }
        if (!err) {
            lease.report(Report{});
            return;
        }
        if (!rotate) {
            try {
                lease.report(report);
            } catch (...) {
                // keep the caller's exception
            }
            std::rethrow_exception(err);
        }
        last = err;
        tried.push_back(lease.key_id());
        if (attempt == max_attempts) {
            report.rotate = false;
            try {
                lease.report(report);
            } catch (...) {
                // keep the rotation exception
            }
            break;
        }
        std::optional<Lease> next = lease.report(report);
        if (!next) {
            LeaseOptions lo;
            lo.exclude_key_ids = tried;
            lo.ttl = options.lease.ttl;
            lo.wait = options.lease.wait;
            lo.exclusive = options.lease.exclusive;
            next = client.lease(service, lo);
        }
        lease = std::move(*next);
    }
    std::rethrow_exception(last);
}

}  // namespace detail

// Lease

std::optional<Lease> Lease::report(const Report& r) {
    if (!core_) throw Error("acnologia: lease " + lease_id_ + " has no client");
    json body = {{"outcome", to_string(r.outcome)}};
    if (r.status_code != 0) body["status_code"] = r.status_code;
    if (r.retry_after.count() > 0) body["retry_after_sec"] = detail::ceil_seconds(r.retry_after);
    if (!r.message.empty()) body["message"] = r.message;
    if (r.rotate) body["rotate"] = true;
    json out = core_->call("POST", "/api/v1/leases/" + url_encode(lease_id_) + "/report", &body);
    reported_ = true;
    if (!out.is_object()) return std::nullopt;
    auto it = out.find("next");
    if (it == out.end() || it->is_null()) return std::nullopt;
    return core_->lease_from_json(*it);
}

std::optional<Lease> Lease::report(Outcome outcome, int status_code, bool rotate) {
    Report r;
    r.outcome = outcome;
    r.status_code = status_code;
    r.rotate = rotate;
    return report(r);
}

std::string Lease::renew(std::chrono::milliseconds ttl) {
    if (!core_) throw Error("acnologia: lease " + lease_id_ + " has no client");
    json body = json::object();
    if (ttl.count() > 0) body["ttl_sec"] = detail::ceil_seconds(ttl);
    json out = core_->call("POST", "/api/v1/leases/" + url_encode(lease_id_) + "/renew", &body);
    if (out.is_object()) {
        std::string exp = detail::json_string(out, "expires_at");
        if (!exp.empty()) expires_at_ = exp;
    }
    return expires_at_;
}

AppliedAuth Lease::apply() const {
    AppliedAuth a;
    std::string value = auth_.prefix + secret_;
    if (auth_.type == "query") {
        a.location = AppliedAuth::Location::query;
        a.name = auth_.name;
        a.value = value;
        return a;
    }
    a.location = AppliedAuth::Location::header;
    a.name = auth_.name.empty() ? "Authorization" : auth_.name;
    if (auth_.type == "bearer" && auth_.prefix.empty()) value = "Bearer " + secret_;
    a.value = value;
    return a;
}

std::string Lease::url(const std::string& path, const Query& query) const {
    std::string u = detail::trim_right(base_url_, '/') + "/" + detail::trim_left(path, '/');
    if (!query.empty()) u += (u.find('?') == std::string::npos ? "?" : "&") + encode_query(query);
    return u;
}

std::string Lease::signed_url(const std::string& path, const Query& query) const {
    AppliedAuth a = apply();
    if (!a.is_query()) return url(path, query);
    Query q = query;
    q.erase(std::remove_if(q.begin(), q.end(), [&](const auto& p) { return p.first == a.name; }), q.end());
    q.emplace_back(a.name, a.value);
    return url(path, q);
}

std::ostream& operator<<(std::ostream& os, const Lease& l) {
    return os << "acnologia::Lease{lease_id=" << l.lease_id() << ", key_id=" << l.key_id()
              << ", service=" << l.service() << ", secret=[redacted], auth={type=" << l.auth().type
              << ", name=" << l.auth().name << ", prefix=" << l.auth().prefix << "}, base_url=" << l.base_url()
              << ", expires_at=" << l.expires_at() << "}";
}

std::string to_string(const Lease& lease) {
    std::ostringstream os;
    os << lease;
    return os.str();
}

// Proxy

std::string Proxy::url(const std::string& path, const Query& query) const {
    std::string u = core_->base_url + "/proxy/" + url_encode(service_) + "/" + detail::trim_left(path, '/');
    if (!query.empty()) u += (u.find('?') == std::string::npos ? "?" : "&") + encode_query(query);
    return u;
}

HttpResponse Proxy::request(const std::string& method, const std::string& path, const std::string& body,
                            const Headers& headers, const Query& query) const {
    std::string p = path;
    const std::string svc_prefix = "/proxy/" + url_encode(service_);
    if (p == svc_prefix || p.rfind(svc_prefix + "/", 0) == 0 || p.rfind(svc_prefix + "?", 0) == 0) {
        p = p.substr(svc_prefix.size());
    }
    HttpRequest req;
    req.method = method;
    for (auto& c : req.method) c = static_cast<char>(std::toupper(static_cast<unsigned char>(c)));
    req.url = url(p, query);
    req.body = body;
    for (const auto& h : headers) {
        const std::string n = detail::lower(h.first);
        if (n == "authorization" || n == "user-agent") continue;
        req.headers.push_back(h);
    }
    req.headers.emplace_back("Authorization", "Bearer " + core_->token);
    req.headers.emplace_back("User-Agent", core_->user_agent);
    req.timeout = std::chrono::milliseconds{0};
    return core_->transport->send(req);
}

HttpResponse Proxy::get(const std::string& path, const Query& query, const Headers& headers) const {
    return request("GET", path, {}, headers, query);
}

HttpResponse Proxy::post(const std::string& path, const std::string& body, const std::string& content_type,
                         const Headers& headers) const {
    Headers h;
    for (const auto& kv : headers) {
        if (detail::lower(kv.first) != "content-type") h.push_back(kv);
    }
    if (!content_type.empty()) h.emplace_back("Content-Type", content_type);
    return request("POST", path, body, h);
}

std::ostream& operator<<(std::ostream& os, const Proxy& p) {
    return os << "acnologia::Proxy{service=" << p.service() << ", url=" << p.url("") << "}";
}

// Client

Client::Client(std::string base_url, std::string token, ClientOptions options) {
    if (base_url.empty()) throw std::invalid_argument("acnologia: base_url is required (or set ACNOLOGIA_URL)");
    if (token.empty()) throw std::invalid_argument("acnologia: token is required (or set ACNOLOGIA_TOKEN)");
    auto core = std::make_shared<detail::ClientCore>();
    core->base_url = detail::trim_right(std::move(base_url), '/');
    core->token = std::move(token);
    core->user_agent =
        options.user_agent.empty() ? std::string("acnologia-cpp/") + version : std::move(options.user_agent);
    core->transport = options.transport ? std::move(options.transport) : make_curl_transport();
    core->timeout = options.timeout;
    core_ = std::move(core);
}

Client Client::from_env(ClientOptions options) {
    const char* url = std::getenv("ACNOLOGIA_URL");
    const char* tok = std::getenv("ACNOLOGIA_TOKEN");
    return Client(url ? url : "", tok ? tok : "", std::move(options));
}

const std::string& Client::base_url() const noexcept { return core_->base_url; }
const std::string& Client::user_agent() const noexcept { return core_->user_agent; }

std::vector<Service> Client::services() const {
    json out = core_->call("GET", "/api/v1/services", nullptr);
    std::vector<Service> svcs;
    if (!out.is_object()) return svcs;
    auto it = out.find("services");
    if (it == out.end() || !it->is_array()) return svcs;
    for (const auto& s : *it) {
        if (!s.is_object()) continue;
        Service v;
        v.slug = detail::json_string(s, "slug");
        v.name = detail::json_string(s, "name");
        v.provider = detail::json_string(s, "provider");
        v.category = detail::json_string(s, "category");
        if (auto a = s.find("available"); a != s.end() && a->is_boolean()) v.available = a->get<bool>();
        v.base_url = detail::json_string(s, "base_url");
        if (auto a = s.find("auth"); a != s.end() && a->is_object()) {
            v.auth.type = detail::json_string(*a, "type");
            v.auth.name = detail::json_string(*a, "name");
            v.auth.prefix = detail::json_string(*a, "prefix");
        }
        svcs.push_back(std::move(v));
    }
    return svcs;
}

Lease Client::lease(const std::string& service, const LeaseOptions& options) const {
    if (options.wait.count() < 0 || options.wait > max_wait) {
        throw std::invalid_argument("acnologia: lease wait must be between 0 and 60s");
    }
    json body = {{"service", service}};
    if (!options.exclude_key_ids.empty()) body["exclude_key_ids"] = options.exclude_key_ids;
    if (options.ttl.count() > 0) body["ttl_sec"] = detail::ceil_seconds(options.ttl);
    std::chrono::milliseconds timeout{0};
    if (options.wait.count() > 0) {
        body["wait_sec"] = detail::ceil_seconds(options.wait);
        timeout = options.wait + std::chrono::seconds(10);
    }
    if (options.exclusive) body["exclusive"] = true;
    json out = core_->call("POST", "/api/v1/lease", &body, timeout);
    return core_->lease_from_json(out);
}

Proxy Client::proxy(std::string service) const { return Proxy(core_, std::move(service)); }

std::ostream& operator<<(std::ostream& os, const Client& c) {
    return os << "acnologia::Client{base_url=" << c.base_url() << ", token=[redacted]}";
}

std::string to_string(const Client& client) {
    std::ostringstream os;
    os << client;
    return os.str();
}

}  // namespace acnologia
