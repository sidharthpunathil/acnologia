#define DOCTEST_CONFIG_IMPLEMENT_WITH_MAIN
#include <doctest/doctest.h>

#include <acnologia/acnologia.hpp>

#include <cstdio>
#include <cstdlib>
#include <fstream>
#include <functional>
#include <memory>
#include <nlohmann/json.hpp>
#include <sstream>
#include <stdexcept>
#include <string>
#include <vector>

using nlohmann::json;
using namespace std::chrono_literals;
namespace acn = acnologia;

namespace {

// Records every request and answers with a handler.
class FakeTransport : public acn::Transport {
public:
    using Handler = std::function<acn::HttpResponse(const acn::HttpRequest&)>;
    explicit FakeTransport(Handler h) : handler(std::move(h)) {}

    acn::HttpResponse send(const acn::HttpRequest& req) override {
        requests.push_back(req);
        return handler(req);
    }

    std::string path(std::size_t i) const {
        const std::string& u = requests.at(i).url;
        return u.substr(std::string("http://acn.test").size());
    }
    json body(std::size_t i) const { return json::parse(requests.at(i).body); }
    std::vector<json> reports() const {
        std::vector<json> out;
        for (const auto& r : requests) {
            if (r.url.find("/report") != std::string::npos) out.push_back(json::parse(r.body));
        }
        return out;
    }

    Handler handler;
    std::vector<acn::HttpRequest> requests;
};

acn::HttpResponse reply(int status, const json& j) { return {status, {{"Content-Type", "application/json"}}, j.dump()}; }

json lease_json(const std::string& id, std::int64_t key, const std::string& secret = "s3cret",
                json auth = {{"type", "query"}, {"name", "api_key"}}) {
    return {{"lease_id", id},          {"key_id", key}, {"service", "serpapi"}, {"secret", secret},
            {"auth", auth},            {"base_url", "https://serpapi.com/"},
            {"expires_at", "2026-11-02T10:00:00Z"}, {"exclusive", false}};
}

void set_env(const char* name, const std::string& value) {
#ifdef _WIN32
    ::_putenv_s(name, value.c_str());
#else
    ::setenv(name, value.c_str(), 1);
#endif
}

void unset_env(const char* name) {
#ifdef _WIN32
    ::_putenv_s(name, "");
#else
    ::unsetenv(name);
#endif
}

void clear_env() {
    for (const char* n : {"ACNOLOGIA_URL", "ACNOLOGIA_TOKEN", "ACNOLOGIA_ACCESS_PASSWORD", "ACNOLOGIA_ACCESS_URL"}) {
        unset_env(n);
    }
}

acn::HttpResponse error_reply(int status, const std::string& code, const std::string& message = "m",
                              json extra = json::object(), acn::Headers headers = {}) {
    json j = extra;
    j["error"] = {{"code", code}, {"message", message}};
    headers.emplace_back("Content-Type", "application/json");
    return {status, headers, j.dump()};
}

std::string header(const acn::HttpRequest& r, const std::string& name) {
    for (const auto& [k, v] : r.headers) {
        if (k == name) return v;
    }
    return {};
}

struct Fixture {
    std::shared_ptr<FakeTransport> fake;
    acn::Client client;

    explicit Fixture(FakeTransport::Handler h)
        : fake(std::make_shared<FakeTransport>(std::move(h))), client(make(fake)) {}

    static acn::Client make(std::shared_ptr<FakeTransport> f) {
        acn::ClientOptions o;
        o.transport = std::move(f);
        return acn::Client("http://acn.test/", "acn_test_token", o);
    }
};

// A pool of keys 1..n that hands out the next key on lease and on rotate.
FakeTransport::Handler pool_handler(int keys, bool rotate_returns_next = true) {
    auto next_key = std::make_shared<int>(1);
    return [=](const acn::HttpRequest& r) {
        if (r.url.find("/api/v1/lease") != std::string::npos && r.url.find("/leases/") == std::string::npos) {
            if (*next_key > keys) return reply(503, {{"error", {{"code", "no_key_available"}, {"message", "empty"}}},
                                                     {"retry_after_sec", 30}});
            int k = (*next_key)++;
            return reply(200, lease_json("lse_" + std::to_string(k), k, "secret" + std::to_string(k)));
        }
        if (r.url.find("/report") != std::string::npos) {
            json b = json::parse(r.body);
            if (rotate_returns_next && b.value("rotate", false) && *next_key <= keys) {
                int k = (*next_key)++;
                return reply(200, {{"ok", true}, {"next", lease_json("lse_" + std::to_string(k), k,
                                                                     "secret" + std::to_string(k))}});
            }
            return reply(200, {{"ok", true}, {"next", nullptr}});
        }
        return reply(404, {{"error", {{"code", "not_found"}, {"message", "no route"}}}});
    };
}

}  // namespace

TEST_CASE("services parses the list and sends the token and user agent") {
    Fixture f([](const acn::HttpRequest&) {
        return reply(200, {{"services",
                            {{{"slug", "serpapi"},
                              {"name", "SerpAPI"},
                              {"provider", "serpapi"},
                              {"category", "search"},
                              {"available", true},
                              {"base_url", "https://serpapi.com"},
                              {"auth", {{"type", "query"}, {"name", "api_key"}}}}}}});
    });
    auto svcs = f.client.services();
    REQUIRE(svcs.size() == 1);
    CHECK(svcs[0].slug == "serpapi");
    CHECK(svcs[0].available);
    CHECK(svcs[0].auth.type == "query");
    CHECK(svcs[0].auth.name == "api_key");
    const auto& r = f.fake->requests[0];
    CHECK(r.method == "GET");
    CHECK(f.fake->path(0) == "/api/v1/services");
    CHECK(header(r, "Authorization") == "Bearer acn_test_token");
    CHECK(header(r, "User-Agent") == std::string("acnologia-cpp/") + acn::version);
    CHECK(header(r, "Accept") == "application/json");
    CHECK(r.timeout == 75s);
}

TEST_CASE("401 and 403 map to Unauthenticated and Forbidden") {
    int status = 401;
    Fixture f([&](const acn::HttpRequest&) {
        if (status == 401) return reply(401, {{"error", {{"code", "unauthenticated"}, {"message", "bad token"}}}});
        return reply(403, {{"error", {{"code", "service_not_allowed"}, {"message", "no"}}}});
    });
    try {
        f.client.services();
        FAIL("expected Unauthenticated");
    } catch (const acn::Unauthenticated& e) {
        CHECK(e.status() == 401);
        CHECK(e.code() == "unauthenticated");
        CHECK(e.message() == "bad token");
    }
    status = 403;
    CHECK_THROWS_AS(f.client.lease("serpapi"), acn::Forbidden);
    try {
        f.client.lease("serpapi");
    } catch (const acn::ApiError& e) {
        CHECK(e.code() == "service_not_allowed");
        CHECK(std::string(e.what()) == "acnologia: http 403: service_not_allowed: no");
    }
}

TEST_CASE("other errors are ApiError, with the body as message when it is not JSON") {
    Fixture f([](const acn::HttpRequest&) { return acn::HttpResponse{500, {}, "  upstream exploded \n"}; });
    try {
        f.client.services();
        FAIL("expected ApiError");
    } catch (const acn::ApiError& e) {
        CHECK(e.status() == 500);
        CHECK(e.code().empty());
        CHECK(e.message() == "upstream exploded");
    }
    Fixture g([](const acn::HttpRequest&) {
        return reply(429, {{"error", {{"code", "app_limit_reached"}, {"message", "daily"}}}});
    });
    CHECK_THROWS_AS(g.client.lease("x"), acn::ApiError);
}

TEST_CASE("lease sends the options and report returns the next lease") {
    Fixture f(pool_handler(3));
    acn::LeaseOptions o;
    o.exclude_key_ids = {7, 8};
    o.ttl = 1500ms;
    o.wait = 2500ms;
    o.exclusive = true;
    acn::Lease l = f.client.lease("serpapi", o);
    CHECK(l.lease_id() == "lse_1");
    CHECK(l.key_id() == 1);
    CHECK(l.secret() == "secret1");
    CHECK(l.expires_at() == "2026-11-02T10:00:00Z");
    json b = f.fake->body(0);
    CHECK(f.fake->path(0) == "/api/v1/lease");
    CHECK(b["service"] == "serpapi");
    CHECK(b["exclude_key_ids"] == json::array({7, 8}));
    CHECK(b["ttl_sec"] == 2);
    CHECK(b["wait_sec"] == 3);
    CHECK(b["exclusive"] == true);
    CHECK(header(f.fake->requests[0], "Content-Type") == "application/json");
    CHECK(f.fake->requests[0].timeout == 75s);  // 2.5s + 10s is shorter than the default

    acn::Report r;
    r.outcome = acn::Outcome::rate_limited;
    r.status_code = 429;
    r.retry_after = 1500ms;
    r.message = "slow down";
    r.rotate = true;
    auto next = l.report(r);
    CHECK(l.reported());
    REQUIRE(next.has_value());
    CHECK(next->key_id() == 2);
    CHECK(f.fake->path(1) == "/api/v1/leases/lse_1/report");
    json rb = f.fake->body(1);
    CHECK(rb == json{{"outcome", "rate_limited"}, {"status_code", 429}, {"retry_after_sec", 2},
                     {"message", "slow down"}, {"rotate", true}});

    auto none = next->report(acn::Outcome::ok);
    CHECK_FALSE(none.has_value());
    CHECK(f.fake->body(2) == json{{"outcome", "ok"}});
}

TEST_CASE("every outcome has its wire name") {
    CHECK(std::string(acn::to_string(acn::Outcome::quota_exhausted)) == "quota_exhausted");
    CHECK(std::string(acn::to_string(acn::Outcome::invalid)) == "invalid");
    CHECK(std::string(acn::to_string(acn::Outcome::busy)) == "busy");
    CHECK(std::string(acn::to_string(acn::Outcome::banned)) == "banned");
    CHECK(std::string(acn::to_string(acn::Outcome::error)) == "error");
}

TEST_CASE("a long wait stretches the request timeout and an out of range wait is rejected") {
    Fixture f(pool_handler(1));
    acn::LeaseOptions o;
    o.wait = 60s;
    f.client.lease("serpapi", o);
    CHECK(f.fake->requests[0].timeout == 75s);
    acn::ClientOptions co;
    co.transport = f.fake;
    co.timeout = 5s;
    acn::Client short_client("http://acn.test", "acn_x", co);
    o.wait = 20s;
    CHECK_THROWS_AS(short_client.lease("serpapi", o), acn::NoKeyAvailable);  // pool of one is used up
    CHECK(f.fake->requests[1].timeout == 30s);
    o.wait = 61s;
    CHECK_THROWS_AS(f.client.lease("serpapi", o), std::invalid_argument);
    o.wait = -1s;
    CHECK_THROWS_AS(f.client.lease("serpapi", o), std::invalid_argument);
}

TEST_CASE("no_key_available, all_keys_busy and service_unavailable") {
    std::string code = "no_key_available";
    Fixture f([&](const acn::HttpRequest&) {
        if (code.empty()) return acn::HttpResponse{503, {}, ""};
        return reply(503, {{"error", {{"code", code}, {"message", "m"}}}, {"retry_after_sec", 3}});
    });
    try {
        f.client.lease("serpapi");
        FAIL("expected NoKeyAvailable");
    } catch (const acn::AllKeysBusy&) {
        FAIL("not busy");
    } catch (const acn::NoKeyAvailable& e) {
        CHECK(e.retry_after() == 3s);
        CHECK_FALSE(e.busy());
        CHECK(e.status() == 503);
        CHECK(std::string(e.what()) == "acnologia: no key available (retry after 3s): m");
    }
    code = "all_keys_busy";
    try {
        f.client.lease("serpapi");
        FAIL("expected AllKeysBusy");
    } catch (const acn::NoKeyAvailable& e) {
        CHECK(e.busy());
        CHECK(dynamic_cast<const acn::AllKeysBusy*>(&e) != nullptr);
        CHECK(e.code() == "all_keys_busy");
    }
    code = "service_unavailable";
    try {
        f.client.lease("serpapi");
        FAIL("expected ServiceUnavailable");
    } catch (const acn::ServiceUnavailable& e) {
        CHECK(e.retry_after() == 3s);
    }
    code = "";
    try {
        f.client.lease("serpapi");
        FAIL("expected NoKeyAvailable");
    } catch (const acn::NoKeyAvailable& e) {
        CHECK(e.code() == "no_key_available");
        CHECK(e.retry_after() == 0ms);
    }
}

TEST_CASE("renew sends ttl_sec and stores the new expiry") {
    bool open = true;
    Fixture f([&](const acn::HttpRequest& r) {
        if (r.url.find("/renew") != std::string::npos) {
            if (!open) return reply(404, {{"error", {{"code", "lease_not_found"}, {"message", "gone"}}}});
            return reply(200, {{"ok", true}, {"expires_at", "2026-11-02T11:00:00Z"}});
        }
        return reply(200, lease_json("lse_9", 9));
    });
    acn::Lease l = f.client.lease("serpapi");
    CHECK(l.renew(10min) == "2026-11-02T11:00:00Z");
    CHECK(l.expires_at() == "2026-11-02T11:00:00Z");
    CHECK(f.fake->path(1) == "/api/v1/leases/lse_9/renew");
    CHECK(f.fake->body(1) == json{{"ttl_sec", 600}});
    f.client.renew(l);
    CHECK(f.fake->body(2) == json::object());
    open = false;
    try {
        l.renew();
        FAIL("expected ApiError");
    } catch (const acn::ApiError& e) {
        CHECK(e.status() == 404);
        CHECK(e.code() == "lease_not_found");
    }
}

TEST_CASE("apply puts the secret where the auth spec says") {
    json auth;
    Fixture f([&](const acn::HttpRequest&) { return reply(200, lease_json("lse_1", 1, "K", auth)); });

    auth = {{"type", "query"}, {"name", "api_key"}};
    auto a = f.client.lease("s").apply();
    CHECK(a.is_query());
    CHECK(a.name == "api_key");
    CHECK(a.value == "K");

    auth = {{"type", "header"}, {"name", "xi-api-key"}};
    a = f.client.lease("s").apply();
    CHECK(a.is_header());
    CHECK(a.header_line() == "xi-api-key: K");

    auth = {{"type", "header"}, {"name", ""}};
    CHECK(f.client.lease("s").apply().name == "Authorization");

    auth = {{"type", "bearer"}, {"name", ""}};
    a = f.client.lease("s").apply();
    CHECK(a.name == "Authorization");
    CHECK(a.value == "Bearer K");

    auth = {{"type", "bearer"}, {"name", "Authorization"}, {"prefix", "Token "}};
    CHECK(f.client.lease("s").apply().value == "Token K");

    auth = {{"type", "header"}, {"name", "X-Key"}, {"prefix", "Key "}};
    CHECK(f.client.lease("s").apply().value == "Key K");

    auth = {{"type", "query"}, {"name", "key"}, {"prefix", "p-"}};
    CHECK(f.client.lease("s").apply().value == "p-K");
}

TEST_CASE("url joins the base and signed_url adds the query key") {
    json auth = {{"type", "query"}, {"name", "api_key"}};
    Fixture f([&](const acn::HttpRequest&) { return reply(200, lease_json("lse_1", 1, "a b&c", auth)); });
    auto l = f.client.lease("serpapi");
    CHECK(l.url("/search.json", {{"q", "coffee beans"}}) == "https://serpapi.com/search.json?q=coffee%20beans");
    CHECK(l.url("search.json") == "https://serpapi.com/search.json");
    CHECK(l.signed_url("/search.json", {{"q", "x"}, {"api_key", "old"}}) ==
          "https://serpapi.com/search.json?q=x&api_key=a%20b%26c");
    auth = {{"type", "header"}, {"name", "X-Key"}};
    auto h = f.client.lease("serpapi");
    CHECK(h.signed_url("/v1") == "https://serpapi.com/v1");
}

TEST_CASE("with_key returns the result and reports ok") {
    Fixture f(pool_handler(3));
    int got = f.client.with_key("serpapi", [](acn::Lease& l) { return static_cast<int>(l.key_id()) * 10; });
    CHECK(got == 10);
    auto reports = f.fake->reports();
    REQUIRE(reports.size() == 1);
    CHECK(reports[0] == json{{"outcome", "ok"}});

    bool called = false;
    f.client.with_key("serpapi", [&](acn::Lease&) { called = true; });
    CHECK(called);
}

TEST_CASE("with_key rotates with the next lease from report") {
    Fixture f(pool_handler(5));
    std::vector<std::int64_t> keys;
    std::string out = f.client.with_key("serpapi", [&](acn::Lease& l) -> std::string {
        keys.push_back(l.key_id());
        if (l.key_id() == 1) throw acn::RateLimited(1h, 429, "too many");
        if (l.key_id() == 2) throw acn::QuotaExhausted(429);
        if (l.key_id() == 3) throw acn::InvalidKey(401);
        return "done";
    });
    CHECK(out == "done");
    CHECK(keys == std::vector<std::int64_t>{1, 2, 3, 4});
    auto r = f.fake->reports();
    REQUIRE(r.size() == 4);
    CHECK(r[0] == json{{"outcome", "rate_limited"}, {"status_code", 429}, {"retry_after_sec", 3600},
                       {"message", "too many"}, {"rotate", true}});
    CHECK(r[1] == json{{"outcome", "quota_exhausted"}, {"status_code", 429}, {"rotate", true}});
    CHECK(r[2] == json{{"outcome", "invalid"}, {"status_code", 401}, {"rotate", true}});
    CHECK(r[3] == json{{"outcome", "ok"}});
    // Only one lease call: the rest came back from report.
    int leases = 0;
    for (const auto& req : f.fake->requests) leases += req.url == "http://acn.test/api/v1/lease";
    CHECK(leases == 1);
}

TEST_CASE("with_key leases again, excluding tried keys, when report has no next") {
    Fixture f(pool_handler(3, false));
    acn::WithKeyOptions o;
    o.lease.exclude_key_ids = {99};
    o.lease.ttl = 30s;
    o.lease.wait = 5s;
    f.client.with_key(
        "serpapi",
        [](acn::Lease& l) {
            if (l.key_id() == 1) throw acn::RateLimited();
        },
        o);
    json second = f.fake->body(2);
    CHECK(f.fake->path(2) == "/api/v1/lease");
    CHECK(second["exclude_key_ids"] == json::array({99, 1}));
    CHECK(second["ttl_sec"] == 30);
    CHECK(second["wait_sec"] == 5);
    CHECK(f.fake->body(0)["wait_sec"] == 5);
}

TEST_CASE("with_key gives up after max_attempts with the last rotation error") {
    Fixture f(pool_handler(10));
    acn::WithKeyOptions o;
    o.max_attempts = 3;
    int calls = 0;
    try {
        f.client.with_key(
            "serpapi",
            [&](acn::Lease&) {
                ++calls;
                throw acn::RateLimited(2s, 429, "attempt " + std::to_string(calls));
            },
            o);
        FAIL("expected RateLimited");
    } catch (const acn::RateLimited& e) {
        CHECK(e.message() == "attempt 3");
        CHECK(e.retry_after() == 2s);
    }
    CHECK(calls == 3);
    auto r = f.fake->reports();
    REQUIRE(r.size() == 3);
    CHECK(r[0]["rotate"] == true);
    CHECK(r[1]["rotate"] == true);
    CHECK_FALSE(r[2].contains("rotate"));
    CHECK(r[2]["outcome"] == "rate_limited");
}

TEST_CASE("with_key reports error and rethrows other exceptions unchanged") {
    Fixture f(pool_handler(3));
    CHECK_THROWS_WITH_AS(f.client.with_key("serpapi", [](acn::Lease&) -> int { throw std::runtime_error("boom"); }),
                         "boom", std::runtime_error);
    auto r = f.fake->reports();
    REQUIRE(r.size() == 1);
    CHECK(r[0] == json{{"outcome", "error"}, {"message", "boom"}});

    CHECK_THROWS_AS(f.client.with_key("serpapi", [](acn::Lease&) { throw 42; }), int);
    r = f.fake->reports();
    REQUIRE(r.size() == 2);
    CHECK(r[1] == json{{"outcome", "error"}, {"message", "unknown exception"}});
}

TEST_CASE("with_key keeps the caller's exception when the report fails") {
    Fixture f([](const acn::HttpRequest& r) {
        if (r.url.find("/report") != std::string::npos) return reply(500, {{"error", {{"code", "x"}, {"message", "y"}}}});
        return reply(200, lease_json("lse_1", 1));
    });
    CHECK_THROWS_AS(f.client.with_key("serpapi", [](acn::Lease&) { throw std::logic_error("mine"); }),
                    std::logic_error);
    // A failed ok report surfaces, like the Go helper.
    CHECK_THROWS_AS(f.client.with_key("serpapi", [](acn::Lease&) {}), acn::ApiError);
}

TEST_CASE("with_key scrubs the secret from the reported message") {
    Fixture f([](const acn::HttpRequest& r) {
        if (r.url.find("/report") != std::string::npos) return reply(200, {{"ok", true}, {"next", nullptr}});
        return reply(200, lease_json("lse_1", 1, "sk/live+1"));
    });
    CHECK_THROWS(f.client.with_key("serpapi", [](acn::Lease& l) {
        throw std::runtime_error("GET " + l.signed_url("/x") + " failed with raw " + l.secret());
    }));
    std::string msg = f.fake->reports().at(0)["message"];
    CHECK(msg.find("sk/live+1") == std::string::npos);
    CHECK(msg.find("sk%2Flive%2B1") == std::string::npos);
    CHECK(msg == "GET https://serpapi.com/x?api_key=[key] failed with raw [key]");
}

TEST_CASE("scrub_message truncates to 500 bytes without splitting UTF-8") {
    std::string s(499, 'a');
    s += "\xC3\xA9tail";  // a two byte character straddles the limit
    std::string out = acn::detail::scrub_message(s, "");
    CHECK(out.size() == 499);
    CHECK(acn::detail::scrub_message(std::string(800, 'b'), "").size() == 500);
}

TEST_CASE("proxy builds URLs and sends through the transport without a timeout") {
    Fixture f([](const acn::HttpRequest&) {
        return acn::HttpResponse{500, {{"X-Acnologia-Key-Id", "4"}, {"X-Acnologia-Attempts", "2"}}, "upstream"};
    });
    auto p = f.client.proxy("serpapi");
    CHECK(p.url("/search.json", {{"q", "x y"}}) == "http://acn.test/proxy/serpapi/search.json?q=x%20y");
    CHECK(f.client.proxy_url("my svc", "a") == "http://acn.test/proxy/my%20svc/a");

    auto resp = p.get("/search.json", {{"engine", "google"}}, {{"Authorization", "Bearer evil"}, {"X-Trace", "1"}});
    CHECK(resp.status == 500);  // returned as is, not thrown
    CHECK(resp.body == "upstream");
    CHECK(resp.header("x-acnologia-key-id") == "4");
    const auto& r = f.fake->requests[0];
    CHECK(r.method == "GET");
    CHECK(r.url == "http://acn.test/proxy/serpapi/search.json?engine=google");
    CHECK(r.timeout == 0ms);
    CHECK(header(r, "Authorization") == "Bearer acn_test_token");
    CHECK(header(r, "X-Trace") == "1");
    int auth_headers = 0;
    for (const auto& h : r.headers) auth_headers += h.first == "Authorization";
    CHECK(auth_headers == 1);

    f.client.proxy_request("serpapi", "delete", "/proxy/serpapi/v1/x?y=1");
    CHECK(f.fake->requests[1].method == "DELETE");
    CHECK(f.fake->requests[1].url == "http://acn.test/proxy/serpapi/v1/x?y=1");

    p.post("/v1/tts", R"({"text":"hi"})");
    CHECK(f.fake->requests[2].method == "POST");
    CHECK(f.fake->requests[2].body == R"({"text":"hi"})");
    CHECK(header(f.fake->requests[2], "Content-Type") == "application/json");

    f.client.proxy_get("serpapi", "/a?b=1", {{"c", "2"}});
    CHECK(f.fake->requests[3].url == "http://acn.test/proxy/serpapi/a?b=1&c=2");
}

TEST_CASE("printing never shows the secret or the token") {
    Fixture f([](const acn::HttpRequest&) {
        return reply(200, lease_json("lse_1", 1, "TOPSECRET", {{"type", "bearer"}, {"name", ""}}));
    });
    auto l = f.client.lease("serpapi");
    std::ostringstream os;
    os << l << " " << f.client << " " << l.apply() << " " << f.client.proxy("serpapi");
    std::string all = os.str() + acn::to_string(l) + acn::to_string(f.client) + acn::to_string(l.apply());
    CHECK(all.find("TOPSECRET") == std::string::npos);
    CHECK(all.find("acn_test_token") == std::string::npos);
    CHECK(all.find("[redacted]") != std::string::npos);
    CHECK(all.find("lse_1") != std::string::npos);
}

TEST_CASE("from_env reads ACNOLOGIA_URL and ACNOLOGIA_TOKEN") {
    auto fake = std::make_shared<FakeTransport>([](const acn::HttpRequest&) { return reply(200, {{"services", json::array()}}); });
    acn::ClientOptions o;
    o.transport = fake;
    o.user_agent = "my-app/1";
    clear_env();
    CHECK_THROWS_AS(acn::Client::from_env(o), std::invalid_argument);
    set_env("ACNOLOGIA_URL", "http://env.test//");
    CHECK_THROWS_AS(acn::Client::from_env(o), std::invalid_argument);
    set_env("ACNOLOGIA_TOKEN", "acn_env");
    auto c = acn::Client::from_env(o);
    CHECK(c.base_url() == "http://env.test");
    CHECK(c.services().empty());
    CHECK(fake->requests[0].url == "http://env.test/api/v1/services");
    CHECK(header(fake->requests[0], "Authorization") == "Bearer acn_env");
    CHECK(header(fake->requests[0], "User-Agent") == "my-app/1");
    clear_env();
    CHECK_THROWS_AS(acn::Client("", "t"), std::invalid_argument);
}

TEST_CASE("a lease outlives its client") {
    std::shared_ptr<FakeTransport> fake = std::make_shared<FakeTransport>(pool_handler(2));
    std::optional<acn::Lease> l;
    {
        acn::ClientOptions o;
        o.transport = fake;
        acn::Client c("http://acn.test", "acn_x", o);
        l = c.lease("serpapi");
    }
    CHECK_FALSE(l->report(acn::Outcome::ok).has_value());
}

TEST_CASE("me parses an ordinary app") {
    Fixture f([](const acn::HttpRequest&) {
        return reply(200, json::parse(R"({
            "app": {"id": 7, "name": "agi-labs-worker"},
            "limits": {"daily_requests": 1000, "monthly_requests": null, "daily_budget": 2.5, "monthly_budget": null},
            "usage": {"today_requests": 12, "today_cost": 0.25, "month_requests": 340, "month_cost": 3.5,
                      "last_used_at": "2026-11-04T09:00:00Z", "limit_hit": null},
            "limit_hit": null,
            "services": [{"slug": "agi-labs", "name": "AGI Labs", "provider": "agi-labs", "category": "llm",
                          "available": 1, "logo_url": null}],
            "share": null})"));
    });
    acn::Me me = f.client.me();
    CHECK(f.fake->requests[0].method == "GET");
    CHECK(f.fake->path(0) == "/api/v1/me");
    CHECK(header(f.fake->requests[0], "Authorization") == "Bearer acn_test_token");
    CHECK(me.app.id == 7);
    CHECK(me.app.name == "agi-labs-worker");
    CHECK(me.limits.daily_requests == 1000);
    CHECK_FALSE(me.limits.monthly_requests.has_value());
    CHECK(me.limits.daily_budget == 2.5);
    CHECK_FALSE(me.limits.monthly_budget.has_value());
    CHECK(me.usage.today_requests == 12);
    CHECK(me.usage.today_cost == 0.25);
    CHECK(me.usage.month_requests == 340);
    CHECK(me.usage.month_cost == 3.5);
    CHECK(me.usage.last_used_at == "2026-11-04T09:00:00Z");
    CHECK_FALSE(me.usage.limit_hit.has_value());
    CHECK_FALSE(me.limit_hit.has_value());
    REQUIRE(me.services.size() == 1);
    CHECK(me.services[0].slug == "agi-labs");
    CHECK(me.services[0].available == 1);
    CHECK(me.services[0].logo_url.empty());
    CHECK_FALSE(me.share.has_value());
}

TEST_CASE("me parses a share token, its limit hit and a paused share") {
    Fixture f([](const acn::HttpRequest&) {
        return reply(200, json::parse(R"({
            "app": {"id": 9, "name": "Team share"},
            "limits": {"daily_requests": 10},
            "usage": {"today_requests": 10, "today_cost": 0, "month_requests": 10, "month_cost": 0,
                      "last_used_at": null,
                      "limit_hit": {"limit": "daily_requests", "reset_at": "2026-11-05T00:00:00Z"}},
            "limit_hit": {"limit": "daily_requests", "reset_at": "2026-11-05T00:00:00Z"},
            "services": [],
            "share": {"id": "shr_1", "name": "Team share", "owner": {"name": "Home server", "id": "acn_srv_abc"},
                      "owner_name": "Home server", "owner_id": "acn_srv_abc", "recipient": "person",
                      "label": "Anyone with the link", "status": "paused", "mode": "proxy",
                      "ends_at": "2026-12-01T00:00:00Z"}})"));
    });
    acn::Me me = f.client.me();
    CHECK(me.usage.last_used_at.empty());
    REQUIRE(me.limit_hit.has_value());
    CHECK(me.limit_hit->limit == "daily_requests");
    CHECK(me.limit_hit->reset_at == "2026-11-05T00:00:00Z");
    REQUIRE(me.usage.limit_hit.has_value());
    CHECK(me.services.empty());
    REQUIRE(me.share.has_value());
    CHECK(me.share->id == "shr_1");
    CHECK(me.share->owner.name == "Home server");
    CHECK(me.share->owner.id == "acn_srv_abc");
    CHECK(me.share->recipient == "person");
    CHECK(me.share->status == "paused");
    CHECK_FALSE(me.share->active());
    CHECK(me.share->ends_at == "2026-12-01T00:00:00Z");
}

TEST_CASE("services reads guide_url and guide fetches Markdown") {
    Fixture f([](const acn::HttpRequest& r) {
        if (r.url.find("/guide") != std::string::npos) {
            if (r.url.find("/services/secret/") != std::string::npos) {
                return error_reply(403, "service_not_allowed", "not yours");
            }
            return acn::HttpResponse{200, {{"Content-Type", "text/markdown"}}, "# AGI Labs\n\nPOST /v1/chat\n"};
        }
        return reply(200, {{"services", {{{"slug", "agi-labs"}, {"guide_url", "/api/v1/services/agi-labs/guide"}}}}});
    });
    CHECK(f.client.services().at(0).guide_url == "/api/v1/services/agi-labs/guide");
    CHECK(f.client.guide("agi-labs") == "# AGI Labs\n\nPOST /v1/chat\n");
    CHECK(f.fake->path(1) == "/api/v1/services/agi-labs/guide?format=md");
    CHECK(header(f.fake->requests[1], "Accept") == "text/markdown");
    CHECK_THROWS_AS(f.client.guide("secret"), acn::Forbidden);
}

TEST_CASE("app_limit_reached is AppLimitReached with limit, reset_at and retry_after") {
    bool body_retry = true;
    Fixture f([&](const acn::HttpRequest&) {
        json extra = {{"limit", "monthly_budget"}, {"reset_at", "2026-12-01T00:00:00Z"}};
        if (body_retry) extra["retry_after_sec"] = 120;
        return error_reply(429, "app_limit_reached", "this app reached its monthly budget limit", extra,
                           {{"Retry-After", "90"}});
    });
    try {
        f.client.lease("agi-labs");
        FAIL("expected AppLimitReached");
    } catch (const acn::AppLimitReached& e) {
        CHECK(e.status() == 429);
        CHECK(e.code() == "app_limit_reached");
        CHECK(e.limit() == "monthly_budget");
        CHECK(e.reset_at() == "2026-12-01T00:00:00Z");
        CHECK(e.retry_after() == 120s);
        CHECK(std::string(e.what()) ==
              "acnologia: app limit reached (monthly_budget, resets 2026-12-01T00:00:00Z) (retry after 120s): "
              "this app reached its monthly budget limit");
    }
    body_retry = false;
    try {
        f.client.services();
        FAIL("expected AppLimitReached");
    } catch (const acn::AppLimitReached& e) {
        CHECK(e.retry_after() == 90s);  // from the Retry-After header
    }
}

TEST_CASE("Retry-After is the fallback for every error") {
    std::string code = "no_key_available";
    Fixture f([&](const acn::HttpRequest&) {
        return error_reply(code == "rate_limited" ? 429 : 503, code, "m", json::object(), {{"Retry-After", "4"}});
    });
    try {
        f.client.lease("agi-labs");
        FAIL("expected NoKeyAvailable");
    } catch (const acn::NoKeyAvailable& e) {
        CHECK(e.retry_after() == 4s);
    }
    code = "service_unavailable";
    try {
        f.client.lease("agi-labs");
        FAIL("expected ServiceUnavailable");
    } catch (const acn::ServiceUnavailable& e) {
        CHECK(e.retry_after() == 4s);
    }
    code = "rate_limited";
    try {
        f.client.lease("agi-labs");
        FAIL("expected ApiError");
    } catch (const acn::ApiError& e) {
        CHECK(e.status() == 429);
        CHECK(e.retry_after() == 4s);
    }
}

TEST_CASE("share, lease, upstream and loop errors have their own types") {
    std::string code;
    int status = 403;
    Fixture f([&](const acn::HttpRequest&) { return error_reply(status, code, "msg"); });

    code = "share_revoked";
    try {
        f.client.services();
        FAIL("expected ShareRevoked");
    } catch (const acn::ShareRevoked& e) {
        CHECK(e.share_status() == "revoked");
        CHECK(e.status() == 403);
    }
    code = "share_paused";
    CHECK_THROWS_AS(f.client.services(), acn::SharePaused);
    try {
        f.client.services();
    } catch (const acn::ShareUnavailable& e) {
        CHECK(e.share_status() == "paused");
    }
    code = "share_expired";
    CHECK_THROWS_AS(f.client.services(), acn::ShareExpired);
    CHECK_THROWS_AS(f.client.services(), acn::Forbidden);  // still a Forbidden for older handlers

    code = "lease_not_allowed";
    CHECK_THROWS_AS(f.client.lease("agi-labs"), acn::LeaseNotAllowed);
    CHECK_THROWS_AS(f.client.lease("agi-labs"), acn::Forbidden);

    code = "lease_revoked";
    CHECK_THROWS_AS(f.client.services(), acn::LeaseRevoked);

    status = 502;
    code = "upstream_blocked";
    CHECK_THROWS_AS(f.client.services(), acn::UpstreamBlocked);
    code = "upstream_unreachable";
    CHECK_THROWS_AS(f.client.services(), acn::UpstreamUnreachable);

    status = 508;
    code = "loop_detected";
    try {
        f.client.services();
        FAIL("expected LoopDetected");
    } catch (const acn::LoopDetected& e) {
        CHECK(e.status() == 508);
        CHECK(std::string(e.what()) == "acnologia: http 508: loop_detected: msg");
    }
}

TEST_CASE("renew throws LeaseRevoked when the app may no longer hold the key") {
    Fixture f([](const acn::HttpRequest& r) {
        if (r.url.find("/renew") != std::string::npos) return error_reply(403, "lease_revoked", "key disabled");
        return reply(200, lease_json("lse_1", 1));
    });
    auto l = f.client.lease("agi-labs");
    CHECK_THROWS_AS(l.renew(), acn::LeaseRevoked);
}

TEST_CASE("proxy answers stay responses unless throw_proxy_errors is set") {
    acn::HttpResponse next;
    auto fake = std::make_shared<FakeTransport>([&](const acn::HttpRequest&) { return next; });
    acn::ClientOptions o;
    o.transport = fake;
    acn::Client plain("http://acn.test", "acn_x", o);
    o.throw_proxy_errors = true;
    acn::Client strict("http://acn.test", "acn_x", o);

    next = error_reply(429, "app_limit_reached", "limit", {{"limit", "daily_requests"}, {"retry_after_sec", 5}});
    CHECK(plain.proxy("agi-labs").post("/v1/chat", "{}").status == 429);
    CHECK(acn::is_acnologia_error(next));
    CHECK_THROWS_AS(plain.proxy("agi-labs").check(next), acn::AppLimitReached);
    CHECK_THROWS_AS(acn::throw_if_acnologia_error(next), acn::AppLimitReached);
    try {
        strict.proxy("agi-labs").post("/v1/chat", "{}");
        FAIL("expected AppLimitReached");
    } catch (const acn::AppLimitReached& e) {
        CHECK(e.limit() == "daily_requests");
        CHECK(e.retry_after() == 5s);
    }

    next = error_reply(403, "share_expired", "over");
    CHECK_THROWS_AS(strict.proxy_get("agi-labs", "/v1/models"), acn::ShareExpired);
    next = error_reply(508, "loop_detected", "loop");
    CHECK_THROWS_AS(strict.proxy_get("agi-labs", "/v1/models"), acn::LoopDetected);
    next = error_reply(503, "all_keys_busy", "busy", {{"retry_after_sec", 1}});
    CHECK_THROWS_AS(strict.proxy_get("agi-labs", "/v1/models"), acn::AllKeysBusy);

    // The provider's own answers are never thrown, even when they look alike.
    next = error_reply(429, "app_limit_reached", "provider side", json::object(), {{"X-Acnologia-Key-Id", "3"}});
    CHECK_FALSE(acn::is_acnologia_error(next));
    CHECK(strict.proxy("agi-labs").post("/v1/chat", "{}").status == 429);
    next = error_reply(429, "rate_limit_exceeded", "slow down");
    CHECK_FALSE(acn::is_acnologia_error(next));
    CHECK(strict.proxy("agi-labs").post("/v1/chat", "{}").status == 429);
    next = error_reply(500, "no_key_available", "wrong status");
    CHECK_FALSE(acn::is_acnologia_error(next));
    next = {200, {}, R"({"error":{"code":"loop_detected"}})"};
    CHECK_FALSE(acn::is_acnologia_error(next));
    next = {404, {}, "not json"};
    CHECK_FALSE(acn::is_acnologia_error(next));
}

TEST_CASE("put, patch and del send their methods") {
    Fixture f([](const acn::HttpRequest&) { return acn::HttpResponse{204, {}, ""}; });
    auto p = f.client.proxy("agi-labs");
    p.put("/v1/files/1", R"({"a":1})");
    p.patch("/v1/files/1", "a=1", "application/x-www-form-urlencoded");
    p.del("/v1/files/1", {{"force", "true"}});
    CHECK(f.fake->requests[0].method == "PUT");
    CHECK(header(f.fake->requests[0], "Content-Type") == "application/json");
    CHECK(f.fake->requests[1].method == "PATCH");
    CHECK(header(f.fake->requests[1], "Content-Type") == "application/x-www-form-urlencoded");
    CHECK(f.fake->requests[1].body == "a=1");
    CHECK(f.fake->requests[2].method == "DELETE");
    CHECK(f.fake->requests[2].url == "http://acn.test/proxy/agi-labs/v1/files/1?force=true");
    CHECK(f.fake->requests[2].body.empty());
}

TEST_CASE("post_idempotent sends an Idempotency-Key and retries a failed connection with it") {
    int fail = 0;
    Fixture f([&](const acn::HttpRequest&) {
        if (fail > 0) {
            --fail;
            throw acn::TransportError("acnologia: POST request failed: connection reset");
        }
        return acn::HttpResponse{200, {{"Idempotent-Replayed", "true"}}, "{}"};
    });
    auto p = f.client.proxy("agi-labs");

    auto resp = p.post_idempotent("/v1/chat", R"({"q":"hi"})", "job-42", "application/json",
                                  {{"idempotency-key", "old"}, {"X-Trace", "1"}});
    CHECK(resp.idempotent_replayed());
    CHECK(header(f.fake->requests[0], "Idempotency-Key") == "job-42");
    CHECK(header(f.fake->requests[0], "idempotency-key").empty());
    CHECK(header(f.fake->requests[0], "X-Trace") == "1");
    CHECK(header(f.fake->requests[0], "Content-Type") == "application/json");

    p.post_idempotent("/v1/chat", "{}");
    std::string generated = header(f.fake->requests[1], "Idempotency-Key");
    CHECK(generated.size() == 32);

    fail = 1;
    p.post_idempotent("/v1/chat", "{}", "retry-me");
    REQUIRE(f.fake->requests.size() == 4);
    CHECK(header(f.fake->requests[2], "Idempotency-Key") == "retry-me");
    CHECK(header(f.fake->requests[3], "Idempotency-Key") == "retry-me");

    fail = 2;
    CHECK_THROWS_AS(p.post_idempotent("/v1/chat", "{}", "twice"), acn::TransportError);
    CHECK_THROWS_AS(p.post_idempotent("/v1/chat", "{}", std::string(201, 'k')), std::invalid_argument);
    CHECK_FALSE(acn::HttpResponse{}.idempotent_replayed());
}

TEST_CASE("new_idempotency_key is 32 random hex characters") {
    std::string a = acn::new_idempotency_key();
    std::string b = acn::new_idempotency_key();
    CHECK(a.size() == 32);
    CHECK(a.find_first_not_of("0123456789abcdef") == std::string::npos);
    CHECK(a != b);
}

TEST_CASE("parse_retry_after reads seconds and HTTP dates") {
    CHECK(acn::parse_retry_after("120") == 120s);
    CHECK(acn::parse_retry_after(" 7 ") == 7s);
    CHECK(acn::parse_retry_after("") == 0ms);
    CHECK(acn::parse_retry_after("-1") == 0ms);
    CHECK(acn::parse_retry_after("soon") == 0ms);
    CHECK(acn::parse_retry_after("Sun, 06 Nov 1994 08:49:37 GMT") == 0ms);  // in the past
    auto later = acn::parse_retry_after("Fri, 01 Jan 2100 00:00:00 GMT");
    CHECK(later > std::chrono::hours(24 * 365));
    acn::HttpResponse r{429, {{"retry-after", "3"}}, ""};
    CHECK(r.retry_after() == 3s);
}

TEST_CASE("the access password is sent as X-Acnologia-Access") {
    clear_env();
    auto fake = std::make_shared<FakeTransport>([](const acn::HttpRequest&) { return reply(200, {{"services", json::array()}}); });
    acn::ClientOptions o;
    o.transport = fake;
    o.access_password = "correct horse battery";
    acn::Client c("http://acn.test", "acn_x", o);
    c.services();
    CHECK(header(fake->requests[0], "X-Acnologia-Access") == "correct horse battery");
    c.proxy("agi-labs").get("/v1/models", {}, {{"X-Acnologia-Access", "other"}});
    int n = 0;
    for (const auto& h : fake->requests[1].headers) n += h.first == "X-Acnologia-Access";
    CHECK(n == 1);
    CHECK(header(fake->requests[1], "X-Acnologia-Access") == "correct horse battery");

    o.access_password.clear();
    acn::Client none("http://acn.test", "acn_x", o);
    none.services();
    CHECK(header(fake->requests[2], "X-Acnologia-Access").empty());
}

TEST_CASE("ACNOLOGIA_ACCESS_PASSWORD goes only to its own server") {
    clear_env();
    auto fake = std::make_shared<FakeTransport>([](const acn::HttpRequest&) { return reply(200, {{"services", json::array()}}); });
    acn::ClientOptions o;
    o.transport = fake;
    set_env("ACNOLOGIA_URL", "https://keys.example.test:443/");
    set_env("ACNOLOGIA_TOKEN", "acn_env");
    set_env("ACNOLOGIA_ACCESS_PASSWORD", "env password 123");

    acn::Client::from_env(o).services();
    CHECK(header(fake->requests[0], "X-Acnologia-Access") == "env password 123");
    acn::Client("", "", o).services();  // empty arguments fall back to the environment
    CHECK(fake->requests[1].url == "https://keys.example.test:443/api/v1/services");
    CHECK(header(fake->requests[1], "Authorization") == "Bearer acn_env");
    CHECK(header(fake->requests[1], "X-Acnologia-Access") == "env password 123");

    acn::Client("https://other.example.test", "acn_x", o).services();
    CHECK(header(fake->requests[2], "X-Acnologia-Access").empty());
    acn::Client("HTTPS://keys.example.test/", "acn_x", o).services();  // same origin, default port left out
    CHECK(header(fake->requests[3], "X-Acnologia-Access") == "env password 123");

    set_env("ACNOLOGIA_ACCESS_URL", "https://other.example.test/");
    acn::Client("https://other.example.test", "acn_x", o).services();
    CHECK(header(fake->requests[4], "X-Acnologia-Access") == "env password 123");
    acn::Client::from_env(o).services();
    CHECK(header(fake->requests[5], "X-Acnologia-Access").empty());
    clear_env();
}

TEST_CASE("error messages never carry the token or the access password") {
    acn::ClientOptions o;
    bool transport_fails = false;
    auto fake = std::make_shared<FakeTransport>([&](const acn::HttpRequest&) -> acn::HttpResponse {
        if (transport_fails) {
            throw acn::TransportError("acnologia: GET request failed: http://acn.test/?acn_token=acn_s3cret_token");
        }
        return error_reply(400, "bad_request", "token acn_s3cret_token and password hunter2hunter2 echoed");
    });
    o.transport = fake;
    o.access_password = "hunter2hunter2";
    acn::Client c("http://acn.test", "acn_s3cret_token", o);
    try {
        c.services();
        FAIL("expected ApiError");
    } catch (const acn::ApiError& e) {
        CHECK(e.message() == "token [redacted] and password [redacted] echoed");
        CHECK(std::string(e.what()).find("acn_s3cret_token") == std::string::npos);
    }
    transport_fails = true;
    try {
        c.services();
        FAIL("expected TransportError");
    } catch (const acn::TransportError& e) {
        CHECK(std::string(e.what()) == "acnologia: GET request failed: http://acn.test/?acn_token=[redacted]");
    }
    try {
        c.proxy("agi-labs").get("/v1/models");
        FAIL("expected TransportError");
    } catch (const acn::TransportError& e) {
        CHECK(std::string(e.what()).find("acn_s3cret_token") == std::string::npos);
    }
}

TEST_CASE("report scrubs the secret from a message you write yourself") {
    Fixture f(pool_handler(1));
    auto l = f.client.lease("agi-labs");
    acn::Report r;
    r.outcome = acn::Outcome::error;
    r.message = "failed with " + l.secret() + " " + std::string(600, 'x');
    l.report(r);
    std::string msg = f.fake->reports().at(0)["message"];
    CHECK(msg.find(l.secret()) == std::string::npos);
    CHECK(msg.rfind("failed with [key] ", 0) == 0);
    CHECK(msg.size() == 500);
}

TEST_CASE("guide_markdown and guide_details read the usage guide") {
    Fixture f([](const acn::HttpRequest& r) {
        if (header(r, "Accept") == "text/markdown") {
            return acn::HttpResponse{200, {{"Content-Type", "text/markdown"}}, "# AGI Labs\n"};
        }
        if (r.url.find("/services/secret/") != std::string::npos) {
            return error_reply(403, "service_not_allowed", "not yours");
        }
        return reply(200, {{"markdown", "# AGI Labs\n"},
                           {"generated", true},
                           {"guide", {{"endpoints", json::array({{{"path", "/v1/chat"}}})}}}});
    });
    CHECK(f.client.guide_markdown("agi-labs") == "# AGI Labs\n");
    CHECK(f.fake->requests[0].method == "GET");
    CHECK(header(f.fake->requests[0], "Authorization") == "Bearer acn_test_token");

    acn::Guide g = f.client.guide_details("agi-labs");
    CHECK(f.fake->path(1) == "/api/v1/services/agi-labs/guide");
    CHECK(header(f.fake->requests[1], "Accept") == "application/json");
    CHECK(g.markdown == "# AGI Labs\n");
    CHECK(g.generated);
    CHECK(json::parse(g.guide_json) == json{{"endpoints", json::array({{{"path", "/v1/chat"}}})}});
    CHECK_THROWS_AS(f.client.guide_details("secret"), acn::Forbidden);
}

TEST_CASE("capabilities lists the capabilities with their params and routes") {
    Fixture f([](const acn::HttpRequest&) {
        return reply(200, {{"capabilities",
                            {{{"slug", "maps-search"},
                              {"name", "Maps search"},
                              {"description", "Find places"},
                              {"strategy", "priority"},
                              {"params", {{"query", {{"type", "string"}, {"required", true}}}}},
                              {"routes", {{{"service", "omkar-cloud"}, {"mapped", true}},
                                          {{"service", "serpapi"}, {"mapped", false}}}},
                              {"url", "http://acn.test/proxy/maps-search"}}}}});
    });
    auto caps = f.client.capabilities();
    CHECK(f.fake->requests[0].method == "GET");
    CHECK(f.fake->path(0) == "/api/v1/capabilities");
    REQUIRE(caps.size() == 1);
    CHECK(caps[0].slug == "maps-search");
    CHECK(caps[0].name == "Maps search");
    CHECK(caps[0].description == "Find places");
    CHECK(caps[0].strategy == "priority");
    CHECK(json::parse(caps[0].params_json)["query"]["required"] == true);
    REQUIRE(caps[0].routes.size() == 2);
    CHECK(caps[0].routes[0].service == "omkar-cloud");
    CHECK(caps[0].routes[0].mapped);
    CHECK_FALSE(caps[0].routes[1].mapped);
    CHECK(caps[0].url == "http://acn.test/proxy/maps-search");
}

TEST_CASE("capability posts the params and reads the capability headers") {
    Fixture f([](const acn::HttpRequest&) {
        return acn::HttpResponse{200,
                                 {{"Content-Type", "application/json"},
                                  {"X-Acnologia-Capability", "maps-search"},
                                  {"X-Acnologia-Service", "serpapi"},
                                  {"X-Acnologia-Tried", "omkar-cloud=503, serpapi=200"},
                                  {"X-Acnologia-Key-Id", "9"},
                                  {"X-Acnologia-Attempts", "1"},
                                  {"X-Acnologia-Cache", "miss"}},
                                 R"({"items":[],"count":0})"};
    });
    acn::CapabilityOptions o;
    o.service = "serpapi";
    o.raw = true;
    o.idempotency_key = "cap-1";
    o.no_cache = true;
    auto resp = f.client.capability("maps-search", R"({"query":"cafes"})", o);
    const auto& r = f.fake->requests[0];
    CHECK(r.method == "POST");
    CHECK(f.fake->path(0) == "/proxy/maps-search?_service=serpapi&_raw=1");
    CHECK(r.body == R"({"query":"cafes"})");
    CHECK(header(r, "Content-Type") == "application/json");
    CHECK(header(r, "Idempotency-Key") == "cap-1");
    CHECK(header(r, "Cache-Control") == "no-cache");
    CHECK(header(r, "Authorization") == "Bearer acn_test_token");
    CHECK(resp.capability() == "maps-search");
    CHECK(resp.service() == "serpapi");
    CHECK(resp.cache() == "miss");
    CHECK(resp.key_id() == 9);
    CHECK(resp.attempts() == 1);
    auto tried = resp.tried();
    REQUIRE(tried.size() == 2);
    CHECK(tried[0].service == "omkar-cloud");
    CHECK(tried[0].status == 503);
    CHECK(tried[1].service == "serpapi");
    CHECK(tried[1].status == 200);

    f.client.capability("maps-search");
    CHECK(f.fake->path(1) == "/proxy/maps-search");
    CHECK(f.fake->requests[1].body == "{}");
    CHECK(header(f.fake->requests[1], "Cache-Control").empty());
    CHECK(header(f.fake->requests[1], "Idempotency-Key").empty());
}

TEST_CASE("capability errors from Acnologia are recognized") {
    acn::HttpResponse next = error_reply(503, "no_route", "none");
    auto fake = std::make_shared<FakeTransport>([&](const acn::HttpRequest&) { return next; });
    acn::ClientOptions o;
    o.transport = fake;
    o.throw_proxy_errors = true;
    acn::Client strict("http://acn.test", "acn_x", o);
    try {
        strict.capability("maps-search", "{}");
        FAIL("expected ApiError");
    } catch (const acn::ApiError& e) {
        CHECK(e.code() == "no_route");
        CHECK(e.status() == 503);
    }
    next = error_reply(400, "bad_params", "query is required");
    CHECK_THROWS_AS(strict.capability("maps-search", "{}"), acn::ApiError);
    next = error_reply(503, "no_route_worked", "all failed");
    CHECK(acn::is_acnologia_error(next));
}

TEST_CASE("request options send Idempotency-Key and Cache-Control and responses expose the headers") {
    Fixture f([](const acn::HttpRequest&) {
        return acn::HttpResponse{200,
                                 {{"X-Acnologia-Key-Id", "12"},
                                  {"X-Acnologia-Attempts", "2"},
                                  {"X-Acnologia-Service", "agi-labs"},
                                  {"X-Acnologia-Cache", "replay"},
                                  {"Idempotent-Replayed", "true"}},
                                 "ok"};
    });
    acn::RequestOptions ro;
    ro.idempotency_key = "order-42";
    ro.no_cache = true;
    auto resp = f.client.proxy("agi-labs").request("post", "/v1/chat", "{}", {{"Cache-Control", "max-age=60"}}, {}, ro);
    const auto& r = f.fake->requests[0];
    CHECK(r.method == "POST");
    CHECK(header(r, "Idempotency-Key") == "order-42");
    CHECK(header(r, "Cache-Control") == "no-cache");
    int cache_headers = 0;
    for (const auto& h : r.headers) cache_headers += h.first == "Cache-Control";
    CHECK(cache_headers == 1);
    CHECK(resp.key_id() == 12);
    CHECK(resp.attempts() == 2);
    CHECK(resp.service() == "agi-labs");
    CHECK(resp.cache() == "replay");
    CHECK(resp.replayed());
    CHECK(resp.capability().empty());
    CHECK(resp.tried().empty());

    acn::HttpResponse bare{200, {}, ""};
    CHECK_FALSE(bare.key_id().has_value());
    CHECK(bare.attempts() == 0);
    CHECK_FALSE(bare.replayed());

    acn::RequestOptions too_long;
    too_long.idempotency_key = std::string(201, 'k');
    CHECK_THROWS_AS(f.client.proxy("agi-labs").request("POST", "/x", "{}", {}, {}, too_long), std::invalid_argument);
}

TEST_CASE("parse_tried reads service=status pairs") {
    auto t = acn::parse_tried("a=200,b=503 , c");
    REQUIRE(t.size() == 3);
    CHECK(t[0].service == "a");
    CHECK(t[0].status == 200);
    CHECK(t[1].service == "b");
    CHECK(t[1].status == 503);
    CHECK(t[2].service == "c");
    CHECK(t[2].status == 0);
    CHECK(acn::parse_tried("").empty());
}

TEST_CASE("forward proxy setup, CA, URL and environment") {
    const std::string pem = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n";
    Fixture f([&](const acn::HttpRequest& r) {
        if (r.url.find("/ca.pem") != std::string::npos) {
            return acn::HttpResponse{200, {{"Content-Type", "application/x-pem-file"}}, pem};
        }
        return reply(200, {{"proxy_url", "http://acn.test:8888"},
                           {"proxy_url_with_token", "http://app:%3Capp%20token%3E@acn.test:8888"},
                           {"ca_url", "http://acn.test/api/v1/proxy/ca.pem"},
                           {"ca_fingerprint", "AB:CD"},
                           {"passthrough", true},
                           {"hosts", {{{"host", "serpapi.com"}, {"service", "serpapi"}, {"name", "SerpAPI"}, {"path", ""}}}},
                           {"env", {{"HTTPS_PROXY", "http://app:%3Capp%20token%3E@acn.test:8888"}, {"NODE_USE_ENV_PROXY", "1"}}},
                           {"service_header", "X-Acnologia-Service"}});
    });
    acn::ForwardProxy fp = f.client.forward_proxy();
    CHECK(f.fake->path(0) == "/api/v1/proxy");
    CHECK(f.fake->requests[0].method == "GET");
    CHECK(header(f.fake->requests[0], "Authorization") == "Bearer acn_test_token");
    CHECK(fp.proxy_url == "http://acn.test:8888");
    CHECK(fp.proxy_url_with_token == "http://app:%3Capp%20token%3E@acn.test:8888");
    CHECK(fp.ca_url == "http://acn.test/api/v1/proxy/ca.pem");
    CHECK(fp.ca_fingerprint == "AB:CD");
    CHECK(fp.passthrough);
    REQUIRE(fp.hosts.size() == 1);
    CHECK(fp.hosts[0].host == "serpapi.com");
    CHECK(fp.hosts[0].service == "serpapi");
    CHECK(fp.hosts[0].name == "SerpAPI");
    CHECK(fp.env.at("NODE_USE_ENV_PROXY") == "1");
    CHECK(fp.service_header == "X-Acnologia-Service");
    CHECK(acn::to_string(fp).find("acn_test_token") == std::string::npos);

    CHECK(f.client.forward_proxy_ca() == pem);
    const auto& ca = f.fake->requests[1];
    CHECK(f.fake->path(1) == "/api/v1/proxy/ca.pem");
    CHECK(header(ca, "Authorization").empty());

    const std::string file = "acn-cpp-test-ca.pem";
    f.client.save_forward_proxy_ca(file);
    std::ifstream in(file, std::ios::binary);
    std::stringstream saved;
    saved << in.rdbuf();
    in.close();
    CHECK(saved.str() == pem);
    std::remove(file.c_str());

    Fixture g([](const acn::HttpRequest&) {
        return reply(200, {{"proxy_url", "http://acn.test:8888"}});
    });
    acn::ClientOptions o;
    o.transport = g.fake;
    acn::Client odd("http://acn.test", "acn_a+b/c=", o);
    CHECK(odd.forward_proxy_url() == "http://app:acn_a%2Bb%2Fc%3D@acn.test:8888");
    auto env = odd.forward_proxy_env();
    CHECK(env.size() == 6);
    CHECK(env.at("HTTPS_PROXY") == "http://app:acn_a%2Bb%2Fc%3D@acn.test:8888");
    CHECK(env.at("HTTP_PROXY") == env.at("HTTPS_PROXY"));
    CHECK(env.at("NODE_USE_ENV_PROXY") == "1");
    CHECK(env.at("NODE_EXTRA_CA_CERTS") == "acnologia-ca.pem");
    CHECK(env.at("REQUESTS_CA_BUNDLE") == "acnologia-ca.pem");
    CHECK(env.at("SSL_CERT_FILE") == "acnologia-ca.pem");
    CHECK(odd.forward_proxy_env("/etc/acn.pem").at("SSL_CERT_FILE") == "/etc/acn.pem");
}

TEST_CASE("ShareUnavailable carries the share status") {
    Fixture f([](const acn::HttpRequest&) {
        return error_reply(403, "share_paused", "paused", json::object(), {{"X-Acnologia-Share", "paused"}});
    });
    try {
        f.client.me();
        FAIL("expected ShareUnavailable");
    } catch (const acn::ShareUnavailable& e) {
        CHECK(e.share_status() == "paused");
        CHECK(e.status() == 403);
    }
}
