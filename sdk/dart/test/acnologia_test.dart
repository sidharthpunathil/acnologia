import 'dart:convert';
import 'dart:io' show Directory, File;

import 'package:acnologia/acnologia.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:test/test.dart';

const base = 'http://acn.test';
const token = 'acn_test';

Map<String, dynamic> leaseJson(int id, {Map<String, dynamic>? auth, String? secret}) => {
      'lease_id': 'lse_$id',
      'key_id': id,
      'service': 'serpapi',
      'secret': secret ?? 'sk$id',
      'auth': auth ?? {'type': 'query', 'name': 'api_key'},
      'base_url': 'https://serpapi.com',
      'expires_at': '2026-11-01T12:00:00.123456789Z',
      'exclusive': false,
    };

http.Response jsonResponse(Object body, {int status = 200, Map<String, String>? headers}) =>
    http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json', ...?headers});

/// A fake pool that hands out keys in order and records requests, replaying
/// the server's real response shapes.
class FakePool {
  FakePool(this.keys);

  final List<int> keys;
  int next = 0;
  bool busy = false;
  bool rotateReturnsNext = true;
  final List<http.Request> requests = [];

  List<Map<String, dynamic>> get reports => [
        for (final r in requests)
          if (r.url.path.endsWith('/report')) jsonDecode(r.body) as Map<String, dynamic>
      ];

  List<Map<String, dynamic>> get leases => [
        for (final r in requests)
          if (r.url.path == '/api/v1/lease') jsonDecode(r.body) as Map<String, dynamic>
      ];

  http.Response _issue(List<int> exclude) {
    while (next < keys.length && exclude.contains(keys[next])) {
      next++;
    }
    if (next >= keys.length) {
      return jsonResponse({
        'error': {'code': 'no_key_available', 'message': 'every key for this service is cooling down, exhausted or disabled'},
        'retry_after_sec': 42,
      }, status: 503, headers: {'retry-after': '42'});
    }
    return jsonResponse(leaseJson(keys[next++]));
  }

  Future<http.Response> handle(http.Request r) async {
    requests.add(r);
    if (r.headers['Authorization'] != 'Bearer $token') {
      return jsonResponse({
        'error': {'code': 'unauthenticated', 'message': 'invalid app token'}
      }, status: 401);
    }
    final body = r.body.isEmpty ? <String, dynamic>{} : jsonDecode(r.body) as Map<String, dynamic>;
    if (r.url.path == '/api/v1/lease') {
      if (busy) {
        return jsonResponse({
          'error': {'code': 'all_keys_busy', 'message': 'every usable key is at its concurrency limit, retry shortly'},
          'retry_after_sec': 1,
        }, status: 503);
      }
      return _issue([for (final e in (body['exclude_key_ids'] as List<dynamic>? ?? [])) e as int]);
    }
    if (r.url.path.endsWith('/report')) {
      if (body['rotate'] == true && body['outcome'] != 'ok' && rotateReturnsNext) {
        final n = _issue([]);
        if (n.statusCode == 200) return jsonResponse({'ok': true, 'next': jsonDecode(n.body)});
      }
      return jsonResponse({'ok': true, 'next': null});
    }
    return jsonResponse({
      'error': {'code': 'not_found', 'message': 'not found'}
    }, status: 404);
  }

  Acnologia client() => Acnologia(baseUrl: base, token: token, httpClient: MockClient(handle));
}

Future<Object> caught(Future<Object?> Function() f) async {
  try {
    await f();
  } catch (e) {
    return e;
  }
  fail('expected an exception');
}

void main() {
  group('client', () {
    test('services sends token and user agent', () async {
      late http.Request seen;
      final c = Acnologia(
        baseUrl: '$base/',
        token: token,
        httpClient: MockClient((r) async {
          seen = r;
          return jsonResponse({
            'services': [
              {
                'slug': 'serpapi',
                'name': 'SerpAPI',
                'provider': 'serpapi',
                'category': 'search',
                'available': true,
                'base_url': 'https://serpapi.com',
                'auth': {'type': 'query', 'name': 'api_key'},
              }
            ]
          });
        }),
      );
      final svcs = await c.services();
      expect(svcs, hasLength(1));
      expect(svcs.first.slug, 'serpapi');
      expect(svcs.first.available, isTrue);
      expect(svcs.first.baseUrl, 'https://serpapi.com');
      expect(svcs.first.auth, const Auth(type: 'query', name: 'api_key'));
      expect(seen.method, 'GET');
      expect(seen.url.toString(), '$base/api/v1/services');
      expect(seen.headers['Authorization'], 'Bearer acn_test');
      expect(seen.headers['User-Agent'], 'acnologia-dart/${Acnologia.version}');
    });

    test('fromEnvironment', () {
      expect(() => Acnologia.fromEnvironment(environment: {}), throwsArgumentError);
      expect(() => Acnologia.fromEnvironment(environment: {'ACNOLOGIA_URL': base}), throwsArgumentError);
      final c = Acnologia.fromEnvironment(environment: {'ACNOLOGIA_URL': '$base/', 'ACNOLOGIA_TOKEN': 'cog_legacy'});
      expect(c.baseUrl, base);
    });

    test('lease body and decode', () async {
      final pool = FakePool([7]);
      final lease = await pool.client().lease('serpapi',
          excludeKeyIds: [1, 2],
          ttl: const Duration(milliseconds: 30500),
          wait: const Duration(milliseconds: 9200),
          exclusive: true);
      expect(lease.leaseId, 'lse_7');
      expect(lease.keyId, 7);
      expect(lease.secret, 'sk7');
      expect(lease.baseUrl, 'https://serpapi.com');
      expect(lease.expiresAt, isNotNull);
      expect(pool.leases.single, {
        'service': 'serpapi',
        'exclude_key_ids': [1, 2],
        'ttl_sec': 31,
        'wait_sec': 10,
        'exclusive': true,
      });
      expect(pool.requests.single.headers['Content-Type'], startsWith('application/json'));
    });

    test('lease omits defaults and rejects a bad wait', () async {
      final pool = FakePool([1]);
      await pool.client().lease('serpapi');
      expect(pool.leases.single.keys, ['service']);
      expect(() => pool.client().lease('serpapi', wait: const Duration(seconds: 61)), throwsRangeError);
    });

    test('error mapping', () async {
      final cases = <(int, String, Matcher)>[
        (
          503,
          '{"error":{"code":"no_key_available","message":"cooling"},"retry_after_sec":42}',
          isA<NoKeyAvailableException>()
              .having((e) => e.retryAfter, 'retryAfter', const Duration(seconds: 42))
              .having((e) => e.busy, 'busy', isFalse)
              .having((e) => e, 'type', isNot(isA<AllKeysBusyException>()))
        ),
        (
          503,
          '{"error":{"code":"all_keys_busy","message":"busy"},"retry_after_sec":1}',
          isA<AllKeysBusyException>()
              .having((e) => e.busy, 'busy', isTrue)
              .having((e) => e.retryAfter, 'retryAfter', const Duration(seconds: 1))
        ),
        (
          503,
          '{"error":{"code":"service_unavailable","message":"x keeps failing"},"retry_after_sec":30}',
          isA<ServiceUnavailableException>().having((e) => e.retryAfter, 'retryAfter', const Duration(seconds: 30))
        ),
        (503, '', isA<NoKeyAvailableException>()),
        (
          401,
          '{"error":{"code":"unauthenticated","message":"invalid app token"}}',
          isA<UnauthenticatedException>().having((e) => e.message, 'message', 'invalid app token')
        ),
        (
          403,
          '{"error":{"code":"service_not_allowed","message":"no"}}',
          isA<ForbiddenException>().having((e) => e.code, 'code', 'service_not_allowed')
        ),
        (
          429,
          '{"error":{"code":"app_limit_reached","message":"daily limit"}}',
          isA<AcnologiaApiException>()
              .having((e) => e.statusCode, 'statusCode', 429)
              .having((e) => e.code, 'code', 'app_limit_reached')
        ),
        (500, 'boom', isA<AcnologiaApiException>().having((e) => e.message, 'message', 'boom')),
      ];
      for (final (status, body, matcher) in cases) {
        final c = Acnologia(baseUrl: base, token: token, httpClient: MockClient((_) async => http.Response(body, status)));
        expect(await caught(() => c.lease('serpapi')), matcher, reason: body);
      }
    });

    test('report returns next and sends the right body', () async {
      final pool = FakePool([1, 2]);
      final lease = await pool.client().lease('serpapi');
      final next = await lease.report(Outcome.rateLimited(const Duration(milliseconds: 2500)),
          statusCode: 429, message: 'slow down', rotate: true);
      expect(next?.keyId, 2);
      expect(pool.requests[1].url.path, '/api/v1/leases/lse_1/report');
      expect(pool.reports.first, {
        'outcome': 'rate_limited',
        'status_code': 429,
        'retry_after_sec': 3,
        'message': 'slow down',
        'rotate': true,
      });
      expect(await next!.report(Outcome.ok), isNull);
      expect(pool.reports.last, {'outcome': 'ok'});
    });

    test('outcome wire values', () {
      expect(
        [
          Outcome.ok,
          Outcome.rateLimited(),
          Outcome.quotaExhausted,
          Outcome.invalid,
          Outcome.error,
          Outcome.busy,
          Outcome.banned,
        ].map((o) => o.wireValue),
        ['ok', 'rate_limited', 'quota_exhausted', 'invalid', 'error', 'busy', 'banned'],
      );
    });

    test('renew', () async {
      final requests = <http.Request>[];
      var renewStatus = 200;
      final c = Acnologia(
        baseUrl: base,
        token: token,
        httpClient: MockClient((r) async {
          requests.add(r);
          if (!r.url.path.endsWith('/renew')) return jsonResponse(leaseJson(1));
          if (renewStatus == 404) {
            return jsonResponse({
              'error': {'code': 'lease_not_found', 'message': 'lease not found, already reported, or expired'}
            }, status: 404);
          }
          return jsonResponse({'ok': true, 'expires_at': '2026-11-01T13:00:00Z'});
        }),
      );
      final lease = await c.lease('serpapi');
      expect(await lease.renew(ttl: const Duration(minutes: 10)), DateTime.utc(2026, 11, 1, 13));
      expect(requests[1].url.path, '/api/v1/leases/lse_1/renew');
      expect(jsonDecode(requests[1].body), {'ttl_sec': 600});
      renewStatus = 404;
      expect(
          await caught(() => lease.renew()),
          isA<AcnologiaApiException>()
              .having((e) => e.code, 'code', 'lease_not_found')
              .having((e) => e.statusCode, 'statusCode', 404));
    });

    test('apply places the secret', () async {
      Future<Lease> lease(Map<String, dynamic> auth) => Acnologia(
              baseUrl: base,
              token: token,
              httpClient: MockClient((_) async => jsonResponse(leaseJson(1, auth: auth, secret: 's+k/1'))))
          .lease('svc');

      final q = (await lease({'type': 'query', 'name': 'api_key'}))
          .apply(Uri.parse('https://serpapi.com/search.json?q=coffee&api_key=old'));
      expect(q.url.queryParameters, {'q': 'coffee', 'api_key': 's+k/1'});
      expect(q.url.toString(), contains('api_key=s%2Bk%2F1'));

      final h = (await lease({'type': 'header', 'name': 'xi-api-key'})).apply(Uri.parse('https://x'), {'a': 'b'});
      expect(h.headers, {'a': 'b', 'xi-api-key': 's+k/1'});

      final b = (await lease({'type': 'bearer', 'name': ''})).apply(Uri.parse('https://x'));
      expect(b.headers, {'Authorization': 'Bearer s+k/1'});

      final t = (await lease({'type': 'bearer', 'name': 'Authorization', 'prefix': 'Token '})).apply(Uri.parse('https://x'));
      expect(t.headers, {'Authorization': 'Token s+k/1'});

      final l = await lease({'type': 'query', 'name': 'key'});
      expect(l.url('/search.json', {'q': 'a b&c'}).toString(), 'https://serpapi.com/search.json?q=a+b%26c');
      final req = l.request('get', 'search.json', query: {'q': 'x'});
      expect(req.method, 'GET');
      expect(req.url.queryParameters, {'q': 'x', 'key': 's+k/1'});
    });

    test('secret and token never appear in toString', () async {
      final c = Acnologia(
          baseUrl: base,
          token: 'acn_supersecret_token',
          httpClient: MockClient((_) async => jsonResponse(leaseJson(1, secret: 'supersecret'))));
      final lease = await c.lease('serpapi');
      for (final s in ['$lease', lease.toString(), '$c', '${c.proxy('serpapi')}']) {
        expect(s, isNot(contains('supersecret')));
      }
      expect('$lease', contains('[redacted]'));
    });

    test('scrubMessage', () {
      expect(scrubMessage('GET https://x/?api_key=a%2Bb failed, a+b', 'a+b'), 'GET https://x/?api_key=[key] failed, [key]');
      expect(scrubMessage('x' * 600, ''), hasLength(500));
    });
  });

  group('withKey', () {
    test('success reports ok', () async {
      final pool = FakePool([1]);
      final v = await pool.client().withKey('serpapi', (lease) async => 'used ${lease.keyId}');
      expect(v, 'used 1');
      expect(pool.reports, [
        {'outcome': 'ok'}
      ]);
    });

    test('rotates on rate limit and quota', () async {
      final pool = FakePool([1, 2, 3]);
      final seen = <int>[];
      final v = await pool.client().withKey('serpapi', (lease) async {
        seen.add(lease.keyId);
        if (lease.keyId == 1) {
          throw const RateLimitedException(retryAfter: Duration(seconds: 60), statusCode: 429, message: 'key sk1 limited');
        }
        if (lease.keyId == 2) throw const QuotaExhaustedException(statusCode: 429);
        return 42;
      });
      expect(v, 42);
      expect(seen, [1, 2, 3]);
      expect(pool.reports.map((r) => r['outcome']), ['rate_limited', 'quota_exhausted', 'ok']);
      expect(pool.reports.first, {
        'outcome': 'rate_limited',
        'status_code': 429,
        'retry_after_sec': 60,
        'message': 'key [key] limited',
        'rotate': true,
      });
    });

    test('leases again when the report has no next', () async {
      final pool = FakePool([1, 2])..rotateReturnsNext = false;
      final seen = <int>[];
      await pool.client().withKey('serpapi', (lease) {
        seen.add(lease.keyId);
        if (lease.keyId == 1) throw const InvalidKeyException(statusCode: 401);
      }, excludeKeyIds: [9], ttl: const Duration(seconds: 30));
      expect(seen, [1, 2]);
      expect(pool.leases[0]['exclude_key_ids'], [9]);
      expect(pool.leases[1]['exclude_key_ids'], [9, 1]);
      expect(pool.leases[1]['ttl_sec'], 30);
      expect(pool.reports.first['outcome'], 'invalid');
    });

    test('gives up after maxAttempts with the last rotation error', () async {
      final pool = FakePool([1, 2, 3, 4]);
      var calls = 0;
      final e = await caught(() => pool.client().withKey<void>('serpapi', (_) {
            calls++;
            throw const RateLimitedException(retryAfter: Duration(seconds: 5));
          }, maxAttempts: 2));
      expect(e, isA<RateLimitedException>().having((e) => e.retryAfter, 'retryAfter', const Duration(seconds: 5)));
      expect(calls, 2);
      expect(pool.reports, hasLength(2));
      expect(pool.reports[0]['rotate'], isTrue);
      expect(pool.reports[1].containsKey('rotate'), isFalse, reason: 'the final attempt must not ask for a next key');
    });

    test('other errors report error and rethrow', () async {
      final pool = FakePool([1, 2]);
      final e = await caught(() => pool.client().withKey<void>('serpapi', (_) async {
            throw StateError('upstream failed for https://serpapi.com/search?api_key=sk1');
          }));
      expect(e, isA<StateError>());
      expect(pool.reports, [
        {'outcome': 'error', 'message': 'Bad state: upstream failed for https://serpapi.com/search?api_key=[key]'}
      ]);
    });

    test('no key available when the pool runs dry', () async {
      final pool = FakePool([1]);
      final e = await caught(
          () => pool.client().withKey<void>('serpapi', (_) => throw const InvalidKeyException()));
      expect(e, isA<NoKeyAvailableException>().having((e) => e.retryAfter, 'retryAfter', const Duration(seconds: 42)));
    });

    test('all keys busy on the first lease', () async {
      final pool = FakePool([1])..busy = true;
      final e = await caught(() => pool.client().withKey('serpapi', (_) => 1, wait: const Duration(seconds: 5)));
      expect(e, isA<AllKeysBusyException>());
      expect(pool.leases.single['wait_sec'], 5);
    });
  });

  group('proxy', () {
    test('get routes under the proxy prefix and exposes headers', () async {
      late http.BaseRequest seen;
      final c = Acnologia(
          baseUrl: base,
          token: token,
          httpClient: MockClient((r) async {
            seen = r;
            return http.Response('{"ok":1}', 200, headers: {
              'x-acnologia-key-id': '7',
              'x-acnologia-attempts': '2',
              'x-acnologia-service': 'serpapi',
            });
          }));
      final resp = await c.proxy('serpapi').get('/search.json', query: {'q': 'coffee', 'n': 3});
      expect(resp.statusCode, 200);
      expect(resp.acnologiaKeyId, 7);
      expect(resp.acnologiaAttempts, 2);
      expect(resp.acnologiaService, 'serpapi');
      expect(seen.url.toString(), '$base/proxy/serpapi/search.json?q=coffee&n=3');
      expect(seen.headers['Authorization'], 'Bearer acn_test');
      expect(seen.followRedirects, isFalse);
    });

    test('post encodes maps as JSON and keeps strings', () async {
      final seen = <http.Request>[];
      final p = Acnologia(
          baseUrl: base,
          token: token,
          httpClient: MockClient((r) async {
            seen.add(r);
            return http.Response('audio', 200, headers: {'x-acnologia-key-id': '1'});
          })).proxy('elevenlabs');
      final resp = await p.post('v1/text-to-speech/abc', body: {'text': 'hi'});
      expect(resp.body, 'audio');
      expect(seen[0].method, 'POST');
      expect(seen[0].url.path, '/proxy/elevenlabs/v1/text-to-speech/abc');
      expect(seen[0].headers['Content-Type'], startsWith('application/json'));
      expect(jsonDecode(seen[0].body), {'text': 'hi'});
      await p.put('/proxy/elevenlabs/v1/x', body: 'raw', headers: {'Content-Type': 'text/plain'});
      expect(seen[1].url.path, '/proxy/elevenlabs/v1/x');
      expect(seen[1].body, 'raw');
    });

    test('upstream errors are returned, Acnologia errors are typed', () async {
      var upstream = true;
      final p = Acnologia(
          baseUrl: base,
          token: token,
          httpClient: MockClient((_) async => upstream
              ? http.Response('{"error":{"code":"x","message":"upstream"}}', 429, headers: {'x-acnologia-key-id': '3'})
              : jsonResponse({
                  'error': {'code': 'service_unavailable', 'message': 'serpapi keeps failing'},
                  'retry_after_sec': 30,
                }, status: 503))).proxy('serpapi');
      final r1 = await p.get('/search.json');
      expect(r1.statusCode, 429);
      expect(r1.acnologiaError, isNull);
      upstream = false;
      final r2 = await p.get('/search.json');
      expect(r2.acnologiaError,
          isA<ServiceUnavailableException>().having((e) => e.retryAfter, 'retryAfter', const Duration(seconds: 30)));
      expect(r2.throwIfAcnologiaError, throwsA(isA<ServiceUnavailableException>()));
    });
  });

  group('0.2.0 surface', () {
    /// A client whose server answers every request with [answer] and records it.
    (Acnologia, List<http.Request>) recording(http.Response Function(http.Request r) answer,
        {String? accessPassword}) {
      final seen = <http.Request>[];
      final c = Acnologia(
          baseUrl: base,
          token: token,
          accessPassword: accessPassword,
          httpClient: MockClient((r) async {
            seen.add(r);
            return answer(r);
          }));
      return (c, seen);
    }

    final fpJson = {
      'proxy_url': 'http://acn.test:8888',
      'proxy_url_with_token': 'http://app:%3Capp%20token%3E@acn.test:8888',
      'ca_url': '$base/api/v1/proxy/ca.pem',
      'ca_fingerprint': 'SHA256:ab:cd',
      'passthrough': true,
      'hosts': [
        {'host': 'serpapi.com', 'service': 'serpapi', 'name': 'SerpAPI', 'path': ''}
      ],
      'env': {'HTTPS_PROXY': 'http://app:%3Capp%20token%3E@acn.test:8888', 'NODE_USE_ENV_PROXY': '1'},
      'service_header': 'X-Acnologia-Service',
    };

    test('version is 0.2.0', () {
      expect(Acnologia.version, '0.2.0');
      expect(Acnologia.defaultUserAgent, 'acnologia-dart/0.2.0');
    });

    test('me parses app, limits, usage, services and share', () async {
      final (c, seen) = recording((_) => jsonResponse({
            'app': {'id': 3, 'name': 'Website'},
            'limits': {'daily_requests': 500, 'monthly_requests': null, 'daily_budget': null, 'monthly_budget': 20.5},
            'usage': {
              'today_requests': 12,
              'today_cost': 0.5,
              'month_requests': 40,
              'month_cost': 2,
              'last_used_at': '2026-11-04T09:00:00Z',
              'limit_hit': null,
            },
            'limit_hit': {'limit': 'daily_requests', 'reset_at': '2026-11-05T00:00:00Z'},
            'services': [
              {'slug': 'serpapi', 'name': 'SerpAPI', 'provider': 'serpapi', 'category': 'search', 'available': 3, 'logo_url': null}
            ],
            'share': {
              'id': 'shr_1',
              'name': 'For Bob',
              'owner_name': 'Alice',
              'recipient': 'person',
              'label': 'bob',
              'status': 'paused',
              'mode': 'proxy',
              'ends_at': null,
            },
          }));
      final me = await c.me();
      expect(seen.single.method, 'GET');
      expect(seen.single.url.toString(), '$base/api/v1/me');
      expect(seen.single.headers['Authorization'], 'Bearer $token');
      expect(me.app.id, 3);
      expect(me.app.name, 'Website');
      expect(me.limits.dailyRequests, 500);
      expect(me.limits.monthlyRequests, isNull);
      expect(me.limits.monthlyBudget, 20.5);
      expect(me.usage.todayRequests, 12);
      expect(me.usage.todayCost, 0.5);
      expect(me.usage.lastUsedAt, DateTime.utc(2026, 11, 4, 9));
      expect(me.limitHit?.limit, 'daily_requests');
      expect(me.limitHit?.resetAt, DateTime.utc(2026, 11, 5));
      expect(me.services.single.slug, 'serpapi');
      expect(me.services.single.available, 3);
      expect(me.services.single.logoUrl, isNull);
      expect(me.share?.status, 'paused');
      expect(me.share?.ownerName, 'Alice');
      expect(me.raw['app'], isA<Map<String, dynamic>>());
    });

    test('services carry guide_url', () async {
      final (c, _) = recording((_) => jsonResponse({
            'services': [
              {'slug': 'serpapi', 'guide_url': '$base/api/v1/services/serpapi/guide'}
            ]
          }));
      expect((await c.services()).single.guideUrl, '$base/api/v1/services/serpapi/guide');
    });

    test('guide and guideMarkdown', () async {
      final (c, seen) = recording((r) => r.headers['Accept'] == 'text/markdown'
          ? http.Response.bytes(utf8.encode('# SerpAPI é'), 200, headers: {'content-type': 'text/markdown'})
          : jsonResponse({
              'markdown': '# SerpAPI',
              'generated': true,
              'guide': {'summary': 'search', 'endpoints': []}
            }));
      final g = await c.guide('serp api');
      expect(seen[0].url.path, '/api/v1/services/serp%20api/guide');
      expect(seen[0].headers['Accept'], 'application/json');
      expect(g.markdown, '# SerpAPI');
      expect(g.generated, isTrue);
      expect((g.guide as Map)['summary'], 'search');

      expect(await c.guideMarkdown('serpapi'), '# SerpAPI é');
      expect(seen[1].method, 'GET');
      expect(seen[1].url.path, '/api/v1/services/serpapi/guide');
      expect(seen[1].headers['Accept'], 'text/markdown');
      expect(seen[1].headers['Authorization'], 'Bearer $token');
    });

    test('guide of a service not allowed is forbidden', () async {
      final (c, _) = recording((_) => jsonResponse({
            'error': {'code': 'service_not_allowed', 'message': 'no'}
          }, status: 403));
      expect(await caught(() => c.guideMarkdown('x')),
          isA<ForbiddenException>().having((e) => e.code, 'code', 'service_not_allowed'));
    });

    test('capabilities', () async {
      final (c, seen) = recording((_) => jsonResponse({
            'capabilities': [
              {
                'slug': 'places',
                'name': 'Places',
                'description': 'find places',
                'strategy': 'priority',
                'params': [
                  {'name': 'q', 'type': 'string', 'required': true}
                ],
                'routes': [
                  {'service': 'serpapi', 'mapped': true},
                  {'service': 'gmaps', 'mapped': false}
                ],
                'url': '$base/proxy/places',
              }
            ]
          }));
      final caps = await c.capabilities();
      expect(seen.single.url.path, '/api/v1/capabilities');
      expect(caps.single.slug, 'places');
      expect(caps.single.strategy, 'priority');
      expect((caps.single.params as List).single['name'], 'q');
      expect(caps.single.routes,
          [const CapabilityRoute(service: 'serpapi', mapped: true), const CapabilityRoute(service: 'gmaps', mapped: false)]);
      expect(caps.single.url, '$base/proxy/places');
    });

    test('capability posts params and reads the answer headers', () async {
      final (c, seen) = recording((_) => http.Response('{"items":[],"count":0}', 200, headers: {
            'x-acnologia-service': 'gmaps',
            'x-acnologia-capability': 'places',
            'x-acnologia-tried': 'serpapi=429, gmaps=200',
            'x-acnologia-key-id': '4',
          }));
      final resp = await c.capability('places', {'q': 'coffee'},
          service: 'gmaps', raw: true, idempotencyKey: 'k1', noCache: true, headers: {'X-Trace': 't'});
      final r = seen.single;
      expect(r.method, 'POST');
      expect(r.url.path, '/proxy/places');
      expect(r.url.queryParameters, {'_service': 'gmaps', '_raw': '1'});
      expect(r.headers['Authorization'], 'Bearer $token');
      expect(r.headers['Content-Type'], startsWith('application/json'));
      expect(r.headers['Idempotency-Key'], 'k1');
      expect(r.headers['Cache-Control'], 'no-cache');
      expect(r.headers['X-Trace'], 't');
      expect(jsonDecode(r.body), {'q': 'coffee'});
      expect(resp.acnologiaService, 'gmaps');
      expect(resp.acnologiaCapability, 'places');
      expect(resp.acnologiaTried,
          [const TriedRoute(service: 'serpapi', status: 429), const TriedRoute(service: 'gmaps', status: 200)]);

      await c.capability('places', {});
      expect(seen[1].url.toString(), '$base/proxy/places');
      expect(seen[1].headers.containsKey('Idempotency-Key'), isFalse);
      expect(seen[1].headers.containsKey('Cache-Control'), isFalse);
    });

    test('capability bad_params is an Acnologia error', () async {
      final (c, _) = recording((_) => jsonResponse({
            'error': {'code': 'bad_params', 'message': 'q is required'}
          }, status: 400));
      final resp = await c.capability('places', {});
      expect(resp.acnologiaError,
          isA<AcnologiaApiException>().having((e) => e.code, 'code', 'bad_params').having((e) => e.statusCode, 's', 400));
    });

    test('proxy idempotencyKey, noCache and the new accessors', () async {
      final (c, seen) = recording((_) => http.Response('{}', 200, headers: {
            'x-acnologia-key-id': '7',
            'x-acnologia-attempts': '1',
            'x-acnologia-service': 'serpapi',
            'x-acnologia-cache': 'hit',
            'idempotent-replayed': 'true',
          }));
      final resp = await c.proxy('serpapi').post('/x', body: {'a': 1}, idempotencyKey: 'order-42', noCache: true);
      expect(seen.single.headers['Idempotency-Key'], 'order-42');
      expect(seen.single.headers['Cache-Control'], 'no-cache');
      expect(resp.acnologiaKeyId, 7);
      expect(resp.acnologiaCache, 'hit');
      expect(resp.acnologiaReplayed, isTrue);
      expect(resp.acnologiaCapability, isNull);
      expect(resp.acnologiaTried, isEmpty);

      await c.proxy('serpapi').get('/y');
      expect(seen[1].headers.containsKey('Idempotency-Key'), isFalse);
      expect(seen[1].headers.containsKey('Cache-Control'), isFalse);
      expect((await c.proxy('serpapi').get('/z')).acnologiaKeyId, 7);
    });

    test('accessPassword is sent on every request, also from the environment', () async {
      final (c, seen) = recording((_) => jsonResponse({'services': []}), accessPassword: 'gatepw');
      await c.services();
      await c.proxy('serpapi').get('/x');
      expect(seen.map((r) => r.headers['X-Acnologia-Access']), ['gatepw', 'gatepw']);
      expect('$c', isNot(contains('gatepw')));

      final (plain, seen2) = recording((_) => jsonResponse({'services': []}));
      await plain.services();
      expect(seen2.single.headers.containsKey('X-Acnologia-Access'), isFalse);

      final env = <http.Request>[];
      await Acnologia.fromEnvironment(
          environment: {'ACNOLOGIA_URL': base, 'ACNOLOGIA_TOKEN': token, 'ACNOLOGIA_ACCESS_PASSWORD': 'envpw'},
          httpClient: MockClient((r) async {
            env.add(r);
            return jsonResponse({'services': []});
          })).services();
      expect(env.single.headers['X-Acnologia-Access'], 'envpw');
    });

    test('forwardProxy parses the setup and hides nothing secret', () async {
      final (c, seen) = recording((_) => jsonResponse(fpJson));
      final fp = await c.forwardProxy();
      expect(seen.single.method, 'GET');
      expect(seen.single.url.path, '/api/v1/proxy');
      expect(seen.single.headers['Authorization'], 'Bearer $token');
      expect(fp.proxyUrl, 'http://acn.test:8888');
      expect(fp.caUrl, '$base/api/v1/proxy/ca.pem');
      expect(fp.caFingerprint, 'SHA256:ab:cd');
      expect(fp.passthrough, isTrue);
      expect(fp.hosts.single.host, 'serpapi.com');
      expect(fp.hosts.single.service, 'serpapi');
      expect(fp.env['NODE_USE_ENV_PROXY'], '1');
      expect(fp.serviceHeader, 'X-Acnologia-Service');
      expect('$fp', isNot(contains(token)));
    });

    test('forwardProxyCA sends no token', () async {
      final (c, seen) = recording((_) =>
          http.Response('-----BEGIN CERTIFICATE-----\nAAA\n-----END CERTIFICATE-----\n', 200,
              headers: {'content-type': 'application/x-pem-file'}),
          accessPassword: 'gatepw');
      final pem = await c.forwardProxyCA();
      expect(pem, startsWith('-----BEGIN CERTIFICATE-----'));
      expect(seen.single.method, 'GET');
      expect(seen.single.url.path, '/api/v1/proxy/ca.pem');
      expect(seen.single.headers.containsKey('Authorization'), isFalse);
      expect(seen.single.headers['X-Acnologia-Access'], 'gatepw');
    });

    test('forwardProxyURL and forwardProxyEnv put the token in the proxy URL', () async {
      final seen = <http.Request>[];
      final c = Acnologia(
          baseUrl: base,
          token: 'acn_a/b+c',
          httpClient: MockClient((r) async {
            seen.add(r);
            return jsonResponse(fpJson);
          }));
      expect(await c.forwardProxyURL(), 'http://app:acn_a%2Fb%2Bc@acn.test:8888');
      expect(await c.forwardProxyEnv(), {
        'HTTPS_PROXY': 'http://app:acn_a%2Fb%2Bc@acn.test:8888',
        'HTTP_PROXY': 'http://app:acn_a%2Fb%2Bc@acn.test:8888',
        'NODE_USE_ENV_PROXY': '1',
        'NODE_EXTRA_CA_CERTS': 'acnologia-ca.pem',
        'REQUESTS_CA_BUNDLE': 'acnologia-ca.pem',
        'SSL_CERT_FILE': 'acnologia-ca.pem',
      });
      final env = await c.forwardProxyEnv('/etc/acn.pem');
      expect(env['SSL_CERT_FILE'], '/etc/acn.pem');
      expect(seen.every((r) => r.url.path == '/api/v1/proxy'), isTrue);
    });

    test('app_limit_reached is AppLimitReachedException', () async {
      final (c, _) = recording((_) => jsonResponse({
            'error': {'code': 'app_limit_reached', 'message': 'daily limit'},
            'limit': 'daily_requests',
            'reset_at': '2026-11-05T00:00:00Z',
            'retry_after_sec': 4210,
          }, status: 429));
      final e = await caught(() => c.me());
      expect(
          e,
          isA<AppLimitReachedException>()
              .having((e) => e.limit, 'limit', 'daily_requests')
              .having((e) => e.resetAt, 'resetAt', DateTime.utc(2026, 11, 5))
              .having((e) => e.retryAfter, 'retryAfter', const Duration(seconds: 4210))
              .having((e) => e.statusCode, 'statusCode', 429));
      expect(e, isA<AcnologiaApiException>());
      expect('$e', contains('daily_requests'));
    });

    test('share errors are ShareUnavailableException', () async {
      for (final st in ['revoked', 'paused', 'expired']) {
        final (c, _) = recording((_) => jsonResponse({
              'error': {'code': 'share_$st', 'message': 'share $st'}
            }, status: 403, headers: {'x-acnologia-share': st}));
        final e = await caught(() => c.services());
        expect(e, isA<ShareUnavailableException>().having((e) => e.status, 'status', st).having((e) => e.code, 'code', 'share_$st'));
        expect(e, isA<ForbiddenException>());
        final resp = await c.proxy('serpapi').get('/x');
        expect(resp.acnologiaError, isA<ShareUnavailableException>().having((e) => e.status, 'status', st));
      }
      final (c, _) = recording((_) => http.Response('forbidden', 403, headers: {'x-acnologia-share': 'revoked'}));
      expect(await caught(() => c.services()), isA<ShareUnavailableException>().having((e) => e.status, 'status', 'revoked'));
    });

    test('saveForwardProxyCA writes the PEM on dart:io', () async {
      final (c, _) = recording((_) => http.Response('PEM', 200));
      final dir = Directory.systemTemp.createTempSync('acn');
      try {
        final path = await c.saveForwardProxyCA('${dir.path}/ca.pem');
        expect(File(path).readAsStringSync(), 'PEM');
      } finally {
        dir.deleteSync(recursive: true);
      }
    }, testOn: 'vm');
  });
}
