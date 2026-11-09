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
    this.guideUrl = '',
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
      guideUrl: json['guide_url'] as String? ?? '',
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

  /// Where the service's usage guide is (`/api/v1/services/{slug}/guide`).
  /// Read it with [Acnologia.guide] or [Acnologia.guideMarkdown].
  final String guideUrl;

  @override
  String toString() => 'Service($slug, available: $available)';
}

/// A service's usage guide, from `GET /api/v1/services/{slug}/guide`.
class Guide {
  /// Creates a guide.
  const Guide({required this.markdown, required this.generated, this.guide});

  /// Decodes the server's answer.
  factory Guide.fromJson(Map<String, dynamic> json) => Guide(
        markdown: json['markdown'] as String? ?? '',
        generated: json['generated'] == true,
        guide: json['guide'],
      );

  /// The guide as Markdown.
  final String markdown;

  /// True when the provider has no written guide and this one was assembled
  /// from its examples and offers.
  final bool generated;

  /// The structured guide, as decoded JSON (endpoints, examples, limits,
  /// errors, notes and so on).
  final Object? guide;

  @override
  String toString() => 'Guide(generated: $generated, ${markdown.length} chars)';
}

/// One provider route of a [Capability].
class CapabilityRoute {
  /// Creates a route.
  const CapabilityRoute({required this.service, required this.mapped});

  /// Decodes one entry of a capability's `routes`.
  factory CapabilityRoute.fromJson(Map<String, dynamic> json) =>
      CapabilityRoute(service: json['service'] as String? ?? '', mapped: json['mapped'] == true);

  /// The service slug the route calls.
  final String service;

  /// True when the route maps the provider's answer into the capability's
  /// common shape.
  final bool mapped;

  @override
  bool operator ==(Object other) => other is CapabilityRoute && other.service == service && other.mapped == mapped;

  @override
  int get hashCode => Object.hash(service, mapped);

  @override
  String toString() => 'CapabilityRoute($service, mapped: $mapped)';
}

/// A capability from `GET /api/v1/capabilities`: one job that several
/// providers do. Call it with [Acnologia.capability].
class Capability {
  /// Creates a capability.
  const Capability({
    required this.slug,
    required this.name,
    this.description = '',
    this.strategy = '',
    this.params,
    this.routes = const [],
    this.url = '',
  });

  /// Decodes one entry of the server's `capabilities` list.
  factory Capability.fromJson(Map<String, dynamic> json) {
    final slug = json['slug'] as String? ?? '';
    final routes = json['routes'];
    return Capability(
      slug: slug,
      name: json['name'] as String? ?? slug,
      description: json['description'] as String? ?? '',
      strategy: json['strategy'] as String? ?? '',
      params: json['params'],
      routes: [
        if (routes is List)
          for (final r in routes)
            if (r is Map<String, dynamic>) CapabilityRoute.fromJson(r)
      ],
      url: json['url'] as String? ?? '',
    );
  }

  /// The slug, used as `/proxy/{slug}`.
  final String slug;

  /// The display name.
  final String name;

  /// What the capability does.
  final String description;

  /// How a route is picked, for example `priority` or `round_robin`.
  final String strategy;

  /// The params it takes, as decoded JSON (a list of `{name, type, required,
  /// description, default}`).
  final Object? params;

  /// The providers it can use.
  final List<CapabilityRoute> routes;

  /// The full URL to call.
  final String url;

  @override
  String toString() => 'Capability($slug, routes: ${routes.length})';
}

/// One route tried by a capability call, from `X-Acnologia-Tried`.
class TriedRoute {
  /// Creates a tried route.
  const TriedRoute({required this.service, required this.status});

  /// Parses `X-Acnologia-Tried` (`service=status` pairs, comma separated).
  static List<TriedRoute> parseHeader(String? header) {
    if (header == null) return const [];
    final out = <TriedRoute>[];
    for (final part in header.split(',')) {
      final p = part.trim();
      if (p.isEmpty) continue;
      final i = p.lastIndexOf('=');
      if (i < 0) {
        out.add(TriedRoute(service: p, status: 0));
      } else {
        out.add(TriedRoute(service: p.substring(0, i).trim(), status: int.tryParse(p.substring(i + 1).trim()) ?? 0));
      }
    }
    return out;
  }

  /// The service slug.
  final String service;

  /// The HTTP status it answered, or 0 when it gave none.
  final int status;

  @override
  bool operator ==(Object other) => other is TriedRoute && other.service == service && other.status == status;

  @override
  int get hashCode => Object.hash(service, status);

  @override
  String toString() => '$service=$status';
}

/// The app behind a token, in [Me].
class MeApp {
  /// Creates the app.
  const MeApp({required this.id, required this.name});

  /// The app id.
  final int id;

  /// The app name.
  final String name;

  @override
  String toString() => 'MeApp($id, $name)';
}

/// An app's daily and monthly limits, in [Me]. Null means no such limit.
class AppLimits {
  /// Creates the limits.
  const AppLimits({this.dailyRequests, this.monthlyRequests, this.dailyBudget, this.monthlyBudget});

  /// Decodes the server's `limits`.
  factory AppLimits.fromJson(Map<String, dynamic> json) => AppLimits(
        dailyRequests: (json['daily_requests'] as num?)?.toInt(),
        monthlyRequests: (json['monthly_requests'] as num?)?.toInt(),
        dailyBudget: json['daily_budget'] as num?,
        monthlyBudget: json['monthly_budget'] as num?,
      );

  /// Requests per day.
  final int? dailyRequests;

  /// Requests per month.
  final int? monthlyRequests;

  /// Spend per day, in US dollars.
  final num? dailyBudget;

  /// Spend per month, in US dollars.
  final num? monthlyBudget;
}

/// A limit the app reached, and when it resets.
class LimitHit {
  /// Creates the hit.
  const LimitHit({required this.limit, this.resetAt});

  /// Decodes the server's `limit_hit`, or returns null.
  static LimitHit? fromJson(Object? json) => json is Map<String, dynamic>
      ? LimitHit(limit: json['limit'] as String? ?? '', resetAt: _parseTime(json['reset_at']))
      : null;

  /// `daily_requests`, `monthly_requests`, `daily_budget` or `monthly_budget`.
  final String limit;

  /// When the limit resets.
  final DateTime? resetAt;

  @override
  String toString() => 'LimitHit($limit, resetAt: ${resetAt?.toIso8601String()})';
}

/// An app's use so far, in [Me].
class AppUsage {
  /// Creates the usage.
  const AppUsage({
    this.todayRequests = 0,
    this.todayCost = 0,
    this.monthRequests = 0,
    this.monthCost = 0,
    this.lastUsedAt,
    this.limitHit,
  });

  /// Decodes the server's `usage`.
  factory AppUsage.fromJson(Map<String, dynamic> json) => AppUsage(
        todayRequests: (json['today_requests'] as num?)?.toInt() ?? 0,
        todayCost: json['today_cost'] as num? ?? 0,
        monthRequests: (json['month_requests'] as num?)?.toInt() ?? 0,
        monthCost: json['month_cost'] as num? ?? 0,
        lastUsedAt: _parseTime(json['last_used_at']),
        limitHit: LimitHit.fromJson(json['limit_hit']),
      );

  /// Requests today.
  final int todayRequests;

  /// Spend today, in US dollars.
  final num todayCost;

  /// Requests this month.
  final int monthRequests;

  /// Spend this month, in US dollars.
  final num monthCost;

  /// The last request, if any.
  final DateTime? lastUsedAt;

  /// The limit reached, if any.
  final LimitHit? limitHit;
}

/// A service the token may use, in [Me].
class MeService {
  /// Creates the service.
  const MeService({
    required this.slug,
    required this.name,
    this.provider = '',
    this.category = '',
    this.available = 0,
    this.logoUrl,
  });

  /// Decodes one entry of the server's `services`.
  factory MeService.fromJson(Map<String, dynamic> json) {
    final slug = json['slug'] as String? ?? '';
    final a = json['available'];
    return MeService(
      slug: slug,
      name: json['name'] as String? ?? slug,
      provider: json['provider'] as String? ?? '',
      category: json['category'] as String? ?? '',
      available: a is num ? a.toInt() : (a == true ? 1 : 0),
      logoUrl: json['logo_url'] as String?,
    );
  }

  /// The service slug.
  final String slug;

  /// The display name.
  final String name;

  /// The provider slug.
  final String provider;

  /// The provider category.
  final String category;

  /// Keys free right now: 1 or 0 for an ordinary app, a count for a share's
  /// token.
  final int available;

  /// The logo as a `data:` URI, or null.
  final String? logoUrl;

  @override
  String toString() => 'MeService($slug, available: $available)';
}

/// The share behind a share's token, in [Me].
class ShareInfo {
  /// Creates the share.
  const ShareInfo({
    required this.id,
    this.name = '',
    this.ownerName = '',
    this.recipient = '',
    this.label = '',
    required this.status,
    this.mode = '',
    this.endsAt,
  });

  /// Decodes the server's `share`, or returns null.
  static ShareInfo? fromJson(Object? json) => json is Map<String, dynamic>
      ? ShareInfo(
          id: '${json['id'] ?? ''}',
          name: json['name'] as String? ?? '',
          ownerName: json['owner_name'] as String? ?? '',
          recipient: json['recipient'] as String? ?? '',
          label: json['label'] as String? ?? '',
          status: json['status'] as String? ?? '',
          mode: json['mode'] as String? ?? '',
          endsAt: _parseTime(json['ends_at']),
        )
      : null;

  /// The share id.
  final String id;

  /// The share name.
  final String name;

  /// Who gave the share.
  final String ownerName;

  /// `server` or `person`.
  final String recipient;

  /// The owner's label for it.
  final String label;

  /// The share's status. `paused`, `expired` or `revoked` when it no longer
  /// serves.
  final String status;

  /// The share mode, for example `proxy`.
  final String mode;

  /// When the share ends, if it has a timer.
  final DateTime? endsAt;

  @override
  String toString() => 'ShareInfo($id, status: $status)';
}

/// What a token may do, from `GET /api/v1/me`. Returned by [Acnologia.me].
class Me {
  /// Creates the answer.
  const Me({
    required this.app,
    this.limits = const AppLimits(),
    this.usage = const AppUsage(),
    this.limitHit,
    this.services = const [],
    this.share,
    this.raw = const {},
  });

  /// Decodes the server's answer.
  factory Me.fromJson(Map<String, dynamic> json) {
    final app = json['app'] is Map<String, dynamic> ? json['app'] as Map<String, dynamic> : const <String, dynamic>{};
    final limits = json['limits'];
    final usage = json['usage'];
    final services = json['services'];
    final u = usage is Map<String, dynamic> ? AppUsage.fromJson(usage) : const AppUsage();
    return Me(
      app: MeApp(id: (app['id'] as num?)?.toInt() ?? 0, name: app['name'] as String? ?? ''),
      limits: limits is Map<String, dynamic> ? AppLimits.fromJson(limits) : const AppLimits(),
      usage: u,
      limitHit: LimitHit.fromJson(json['limit_hit']) ?? u.limitHit,
      services: [
        if (services is List)
          for (final sv in services)
            if (sv is Map<String, dynamic>) MeService.fromJson(sv)
      ],
      share: ShareInfo.fromJson(json['share']),
      raw: json,
    );
  }

  /// The app.
  final MeApp app;

  /// Its limits.
  final AppLimits limits;

  /// Its use so far.
  final AppUsage usage;

  /// The limit reached, if any.
  final LimitHit? limitHit;

  /// The services the token may use.
  final List<MeService> services;

  /// Null for an ordinary app; the share for a share's token.
  final ShareInfo? share;

  /// The decoded JSON, for fields this class does not name.
  final Map<String, dynamic> raw;

  @override
  String toString() => 'Me(app: ${app.name}, services: ${services.length}, share: ${share?.status})';
}

/// A provider host the forward proxy answers for, in [ForwardProxy].
class ForwardProxyHost {
  /// Creates the host.
  const ForwardProxyHost({required this.host, required this.service, this.name = '', this.path = ''});

  /// Decodes one entry of the server's `hosts`.
  factory ForwardProxyHost.fromJson(Map<String, dynamic> json) => ForwardProxyHost(
        host: json['host'] as String? ?? '',
        service: json['service'] as String? ?? '',
        name: json['name'] as String? ?? '',
        path: json['path'] as String? ?? '',
      );

  /// The provider host, for example `serpapi.com`.
  final String host;

  /// The service slug a request to this host runs as.
  final String service;

  /// The service name.
  final String name;

  /// The base path on the host, if any.
  final String path;

  @override
  String toString() => 'ForwardProxyHost($host$path -> $service)';
}

/// How to use the forward proxy, from `GET /api/v1/proxy`. Returned by
/// [Acnologia.forwardProxy].
///
/// Nothing here holds the token: [proxyUrlWithToken] has a `<app token>`
/// placeholder. [Acnologia.forwardProxyURL] fills it in.
class ForwardProxy {
  /// Creates the setup.
  const ForwardProxy({
    required this.proxyUrl,
    this.proxyUrlWithToken = '',
    this.caUrl = '',
    this.caFingerprint = '',
    this.passthrough = false,
    this.hosts = const [],
    this.env = const {},
    this.serviceHeader = '',
  });

  /// Decodes the server's answer.
  factory ForwardProxy.fromJson(Map<String, dynamic> json) {
    final hosts = json['hosts'];
    final env = json['env'];
    return ForwardProxy(
      proxyUrl: json['proxy_url'] as String? ?? '',
      proxyUrlWithToken: json['proxy_url_with_token'] as String? ?? '',
      caUrl: json['ca_url'] as String? ?? '',
      caFingerprint: json['ca_fingerprint'] as String? ?? '',
      passthrough: json['passthrough'] == true,
      hosts: [
        if (hosts is List)
          for (final h in hosts)
            if (h is Map<String, dynamic>) ForwardProxyHost.fromJson(h)
      ],
      env: {
        if (env is Map)
          for (final e in env.entries) '${e.key}': '${e.value}'
      },
      serviceHeader: json['service_header'] as String? ?? '',
    );
  }

  /// The proxy URL, without credentials.
  final String proxyUrl;

  /// The proxy URL with user `app` and a `<app token>` placeholder.
  final String proxyUrlWithToken;

  /// Where to download the CA certificate.
  final String caUrl;

  /// The CA certificate's fingerprint.
  final String caFingerprint;

  /// Whether HTTPS to hosts that are no service is tunneled through.
  final bool passthrough;

  /// The provider hosts of the services the token may use.
  final List<ForwardProxyHost> hosts;

  /// The environment to set, with the token placeholder.
  final Map<String, String> env;

  /// The request header that picks a service when several match a host.
  final String serviceHeader;

  @override
  String toString() => 'ForwardProxy($proxyUrl, hosts: ${hosts.length}, passthrough: $passthrough)';
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
