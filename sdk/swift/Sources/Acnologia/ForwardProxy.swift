import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

extension Acnologia {
    /// The default file name for the forward proxy's CA certificate.
    public static let defaultCAPath = "acnologia-ca.pem"

    /// How to use the forward proxy: its address, CA, the hosts it routes and the environment
    /// an app needs. The answer never holds the token (it has an `<app token>` placeholder).
    public func forwardProxy() async throws -> ForwardProxy {
        try await call("GET", "/api/v1/proxy", body: Optional<LeaseRequest>.none)
    }

    /// The forward proxy's CA certificate as PEM text. The route is public, so no token is sent.
    public func forwardProxyCA() async throws -> String {
        let data = try await raw("GET", "/api/v1/proxy/ca.pem", accept: "application/x-pem-file, */*", withToken: false)
        return String(decoding: data, as: UTF8.self)
    }

    /// Downloads the forward proxy's CA certificate and writes it to `path`.
    ///
    /// - Returns: The file URL written.
    @discardableResult
    public func forwardProxyCA(saveTo path: String = Acnologia.defaultCAPath) async throws -> URL {
        let pem = try await forwardProxyCA()
        let url = URL(fileURLWithPath: path)
        try Data(pem.utf8).write(to: url, options: .atomic)
        return url
    }

    /// The forward proxy URL with user `app` and this client's token as the password,
    /// percent-escaped, such as `http://app:acn_...@host:8888`.
    ///
    /// The result contains the app token: do not log it.
    public func forwardProxyURL() async throws -> String {
        let fp = try await forwardProxy()
        return try forwardProxyURL(from: fp)
    }

    /// The environment an app needs to send its own HTTP calls through the forward proxy:
    /// `HTTPS_PROXY` and `HTTP_PROXY` (``forwardProxyURL()``), `NODE_USE_ENV_PROXY=1`, and
    /// `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE` and `SSL_CERT_FILE` set to `caPath`.
    ///
    /// The proxy variables contain the app token: do not log them.
    public func forwardProxyEnv(caPath: String = Acnologia.defaultCAPath) async throws -> [String: String] {
        let url = try await forwardProxyURL()
        return [
            "HTTPS_PROXY": url,
            "HTTP_PROXY": url,
            "NODE_USE_ENV_PROXY": "1",
            "NODE_EXTRA_CA_CERTS": caPath,
            "REQUESTS_CA_BUNDLE": caPath,
            "SSL_CERT_FILE": caPath,
        ]
    }

    func forwardProxyURL(from fp: ForwardProxy) throws -> String {
        guard var comps = URLComponents(string: fp.proxyURL), comps.host != nil else {
            throw AcnologiaError.decoding("forward proxy answer has no proxy_url")
        }
        comps.percentEncodedUser = "app"
        comps.percentEncodedPassword = strictEscape(token)
        guard let s = comps.string else {
            throw AcnologiaError.decoding("bad proxy_url \(fp.proxyURL)")
        }
        return s
    }
}
