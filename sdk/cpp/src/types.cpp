#include "acnologia/types.hpp"

#include <cctype>
#include <cstdint>
#include <ctime>
#include <iomanip>
#include <locale>
#include <ostream>
#include <random>
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

std::string new_idempotency_key() {
    static const char hex[] = "0123456789abcdef";
    std::random_device rd;
    std::string out;
    out.reserve(32);
    for (int i = 0; i < 4; ++i) {
        std::uint32_t v = rd();
        for (int b = 0; b < 8; ++b) {
            out += hex[v & 15];
            v >>= 4;
        }
    }
    return out;
}

namespace {

// Days since 1970-01-01 for a proleptic Gregorian date (Howard Hinnant).
long long days_from_civil(long long y, unsigned m, unsigned d) {
    y -= m <= 2;
    const long long era = (y >= 0 ? y : y - 399) / 400;
    const unsigned yoe = static_cast<unsigned>(y - era * 400);
    const unsigned doy = (153 * (m > 2 ? m - 3 : m + 9) + 2) / 5 + d - 1;
    const unsigned doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    return era * 146097 + static_cast<long long>(doe) - 719468;
}

}  // namespace

std::chrono::milliseconds parse_retry_after(const std::string& value) {
    std::size_t b = 0, e = value.size();
    while (b < e && std::isspace(static_cast<unsigned char>(value[b]))) ++b;
    while (e > b && std::isspace(static_cast<unsigned char>(value[e - 1]))) --e;
    const std::string v = value.substr(b, e - b);
    if (v.empty()) return std::chrono::milliseconds{0};
    bool digits = true;
    for (char c : v) digits = digits && std::isdigit(static_cast<unsigned char>(c));
    if (digits) {
        if (v.size() > 9) return std::chrono::milliseconds{0};
        return std::chrono::seconds(std::stoll(v));
    }
    // IMF-fixdate: Sun, 06 Nov 1994 08:49:37 GMT
    std::tm tm{};
    std::istringstream in(v);
    in.imbue(std::locale::classic());
    in >> std::get_time(&tm, "%a, %d %b %Y %H:%M:%S");
    if (in.fail()) return std::chrono::milliseconds{0};
    long long days = days_from_civil(tm.tm_year + 1900LL, static_cast<unsigned>(tm.tm_mon + 1),
                                     static_cast<unsigned>(tm.tm_mday));
    std::chrono::seconds at{days * 86400 + tm.tm_hour * 3600 + tm.tm_min * 60 + tm.tm_sec};
    auto now = std::chrono::duration_cast<std::chrono::milliseconds>(
        std::chrono::system_clock::now().time_since_epoch());
    auto left = std::chrono::duration_cast<std::chrono::milliseconds>(at) - now;
    return left.count() > 0 ? left : std::chrono::milliseconds{0};
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
