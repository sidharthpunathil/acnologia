part of '../acnologia.dart';

/// Where a provider expects the key.
///
/// [type] is `query`, `header` or `bearer`; [name] is the query parameter or
/// header name. A non empty [prefix] goes in front of the secret for every
/// type, and for `bearer` it replaces the default `Bearer ` prefix.
class Auth {
  /// Creates an auth description.
  const Auth({required this.type, required this.name, this.prefix = ''});

  /// Decodes the server's `auth` object.
  factory Auth.fromJson(Map<String, dynamic> json) => Auth(
        type: json['type'] as String? ?? 'header',
        name: json['name'] as String? ?? '',
        prefix: json['prefix'] as String? ?? '',
      );

  /// `query`, `header` or `bearer`.
  final String type;

  /// The query parameter or header name.
  final String name;

  /// Text put in front of the secret, for example `Token `.
  final String prefix;

  /// The JSON form, as the server sends it.
  Map<String, dynamic> toJson() => {
        'type': type,
        'name': name,
        if (prefix.isNotEmpty) 'prefix': prefix,
      };

  @override
  bool operator ==(Object other) =>
      other is Auth && other.type == type && other.name == name && other.prefix == prefix;

  @override
  int get hashCode => Object.hash(type, name, prefix);

  @override
  String toString() => 'Auth($type, $name${prefix.isEmpty ? '' : ', prefix: "$prefix"'})';
}

/// A service from `GET /api/v1/services`.
class Service {
  /// Creates a service.
  const Service({
    required this.slug,
    required this.name,
    required this.provider,
    required this.category,
    required this.available,
    required this.baseUrl,
    required this.auth,
  });

  /// Decodes one entry of the server's `services` list.
  factory Service.fromJson(Map<String, dynamic> json) {
    final slug = json['slug'] as String? ?? '';
    final auth = json['auth'];
    return Service(
      slug: slug,
      name: json['name'] as String? ?? slug,
      provider: json['provider'] as String? ?? '',
      category: json['category'] as String? ?? '',
      available: json['available'] == true,
      baseUrl: json['base_url'] as String? ?? '',
      auth: auth is Map<String, dynamic> ? Auth.fromJson(auth) : const Auth(type: 'header', name: ''),
    );
  }

  /// The service slug used in leases and proxy paths.
  final String slug;

  /// The display name.
  final String name;

  /// The provider slug.
  final String provider;

  /// The provider category, for example `search`.
  final String category;

  /// Whether a key can be leased right now. The server never says how many exist.
  final bool available;

  /// The provider base URL.
  final String baseUrl;

  /// Where the provider expects the key.
  final Auth auth;

  @override
  String toString() => 'Service($slug, available: $available)';
}

/// The result of using a leased key, sent with [Lease.report].
final class Outcome {
  const Outcome._(this.wireValue, [this.retryAfter]);

  /// The call worked.
  static const Outcome ok = Outcome._('ok');

  /// The key used up its quota.
  static const Outcome quotaExhausted = Outcome._('quota_exhausted');

  /// The provider rejected the key.
  static const Outcome invalid = Outcome._('invalid');

  /// Something else failed.
  static const Outcome error = Outcome._('error');

  /// The account is at its concurrency cap; the key steps aside briefly.
  static const Outcome busy = Outcome._('busy');

  /// The provider suspended the account; the key leaves rotation.
  static const Outcome banned = Outcome._('banned');

  /// The provider rate limited the key. [retryAfter] sets its cooldown.
  static Outcome rateLimited([Duration? retryAfter]) => Outcome._('rate_limited', retryAfter);

  /// The value sent on the wire.
  final String wireValue;

  /// The cooldown for a rate limited key.
  final Duration? retryAfter;

  @override
  bool operator ==(Object other) =>
      other is Outcome && other.wireValue == wireValue && other.retryAfter == retryAfter;

  @override
  int get hashCode => Object.hash(wireValue, retryAfter);

  @override
  String toString() => retryAfter == null ? wireValue : '$wireValue(${_formatDuration(retryAfter!)})';
}

int _ceilSeconds(Duration d) => (d.inMicroseconds / Duration.microsecondsPerSecond).ceil();

DateTime? _parseTime(Object? s) => s is String && s.isNotEmpty ? DateTime.tryParse(s) : null;
