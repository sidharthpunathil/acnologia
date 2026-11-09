import XCTest
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif
@testable import Acnologia

final class DiscoveryTests: XCTestCase {
    func testMe() async throws {
        StubProtocol.install { _ in
            StubResponse(body: """
            {"app":{"id":12,"name":"crawler"},
             "limits":{"daily_requests":1000,"monthly_requests":null,"daily_budget":2.5,"monthly_budget":null},
             "usage":{"today_requests":40,"today_cost":0.12,"month_requests":900,"month_cost":3.4,"last_used_at":"2026-11-01T10:00:00.123456Z","limit_hit":null},
             "limit_hit":{"limit":"daily_requests","reset_at":"2026-11-02T00:00:00Z"},
             "services":[{"slug":"serpapi","name":"SerpAPI","provider":"serpapi","category":"search","available":1,"logo_url":null}],
             "share":{"id":"sh_1","name":"Team","owner":{"name":"Ana","id":"o1"},"owner_name":"Ana","owner_id":"o1","recipient":"server-b","label":"B","status":"paused","mode":"proxy","ends_at":null}}
            """)
        }
        let me = try await makeClient().me()
        let r = StubProtocol.requests[0]
        XCTAssertEqual(r.method, "GET")
        XCTAssertEqual(r.url.absoluteString, "\(base)/api/v1/me")
        XCTAssertEqual(r.header("Authorization"), "Bearer acn_test")
        XCTAssertEqual(me.app, MeApp(id: 12, name: "crawler"))
        XCTAssertEqual(me.limits, AppLimits(dailyRequests: 1000, dailyBudget: 2.5))
        XCTAssertEqual(me.usage.todayRequests, 40)
        XCTAssertEqual(me.usage.monthCost, 3.4)
        XCTAssertNotNil(me.usage.lastUsedAt)
        XCTAssertNil(me.usage.limitHit)
        XCTAssertEqual(me.limitHit?.limit, "daily_requests")
        XCTAssertEqual(me.limitHit?.resetAt, ISO8601DateFormatter().date(from: "2026-11-02T00:00:00Z"))
        XCTAssertEqual(me.services.map(\.slug), ["serpapi"])
        XCTAssertEqual(me.services[0].available, 1)
        XCTAssertNil(me.services[0].logoURL)
        XCTAssertEqual(me.share?.status, "paused")
        XCTAssertEqual(me.share?.ownerName, "Ana")
        XCTAssertNil(me.share?.endsAt)
    }

    func testMeOrdinaryApp() async throws {
        StubProtocol.install { _ in
            StubResponse(body: #"{"app":{"id":1,"name":"a"},"limits":{},"usage":{},"limit_hit":null,"services":[],"share":null}"#)
        }
        let me = try await makeClient().me()
        XCTAssertNil(me.share)
        XCTAssertNil(me.limitHit)
        XCTAssertEqual(me.limits, AppLimits())
    }

    func testServicesGuideURL() async throws {
        StubProtocol.install { _ in
            StubResponse(body: #"{"services":[{"slug":"serpapi","name":"SerpAPI","available":true,"base_url":"https://serpapi.com","auth":{"type":"query","name":"api_key"},"guide_url":"http://acn.test/api/v1/services/serpapi/guide"}]}"#)
        }
        let svcs = try await makeClient().services()
        XCTAssertEqual(svcs[0].guideURL, "http://acn.test/api/v1/services/serpapi/guide")
    }

    func testGuide() async throws {
        StubProtocol.install { _ in
            StubResponse(body: ##"{"markdown":"# SerpAPI","generated":true,"guide":{"summary":"search","endpoints":[{"path":"/search.json"}]}}"##)
        }
        let g = try await makeClient().guide("serpapi")
        let r = StubProtocol.requests[0]
        XCTAssertEqual(r.method, "GET")
        XCTAssertEqual(r.url.absoluteString, "\(base)/api/v1/services/serpapi/guide")
        XCTAssertEqual(r.header("Accept"), "application/json")
        XCTAssertEqual(g.markdown, "# SerpAPI")
        XCTAssertTrue(g.generated)
        XCTAssertEqual(g.guide["summary"], "search")
        XCTAssertEqual(g.guide["endpoints"], [["path": "/search.json"]])
    }

    func testGuideMarkdown() async throws {
        StubProtocol.install { _ in
            StubResponse(headers: ["Content-Type": "text/markdown"], body: "# SerpAPI\n\nSearch.")
        }
        let md = try await makeClient().guideMarkdown("serpapi")
        let r = StubProtocol.requests[0]
        XCTAssertEqual(r.url.path, "/api/v1/services/serpapi/guide")
        XCTAssertEqual(r.header("Accept"), "text/markdown")
        XCTAssertEqual(r.header("Authorization"), "Bearer acn_test")
        XCTAssertEqual(md, "# SerpAPI\n\nSearch.")
    }

    func testGuideNotAllowed() async {
        StubProtocol.install { _ in
            StubResponse(status: 403, body: #"{"error":{"code":"service_not_allowed","message":"no"}}"#)
        }
        do {
            _ = try await makeClient().guideMarkdown("x")
            XCTFail("expected an error")
        } catch let e as AcnologiaError {
            XCTAssertEqual(e, .forbidden(code: "service_not_allowed", message: "no"))
        } catch { XCTFail("got \(error)") }
    }

    func testCapabilities() async throws {
        StubProtocol.install { _ in
            StubResponse(body: """
            {"capabilities":[{"slug":"places","name":"Find places","description":"Search businesses","strategy":"priority",
              "params":[{"name":"query","type":"string","required":true}],
              "routes":[{"service":"gmaps","mapped":true},{"service":"serpapi","mapped":false}],
              "url":"http://acn.test/proxy/places"}]}
            """)
        }
        let caps = try await makeClient().capabilities()
        XCTAssertEqual(StubProtocol.requests[0].url.absoluteString, "\(base)/api/v1/capabilities")
        XCTAssertEqual(caps.count, 1)
        XCTAssertEqual(caps[0].slug, "places")
        XCTAssertEqual(caps[0].strategy, "priority")
        XCTAssertEqual(caps[0].routes, [CapabilityRoute(service: "gmaps", mapped: true), CapabilityRoute(service: "serpapi", mapped: false)])
        XCTAssertEqual(caps[0].params, [["name": "query", "type": "string", "required": true]])
        XCTAssertEqual(caps[0].url, "http://acn.test/proxy/places")
    }

    func testCapabilityCall() async throws {
        StubProtocol.install { _ in
            StubResponse(headers: ["Content-Type": "application/json", "X-Acnologia-Capability": "places",
                                   "X-Acnologia-Service": "serpapi", "X-Acnologia-Tried": "gmaps=503, serpapi=200",
                                   "X-Acnologia-Key-Id": "4", "X-Acnologia-Attempts": "1"],
                         body: #"{"items":[],"count":0,"capability":"places","service":"serpapi"}"#)
        }
        let resp = try await makeClient().capability(
            "places", params: ["query": "coffee", "limit": 5],
            options: CapabilityOptions(service: "serpapi", raw: true, proxy: ProxyOptions(idempotencyKey: "k1", noCache: true)))
        let r = StubProtocol.requests[0]
        XCTAssertEqual(r.method, "POST")
        XCTAssertEqual(r.url.absoluteString, "\(base)/proxy/places?_service=serpapi&_raw=1")
        XCTAssertEqual(r.header("Content-Type"), "application/json")
        XCTAssertEqual(r.header("Authorization"), "Bearer acn_test")
        XCTAssertEqual(r.header("Idempotency-Key"), "k1")
        XCTAssertEqual(r.header("Cache-Control"), "no-cache")
        XCTAssertEqual(r.json["query"] as? String, "coffee")
        XCTAssertEqual(r.json["limit"] as? Int, 5)
        XCTAssertEqual(resp.capability, "places")
        XCTAssertEqual(resp.service, "serpapi")
        XCTAssertEqual(resp.tried, [TriedRoute(service: "gmaps", status: 503), TriedRoute(service: "serpapi", status: 200)])

        StubProtocol.install { _ in StubResponse(body: "{}") }
        struct Params: Encodable { let query: String }
        _ = try await makeClient().capability("places", params: Params(query: "tea"))
        let r2 = StubProtocol.requests[0]
        XCTAssertEqual(r2.url.absoluteString, "\(base)/proxy/places")
        XCTAssertEqual(r2.json["query"] as? String, "tea")
        XCTAssertNil(r2.header("Idempotency-Key"))
    }

    func testCapabilityBadParams() async throws {
        StubProtocol.install { _ in
            StubResponse(status: 400, body: #"{"error":{"code":"bad_params","message":"query is required"}}"#)
        }
        let resp = try await makeClient().capability("places")
        XCTAssertEqual(StubProtocol.requests[0].body, Data("{}".utf8))
        XCTAssertEqual(resp.acnologiaError, .api(statusCode: 400, code: "bad_params", message: "query is required"))
    }

    static let forwardJSON = """
    {"proxy_url":"http://acn.test:8888","proxy_url_with_token":"http://app:<app token>@acn.test:8888",
     "ca_url":"http://acn.test/api/v1/proxy/ca.pem","ca_fingerprint":"AB:CD","passthrough":true,
     "hosts":[{"host":"serpapi.com","service":"serpapi","name":"SerpAPI"},{"host":"tiktok-scraper.omkar.cloud","service":"omkar","name":"Omkar","path":"/~tiktok"}],
     "env":{"HTTPS_PROXY":"http://app:<app token>@acn.test:8888","NODE_USE_ENV_PROXY":"1"},
     "service_header":"X-Acnologia-Service"}
    """

    func testForwardProxy() async throws {
        StubProtocol.install { _ in StubResponse(body: Self.forwardJSON) }
        let fp = try await makeClient().forwardProxy()
        let r = StubProtocol.requests[0]
        XCTAssertEqual(r.method, "GET")
        XCTAssertEqual(r.url.absoluteString, "\(base)/api/v1/proxy")
        XCTAssertEqual(r.header("Authorization"), "Bearer acn_test")
        XCTAssertEqual(fp.proxyURL, "http://acn.test:8888")
        XCTAssertEqual(fp.caFingerprint, "AB:CD")
        XCTAssertTrue(fp.passthrough)
        XCTAssertEqual(fp.hosts[1], ForwardProxyHost(host: "tiktok-scraper.omkar.cloud", service: "omkar", name: "Omkar", path: "/~tiktok"))
        XCTAssertNil(fp.hosts[0].path)
        XCTAssertEqual(fp.env["NODE_USE_ENV_PROXY"], "1")
        XCTAssertEqual(fp.serviceHeader, "X-Acnologia-Service")
        var dumped = ""
        dump(fp, to: &dumped)
        for text in ["\(fp)", String(reflecting: fp), dumped] {
            XCTAssertFalse(text.contains("acn_test"), text)
        }
    }

    func testForwardProxyURLAndEnv() async throws {
        StubProtocol.install { _ in StubResponse(body: Self.forwardJSON) }
        let client = try Acnologia(baseURL: base, token: "acn_a+b/c", configuration: StubProtocol.configuration())
        let url = try await client.forwardProxyURL()
        XCTAssertEqual(url, "http://app:acn_a%2Bb%2Fc@acn.test:8888")
        let env = try await client.forwardProxyEnv(caPath: "/tmp/ca.pem")
        XCTAssertEqual(env, [
            "HTTPS_PROXY": url, "HTTP_PROXY": url, "NODE_USE_ENV_PROXY": "1",
            "NODE_EXTRA_CA_CERTS": "/tmp/ca.pem", "REQUESTS_CA_BUNDLE": "/tmp/ca.pem", "SSL_CERT_FILE": "/tmp/ca.pem",
        ])
        let def = try await client.forwardProxyEnv()
        XCTAssertEqual(def["SSL_CERT_FILE"], "acnologia-ca.pem")
        XCTAssertTrue(StubProtocol.requests.allSatisfy { $0.url.path == "/api/v1/proxy" })
    }

    func testForwardProxyCASendsNoToken() async throws {
        let pem = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"
        StubProtocol.install { _ in StubResponse(headers: ["Content-Type": "application/x-pem-file"], body: pem) }
        let client = try Acnologia(baseURL: base, token: token, configuration: StubProtocol.configuration(), accessPassword: "door")
        let got = try await client.forwardProxyCA()
        XCTAssertEqual(got, pem)
        let r = StubProtocol.requests[0]
        XCTAssertEqual(r.method, "GET")
        XCTAssertEqual(r.url.absoluteString, "\(base)/api/v1/proxy/ca.pem")
        XCTAssertNil(r.header("Authorization"))
        XCTAssertEqual(r.header("X-Acnologia-Access"), "door")

        let path = FileManager.default.temporaryDirectory.appendingPathComponent("acn-ca-\(UUID().uuidString).pem").path
        defer { try? FileManager.default.removeItem(atPath: path) }
        let written = try await client.forwardProxyCA(saveTo: path)
        XCTAssertEqual(try String(contentsOf: written, encoding: .utf8), pem)
    }

    func testAccessPassword() async throws {
        StubProtocol.install { _ in StubResponse(body: #"{"services":[]}"#) }
        let c = try Acnologia.fromEnvironment(["ACNOLOGIA_URL": base, "ACNOLOGIA_TOKEN": token, "ACNOLOGIA_ACCESS_PASSWORD": "door"],
                                              configuration: StubProtocol.configuration())
        _ = try await c.services()
        _ = try await c.proxy("serpapi").get("/x")
        XCTAssertEqual(StubProtocol.requests.map { $0.header("X-Acnologia-Access") }, ["door", "door"])
        for text in [c.description, String(reflecting: c)] {
            XCTAssertFalse(text.contains("door"), text)
        }
        var dumped = ""
        dump(c, to: &dumped)
        XCTAssertFalse(dumped.contains("door"))

        StubProtocol.install { _ in StubResponse(body: #"{"services":[]}"#) }
        _ = try await makeClient().services()
        XCTAssertNil(StubProtocol.requests[0].header("X-Acnologia-Access"))
    }

    func testShareUnavailableFromHeader() async throws {
        StubProtocol.install { _ in
            StubResponse(status: 403, headers: ["Content-Type": "application/json", "X-Acnologia-Share": "revoked"],
                         body: #"{"error":{"code":"share_revoked","message":"the owner revoked this share"}}"#)
        }
        let resp = try await makeClient().proxy("serpapi").get("/search.json")
        let e = try XCTUnwrap(resp.acnologiaError)
        XCTAssertEqual(e, .shareUnavailable(status: "revoked", message: "the owner revoked this share"))
        XCTAssertEqual(e.code, "share_revoked")
        XCTAssertEqual(e.statusCode, 403)
    }

    func testAppLimitReachedRetryAfterHeader() async {
        StubProtocol.install { _ in
            StubResponse(status: 429, headers: ["Content-Type": "application/json", "Retry-After": "120"],
                         body: #"{"error":{"code":"app_limit_reached","message":"this app reached its monthly budget limit"},"limit":"monthly_budget","reset_at":"2026-12-01T00:00:00Z"}"#)
        }
        do {
            _ = try await makeClient().me()
            XCTFail("expected an error")
        } catch let e as AcnologiaError {
            guard case let .appLimitReached(limit, resetAt, retryAfter, _) = e else { return XCTFail("got \(e)") }
            XCTAssertEqual(limit, "monthly_budget")
            XCTAssertNotNil(resetAt)
            XCTAssertEqual(retryAfter, 120)
            XCTAssertEqual(e.retryAfter, 120)
            XCTAssertEqual(e.statusCode, 429)
            XCTAssertEqual(e.code, "app_limit_reached")
        } catch { XCTFail("got \(error)") }
    }

    func testParseTried() {
        XCTAssertEqual(parseTried("a=503, b=200"), [TriedRoute(service: "a", status: 503), TriedRoute(service: "b", status: 200)])
        XCTAssertEqual(parseTried(nil), [])
        XCTAssertEqual(parseTried("junk, c=429"), [TriedRoute(service: "c", status: 429)])
    }
}
