# Acnologia for Ruby

Call APIs with the keys your Acnologia server holds, using your app's own `acn_` token. In proxy mode the server attaches the key, moves to the next key on rate limits and quota errors, and the secret never reaches your process. In lease mode you borrow a key for code that needs it raw, and `with_key` reports and rotates for you.

Ruby 2.6 or later. Standard library only (`net/http`, `json`).

## Quick start

```ruby
require "acnologia"

acn = Acnologia::Client.new("http://localhost:8080", "acn_your_app_token")

res = acn.proxy("weather").get("/v1/forecast", { "city" => "Lisbon" })
puts res.status, res.json if res.ok?
```

`weather` is the slug of a service on your server. The request goes to `http://localhost:8080/proxy/weather/v1/forecast?city=Lisbon`.

## Install

The gem is not on RubyGems yet. Add it from git in your Gemfile, then run `bundle install`:

```ruby
gem "acnologia", git: "https://github.com/sidharthpunathil/acnologia.git", glob: "sdk/ruby/*.gemspec"
```

## Connect

Create an app in the dashboard and copy its token (`acn_...`, shown once).

```ruby
acn = Acnologia::Client.new("http://localhost:8080", "acn_your_app_token")

# or read ACNOLOGIA_URL and ACNOLOGIA_TOKEN from the environment
acn = Acnologia::Client.new
```

The constructor raises `ArgumentError` when the URL or the token is missing. It also takes `timeout:` (seconds, default 30) and `transport:` (see [Tests](#tests)).

`acn.services` returns the services this token may use, as an array of hashes from `GET /api/v1/services`.

## Proxy mode

`acn.proxy(service)` returns an `Acnologia::Proxy` with `get`, `post`, `put`, `delete` and `request`:

```ruby
proxy = acn.proxy("weather")

proxy.get("/v1/forecast", { "city" => "Lisbon" }, headers: { "Accept" => "application/json" })
proxy.post("/v1/alerts", { "city" => "Lisbon", "level" => "high" })
proxy.request("PATCH", "/v1/alerts/42", query: {}, headers: {}, body: { "level" => "low" })
```

Hash and Array bodies are sent as JSON with `Content-Type: application/json`; a String body is sent as is. `proxy.url(path, query)` returns the full proxy URL.

Every call returns an `Acnologia::Response`, including non 2xx responses: proxy calls never raise for a provider status. It has `status`, `headers` (lower cased names), `body`, `ok?`, `json` and `res["X-Acnologia-Key-Id"]`, the id of the key that served the request.

## Lease mode

Use a lease when a library insists on holding the raw key.

```ruby
lease = acn.lease("weather")
query, headers = lease.apply(query: { "city" => "Lisbon" })
url = lease.url("/v1/forecast", query)
# call the provider with url and headers, then:
lease.report("ok")
```

`acn.lease(service, exclude_key_ids: nil, ttl_sec: nil, wait_sec: nil, exclusive: false)` returns an `Acnologia::Lease`:

| Field | Meaning |
|---|---|
| `lease_id`, `key_id`, `service` | Which lease and key this is |
| `secret` | The provider key. Never log it |
| `auth` | Hash with `"type"`, `"name"` and `"prefix"` |
| `base_url` | The provider's base URL |
| `expires_at` | When the lease ends. `renew` updates it |
| `exclusive` / `exclusive?` | True when no other caller can get this key |
| `reported` | True once the lease has been reported |

`inspect` and `to_s` print `secret=***`.

### Put the key on the request

`lease.apply(query: {}, headers: {})` returns `[query, headers]` copies with the secret placed where `auth["type"]` says:

- `query`: the query parameter named by `auth["name"]`
- `header`: the header named by `auth["name"]` (or `Authorization`)
- `bearer`: `Authorization: Bearer <secret>`, or the header named by `auth["name"]`

A non empty `auth["prefix"]` goes in front of the secret for every type. For `bearer` it replaces `Bearer `, so `"Token "` gives `Authorization: Token ...`.

`lease.url(path, query)` joins `base_url`, the path and an optional query hash.

### Report the outcome

```ruby
next_lease = lease.report("rate_limited", status_code: 429, retry_after_sec: 60, rotate: true)
```

`lease.report(outcome, status_code: nil, retry_after_sec: nil, message: nil, rotate: false)` accepts `ok`, `rate_limited`, `busy`, `quota_exhausted`, `invalid`, `banned` or `error`. With `rotate: true` it returns the next `Lease`, or `nil` when no other key is free.

Report on every path, including errors. With a manual lease, use `ensure`:

```ruby
lease = acn.lease("weather")
begin
  # call the provider
  lease.report("ok")
ensure
  lease.report("error") unless lease.reported
end
```

### Renew for long jobs

`lease.renew(ttl_sec: nil)` extends an open lease and updates `lease.expires_at`. `nil` uses the server default of 120 seconds, and the server caps it at 24 hours. `acn.renew(lease, ttl_sec: nil)` does the same and returns the new expiry without updating the lease. A lease that is no longer open raises `Acnologia::Error` with code `lease_not_found`.

## with_key

`with_key` leases a key, yields it, reports the outcome and returns the block's value:

```ruby
require "net/http"

forecast = acn.with_key("weather") do |lease|
  query, headers = lease.apply(query: { "city" => "Lisbon" })
  uri = URI(lease.url("/v1/forecast", query))
  req = Net::HTTP::Get.new(uri, headers)
  res = Net::HTTP.start(uri.hostname, uri.port, use_ssl: uri.scheme == "https") { |http| http.request(req) }
  case res.code.to_i
  when 429 then raise Acnologia::RateLimited.new(retry_after: res["retry-after"]&.to_i, status_code: 429)
  when 402 then raise Acnologia::QuotaExhausted.new(status_code: 402)
  when 401, 403 then raise Acnologia::InvalidKey.new(status_code: res.code.to_i)
  end
  JSON.parse(res.body)
end
```

Map the provider's responses to these exceptions to rotate:

| Raise in the block | Reported as | Then |
|---|---|---|
| nothing | `ok` | returns the block's value |
| `Acnologia::RateLimited` | `rate_limited` | retries with the next key |
| `Acnologia::QuotaExhausted` | `quota_exhausted` | retries with the next key |
| `Acnologia::InvalidKey` | `invalid` | retries with the next key |
| any other exception | `error` | re-raises it |

All three take `status_code:`, `retry_after:` (seconds) and an optional message, and subclass `Acnologia::KeyProblem`.

`acn.with_key(service, max_attempts: 5, ttl_sec: nil, wait_sec: nil, exclusive: false)` passes `ttl_sec`, `wait_sec` and `exclusive` to every lease it takes. After `max_attempts` keys it re-raises the last exception. It reports every lease, even when the block raises, `break`s, returns early or `throw`s, and it replaces the secret with `[key]` in any message it reports.

## Concurrency limits

Some services cap how many requests one key may run at once. Acnologia enforces the cap per key, so on such a service each lease holds one of the key's slots until you report it or its TTL runs out. The default TTL is 120 seconds on limited services and 300 otherwise.

- **Always report.** `with_key` does it for you. With a manual lease, report in `ensure`, or the slot stays taken until the TTL expires.
- **Wait instead of failing.** Pass `wait_sec:` (0 to 60) and the server waits that long for a free slot. The `net/http` read timeout is extended to cover the wait.

```ruby
forecast = acn.with_key("weather", wait_sec: 20) { |lease| fetch_forecast(lease) }

begin
  lease = acn.lease("weather")
rescue Acnologia::AllKeysBusy => e
  sleep(e.retry_after || 1)
  retry
end
```

Proxy mode handles this on the server: it queues proxied requests for up to the service's `queue_timeout_sec` and never sends more than the limit in parallel on one key.

## Errors

| Class | When |
|---|---|
| `Acnologia::Error` | Any non 2xx from Acnologia (`status`, `code`), or a transport failure (`status` 0) |
| `Acnologia::NoKeyAvailable` | 503 `no_key_available`: every key is cooling down, exhausted or excluded. `retry_after` in seconds |
| `Acnologia::AllKeysBusy` | 503 `all_keys_busy`: every usable key is at its concurrency limit. Subclass of `NoKeyAvailable`, `busy?` is true |
| `Acnologia::ServiceUnavailable` | 503 `service_unavailable`: the provider keeps failing and its circuit breaker is open. `retry_after` in seconds. Not a `NoKeyAvailable` |
| `Acnologia::RateLimited`, `QuotaExhausted`, `InvalidKey` | Raised by you inside `with_key` |

`AllKeysBusy` clears as soon as another caller reports, so a short sleep or `wait_sec:` usually fixes it. Because it subclasses `NoKeyAvailable`, a `rescue Acnologia::NoKeyAvailable` catches both; check `busy?` to tell them apart.

All of these except the `KeyProblem` classes subclass `Acnologia::Error`. A 401 (bad token) or 403 (for example `lease_not_allowed` on a proxy only service) is an `Acnologia::Error`: check `status` and `code`.

## Tests

```bash
ruby -Ilib -Itest test/test_acnologia.rb
```

To run without a network, pass a transport to the client, as the tests do:

```ruby
acn = Acnologia::Client.new("http://acn.test", "acn_test",
  transport: ->(method, url, headers, body) { Acnologia::Response.new(200, {}, "{}") })
```

## More

- [All SDKs](../README.md)
- [HTTP API and error codes](../../docs/api.md)
- [Request flows](../../docs/flows.md): what the proxy and leases do on the server
