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
}
