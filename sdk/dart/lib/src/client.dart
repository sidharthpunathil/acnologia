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
  Acnologia({
    required String baseUrl,
    required String token,
    http.Client? httpClient,
    String? userAgent,
    this.timeout = defaultTimeout,
  })  : baseUrl = _trimSlash(baseUrl),
        _token = token,
        _http = httpClient ?? http.Client(),
        _ownsHttp = httpClient == null,
        userAgent = userAgent ?? defaultUserAgent {
    if (baseUrl.isEmpty || !Uri.parse(baseUrl).hasScheme) {
      throw ArgumentError.value(baseUrl, 'baseUrl', 'must be an absolute URL such as http://localhost:8080');
    }
    if (token.isEmpty) throw ArgumentError.value(token, 'token', 'is required');
  }

  /// Creates a client from the `ACNOLOGIA_URL` and `ACNOLOGIA_TOKEN`
  /// environment variables.
  ///
  /// [environment] defaults to the process environment, then to values given
  /// at compile time with `--dart-define` (the only option on the web).
  /// Throws [ArgumentError] when either is missing.
  factory Acnologia.fromEnvironment({
    Map<String, String>? environment,
    http.Client? httpClient,
    String? userAgent,
    Duration timeout = defaultTimeout,
  }) {
    final e = environment ?? env.environment();
    const defUrl = String.fromEnvironment('ACNOLOGIA_URL');
    const defToken = String.fromEnvironment('ACNOLOGIA_TOKEN');
    final url = e['ACNOLOGIA_URL'] ?? (environment == null ? defUrl : '');
    final token = e['ACNOLOGIA_TOKEN'] ?? (environment == null ? defToken : '');
    if (url.isEmpty) throw ArgumentError('ACNOLOGIA_URL is not set');
    if (token.isEmpty) throw ArgumentError('ACNOLOGIA_TOKEN is not set');
    return Acnologia(baseUrl: url, token: token, httpClient: httpClient, userAgent: userAgent, timeout: timeout);
  }

  /// The SDK version.
  static const String version = '0.1.0';

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
  final http.Client _http;
  final bool _ownsHttp;

  /// Closes the HTTP client if this instance created it.
  void close() {
    if (_ownsHttp) _http.close();
  }

  @override
  String toString() => 'Acnologia(baseUrl: $baseUrl, token: [redacted])';

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

  Map<String, String> _authHeaders() => {
        'Authorization': 'Bearer $_token',
        if (env.canSetUserAgent && userAgent.isNotEmpty) 'User-Agent': userAgent,
      };

  Future<Map<String, dynamic>> _json(String method, String path, [Map<String, dynamic>? body]) async {
    final req = http.Request(method, Uri.parse('$baseUrl$path'))
      ..headers.addAll(_authHeaders())
      ..headers['Accept'] = 'application/json';
    if (body != null) {
      req.headers['Content-Type'] = 'application/json';
      req.body = jsonEncode(body);
    }
    Future<http.Response> run() async => http.Response.fromStream(await _http.send(req));
    final resp = timeout > Duration.zero ? await run().timeout(timeout) : await run();
    if (resp.statusCode < 200 || resp.statusCode > 299) {
      throw _parseError(resp.statusCode, resp.body,
          reasonPhrase: resp.reasonPhrase, retryAfterHeader: resp.headers['retry-after']);
    }
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

String _trimSlash(String s) {
  var out = s;
  while (out.endsWith('/')) {
    out = out.substring(0, out.length - 1);
  }
  return out;
}
