import 'dart:convert';

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
}
