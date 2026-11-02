part of '../acnologia.dart';

const int _maxReportMessage = 500;

/// Removes [secret] (raw and URL encoded) from [message] and truncates it to
/// 500 characters. Errors from query auth services often carry the full
/// request URL, key included, and a report must not copy it to the server.
String scrubMessage(String message, String secret) {
  var out = message;
  if (secret.isNotEmpty) {
    out = out
        .replaceAll(secret, '[key]')
        .replaceAll(Uri.encodeComponent(secret), '[key]')
        .replaceAll(Uri.encodeQueryComponent(secret), '[key]');
  }
  return out.length > _maxReportMessage ? out.substring(0, _maxReportMessage) : out;
}

/// Adds [withKey] to [Acnologia].
extension AcnologiaWithKey on Acnologia {
  /// Leases a key for [service] and runs [body] with it, rotating keys on key
  /// problems.
  ///
  /// * [body] completes: the lease is reported `ok` and its value returned. A
  ///   failed report is thrown.
  /// * [body] throws a [RateLimitedException], [QuotaExhaustedException] or
  ///   [InvalidKeyException]: the outcome is reported with `rotate` and [body]
  ///   runs again with the next key, up to [maxAttempts] keys. The final
  ///   attempt is reported without asking for a next key, and its exception is
  ///   thrown.
  /// * [body] throws anything else: the lease is reported `error` and the
  ///   error is rethrown unchanged.
  ///
  /// When the pool has no further key, [NoKeyAvailableException] (or
  /// [AllKeysBusyException]) is thrown. Every lease is reported, which matters
  /// on services with a concurrency limit.
  ///
  /// [excludeKeyIds] applies to the first lease; [ttl], [wait] and [exclusive]
  /// apply to every lease.
  Future<T> withKey<T>(
    String service,
    FutureOr<T> Function(Lease lease) body, {
    int maxAttempts = 5,
    List<int> excludeKeyIds = const [],
    Duration? ttl,
    Duration? wait,
    bool exclusive = false,
  }) async {
    final attempts = maxAttempts > 0 ? maxAttempts : 5;
    final tried = <int>[...excludeKeyIds];
    var lease = await this.lease(service, excludeKeyIds: excludeKeyIds, ttl: ttl, wait: wait, exclusive: exclusive);
    Object? lastError;
    StackTrace? lastStack;

    for (var attempt = 1; attempt <= attempts; attempt++) {
      final T result;
      try {
        result = await body(lease);
      } on KeyRotationException catch (e, st) {
        lastError = e;
        lastStack = st;
        tried.add(lease.keyId);
        final message = e.message.isEmpty ? null : scrubMessage(e.message, lease.secret);
        if (attempt == attempts) {
          try {
            await lease.report(e.outcome, statusCode: e.statusCode, message: message);
          } catch (_) {
            // keep the rotation error
          }
          break;
        }
        final next = await lease.report(e.outcome, statusCode: e.statusCode, message: message, rotate: true);
        lease = next ??
            await this.lease(service, excludeKeyIds: List.of(tried), ttl: ttl, wait: wait, exclusive: exclusive);
        continue;
      } catch (e) {
        try {
          await lease.report(Outcome.error, message: scrubMessage(e.toString(), lease.secret));
        } catch (_) {
          // keep the caller's error
        }
        rethrow;
      }
      await lease.report(Outcome.ok);
      return result;
    }
    Error.throwWithStackTrace(lastError!, lastStack!);
  }
}
