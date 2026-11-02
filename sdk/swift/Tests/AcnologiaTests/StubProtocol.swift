import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif
@testable import Acnologia

/// A request as the stub saw it, with the body read from the stream.
struct Recorded {
    let method: String
    let url: URL
    let headers: [String: String]
    let body: Data

    var json: [String: Any] {
        (try? JSONSerialization.jsonObject(with: body)) as? [String: Any] ?? [:]
    }

    func header(_ name: String) -> String? {
        headers.first { $0.key.lowercased() == name.lowercased() }?.value
    }
}

struct StubResponse {
    var status: Int = 200
    var headers: [String: String] = ["Content-Type": "application/json"]
    var body: String = "{}"
}

/// Replays canned responses for every request made through a session configured with it.
final class StubProtocol: URLProtocol {
    private static let lock = NSLock()
    private static var handler: ((Recorded) -> StubResponse)?
    private static var log: [Recorded] = []

    static func install(_ h: @escaping (Recorded) -> StubResponse) {
        lock.lock(); defer { lock.unlock() }
        handler = h
        log = []
    }

    static var requests: [Recorded] {
        lock.lock(); defer { lock.unlock() }
        return log
    }

    static func configuration() -> URLSessionConfiguration {
        let c = URLSessionConfiguration.ephemeral
        c.protocolClasses = [StubProtocol.self]
        return c
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var body = request.httpBody ?? Data()
        if body.isEmpty, let stream = request.httpBodyStream {
            stream.open()
            var buf = [UInt8](repeating: 0, count: 4096)
            while stream.hasBytesAvailable {
                let n = stream.read(&buf, maxLength: buf.count)
                if n <= 0 { break }
                body.append(buf, count: n)
            }
            stream.close()
        }
        let rec = Recorded(method: request.httpMethod ?? "GET", url: request.url!,
                           headers: request.allHTTPHeaderFields ?? [:], body: body)
        StubProtocol.lock.lock()
        StubProtocol.log.append(rec)
        let h = StubProtocol.handler
        StubProtocol.lock.unlock()
        let out = h?(rec) ?? StubResponse(status: 500, body: "no stub")
        let resp = HTTPURLResponse(url: request.url!, statusCode: out.status, httpVersion: "HTTP/1.1", headerFields: out.headers)!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(out.body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

let base = "http://acn.test"
let token = "acn_test"

func makeClient() -> Acnologia {
    try! Acnologia(baseURL: base, token: token, configuration: StubProtocol.configuration())
}

func leaseJSON(_ id: Int64, auth: String = #"{"type":"query","name":"api_key"}"#, secret: String? = nil) -> String {
    """
    {"lease_id":"lse_\(id)","key_id":\(id),"service":"serpapi","secret":"\(secret ?? "sk\(id)")",\
    "auth":\(auth),"base_url":"https://serpapi.com","expires_at":"2026-11-01T12:00:00.123456789Z","exclusive":false}
    """
}

/// A fake pool that hands out keys in order and records reports, like the server does.
final class FakePool: @unchecked Sendable {
    var keys: [Int64]
    var next = 0
    var busy = false
    var rotateReturnsNext = true
    private let lock = NSLock()

    init(keys: [Int64]) { self.keys = keys }

    func handle(_ r: Recorded) -> StubResponse {
        lock.lock(); defer { lock.unlock() }
        guard r.header("Authorization") == "Bearer \(token)" else {
            return StubResponse(status: 401, body: #"{"error":{"code":"unauthenticated","message":"invalid app token"}}"#)
        }
        let path = r.url.path
        if path == "/api/v1/lease" {
            if busy {
                return StubResponse(status: 503, headers: ["Retry-After": "1"],
                                    body: #"{"error":{"code":"all_keys_busy","message":"every usable key is at its concurrency limit, retry shortly"},"retry_after_sec":1}"#)
            }
            return issue(excluding: (r.json["exclude_key_ids"] as? [NSNumber])?.map { $0.int64Value } ?? [])
        }
        if path.hasSuffix("/report") {
            let outcome = r.json["outcome"] as? String ?? ""
            let rotate = r.json["rotate"] as? Bool ?? false
            if rotate && outcome != "ok" && rotateReturnsNext {
                let n = issue(excluding: [])
                if n.status == 200 { return StubResponse(body: #"{"ok":true,"next":"# + n.body + "}") }
            }
            return StubResponse(body: #"{"ok":true,"next":null}"#)
        }
        return StubResponse(status: 404, body: #"{"error":{"code":"not_found","message":"not found"}}"#)
    }

    private func issue(excluding: [Int64]) -> StubResponse {
        while next < keys.count, excluding.contains(keys[next]) { next += 1 }
        guard next < keys.count else {
            return StubResponse(status: 503, headers: ["Retry-After": "42"],
                                body: #"{"error":{"code":"no_key_available","message":"every key for this service is cooling down, exhausted or disabled"},"retry_after_sec":42}"#)
        }
        let id = keys[next]
        next += 1
        return StubResponse(body: leaseJSON(id))
    }
}
