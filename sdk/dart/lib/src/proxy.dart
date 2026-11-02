part of '../acnologia.dart';

/// Sends requests through Acnologia's proxy for one service.
///
/// Acnologia injects a key, retries on rate limits with the next key, and
/// never returns the secret. Upstream responses come back as they are: a
/// non-2xx status is not thrown. Redirects are not followed, so the app token
/// is never replayed to another host (browsers follow redirects on their own).
///
/// Responses carry `X-Acnologia-Key-Id` and `X-Acnologia-Attempts`; read them
/// with [AcnologiaProxyResponse.acnologiaKeyId] and
/// [AcnologiaProxyResponse.acnologiaAttempts].
class Proxy {
  Proxy._(this._client, this.service);

  final Acnologia _client;

  /// The service slug (or service group).
  final String service;

  String get _prefix => '${_client.baseUrl}/proxy/${Uri.encodeComponent(service)}';

  /// The full proxy URL for a provider [path] and [query].
  Uri url(String path, [Map<String, Object?>? query]) {
    var p = path.replaceFirst(RegExp(r'^/+'), '');
    final svcPrefix = 'proxy/${Uri.encodeComponent(service)}';
    if (p == svcPrefix || p.startsWith('$svcPrefix/')) {
      p = p.substring(svcPrefix.length).replaceFirst(RegExp(r'^/+'), '');
    }
    return _withQuery(Uri.parse('$_prefix/$p'), query);
  }

  /// Sends a request and returns the streamed response, for large or streamed
  /// bodies. [body] may be a `String`, a `List<int>`, or any other value,
  /// which is sent as JSON with `Content-Type: application/json` unless you
  /// set one.
  Future<http.StreamedResponse> stream(
    String method,
    String path, {
    Map<String, Object?>? query,
    Map<String, String>? headers,
    Object? body,
  }) {
    final req = http.Request(method.toUpperCase(), url(path, query))
      ..followRedirects = false
      ..headers.addAll(headers ?? const {});
    if (body is String) {
      req.body = body;
    } else if (body is List<int>) {
      req.bodyBytes = body;
    } else if (body != null) {
      if (!req.headers.keys.any((k) => k.toLowerCase() == 'content-type')) {
        req.headers['Content-Type'] = 'application/json';
      }
      req.body = jsonEncode(body);
    }
    req.headers.addAll(_client._authHeaders());
    return _client._http.send(req);
  }

  /// Sends a request and returns the whole response. See [stream] for [body].
  Future<http.Response> request(
    String method,
    String path, {
    Map<String, Object?>? query,
    Map<String, String>? headers,
    Object? body,
  }) async =>
      http.Response.fromStream(await stream(method, path, query: query, headers: headers, body: body));

  /// Sends a GET with optional query parameters.
  Future<http.Response> get(String path, {Map<String, Object?>? query, Map<String, String>? headers}) =>
      request('GET', path, query: query, headers: headers);

  /// Sends a DELETE.
  Future<http.Response> delete(String path, {Map<String, Object?>? query, Map<String, String>? headers}) =>
      request('DELETE', path, query: query, headers: headers);

  /// Sends a POST. Maps and lists are sent as JSON.
  Future<http.Response> post(String path, {Object? body, Map<String, Object?>? query, Map<String, String>? headers}) =>
      request('POST', path, query: query, headers: headers, body: body);

  /// Sends a PUT. Maps and lists are sent as JSON.
  Future<http.Response> put(String path, {Object? body, Map<String, Object?>? query, Map<String, String>? headers}) =>
      request('PUT', path, query: query, headers: headers, body: body);

  /// Sends a PATCH. Maps and lists are sent as JSON.
  Future<http.Response> patch(String path, {Object? body, Map<String, Object?>? query, Map<String, String>? headers}) =>
      request('PATCH', path, query: query, headers: headers, body: body);

  @override
  String toString() => 'Proxy($service)';
}

/// Acnologia's headers on a proxied response.
extension AcnologiaProxyResponse on http.BaseResponse {
  /// The pool key that served the request (`X-Acnologia-Key-Id`).
  int? get acnologiaKeyId => int.tryParse(headers['x-acnologia-key-id'] ?? '');

  /// How many keys were tried (`X-Acnologia-Attempts`).
  int? get acnologiaAttempts => int.tryParse(headers['x-acnologia-attempts'] ?? '');

  /// The service that answered, which matters for groups (`X-Acnologia-Service`).
  String? get acnologiaService => headers['x-acnologia-service'];

  /// The cache status (`X-Acnologia-Cache`), when caching applies.
  String? get acnologiaCache => headers['x-acnologia-cache'];
}

/// Typed errors for proxied responses that Acnologia itself refused.
extension AcnologiaProxyError on http.Response {
  /// The typed exception when Acnologia refused the request (no key, all keys
  /// busy, service unavailable, unauthenticated, forbidden and so on). Null
  /// for a 2xx and for any answer from the provider (those carry
  /// `X-Acnologia-Key-Id`).
  AcnologiaException? get acnologiaError {
    if ((statusCode >= 200 && statusCode < 300) || headers.containsKey('x-acnologia-key-id')) return null;
    try {
      final data = jsonDecode(body);
      if (data is! Map<String, dynamic> || data['error'] is! Map) return null;
    } on FormatException {
      return null;
    }
    return _parseError(statusCode, body, reasonPhrase: reasonPhrase, retryAfterHeader: headers['retry-after']);
  }

  /// Throws [acnologiaError] when there is one, and returns this response
  /// otherwise.
  http.Response throwIfAcnologiaError() {
    final e = acnologiaError;
    if (e != null) throw e;
    return this;
  }
}
