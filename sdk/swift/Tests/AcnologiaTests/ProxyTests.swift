import XCTest
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif
@testable import Acnologia

final class ProxyTests: XCTestCase {
    func testGetRoutesUnderProxyPrefix() async throws {
        StubProtocol.install { _ in
            StubResponse(status: 200, headers: ["Content-Type": "application/json", "X-Acnologia-Key-Id": "7",
                                                "X-Acnologia-Attempts": "2", "X-Acnologia-Service": "serpapi"],
                         body: #"{"ok":1}"#)
        }
        let resp = try await makeClient().proxy("serpapi").get("/search.json", query: [URLQueryItem(name: "q", value: "coffee")])
        XCTAssertEqual(resp.statusCode, 200)
        XCTAssertEqual(resp.keyID, 7)
        XCTAssertEqual(resp.attempts, 2)
        XCTAssertEqual(resp.servedBy, "serpapi")
        XCTAssertEqual(try resp.decode([String: Int].self), ["ok": 1])
        let r = StubProtocol.requests[0]
        XCTAssertEqual(r.url.absoluteString, "\(base)/proxy/serpapi/search.json?q=coffee")
        XCTAssertEqual(r.header("Authorization"), "Bearer acn_test")
        XCTAssertEqual(r.header("User-Agent"), "acnologia-swift/\(Acnologia.version)")
    }

    func testPostJSON() async throws {
        StubProtocol.install { _ in StubResponse(status: 200, headers: ["X-Acnologia-Key-Id": "1"], body: "audio") }
        let resp = try await makeClient().proxy("elevenlabs").postJSON("v1/text-to-speech/abc", ["text": "hi"])
        XCTAssertEqual(resp.text, "audio")
        let r = StubProtocol.requests[0]
        XCTAssertEqual(r.method, "POST")
        XCTAssertEqual(r.url.path, "/proxy/elevenlabs/v1/text-to-speech/abc")
        XCTAssertEqual(r.header("Content-Type"), "application/json")
        XCTAssertEqual(r.json["text"] as? String, "hi")
    }

    func testSendRelativeAndPrefixedRequests() async throws {
        StubProtocol.install { _ in StubResponse(status: 200, body: "{}") }
        let proxy = makeClient().proxy("elevenlabs")
        var req = URLRequest(url: URL(string: "/v1/voices?page=2")!)
        req.httpMethod = "GET"
        _ = try await proxy.send(req)
        _ = try await proxy.send(URLRequest(url: URL(string: "http://elsewhere/proxy/elevenlabs/v1/models")!))
        XCTAssertEqual(StubProtocol.requests[0].url.absoluteString, "\(base)/proxy/elevenlabs/v1/voices?page=2")
        XCTAssertEqual(StubProtocol.requests[1].url.absoluteString, "\(base)/proxy/elevenlabs/v1/models")
    }

    func testUpstreamErrorsAreReturnedNotThrown() async throws {
        StubProtocol.install { _ in
            StubResponse(status: 429, headers: ["X-Acnologia-Key-Id": "3"], body: #"{"error":{"code":"rate","message":"upstream"}}"#)
        }
        let resp = try await makeClient().proxy("serpapi").get("/search.json")
        XCTAssertEqual(resp.statusCode, 429)
        XCTAssertNil(resp.acnologiaError, "a provider answer is not an Acnologia error")
    }

    func testAcnologiaErrorOnProxyResponse() async throws {
        StubProtocol.install { _ in
            StubResponse(status: 503, headers: ["Retry-After": "30"],
                         body: #"{"error":{"code":"service_unavailable","message":"serpapi keeps failing, so Acnologia paused traffic to it"},"retry_after_sec":30}"#)
        }
        let resp = try await makeClient().proxy("serpapi").get("/search.json")
        XCTAssertEqual(resp.acnologiaError, .serviceUnavailable(retryAfter: 30, message: "serpapi keeps failing, so Acnologia paused traffic to it"))
        XCTAssertThrowsError(try resp.throwingAcnologiaError())
    }

    func testProxyOptionsAndAccessors() async throws {
        StubProtocol.install { _ in
            StubResponse(status: 200, headers: ["Content-Type": "application/json", "X-Acnologia-Key-Id": "9",
                                                "X-Acnologia-Attempts": "3", "X-Acnologia-Service": "serpapi",
                                                "X-Acnologia-Cache": "hit", "Idempotent-Replayed": "true"],
                         body: "{}")
        }
        let proxy = makeClient().proxy("serpapi")
        let resp = try await proxy.postJSON("/charge", ["a": 1], options: ProxyOptions(idempotencyKey: "order-42", noCache: true))
        let r = StubProtocol.requests[0]
        XCTAssertEqual(r.header("Idempotency-Key"), "order-42")
        XCTAssertEqual(r.header("Cache-Control"), "no-cache")
        XCTAssertEqual(resp.keyID, 9)
        XCTAssertEqual(resp.attempts, 3)
        XCTAssertEqual(resp.service, "serpapi")
        XCTAssertEqual(resp.cache, "hit")
        XCTAssertTrue(resp.replayed)
        XCTAssertNil(resp.capability)
        XCTAssertEqual(resp.tried, [])

        _ = try await proxy.get("/search.json")
        let r2 = StubProtocol.requests[1]
        XCTAssertNil(r2.header("Idempotency-Key"))
        XCTAssertNil(r2.header("Cache-Control"))

        _ = try await proxy.send(URLRequest(url: URL(string: "/v1/x")!), options: ProxyOptions(noCache: true))
        XCTAssertEqual(StubProtocol.requests[2].header("Cache-Control"), "no-cache")
    }
}
