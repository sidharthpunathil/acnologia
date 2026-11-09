import XCTest
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif
@testable import Acnologia

final class ClientTests: XCTestCase {
    func testServicesSendsTokenAndUserAgent() async throws {
        StubProtocol.install { _ in
            StubResponse(body: #"{"services":[{"slug":"serpapi","name":"SerpAPI","provider":"serpapi","category":"search","available":true,"base_url":"https://serpapi.com","auth":{"type":"query","name":"api_key"}}]}"#)
        }
        let svcs = try await makeClient().services()
        XCTAssertEqual(svcs.count, 1)
        XCTAssertEqual(svcs[0].slug, "serpapi")
        XCTAssertEqual(svcs[0].baseURL, "https://serpapi.com")
        XCTAssertEqual(svcs[0].auth, Auth(type: "query", name: "api_key"))
        XCTAssertTrue(svcs[0].available)
        let r = try XCTUnwrap(StubProtocol.requests.first)
        XCTAssertEqual(r.method, "GET")
        XCTAssertEqual(r.url.absoluteString, "\(base)/api/v1/services")
        XCTAssertEqual(r.header("Authorization"), "Bearer acn_test")
        XCTAssertEqual(r.header("User-Agent"), "acnologia-swift/\(Acnologia.version)")
    }

    func testFromEnvironment() throws {
        XCTAssertThrowsError(try Acnologia.fromEnvironment([:]))
        XCTAssertThrowsError(try Acnologia.fromEnvironment(["ACNOLOGIA_URL": base]))
        let c = try Acnologia.fromEnvironment(["ACNOLOGIA_URL": base + "/", "ACNOLOGIA_TOKEN": "cog_legacy"])
        XCTAssertEqual(c.baseURL.absoluteString, base)
    }

    func testLeaseBodyAndDecode() async throws {
        StubProtocol.install { _ in StubResponse(body: leaseJSON(7)) }
        let lease = try await makeClient().lease("serpapi", options: LeaseOptions(excludeKeyIDs: [1, 2], ttl: 30.5, wait: 9.2, exclusive: true))
        XCTAssertEqual(lease.leaseID, "lse_7")
        XCTAssertEqual(lease.keyID, 7)
        XCTAssertEqual(lease.secret, "sk7")
        XCTAssertEqual(lease.baseURL, "https://serpapi.com")
        XCTAssertNotNil(lease.expiresAt)
        let body = StubProtocol.requests[0].json
        XCTAssertEqual(body["service"] as? String, "serpapi")
        XCTAssertEqual(body["exclude_key_ids"] as? [Int], [1, 2])
        XCTAssertEqual(body["ttl_sec"] as? Int, 31)
        XCTAssertEqual(body["wait_sec"] as? Int, 10)
        XCTAssertEqual(body["exclusive"] as? Bool, true)
        XCTAssertEqual(StubProtocol.requests[0].header("Content-Type"), "application/json")
    }

    func testLeaseOmitsDefaults() async throws {
        StubProtocol.install { _ in StubResponse(body: leaseJSON(1)) }
        _ = try await makeClient().lease("serpapi")
        XCTAssertEqual(Set(StubProtocol.requests[0].json.keys), ["service"])
    }

    func testLeaseRejectsBadWait() async {
        StubProtocol.install { _ in StubResponse(body: leaseJSON(1)) }
        do {
            _ = try await makeClient().lease("serpapi", options: LeaseOptions(wait: 61))
            XCTFail("expected an error")
        } catch let e as AcnologiaError {
            guard case .invalidArgument = e else { return XCTFail("got \(e)") }
        } catch { XCTFail("got \(error)") }
        XCTAssertTrue(StubProtocol.requests.isEmpty)
    }

    func testErrorMapping() async {
        let cases: [(Int, String, AcnologiaError)] = [
            (503, #"{"error":{"code":"no_key_available","message":"cooling"},"retry_after_sec":42}"#,
             .noKeyAvailable(retryAfter: 42, message: "cooling")),
            (503, #"{"error":{"code":"all_keys_busy","message":"busy"},"retry_after_sec":1}"#,
             .allKeysBusy(retryAfter: 1, message: "busy")),
            (503, #"{"error":{"code":"service_unavailable","message":"x keeps failing"},"retry_after_sec":30}"#,
             .serviceUnavailable(retryAfter: 30, message: "x keeps failing")),
            (503, "", .noKeyAvailable(retryAfter: nil, message: HTTPURLResponse.localizedString(forStatusCode: 503))),
            (401, #"{"error":{"code":"unauthenticated","message":"invalid app token"}}"#,
             .unauthenticated(message: "invalid app token")),
            (403, #"{"error":{"code":"service_not_allowed","message":"this app cannot use service x"}}"#,
             .forbidden(code: "service_not_allowed", message: "this app cannot use service x")),
            (429, #"{"error":{"code":"app_limit_reached","message":"daily limit"},"limit":"daily_requests","reset_at":"2026-11-02T00:00:00Z","retry_after_sec":3600}"#,
             .appLimitReached(limit: "daily_requests", resetAt: ISO8601DateFormatter().date(from: "2026-11-02T00:00:00Z"),
                              retryAfter: 3600, message: "daily limit")),
            (403, #"{"error":{"code":"share_paused","message":"the owner paused this share"}}"#,
             .shareUnavailable(status: "paused", message: "the owner paused this share")),
            (500, "boom", .api(statusCode: 500, code: "", message: "boom")),
        ]
        for (status, body, want) in cases {
            StubProtocol.install { _ in StubResponse(status: status, body: body) }
            do {
                _ = try await makeClient().lease("serpapi")
                XCTFail("expected \(want)")
            } catch let e as AcnologiaError {
                XCTAssertEqual(e, want)
            } catch { XCTFail("got \(error)") }
        }
        XCTAssertTrue(AcnologiaError.allKeysBusy(retryAfter: 1, message: "").isNoKeyAvailable)
        XCTAssertEqual(AcnologiaError.allKeysBusy(retryAfter: 1, message: "").statusCode, 503)
    }

    func testReportReturnsNext() async throws {
        let pool = FakePool(keys: [1, 2])
        StubProtocol.install(pool.handle)
        let lease = try await makeClient().lease("serpapi")
        let next = try await lease.report(.rateLimited(retryAfter: 2.5), statusCode: 429, message: "slow down", rotate: true)
        XCTAssertEqual(next?.keyID, 2)
        let r = StubProtocol.requests[1]
        XCTAssertEqual(r.url.path, "/api/v1/leases/lse_1/report")
        XCTAssertEqual(r.json["outcome"] as? String, "rate_limited")
        XCTAssertEqual(r.json["retry_after_sec"] as? Int, 3)
        XCTAssertEqual(r.json["status_code"] as? Int, 429)
        XCTAssertEqual(r.json["message"] as? String, "slow down")
        XCTAssertEqual(r.json["rotate"] as? Bool, true)

        let none = try await next!.report(.ok)
        XCTAssertNil(none)
        XCTAssertEqual(Set(StubProtocol.requests[2].json.keys), ["outcome"])
    }

    func testOutcomeWireValues() {
        let all: [Outcome] = [.ok, .rateLimited(), .quotaExhausted, .invalid, .error, .busy, .banned]
        XCTAssertEqual(all.map(\.wireValue), ["ok", "rate_limited", "quota_exhausted", "invalid", "error", "busy", "banned"])
    }

    func testRenew() async throws {
        StubProtocol.install { r in
            r.url.path.hasSuffix("/renew")
                ? StubResponse(body: #"{"ok":true,"expires_at":"2026-11-01T13:00:00Z"}"#)
                : StubResponse(body: leaseJSON(1))
        }
        let lease = try await makeClient().lease("serpapi")
        let exp = try await lease.renew(ttl: 600)
        XCTAssertEqual(exp, ISO8601DateFormatter().date(from: "2026-11-01T13:00:00Z"))
        XCTAssertEqual(StubProtocol.requests[1].url.path, "/api/v1/leases/lse_1/renew")
        XCTAssertEqual(StubProtocol.requests[1].json["ttl_sec"] as? Int, 600)

        StubProtocol.install { _ in
            StubResponse(status: 404, body: #"{"error":{"code":"lease_not_found","message":"lease not found, already reported, or expired"}}"#)
        }
        do {
            try await lease.renew()
            XCTFail("expected an error")
        } catch let e as AcnologiaError {
            XCTAssertEqual(e.code, "lease_not_found")
            XCTAssertEqual(e.statusCode, 404)
        }
    }

    func testApply() async throws {
        let client = makeClient()
        func lease(_ auth: String, secret: String = "s+k/1") async throws -> Lease {
            StubProtocol.install { _ in StubResponse(body: leaseJSON(1, auth: auth, secret: secret)) }
            return try await client.lease("svc")
        }
        var req = URLRequest(url: URL(string: "https://serpapi.com/search.json?q=coffee&api_key=old")!)
        try await lease(#"{"type":"query","name":"api_key"}"#).apply(to: &req)
        XCTAssertEqual(req.url?.absoluteString, "https://serpapi.com/search.json?q=coffee&api_key=s%2Bk%2F1")

        req = URLRequest(url: URL(string: "https://api.elevenlabs.io/v1/voices")!)
        try await lease(#"{"type":"header","name":"xi-api-key"}"#).apply(to: &req)
        XCTAssertEqual(req.value(forHTTPHeaderField: "xi-api-key"), "s+k/1")

        try await lease(#"{"type":"bearer","name":""}"#).apply(to: &req)
        XCTAssertEqual(req.value(forHTTPHeaderField: "Authorization"), "Bearer s+k/1")

        try await lease(#"{"type":"bearer","name":"Authorization","prefix":"Token "}"#).apply(to: &req)
        XCTAssertEqual(req.value(forHTTPHeaderField: "Authorization"), "Token s+k/1")

        let l = try await lease(#"{"type":"query","name":"key"}"#)
        let u = try l.url("/search.json", query: [URLQueryItem(name: "q", value: "a b&c")])
        XCTAssertEqual(u.absoluteString, "https://serpapi.com/search.json?q=a%20b%26c")
    }

    func testSecretIsRedacted() async throws {
        StubProtocol.install { _ in StubResponse(body: leaseJSON(1, secret: "supersecret")) }
        let client = makeClient()
        let lease = try await client.lease("serpapi")
        var dumped = ""
        dump(lease, to: &dumped)
        for text in [lease.description, lease.debugDescription, "\(lease)", String(reflecting: lease), dumped,
                     client.description, client.debugDescription, "\(client)"] {
            XCTAssertFalse(text.contains("supersecret"), text)
            XCTAssertFalse(text.contains("acn_test"), text)
        }
        XCTAssertTrue(lease.description.contains("[redacted]"))
    }

    func testScrubMessage() {
        XCTAssertEqual(scrubMessage("GET https://x/?api_key=a%2Bb failed, a+b", secret: "a+b"),
                       "GET https://x/?api_key=[key] failed, [key]")
        XCTAssertEqual(scrubMessage(String(repeating: "x", count: 600), secret: "").count, 500)
    }
}
