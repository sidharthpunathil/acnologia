// Runs the libcurl transport against a tiny HTTP server on 127.0.0.1 with an
// ephemeral port. POSIX only.
#define DOCTEST_CONFIG_IMPLEMENT_WITH_MAIN
#include <doctest/doctest.h>

#include <acnologia/acnologia.hpp>

#include <arpa/inet.h>
#include <netinet/in.h>
#include <poll.h>
#include <sys/socket.h>
#include <unistd.h>

#include <atomic>
#include <mutex>
#include <string>
#include <thread>
#include <vector>

namespace acn = acnologia;

namespace {

struct Seen {
    std::string method, path, auth, user_agent, body, access, idempotency_key, accept;
};

class TinyServer {
public:
    TinyServer() {
        fd_ = ::socket(AF_INET, SOCK_STREAM, 0);
        int one = 1;
        ::setsockopt(fd_, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
        sockaddr_in addr{};
        addr.sin_family = AF_INET;
        addr.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
        addr.sin_port = 0;
        REQUIRE(::bind(fd_, reinterpret_cast<sockaddr*>(&addr), sizeof addr) == 0);
        REQUIRE(::listen(fd_, 16) == 0);
        socklen_t len = sizeof addr;
        ::getsockname(fd_, reinterpret_cast<sockaddr*>(&addr), &len);
        port_ = ntohs(addr.sin_port);
        thread_ = std::thread([this] { loop(); });
    }
    ~TinyServer() {
        stop_ = true;
        thread_.join();
        ::close(fd_);
    }

    std::string url() const { return "http://127.0.0.1:" + std::to_string(port_); }
    std::vector<Seen> seen() {
        std::lock_guard<std::mutex> g(mu_);
        return seen_;
    }

private:
    static std::string header_value(const std::string& head, const std::string& name) {
        std::string lower_head = head;
        for (auto& c : lower_head) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
        auto pos = lower_head.find("\r\n" + name + ":");
        if (pos == std::string::npos) return {};
        auto start = pos + 2 + name.size() + 1;
        auto end = head.find("\r\n", start);
        std::string v = head.substr(start, end - start);
        while (!v.empty() && v.front() == ' ') v.erase(0, 1);
        return v;
    }

    void loop() {
        while (!stop_) {
            pollfd p{fd_, POLLIN, 0};
            if (::poll(&p, 1, 50) <= 0) continue;
            int c = ::accept(fd_, nullptr, nullptr);
            if (c < 0) return;
            handle(c);
            ::close(c);
        }
    }

    void handle(int c) {
        std::string data;
        char buf[4096];
        std::size_t head_end = std::string::npos;
        while ((head_end = data.find("\r\n\r\n")) == std::string::npos) {
            ssize_t n = ::recv(c, buf, sizeof buf, 0);
            if (n <= 0) return;
            data.append(buf, static_cast<std::size_t>(n));
        }
        std::string head = data.substr(0, head_end);
        std::size_t want = 0;
        std::string cl = header_value(head, "content-length");
        if (!cl.empty()) want = std::stoul(cl);
        std::string body = data.substr(head_end + 4);
        while (body.size() < want) {
            ssize_t n = ::recv(c, buf, sizeof buf, 0);
            if (n <= 0) break;
            body.append(buf, static_cast<std::size_t>(n));
        }
        Seen s;
        auto sp1 = head.find(' ');
        auto sp2 = head.find(' ', sp1 + 1);
        s.method = head.substr(0, sp1);
        s.path = head.substr(sp1 + 1, sp2 - sp1 - 1);
        s.auth = header_value(head, "authorization");
        s.user_agent = header_value(head, "user-agent");
        s.access = header_value(head, "x-acnologia-access");
        s.idempotency_key = header_value(head, "idempotency-key");
        s.accept = header_value(head, "accept");
        s.body = body;
        {
            std::lock_guard<std::mutex> g(mu_);
            seen_.push_back(s);
        }
        int status = 200;
        std::string extra, out;
        if (s.path == "/api/v1/services") {
            out = R"({"services":[{"slug":"echo","name":"Echo","provider":"echo","category":"test","available":true,"base_url":"http://upstream.test","auth":{"type":"header","name":"X-Key"}}]})";
        } else if (s.path == "/api/v1/lease" && s.body.find("\"empty\"") != std::string::npos) {
            status = 503;
            out = R"({"error":{"code":"all_keys_busy","message":"busy"},"retry_after_sec":1})";
        } else if (s.path == "/api/v1/lease") {
            out = R"({"lease_id":"lse_a","key_id":5,"service":"echo","secret":"sek","auth":{"type":"header","name":"X-Key"},"base_url":"http://upstream.test","expires_at":"2026-11-02T10:00:00Z"})";
        } else if (s.path == "/api/v1/leases/lse_a/report") {
            out = R"({"ok":true,"next":null})";
        } else if (s.path == "/api/v1/me") {
            out = R"({"app":{"id":3,"name":"agi-labs-worker"},"limits":{"daily_requests":50},)"
                  R"("usage":{"today_requests":2,"today_cost":0,"month_requests":9,"month_cost":0,"last_used_at":null,"limit_hit":null},)"
                  R"("limit_hit":null,"services":[{"slug":"echo","name":"Echo","provider":"echo","category":"test","available":1,"logo_url":null}],"share":null})";
        } else if (s.path == "/api/v1/services/echo/guide?format=md") {
            extra = "Content-Type: text/markdown\r\n";
            out = "# Echo\n";
        } else if (s.path == "/proxy/echo/limited") {
            status = 429;
            extra = "Retry-After: 7\r\nContent-Type: application/json\r\n";
            out = R"({"error":{"code":"app_limit_reached","message":"limit"},"limit":"daily_requests","reset_at":"2026-11-05T00:00:00Z"})";
        } else if (s.path.rfind("/proxy/echo/redirect", 0) == 0) {
            status = 302;
            extra = "Location: http://elsewhere.test/\r\n";
        } else if (s.path.rfind("/proxy/echo/", 0) == 0) {
            extra = "X-Acnologia-Key-Id: 5\r\nX-Acnologia-Attempts: 1\r\n";
            out = s.method + " " + s.path + " " + s.body;
        } else {
            status = 404;
            out = R"({"error":{"code":"not_found","message":"no route"}})";
        }
        std::string reason = status == 200   ? "OK"
                             : status == 302 ? "Found"
                             : status == 429 ? "Too Many Requests"
                             : status == 503 ? "Service Unavailable"
                                             : "Not Found";
        std::string resp = "HTTP/1.1 " + std::to_string(status) + " " + reason + "\r\nContent-Length: " +
                           std::to_string(out.size()) + "\r\nConnection: close\r\n" + extra + "\r\n" + out;
        std::size_t sent = 0;
        while (sent < resp.size()) {
            ssize_t n = ::send(c, resp.data() + sent, resp.size() - sent, 0);
            if (n <= 0) break;
            sent += static_cast<std::size_t>(n);
        }
    }

    int fd_ = -1;
    int port_ = 0;
    std::atomic<bool> stop_{false};
    std::thread thread_;
    std::mutex mu_;
    std::vector<Seen> seen_;
};

}  // namespace

TEST_CASE("the libcurl transport talks to a real HTTP server") {
    TinyServer server;
    acn::Client client(server.url(), "acn_integration");

    auto svcs = client.services();
    REQUIRE(svcs.size() == 1);
    CHECK(svcs[0].slug == "echo");
    CHECK(svcs[0].auth.name == "X-Key");

    std::string got = client.with_key("echo", [](acn::Lease& l) { return l.apply().header_line(); });
    CHECK(got == "X-Key: sek");

    acn::LeaseOptions o;
    o.exclude_key_ids = {1};
    CHECK_THROWS_AS(client.lease("empty", o), acn::AllKeysBusy);

    auto resp = client.proxy("echo").post("/v1/things", R"({"a":1})");
    CHECK(resp.status == 200);
    CHECK(resp.header("X-Acnologia-Key-Id") == "5");
    CHECK(resp.body == R"(POST /proxy/echo/v1/things {"a":1})");

    auto redirect = client.proxy_get("echo", "/redirect");
    CHECK(redirect.status == 302);  // never followed

    auto seen = server.seen();
    REQUIRE(seen.size() == 6);
    CHECK(seen[0].auth == "Bearer acn_integration");
    CHECK(seen[0].user_agent == std::string("acnologia-cpp/") + acn::version);
    CHECK(seen[2].path == "/api/v1/leases/lse_a/report");
    CHECK(seen[2].body == R"({"outcome":"ok"})");
}

TEST_CASE("me, guide, Idempotency-Key, Retry-After and the access header over libcurl") {
    TinyServer server;
    acn::ClientOptions o;
    o.access_password = "local access password";
    o.throw_proxy_errors = true;
    acn::Client client(server.url(), "acn_integration", o);

    acn::Me me = client.me();
    CHECK(me.app.name == "agi-labs-worker");
    CHECK(me.limits.daily_requests == 50);
    REQUIRE(me.services.size() == 1);
    CHECK(me.services[0].available == 1);

    CHECK(client.guide("echo") == "# Echo\n");

    auto resp = client.proxy("echo").post_idempotent("/v1/chat", R"({"q":"hi"})", "job-1");
    CHECK(resp.status == 200);

    try {
        client.proxy_get("echo", "/limited");
        FAIL("expected AppLimitReached");
    } catch (const acn::AppLimitReached& e) {
        CHECK(e.limit() == "daily_requests");
        CHECK(e.reset_at() == "2026-11-05T00:00:00Z");
        CHECK(e.retry_after() == std::chrono::seconds(7));
    }

    auto seen = server.seen();
    REQUIRE(seen.size() == 4);
    for (const auto& s : seen) CHECK(s.access == "local access password");
    CHECK(seen[1].accept == "text/markdown");
    CHECK(seen[2].idempotency_key == "job-1");
}

TEST_CASE("a refused connection is a TransportError") {
    int port = 0;
    {
        TinyServer s;
        port = std::stoi(s.url().substr(s.url().rfind(':') + 1));
    }
    acn::ClientOptions o;
    o.timeout = std::chrono::seconds(5);
    acn::Client client("http://127.0.0.1:" + std::to_string(port), "acn_x", o);
    CHECK_THROWS_AS(client.services(), acn::TransportError);
}
