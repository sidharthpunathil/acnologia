#define DOCTEST_CONFIG_IMPLEMENT_WITH_MAIN
#include <doctest/doctest.h>

#include <acnologia/acnologia.hpp>

#include <cstdlib>
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
    ::unsetenv("ACNOLOGIA_URL");
    ::unsetenv("ACNOLOGIA_TOKEN");
    CHECK_THROWS_AS(acn::Client::from_env(o), std::invalid_argument);
    ::setenv("ACNOLOGIA_URL", "http://env.test//", 1);
    CHECK_THROWS_AS(acn::Client::from_env(o), std::invalid_argument);
    ::setenv("ACNOLOGIA_TOKEN", "acn_env", 1);
    auto c = acn::Client::from_env(o);
    CHECK(c.base_url() == "http://env.test");
    CHECK(c.services().empty());
    CHECK(fake->requests[0].url == "http://env.test/api/v1/services");
    CHECK(header(fake->requests[0], "Authorization") == "Bearer acn_env");
    CHECK(header(fake->requests[0], "User-Agent") == "my-app/1");
    ::unsetenv("ACNOLOGIA_URL");
    ::unsetenv("ACNOLOGIA_TOKEN");
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
