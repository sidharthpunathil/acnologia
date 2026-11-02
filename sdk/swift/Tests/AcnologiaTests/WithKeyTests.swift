import XCTest
@testable import Acnologia

private struct Boom: Error, CustomStringConvertible {
    var description: String { "upstream failed for https://serpapi.com/search?api_key=sk1" }
}

final class WithKeyTests: XCTestCase {
    private func reports() -> [Recorded] {
        StubProtocol.requests.filter { $0.url.path.hasSuffix("/report") }
    }

    func testSuccessReportsOK() async throws {
        let pool = FakePool(keys: [1])
        StubProtocol.install(pool.handle)
        let value = try await makeClient().withKey("serpapi") { lease in "used \(lease.keyID)" }
        XCTAssertEqual(value, "used 1")
        XCTAssertEqual(reports().count, 1)
        XCTAssertEqual(reports()[0].json["outcome"] as? String, "ok")
    }

    func testRotatesOnRateLimit() async throws {
        let pool = FakePool(keys: [1, 2, 3])
        StubProtocol.install(pool.handle)
        var seen: [Int64] = []
        let value = try await makeClient().withKey("serpapi") { lease -> Int in
            seen.append(lease.keyID)
            switch lease.keyID {
            case 1: throw AcnologiaError.rateLimited(retryAfter: 60, statusCode: 429, message: "key sk1 limited")
            case 2: throw AcnologiaError.quotaExhausted(statusCode: 429)
            default: return 42
            }
        }
        XCTAssertEqual(value, 42)
        XCTAssertEqual(seen, [1, 2, 3])
        let r = reports()
        XCTAssertEqual(r.map { $0.json["outcome"] as? String }, ["rate_limited", "quota_exhausted", "ok"])
        XCTAssertEqual(r[0].json["rotate"] as? Bool, true)
        XCTAssertEqual(r[0].json["retry_after_sec"] as? Int, 60)
        XCTAssertEqual(r[0].json["message"] as? String, "key [key] limited")
    }

    func testLeasesAgainWhenReportHasNoNext() async throws {
        let pool = FakePool(keys: [1, 2])
        pool.rotateReturnsNext = false
        StubProtocol.install(pool.handle)
        var seen: [Int64] = []
        try await makeClient().withKey("serpapi", options: LeaseOptions(excludeKeyIDs: [9], ttl: 30)) { lease in
            seen.append(lease.keyID)
            if lease.keyID == 1 { throw AcnologiaError.invalidKey(statusCode: 401) }
        }
        XCTAssertEqual(seen, [1, 2])
        let leases = StubProtocol.requests.filter { $0.url.path == "/api/v1/lease" }
        XCTAssertEqual(leases.count, 2)
        XCTAssertEqual(leases[0].json["exclude_key_ids"] as? [Int], [9])
        XCTAssertEqual(leases[1].json["exclude_key_ids"] as? [Int], [9, 1])
        XCTAssertEqual(leases[1].json["ttl_sec"] as? Int, 30)
        XCTAssertEqual(reports()[0].json["outcome"] as? String, "invalid")
    }

    func testGivesUpAfterMaxAttempts() async {
        let pool = FakePool(keys: [1, 2, 3, 4])
        StubProtocol.install(pool.handle)
        var calls = 0
        do {
            try await makeClient().withKey("serpapi", maxAttempts: 2) { _ in
                calls += 1
                throw AcnologiaError.rateLimited(retryAfter: 5)
            }
            XCTFail("expected an error")
        } catch let e as AcnologiaError {
            XCTAssertEqual(e, .rateLimited(retryAfter: 5))
        } catch { XCTFail("got \(error)") }
        XCTAssertEqual(calls, 2)
        let r = reports()
        XCTAssertEqual(r.count, 2)
        XCTAssertEqual(r[0].json["rotate"] as? Bool, true)
        XCTAssertNil(r[1].json["rotate"], "the final attempt must not ask for a next key")
    }

    func testOtherErrorsReportErrorAndRethrow() async {
        let pool = FakePool(keys: [1, 2])
        StubProtocol.install(pool.handle)
        do {
            try await makeClient().withKey("serpapi") { _ in throw Boom() }
            XCTFail("expected an error")
        } catch is Boom {
        } catch { XCTFail("got \(error)") }
        let r = reports()
        XCTAssertEqual(r.count, 1)
        XCTAssertEqual(r[0].json["outcome"] as? String, "error")
        XCTAssertEqual(r[0].json["message"] as? String, "upstream failed for https://serpapi.com/search?api_key=[key]")
        XCTAssertNil(r[0].json["rotate"])
    }

    func testNoKeyAvailableWhenPoolRunsDry() async {
        let pool = FakePool(keys: [1])
        StubProtocol.install(pool.handle)
        do {
            try await makeClient().withKey("serpapi") { _ in throw AcnologiaError.invalidKey() }
            XCTFail("expected an error")
        } catch let e as AcnologiaError {
            XCTAssertEqual(e, .noKeyAvailable(retryAfter: 42, message: "every key for this service is cooling down, exhausted or disabled"))
        } catch { XCTFail("got \(error)") }
    }

    func testAllKeysBusyOnFirstLease() async {
        let pool = FakePool(keys: [1])
        pool.busy = true
        StubProtocol.install(pool.handle)
        do {
            try await makeClient().withKey("serpapi", options: LeaseOptions(wait: 5)) { _ in }
            XCTFail("expected an error")
        } catch let e as AcnologiaError {
            guard case .allKeysBusy(let retry, _) = e else { return XCTFail("got \(e)") }
            XCTAssertEqual(retry, 1)
            XCTAssertTrue(e.isNoKeyAvailable)
        } catch { XCTFail("got \(error)") }
        XCTAssertEqual(StubProtocol.requests[0].json["wait_sec"] as? Int, 5)
    }

    func testCancelledTaskStillReports() async throws {
        let pool = FakePool(keys: [1, 2])
        StubProtocol.install(pool.handle)
        let client = makeClient()
        let task = Task {
            try await client.withKey("serpapi") { _ in
                withUnsafeCurrentTask { $0?.cancel() }
                throw AcnologiaError.rateLimited(retryAfter: 1)
            }
        }
        do {
            try await task.value
            XCTFail("expected cancellation")
        } catch is CancellationError {
        } catch { XCTFail("got \(error)") }
        let r = reports()
        XCTAssertEqual(r.count, 1)
        XCTAssertEqual(r[0].json["outcome"] as? String, "rate_limited")
        XCTAssertNil(r[0].json["rotate"])
    }
}
