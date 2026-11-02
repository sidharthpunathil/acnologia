#include "acnologia/types.hpp"

#include <cctype>
#include <ostream>
#include <sstream>

#include "acnologia/transport.hpp"

namespace acnologia {

const char* to_string(Outcome outcome) noexcept {
    switch (outcome) {
        case Outcome::ok: return "ok";
        case Outcome::rate_limited: return "rate_limited";
        case Outcome::quota_exhausted: return "quota_exhausted";
        case Outcome::invalid: return "invalid";
        case Outcome::error: return "error";
        case Outcome::busy: return "busy";
        case Outcome::banned: return "banned";
    }
    return "error";
}

std::ostream& operator<<(std::ostream& os, Outcome outcome) { return os << to_string(outcome); }

std::ostream& operator<<(std::ostream& os, const AppliedAuth& auth) {
    return os << "acnologia::AppliedAuth{" << (auth.is_query() ? "query" : "header") << " " << auth.name
              << "=[redacted]}";
}

std::string to_string(const AppliedAuth& auth) {
    std::ostringstream os;
    os << auth;
    return os.str();
}

std::string url_encode(const std::string& s) {
    static const char hex[] = "0123456789ABCDEF";
    std::string out;
    out.reserve(s.size());
    for (unsigned char c : s) {
        if (std::isalnum(c) || c == '-' || c == '.' || c == '_' || c == '~') {
            out += static_cast<char>(c);
        } else {
            out += '%';
            out += hex[c >> 4];
            out += hex[c & 15];
        }
    }
    return out;
}

std::string encode_query(const Query& q) {
    std::string out;
    for (const auto& [k, v] : q) {
        if (!out.empty()) out += '&';
        out += url_encode(k) + "=" + url_encode(v);
    }
    return out;
}

std::string HttpResponse::header(const std::string& name) const {
    auto lower = [](std::string s) {
        for (auto& c : s) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
        return s;
    };
    const std::string want = lower(name);
    for (const auto& [k, v] : headers) {
        if (lower(k) == want) return v;
    }
    return {};
}

}  // namespace acnologia
