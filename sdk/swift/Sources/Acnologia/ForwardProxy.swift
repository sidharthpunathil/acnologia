import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

extension Acnologia {
    /// The default file name for the forward proxy's CA certificate.
    public static let defaultCAPath = "acnologia-ca.pem"

    /// The default file name for the system roots plus the forward proxy's CA certificate.
    public static let defaultCABundlePath = "acnologia-ca-bundle.pem"

    /// Hosts that stay off the forward proxy, which refuses hosts that are no service.
    public static let noProxy = "localhost,127.0.0.1,::1"

    static let systemRootPaths = [
        "/etc/ssl/certs/ca-certificates.crt",
        "/etc/pki/tls/certs/ca-bundle.crt",
        "/etc/ssl/ca-bundle.pem",
        "/etc/ssl/cert.pem",
    ]

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

    /// Downloads the forward proxy's CA certificate and writes the system roots followed by it
    /// to `path`. `SSL_CERT_FILE` and `REQUESTS_CA_BUNDLE` replace the whole trust store, so
    /// they need this bundle, not the CA alone. The roots come from `$SSL_CERT_FILE` (when it
    /// is a PEM bundle other than `path`) or the usual system bundles. Throws
    /// ``AcnologiaError/invalidArgument(_:)`` when there is none.
    ///
    /// - Returns: The file URL written.
    @discardableResult
    public func forwardProxyCABundle(saveTo path: String = Acnologia.defaultCABundlePath) async throws -> URL {
        let target = URL(fileURLWithPath: path)
        guard let roots = Acnologia.systemRoots(target: target, envCertFile: ProcessInfo.processInfo.environment["SSL_CERT_FILE"]) else {
            throw AcnologiaError.invalidArgument(
                "no system root certificates found. SSL_CERT_FILE and REQUESTS_CA_BUNDLE need a bundle "
                    + "of the system roots plus the Acnologia CA; NODE_EXTRA_CA_CERTS takes the CA alone")
        }
        let pem = try await forwardProxyCA()
        try Data(Acnologia.caBundle(roots: roots, ca: pem).utf8).write(to: target, options: .atomic)
        return target
    }

    /// The first system root bundle found (`envCertFile` first), skipping `target`.
    static func systemRoots(target: URL, envCertFile: String?) -> String? {
        #if os(Windows)
        return nil
        #else
        var paths = systemRootPaths
        if let env = envCertFile, !env.isEmpty { paths.insert(env, at: 0) }
        let targetPath = target.standardizedFileURL.resolvingSymlinksInPath().path
        for p in paths {
            let url = URL(fileURLWithPath: p)
            if url.standardizedFileURL.resolvingSymlinksInPath().path == targetPath { continue }
            guard let data = try? Data(contentsOf: url) else { continue }
            let text = String(decoding: data, as: UTF8.self)
            if text.contains("BEGIN CERTIFICATE") { return text }
        }
        return nil
        #endif
    }

    /// The system roots followed by the Acnologia CA.
    static func caBundle(roots: String, ca: String) -> String {
        func trim(_ s: String) -> String {
            var s = s
            while s.hasSuffix("\n") { s.removeLast() }
            return s
        }
        return trim(roots) + "\n\n# Acnologia forward proxy CA\n" + trim(ca) + "\n"
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
    /// `HTTPS_PROXY` and `HTTP_PROXY` (``forwardProxyURL()``), `NODE_USE_ENV_PROXY=1`,
    /// `NODE_EXTRA_CA_CERTS` set to `caPath`, `REQUESTS_CA_BUNDLE` and `SSL_CERT_FILE` set to
    /// `bundlePath` (the system roots plus the CA, see ``forwardProxyCABundle(saveTo:)``), and
    /// `NO_PROXY` and `no_proxy` set to ``noProxy``.
    ///
    /// The proxy variables contain the app token: do not log them.
    public func forwardProxyEnv(
        caPath: String = Acnologia.defaultCAPath,
        bundlePath: String = Acnologia.defaultCABundlePath
    ) async throws -> [String: String] {
        let url = try await forwardProxyURL()
        return [
            "HTTPS_PROXY": url,
            "HTTP_PROXY": url,
            "NODE_USE_ENV_PROXY": "1",
            "NODE_EXTRA_CA_CERTS": caPath,
            "REQUESTS_CA_BUNDLE": bundlePath,
            "SSL_CERT_FILE": bundlePath,
            "NO_PROXY": Acnologia.noProxy,
            "no_proxy": Acnologia.noProxy,
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
