part of '../acnologia.dart';

/// Base class for every error the SDK throws, and for the errors you throw
/// from a [Acnologia.withKey] callback to rotate keys.
abstract class AcnologiaException implements Exception {
  /// Creates an exception.
  const AcnologiaException(this.message, {this.statusCode, required this.code});

  /// The human readable message.
  final String message;

  /// The HTTP status code, when there is one.
  final int? statusCode;

  /// The machine readable error code from the server, or the outcome name for
  /// rotation errors.
  final String code;

  /// Seconds to wait before retrying, when known.
  Duration? get retryAfter => null;

  String get _label;

  @override
  String toString() {
    final buf = StringBuffer('acnologia: $_label');
    final retry = retryAfter;
    if (retry != null && retry > Duration.zero) {
      buf.write(' (retry after ${_formatDuration(retry)})');
    }
    if (message.isNotEmpty) buf.write(': $message');
    return buf.toString();
  }
}

String _formatDuration(Duration d) {
  if (d.inMicroseconds % Duration.microsecondsPerSecond == 0) {
    return '${d.inSeconds}s';
  }
  return '${d.inMicroseconds / Duration.microsecondsPerSecond}s';
}

/// Throw one of these from a [Acnologia.withKey] callback to report the key
/// and retry with the next one.
abstract class KeyRotationException extends AcnologiaException {
  /// Creates a rotation exception.
  const KeyRotationException(super.message, {super.statusCode, required super.code});

  /// The outcome reported for the key.
  Outcome get outcome;
}

/// The provider rate limited the key.
class RateLimitedException extends KeyRotationException {
  /// Creates the exception. [retryAfter] sets how long the key cools down.
  const RateLimitedException({Duration? retryAfter, int? statusCode, String message = ''})
      : _retryAfter = retryAfter,
        super(message, statusCode: statusCode, code: 'rate_limited');

  final Duration? _retryAfter;

  @override
  Duration? get retryAfter => _retryAfter;

  @override
  Outcome get outcome => Outcome.rateLimited(_retryAfter);

  @override
  String get _label => 'rate limited';
}

/// The key used up its quota.
class QuotaExhaustedException extends KeyRotationException {
  /// Creates the exception.
  const QuotaExhaustedException({int? statusCode, String message = ''})
      : super(message, statusCode: statusCode, code: 'quota_exhausted');

  @override
  Outcome get outcome => Outcome.quotaExhausted;

  @override
  String get _label => 'quota exhausted';
}

/// The provider rejected the key.
class InvalidKeyException extends KeyRotationException {
  /// Creates the exception.
  const InvalidKeyException({int? statusCode, String message = ''})
      : super(message, statusCode: statusCode, code: 'invalid');

  @override
  Outcome get outcome => Outcome.invalid;

  @override
  String get _label => 'invalid key';
}

/// No key can be leased right now (HTTP 503).
///
/// [code] is `no_key_available` when every key is cooling down, exhausted,
/// disabled or excluded, and `all_keys_busy` when usable keys exist but every
/// one is at its concurrency limit (see [busy] and [AllKeysBusyException]).
class NoKeyAvailableException extends AcnologiaException {
  /// Creates the exception.
  const NoKeyAvailableException({Duration? retryAfter, String message = '', String code = 'no_key_available'})
      : _retryAfter = retryAfter,
        super(message, statusCode: 503, code: code);

  final Duration? _retryAfter;

  @override
  Duration? get retryAfter => _retryAfter;

  /// True when every usable key is at its concurrency limit. It clears as soon
  /// as another caller reports a lease.
  bool get busy => code == 'all_keys_busy';

  @override
  String get _label => busy ? 'all keys busy' : 'no key available';
}

/// Every usable key is at its concurrency limit (HTTP 503 `all_keys_busy`).
///
/// Extends [NoKeyAvailableException], so existing handlers still catch it.
/// Retry after [retryAfter] (usually one second) or lease with `wait`.
class AllKeysBusyException extends NoKeyAvailableException {
  /// Creates the exception.
  const AllKeysBusyException({super.retryAfter, super.message}) : super(code: 'all_keys_busy');
}

/// The provider keeps failing and its circuit breaker is open
/// (HTTP 503 `service_unavailable`).
class ServiceUnavailableException extends AcnologiaException {
  /// Creates the exception.
  const ServiceUnavailableException({Duration? retryAfter, String message = ''})
      : _retryAfter = retryAfter,
        super(message, statusCode: 503, code: 'service_unavailable');

  final Duration? _retryAfter;

  @override
  Duration? get retryAfter => _retryAfter;

  @override
  String get _label => 'service unavailable';
}

/// The app token is missing, wrong, revoked or expired (HTTP 401).
class UnauthenticatedException extends AcnologiaException {
  /// Creates the exception.
  const UnauthenticatedException({String message = ''})
      : super(message, statusCode: 401, code: 'unauthenticated');

  @override
  String get _label => 'http 401: unauthenticated';
}

/// The app is disabled, or may not use this service or mode (HTTP 403).
/// [code] is for example `service_not_allowed`, `lease_not_allowed` or
/// `proxy_not_allowed`.
class ForbiddenException extends AcnologiaException {
  /// Creates the exception.
  const ForbiddenException({String code = 'forbidden', String message = ''})
      : super(message, statusCode: 403, code: code);

  @override
  String get _label => 'http 403: $code';
}

/// The share behind a share's token was revoked, paused or let expire
/// (HTTP 403 `share_revoked`, `share_paused` or `share_expired`).
///
/// Extends [ForbiddenException], so existing handlers still catch it.
class ShareUnavailableException extends ForbiddenException {
  /// Creates the exception. [status] is `revoked`, `paused` or `expired`.
  const ShareUnavailableException({required this.status, super.message}) : super(code: 'share_$status');

  /// `revoked`, `paused` or `expired`, also sent as `X-Acnologia-Share`.
  final String status;
}

/// Any other non-2xx answer from the consumer API.
class AcnologiaApiException extends AcnologiaException {
  /// Creates the exception.
  const AcnologiaApiException({required int statusCode, String code = '', String message = ''})
      : super(message, statusCode: statusCode, code: code);

  @override
  String get _label => code.isEmpty ? 'http $statusCode' : 'http $statusCode: $code';
}

/// The app used up a daily or monthly request or budget limit (HTTP 429
/// `app_limit_reached`).
///
/// Extends [AcnologiaApiException], so existing handlers still catch it.
class AppLimitReachedException extends AcnologiaApiException {
  /// Creates the exception.
  const AppLimitReachedException({this.limit = '', this.resetAt, Duration? retryAfter, super.message})
      : _retryAfter = retryAfter,
        super(statusCode: 429, code: 'app_limit_reached');

  /// Which limit: `daily_requests`, `monthly_requests`, `daily_budget` or
  /// `monthly_budget`. Empty when the server did not say.
  final String limit;

  /// When the limit resets, if the server said.
  final DateTime? resetAt;

  final Duration? _retryAfter;

  @override
  Duration? get retryAfter => _retryAfter;

  @override
  String get _label => limit.isEmpty ? 'http 429: app_limit_reached' : 'http 429: app_limit_reached ($limit)';
}

/// Builds the typed exception for a non-2xx answer from Acnologia itself.
AcnologiaException _parseError(int status, String body,
    {String? reasonPhrase, String? retryAfterHeader, String? shareHeader}) {
  var code = '';
  var message = '';
  var limit = '';
  DateTime? resetAt;
  Duration? retryAfter;
  Object? data;
  try {
    data = body.isEmpty ? null : jsonDecode(body);
  } on FormatException {
    data = null;
  }
  if (data is Map<String, dynamic>) {
    final err = data['error'];
    if (err is Map<String, dynamic>) {
      code = err['code'] is String ? err['code'] as String : '';
      message = err['message'] is String ? err['message'] as String : '';
    } else if (err is String) {
      message = err;
    }
    final ra = data['retry_after_sec'];
    if (ra is num) retryAfter = _secondsToDuration(ra);
    if (data['limit'] is String) limit = data['limit'] as String;
    resetAt = _parseTime(data['reset_at']);
  }
  if (retryAfter == null && retryAfterHeader != null) {
    final n = num.tryParse(retryAfterHeader.trim());
    if (n != null) retryAfter = _secondsToDuration(n);
  }
  if (message.isEmpty && code.isEmpty) {
    message = body.trim();
    if (message.length > 200) message = message.substring(0, 200);
    if (message.isEmpty) message = reasonPhrase ?? 'HTTP $status';
  }
  switch (code) {
    case 'all_keys_busy':
      return AllKeysBusyException(retryAfter: retryAfter, message: message);
    case 'no_key_available':
      return NoKeyAvailableException(retryAfter: retryAfter, message: message);
    case 'service_unavailable':
      return ServiceUnavailableException(retryAfter: retryAfter, message: message);
    case '' when status == 503:
      return NoKeyAvailableException(retryAfter: retryAfter, message: message);
    case 'app_limit_reached':
      return AppLimitReachedException(limit: limit, resetAt: resetAt, retryAfter: retryAfter, message: message);
    case 'share_revoked' || 'share_paused' || 'share_expired':
      return ShareUnavailableException(status: code.substring('share_'.length), message: message);
  }
  final share = shareHeader?.trim() ?? '';
  if (status == 403 && code.isEmpty && (share == 'revoked' || share == 'paused' || share == 'expired')) {
    return ShareUnavailableException(status: share, message: message);
  }
  if (status == 401) return UnauthenticatedException(message: message);
  if (status == 403) return ForbiddenException(code: code.isEmpty ? 'forbidden' : code, message: message);
  return AcnologiaApiException(statusCode: status, code: code, message: message);
}

Duration _secondsToDuration(num s) => Duration(microseconds: (s * Duration.microsecondsPerSecond).round());
