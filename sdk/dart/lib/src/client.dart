part of '../acnologia.dart';

/// A client for an Acnologia key pool.
///
/// ```dart
/// final acn = Acnologia(baseUrl: 'http://localhost:8080', token: 'acn_...');
/// final lease = await acn.lease('serpapi');
/// ```
///
/// The app token (`acn_...`, or the older `cog_...`) never appears in
/// [toString].
class Acnologia {
  /// Creates a client.
  ///
  /// [baseUrl] is the Acnologia server, for example `http://localhost:8080`,
  /// and [token] the app token. Pass [httpClient] to reuse a client or to test
  /// with `MockClient`; otherwise the SDK creates one and [close] closes it.
  /// [timeout] bounds API calls (lease, report, renew, services), not proxy
  /// calls; it is longer than [maxWait] so a waiting lease can still succeed.
  /// [accessPassword] is sent as `X-Acnologia-Access` on every request, for
  /// a server behind the access password.
  Acnologia({
    required String baseUrl,
    required String token,
    http.Client? httpClient,
    String? userAgent,
    this.timeout = defaultTimeout,
    String? accessPassword,
  })  : baseUrl = _trimSlash(baseUrl),
        _token = token,
        _accessPassword = accessPassword ?? '',
        _http = httpClient ?? http.Client(),
        _ownsHttp = httpClient == null,
        userAgent = userAgent ?? defaultUserAgent {
    if (baseUrl.isEmpty || !Uri.parse(baseUrl).hasScheme) {
      throw ArgumentError.value(baseUrl, 'baseUrl', 'must be an absolute URL such as http://localhost:8080');
    }
    if (token.isEmpty) throw ArgumentError.value(token, 'token', 'is required');
  }

  /// Creates a client from the `ACNOLOGIA_URL` and `ACNOLOGIA_TOKEN`
  /// environment variables, and `ACNOLOGIA_ACCESS_PASSWORD` when it is set
  /// (an [accessPassword] argument wins over it).
  ///
  /// [environment] defaults to the process environment, then to values given
  /// at compile time with `--dart-define` (the only option on the web).
  /// Throws [ArgumentError] when either is missing.
  factory Acnologia.fromEnvironment({
    Map<String, String>? environment,
    http.Client? httpClient,
    String? userAgent,
    Duration timeout = defaultTimeout,
    String? accessPassword,
  }) {
    final e = environment ?? env.environment();
    const defUrl = String.fromEnvironment('ACNOLOGIA_URL');
    const defToken = String.fromEnvironment('ACNOLOGIA_TOKEN');
    const defAccess = String.fromEnvironment('ACNOLOGIA_ACCESS_PASSWORD');
    final url = e['ACNOLOGIA_URL'] ?? (environment == null ? defUrl : '');
    final token = e['ACNOLOGIA_TOKEN'] ?? (environment == null ? defToken : '');
    final access = accessPassword ?? e['ACNOLOGIA_ACCESS_PASSWORD'] ?? (environment == null ? defAccess : '');
    if (url.isEmpty) throw ArgumentError('ACNOLOGIA_URL is not set');
    if (token.isEmpty) throw ArgumentError('ACNOLOGIA_TOKEN is not set');
    return Acnologia(
        baseUrl: url,
        token: token,
        httpClient: httpClient,
        userAgent: userAgent,
        timeout: timeout,
        accessPassword: access);
  }

  /// The SDK version.
  static const String version = '0.2.0';

  /// The default `User-Agent`.
  static const String defaultUserAgent = 'acnologia-dart/$version';

  /// The default timeout for API calls.
  static const Duration defaultTimeout = Duration(seconds: 75);

  /// The longest `wait` the server accepts for a lease.
  static const Duration maxWait = Duration(seconds: 60);

  /// The Acnologia server, without a trailing slash.
  final String baseUrl;

  /// The `User-Agent` sent with every request (not on the web, where browsers
  /// refuse it).
  final String userAgent;

  /// Timeout for API calls.
  final Duration timeout;

  final String _token;
  final String _accessPassword;
  final http.Client _http;
  final bool _ownsHttp;

  /// Closes the HTTP client if this instance created it.
  void close() {
    if (_ownsHttp) _http.close();
  }

  @override
  String toString() => 'Acnologia(baseUrl: $baseUrl, token: [redacted])';

  /// Returns what this token may do: the app, its limits and use, the
  /// services it may call and, for a share's token, the share
  /// (`GET /api/v1/me`). A share token whose share stopped still gets an
  /// answer, with [ShareInfo.status] saying so.
  Future<Me> me() async => Me.fromJson(await _json('GET', '/api/v1/me'));

  /// Lists the services this app token may use.
  Future<List<Service>> services() async {
    final data = await _json('GET', '/api/v1/services');
    final list = data['services'];
    if (list is! List) return const [];
    return [for (final s in list) if (s is Map<String, dynamic>) Service.fromJson(s)];
  }

  /// Leases a key from the pool.
  ///
  /// [excludeKeyIds] are keys that must not be returned. [ttl] is the lease
  /// lifetime (the server default is 120 seconds on services with a
  /// concurrency limit, 300 otherwise). [wait], from zero to [maxWait], lets
  /// the server wait for a free slot when every usable key is busy. Set
  /// [exclusive] to keep the key from every other caller until you report.
  ///
  /// Always report the lease: on services with a concurrency limit it holds
  /// one of the key's slots until it is reported or its TTL expires.
  ///
  /// Throws [NoKeyAvailableException] when the pool is empty or cooling down,
  /// [AllKeysBusyException] when every usable key is at its concurrency limit
  /// and [wait] ran out, and other [AcnologiaException]s for other failures.
  Future<Lease> lease(
    String service, {
    List<int> excludeKeyIds = const [],
    Duration? ttl,
    Duration? wait,
    bool exclusive = false,
  }) async {
    if (wait != null && (wait.isNegative || wait > maxWait)) {
      throw RangeError('lease wait must be between 0 and 60 seconds');
    }
    final body = <String, dynamic>{
      'service': service,
      if (excludeKeyIds.isNotEmpty) 'exclude_key_ids': excludeKeyIds,
      if (ttl != null && ttl > Duration.zero) 'ttl_sec': _ceilSeconds(ttl),
      if (wait != null && wait > Duration.zero) 'wait_sec': _ceilSeconds(wait),
      if (exclusive) 'exclusive': true,
    };
    final data = await _json('POST', '/api/v1/lease', body);
    return Lease._fromJson(this, data);
  }

  /// Returns a helper that sends requests through `/proxy/{service}`.
  Proxy proxy(String service) => Proxy._(this, service);

  /// Returns the usage guide of a service the token may use
  /// (`GET /api/v1/services/{slug}/guide`).
  Future<Guide> guide(String slug) async =>
      Guide.fromJson(await _json('GET', '/api/v1/services/${Uri.encodeComponent(slug)}/guide'));

  /// Returns the usage guide of a service as Markdown.
  Future<String> guideMarkdown(String slug) async =>
      (await _send('GET', '/api/v1/services/${Uri.encodeComponent(slug)}/guide', accept: 'text/markdown')).bodyText;

  /// Lists the capabilities this app may call (`GET /api/v1/capabilities`).
  /// Empty for a share's token.
  Future<List<Capability>> capabilities() async {
    final list = (await _json('GET', '/api/v1/capabilities'))['capabilities'];
    if (list is! List) return const [];
    return [for (final c in list) if (c is Map<String, dynamic>) Capability.fromJson(c)];
  }

  /// Calls a capability: `POST /proxy/{slug}` with [params] as the JSON body.
  ///
  /// Acnologia picks a provider, translates the params, rotates keys and
  /// moves to the next provider on failure. [service] pins one provider
  /// (`?_service=`), and [raw] asks for the provider's own body (`?_raw=1`).
  /// The answer comes back like a [Proxy] response: read
  /// [AcnologiaProxyResponse.acnologiaService],
  /// [AcnologiaProxyResponse.acnologiaCapability] and
  /// [AcnologiaProxyResponse.acnologiaTried] on it, and
  /// [AcnologiaProxyError.acnologiaError] for a refusal such as `bad_params`.
  Future<http.Response> capability(
    String slug,
    Map<String, Object?> params, {
    String? service,
    bool raw = false,
    Map<String, String>? headers,
    String? idempotencyKey,
    bool noCache = false,
  }) async {
    final query = <String, Object?>{
      if (service != null && service.isNotEmpty) '_service': service,
      if (raw) '_raw': '1',
    };
    final req = http.Request('POST', _withQuery(Uri.parse('$baseUrl/proxy/${Uri.encodeComponent(slug)}'), query))
      ..followRedirects = false
      ..headers.addAll(headers ?? const {})
      ..headers['Content-Type'] = 'application/json'
      ..headers.addAll(_proxyOptionHeaders(idempotencyKey, noCache))
      ..headers.addAll(_authHeaders())
      ..body = jsonEncode(params);
    return http.Response.fromStream(await _http.send(req));
  }

  /// Returns how to use the forward proxy (`GET /api/v1/proxy`): its URL, the
  /// CA to trust, the provider hosts it answers for and the environment to
  /// set. Nothing in it holds the token.
  Future<ForwardProxy> forwardProxy() async => ForwardProxy.fromJson(await _json('GET', '/api/v1/proxy'));

  /// Downloads the forward proxy's CA certificate as PEM text
  /// (`GET /api/v1/proxy/ca.pem`). Public: the app token is not sent.
  Future<String> forwardProxyCA() async =>
      (await _send('GET', '/api/v1/proxy/ca.pem', accept: 'application/x-pem-file', token: false)).bodyText;

  /// Downloads the forward proxy's CA certificate and writes it to [path],
  /// then returns [path]. Not available on the web, where it throws
  /// [UnsupportedError].
  Future<String> saveForwardProxyCA([String path = 'acnologia-ca.pem']) async {
    await env.writeTextFile(path, await forwardProxyCA());
    return path;
  }

  /// Returns the forward proxy URL with user `app` and this client's token as
  /// the password, for example `http://app:acn_...@host:8888`.
  ///
  /// The result holds the app token: never log it.
  Future<String> forwardProxyURL() async => _forwardProxyURL(await forwardProxy());

  /// Returns the environment an app needs to send its own HTTP calls through
  /// the forward proxy: `HTTPS_PROXY` and `HTTP_PROXY` (the
  /// [forwardProxyURL]), `NODE_USE_ENV_PROXY=1`, and `NODE_EXTRA_CA_CERTS`,
  /// `REQUESTS_CA_BUNDLE` and `SSL_CERT_FILE` set to [caPath]. Save the CA
  /// there with [saveForwardProxyCA].
  ///
  /// The result holds the app token: never log it.
  Future<Map<String, String>> forwardProxyEnv([String caPath = 'acnologia-ca.pem']) async {
    final url = _forwardProxyURL(await forwardProxy());
    return {
      'HTTPS_PROXY': url,
      'HTTP_PROXY': url,
      'NODE_USE_ENV_PROXY': '1',
      'NODE_EXTRA_CA_CERTS': caPath,
      'REQUESTS_CA_BUNDLE': caPath,
      'SSL_CERT_FILE': caPath,
    };
  }

  String _forwardProxyURL(ForwardProxy fp) {
    final u = Uri.parse(fp.proxyUrl);
    final auth = 'app:${Uri.encodeComponent(_token)}';
    final port = u.hasPort ? ':${u.port}' : '';
    return '${u.scheme}://$auth@${u.host}$port${u.path}';
  }

  Map<String, String> _authHeaders({bool token = true}) => {
        if (token) 'Authorization': 'Bearer $_token',
        if (_accessPassword.isNotEmpty) 'X-Acnologia-Access': _accessPassword,
        if (env.canSetUserAgent && userAgent.isNotEmpty) 'User-Agent': userAgent,
      };

  Future<http.Response> _send(String method, String path,
      {Map<String, dynamic>? body, String accept = 'application/json', bool token = true}) async {
    final req = http.Request(method, Uri.parse('$baseUrl$path'))
      ..headers.addAll(_authHeaders(token: token))
      ..headers['Accept'] = accept;
    if (body != null) {
      req.headers['Content-Type'] = 'application/json';
      req.body = jsonEncode(body);
    }
    Future<http.Response> run() async => http.Response.fromStream(await _http.send(req));
    final resp = timeout > Duration.zero ? await run().timeout(timeout) : await run();
    if (resp.statusCode < 200 || resp.statusCode > 299) {
      throw _parseError(resp.statusCode, resp.body,
          reasonPhrase: resp.reasonPhrase,
          retryAfterHeader: resp.headers['retry-after'],
          shareHeader: resp.headers['x-acnologia-share']);
    }
    return resp;
  }

  Future<Map<String, dynamic>> _json(String method, String path, [Map<String, dynamic>? body]) async {
    final resp = await _send(method, path, body: body);
    if (resp.bodyBytes.isEmpty) return const {};
    final Object? data;
    try {
      data = jsonDecode(utf8.decode(resp.bodyBytes));
    } on FormatException catch (e) {
      throw AcnologiaApiException(statusCode: resp.statusCode, code: 'decode', message: 'decode response: ${e.message}');
    }
    return data is Map<String, dynamic> ? data : const {};
  }
}

extension on http.Response {
  /// The body as UTF-8, whatever charset the server named (or left out).
  String get bodyText => utf8.decode(bodyBytes, allowMalformed: true);
}

String _trimSlash(String s) {
  var out = s;
  while (out.endsWith('/')) {
    out = out.substring(0, out.length - 1);
  }
  return out;
}
