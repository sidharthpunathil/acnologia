part of '../acnologia.dart';

/// A key handed out by the pool. Report its outcome with [report] when done.
///
/// The secret never appears in [toString]. Read [secret] directly when you
/// need it, or let [apply] place it on a request.
class Lease {
  Lease._(this._client, this.leaseId, this.keyId, this.service, this.secret, this.auth, this.baseUrl,
      this.expiresAt, this.exclusive);

  factory Lease._fromJson(Acnologia client, Map<String, dynamic> json) {
    final auth = json['auth'];
    return Lease._(
      client,
      json['lease_id'] as String? ?? '',
      (json['key_id'] as num?)?.toInt() ?? 0,
      json['service'] as String? ?? '',
      json['secret'] as String? ?? '',
      auth is Map<String, dynamic> ? Auth.fromJson(auth) : const Auth(type: 'header', name: ''),
      _trimSlash(json['base_url'] as String? ?? ''),
      _parseTime(json['expires_at']),
      json['exclusive'] == true,
    );
  }

  final Acnologia _client;

  /// The lease id, `lse_...`.
  final String leaseId;

  /// The pool key id.
  final int keyId;

  /// The service slug.
  final String service;

  /// The provider key. Never log it.
  final String secret;

  /// Where the provider expects the key.
  final Auth auth;

  /// The provider base URL, without a trailing slash.
  final String baseUrl;

  /// When the lease expires, if the server said.
  final DateTime? expiresAt;

  /// True when the lease was taken with `exclusive: true`.
  final bool exclusive;

  /// Reports how the key behaved.
  ///
  /// [outcome] says what happened; [Outcome.rateLimited] carries the cooldown.
  /// [statusCode] is the provider's HTTP status and [message] a short note for
  /// the dashboard (the server removes the secret from it). Set [rotate] to
  /// get the next key in the same round trip.
  ///
  /// Returns the next lease when [rotate] is true and another key is
  /// available, otherwise null.
  Future<Lease?> report(Outcome outcome, {int? statusCode, String? message, bool rotate = false}) async {
    final retry = outcome.retryAfter;
    final body = <String, dynamic>{
      'outcome': outcome.wireValue,
      if (statusCode != null && statusCode != 0) 'status_code': statusCode,
      if (retry != null && retry > Duration.zero) 'retry_after_sec': _ceilSeconds(retry),
      if (message != null && message.isNotEmpty) 'message': message,
      if (rotate) 'rotate': true,
    };
    final data = await _client._json('POST', '/api/v1/leases/${Uri.encodeComponent(leaseId)}/report', body);
    final next = data['next'];
    return next is Map<String, dynamic> ? Lease._fromJson(_client, next) : null;
  }

  /// Extends an open lease so a long job keeps its key, and returns the new
  /// expiry. [ttl] is the new lifetime from now (server default 120 seconds,
  /// at most 24 hours).
  ///
  /// Throws [AcnologiaApiException] with code `lease_not_found` when the lease
  /// is not yours or no longer open.
  Future<DateTime> renew({Duration? ttl}) async {
    final body = <String, dynamic>{
      if (ttl != null && ttl > Duration.zero) 'ttl_sec': _ceilSeconds(ttl),
    };
    final data = await _client._json('POST', '/api/v1/leases/${Uri.encodeComponent(leaseId)}/renew', body);
    final exp = _parseTime(data['expires_at']);
    if (exp == null) {
      throw const AcnologiaApiException(statusCode: 200, code: 'decode', message: 'renew response has no expires_at');
    }
    return exp;
  }

  /// Puts the secret where the provider expects it and returns the new URL
  /// and headers. The inputs are not changed.
  ///
  /// `query` sets the query parameter [Auth.name] (default `api_key`),
  /// `header` sets header [Auth.name], and `bearer` sets [Auth.name] (default
  /// `Authorization`) to `Bearer <secret>`. A non empty [Auth.prefix] goes in
  /// front of the secret for every type; for `bearer` it replaces `Bearer `.
  ({Uri url, Map<String, String> headers}) apply(Uri url, [Map<String, String>? headers]) {
    final h = <String, String>{...?headers};
    final value = auth.prefix + secret;
    switch (auth.type) {
      case 'query':
        final name = auth.name.isEmpty ? 'api_key' : auth.name;
        final q = <String, List<String>>{...url.queryParametersAll, name: [value]};
        return (url: url.replace(queryParameters: q), headers: h);
      case 'bearer':
        h[auth.name.isEmpty ? 'Authorization' : auth.name] = auth.prefix.isEmpty ? 'Bearer $secret' : value;
      default:
        h[auth.name.isEmpty ? 'Authorization' : auth.name] = value;
    }
    return (url: url, headers: h);
  }

  /// Joins [baseUrl] with [path] and [query]. The secret is not added; use
  /// [apply] or [request] for that.
  Uri url(String path, [Map<String, Object?>? query]) {
    final p = path.replaceFirst(RegExp(r'^/+'), '');
    return _withQuery(Uri.parse('$baseUrl/$p'), query);
  }

  /// Builds an [http.Request] to the provider with the secret applied. Send
  /// it with any `http.Client`.
  http.Request request(String method, String path, {Map<String, Object?>? query, Map<String, String>? headers}) {
    final applied = apply(url(path, query), headers);
    return http.Request(method.toUpperCase(), applied.url)..headers.addAll(applied.headers);
  }

  @override
  String toString() => 'Lease(leaseId: $leaseId, keyId: $keyId, service: $service, secret: [redacted], '
      'auth: ${auth.type}/${auth.name}, baseUrl: $baseUrl, expiresAt: ${expiresAt?.toIso8601String()})';
}

/// Adds [query] to [uri], keeping any query it already has. List values
/// become repeated parameters; null values are skipped.
Uri _withQuery(Uri uri, Map<String, Object?>? query) {
  if (query == null || query.isEmpty) return uri;
  final q = <String, List<String>>{...uri.queryParametersAll};
  query.forEach((k, v) {
    if (v == null) return;
    q[k] = v is Iterable ? [for (final e in v) '$e'] : ['$v'];
  });
  return uri.replace(queryParameters: q);
}
