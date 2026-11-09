# Acnologia for Ruby

Call APIs with the keys your Acnologia server holds, using your app's own `acn_` token. In proxy mode the server attaches the key, moves to the next key on rate limits and quota errors, and the secret never reaches your process. In lease mode you borrow a key for code that needs it raw, and `with_key` reports and rotates for you.

Ruby 3.1 or later. Standard library only (`net/http`, `json`, `uri`, `securerandom`, `time`). No runtime gems.

## Quick start

```ruby
require "acnologia"

acn = Acnologia::Client.new("https://keys.example.com", "acn_your_app_token")

res = acn.proxy("agi-labs").post("/v1/chat", { "prompt" => "Hello" })
puts res.status, res.json if res.ok?
```

`agi-labs` is the slug of a service on your server. The request goes to `https://keys.example.com/proxy/agi-labs/v1/chat`.

## Install

The gem is not on RubyGems yet. Pick one of these.

From git, in your Gemfile, then `bundle install`:

```ruby
gem "acnologia", git: "https://github.com/sidharthpunathil/acnologia.git", glob: "sdk/ruby/*.gemspec"
```

From a checkout, by path:

```ruby
gem "acnologia", path: "/path/to/acnologia/sdk/ruby"
```

Or build and install the gem:

```sh
cd sdk/ruby
gem build acnologia.gemspec
gem install ./acnologia-0.2.0.gem
```

## Connect

Create an app in the dashboard and copy its token (`acn_...`, shown once).

```ruby
acn = Acnologia::Client.new("https://keys.example.com", "acn_your_app_token")

# or read ACNOLOGIA_URL and ACNOLOGIA_TOKEN from the environment
acn = Acnologia::Client.new
```

The constructor raises `ArgumentError` when the URL or the token is missing. An argument you pass wins over the environment; `nil` or an empty string falls back to it.

| Option | Default | Meaning |
|---|---|---|
| `timeout:` | `30` | Seconds for both the connect and the read timeout |
| `open_timeout:` | `timeout` | Seconds to open the connection |
| `read_timeout:` | `timeout` | Seconds to wait for an answer. A lease with `wait_sec:` gets enough to cover the wait |
| `access_password:` | `nil` | The access password of a locked deploy. See [Access password](#access-password) |
| `max_retries:` | `0` | Retries after a 503 or a 429 `app_limit_reached`. See [Retries](#retries) |
| `max_retry_wait:` | `60` | Longest Retry-After, in seconds, that a retry will sleep |
| `idempotency:` | `false` | Send a new `Idempotency-Key` with every proxied POST and PATCH |
| `headers:` | `{}` | Extra headers sent with every request |
| `transport:` | `nil` | A callable for tests. See [Tests](#tests) |

`inspect` and `to_s` print `token=***`. The token never appears in an exception message.

## Proxy mode

`acn.proxy(service)` returns an `Acnologia::Proxy` with `get`, `head`, `options`, `post`, `put`, `patch`, `delete` and `request`:

```ruby
proxy = acn.proxy("agi-labs")

proxy.get("/v1/models", { "limit" => 10 }, headers: { "Accept" => "application/json" })
proxy.post("/v1/chat", { "prompt" => "Hello" })
proxy.patch("/v1/chats/42", { "title" => "Greeting" })
proxy.request("POST", "/v1/chat", query: {}, headers: {}, body: "raw text")
```

Hash and Array bodies are sent as JSON with `Content-Type: application/json`. A String body is sent as is. `proxy.url(path, query)` returns the full proxy URL.

Every call returns an `Acnologia::Response`. Proxy calls do not raise for a non 2xx status:

| Method | Meaning |
|---|---|
| `status`, `headers`, `body` | The raw answer. Header names are lower cased |
| `ok?` | True for a 2xx |
| `json` | The body parsed as JSON |
| `res["X-Acnologia-Key-Id"]` | Any header, by name |
| `key_id`, `attempts` | The key that served the request and how many keys the proxy tried. `key_id` is `nil` for a share's token |
| `service` | The service that answered (`X-Acnologia-Service`). For a group or capability, the member that served it |
| `cache` | `X-Acnologia-Cache`: `hit`, `miss` and so on, or `nil` |
| `replayed?` / `replayed` | True when the proxy replayed a stored answer for a repeated `Idempotency-Key` |
| `capability` | The capability that served the call (`X-Acnologia-Capability`), or `nil` |
| `tried` | The providers a capability or group call tried, as `Acnologia::Tried` (`service`, `status`) |
| `retry_after` | Seconds from the `Retry-After` header, or `nil` |
| `error` | The typed Acnologia error this answer carries, or `nil` |
| `raise_for_status!` | Raises `error` when there is one, else returns the response |

Every proxy method takes `no_cache: true`, which sends `Cache-Control: no-cache` so a service with a cache TTL calls the provider instead of answering from its cache:

```ruby
res = proxy.get("/v1/models", no_cache: true)
res.cache  # "bypass" or "miss", never "hit"
```

A `{service}` may also be a service group or a capability slug. The call is the same.

`error` maps only Acnologia's own error codes. A 401 or 403 from the provider stays a plain `Acnologia::Error`, so you can tell it apart from your own token being wrong.

### Idempotency

The proxy honors `Idempotency-Key`. A repeat of the same request with the same key within 10 minutes gets the stored answer and the provider is not called again.

```ruby
res = proxy.post("/v1/chat", { "prompt" => "Hello" }, idempotency_key: true)        # a new random key
res = proxy.post("/v1/orders", { "sku" => "a1" }, idempotency_key: "order-1234")     # your own key
res.replayed?  # true on a replay
```

`post`, `put` and `patch` take `idempotency_key:`. `true` sends a new random UUID. A String is sent as is. A key you already set in `headers:` is kept. With `Client.new(..., idempotency: true)`, every POST and PATCH gets a random key unless you pass `idempotency_key: false`.

## Lease mode

Use a lease when a library insists on holding the raw key.

```ruby
lease = acn.lease("agi-labs")
query, headers = lease.apply(headers: { "Content-Type" => "application/json" })
url = lease.url("/v1/chat", query)
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

Pass a block and the lease reports itself when the block ends: `ok` when it returns, the outcome of a `KeyProblem` it raises, or `error` for any other exception. The block's value is returned. A lease the block reported itself is not reported again.

```ruby
reply = acn.lease("agi-labs") do |lease|
  call_agi_labs(lease)
end
```

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

`lease.report(outcome, status_code: nil, retry_after_sec: nil, message: nil, rotate: false)` accepts `ok`, `rate_limited`, `busy`, `quota_exhausted`, `invalid`, `banned` or `error`. With `rotate: true` it returns the next `Lease`, or `nil` when no other key is free or the app reached a limit.

Report on every path, including errors. With a manual lease, use `ensure`:

```ruby
lease = acn.lease("agi-labs")
begin
  # call the provider
  lease.report("ok")
ensure
  lease.report("error") unless lease.reported
end
```

### Renew for long jobs

`lease.renew(ttl_sec: nil)` extends an open lease and updates `lease.expires_at`. `nil` uses the server default of 120 seconds, and the server caps it at 24 hours. `acn.renew(lease, ttl_sec: nil)` does the same and returns the new expiry without updating the lease. A lease that is no longer open raises `Acnologia::Error` with code `lease_not_found`.

```ruby
lease = acn.lease("agi-labs", ttl_sec: 300)
chunks.each do |chunk|
  lease.renew(ttl_sec: 300)
  send_chunk(lease, chunk)
end
lease.report("ok")
```

## with_key

`with_key` leases a key, yields it, reports the outcome and returns the block's value:

```ruby
require "net/http"

reply = acn.with_key("agi-labs") do |lease|
  query, headers = lease.apply(headers: { "Content-Type" => "application/json" })
  uri = URI(lease.url("/v1/chat", query))
  res = Net::HTTP.post(uri, JSON.generate("prompt" => "Hello"), headers)
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

## What this token can do

`acn.me` calls `GET /api/v1/me` and returns an `Acnologia::Me`:

```ruby
me = acn.me
me.app.name                  # "billing-bot"
me.limits.daily_requests     # 1000, or nil when unset
me.usage.today_requests      # 12
me.services.map(&:slug)      # ["agi-labs"]
me.limited?                  # true once a request or budget limit is reached
me.limit_hit&.reset_at       # when that limit resets
me.share?                    # true for a share's token
me.share&.status             # "active", "paused", "expired" or "revoked"
```

| Part | Fields |
|---|---|
| `app` | `id`, `name` |
| `limits` | `daily_requests`, `monthly_requests`, `daily_budget`, `monthly_budget` (each `nil` when unset) |
| `usage` | `today_requests`, `today_cost`, `month_requests`, `month_cost`, `last_used_at`, `limit_hit` |
| `limit_hit` | `limit`, `reset_at`, or `nil` |
| `services` | `slug`, `name`, `provider`, `category`, `available`, `logo_url`, and `available?` |
| `share` | `id`, `name`, `owner` (`name`, `id`), `recipient`, `label`, `status`, `mode`, `ends_at`, and `active?`. `nil` for an ordinary app |
| `raw` | The parsed JSON |

A share's token still gets an answer when its share is paused, expired or revoked. `share.status` says which, and `services` is empty.

`acn.services` returns the services this token may use, as hashes from `GET /api/v1/services`: `slug`, `name`, `provider`, `category`, `available`, `base_url`, `auth` and `guide_url`.

### Guides

```ruby
md = acn.guide_markdown("agi-labs")   # the usage guide as Markdown (acn.guide does the same)
g = acn.guide_doc("agi-labs")         # Acnologia::Guide
g.markdown                            # the same Markdown
g.generated?                          # true when the server built it from the provider spec
g.guide["endpoints"]                  # the structured guide, as parsed JSON
```

## Capabilities

A capability is one call (say `web-search`) that the server sends to whichever provider can serve it.

```ruby
acn.capabilities.each do |c|
  puts c.slug, c.description, c.strategy
  c.params                  # parsed JSON describing the params
  c.routes.map(&:service)   # providers it can use, each with mapped
end

res = acn.capability("web-search", { "query" => "coffee" })
res.json                    # the mapped answer
res.service                 # the provider that answered
res.tried.map { |t| "#{t.service}=#{t.status}" }

acn.capability("web-search", { "query" => "coffee" }, service: "tavily", raw: true)
```

`acn.capability(slug, params = nil, service: nil, raw: false, headers: {}, idempotency_key: nil, no_cache: false)` POSTs `params` as JSON to `/proxy/{slug}`. `service:` adds `?_service=` to use only that provider, and `raw: true` adds `?_raw=1` for the provider's own body. Like a proxy call, it returns the `Acnologia::Response` and does not raise for a non 2xx.

## Forward proxy

Apps can also call a provider's own URL with Acnologia as their `HTTPS_PROXY`. The proxy puts a key on the request. The app trusts the proxy's CA and uses its token as the proxy password.

```ruby
acn.save_forward_proxy_ca("acnologia-ca.pem")   # public route, sends no token
env = acn.forward_proxy_env("acnologia-ca.pem")
# {"HTTPS_PROXY"=>"http://app:acn_...@keys.example.com:8888", "HTTP_PROXY"=>..., "NODE_USE_ENV_PROXY"=>"1",
#  "NODE_EXTRA_CA_CERTS"=>"acnologia-ca.pem", "REQUESTS_CA_BUNDLE"=>"acnologia-ca.pem", "SSL_CERT_FILE"=>"acnologia-ca.pem"}
system(env, "python", "my_script.py")           # the child's own HTTP client goes through the proxy
```

In Ruby itself, `forward_proxy_http` returns a `Net::HTTP` that goes through the proxy and trusts the system roots plus the Acnologia CA:

```ruby
http = acn.forward_proxy_http("api.openai.com")   # downloads the CA, or pass ca_path:
res = http.start { |h| h.get("/v1/models") }      # no key in your code
```

| Call | Returns |
|---|---|
| `forward_proxy` | `Acnologia::ForwardProxy`: `proxy_url`, `proxy_url_with_token`, `ca_url`, `ca_fingerprint`, `passthrough?`, `hosts` (`host`, `service`, `name`, `path`), `env`, `service_header`, `raw` |
| `forward_proxy_ca` | The CA certificate as PEM text, from `GET /api/v1/proxy/ca.pem`, sent without a token |
| `save_forward_proxy_ca(path = "acnologia-ca.pem")` | Writes the PEM to `path` and returns `path` |
| `forward_proxy_url` | `proxy_url` with user `app` and your token as the password, percent escaped |
| `forward_proxy_env(ca_path = "acnologia-ca.pem")` | `HTTPS_PROXY`, `HTTP_PROXY`, `NODE_USE_ENV_PROXY`, `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`, `SSL_CERT_FILE` |
| `forward_proxy_http(host, port = 443, use_ssl:, ca_path: nil)` | A configured `Net::HTTP` |

`ForwardProxy` holds a `<app token>` placeholder, never the token, so it is safe to print. `forward_proxy_url`, `forward_proxy_env` and `forward_proxy_http` contain the real token: keep them out of logs.

## Concurrency limits

Some services cap how many requests one key may run at once. Acnologia enforces the cap per key, so on such a service each lease holds one of the key's slots until you report it or its TTL runs out. The default TTL is 120 seconds on limited services and 300 otherwise.

- **Always report.** `with_key` and the block form of `lease` do it for you. With a manual lease, report in `ensure`, or the slot stays taken until the TTL expires.
- **Wait instead of failing.** Pass `wait_sec:` (0 to 60) and the server waits that long for a free slot. The read timeout is extended to cover the wait.

```ruby
reply = acn.with_key("agi-labs", wait_sec: 20) { |lease| call_agi_labs(lease) }

begin
  lease = acn.lease("agi-labs")
rescue Acnologia::AllKeysBusy => e
  sleep(e.retry_after || 1)
  retry
end
```

Proxy mode handles this on the server: it queues proxied requests for up to the service's `queue_timeout_sec` and never sends more than the limit in parallel on one key.

## Retries

Every error carries `retry_after`, in seconds: the body's `retry_after_sec`, else the `Retry-After` header (seconds or an HTTP date), else `nil`.

With `max_retries:`, the client sleeps that long (1 second when the server gives none) and tries again after a 503 (`no_key_available`, `all_keys_busy`, `service_unavailable`) or a 429 `app_limit_reached`. A wait longer than `max_retry_wait:` is not slept: the error is raised at once, so a daily limit never blocks for hours.

```ruby
acn = Acnologia::Client.new(max_retries: 3, max_retry_wait: 30)
```

Retries apply to `services`, `me`, `guide_doc`, `capabilities`, `forward_proxy`, `lease` and proxy calls. Reports and renewals are never retried. A proxied POST or PATCH is retried only when it has an `Idempotency-Key`, and every attempt sends the same key.

## Access password

The consumer routes (`/proxy`, `/api/v1`) pass a deploy's access password lock with the app token alone, so most clients never need this. Set it when something in front of those routes also asks for the password. The client then sends it as `X-Acnologia-Access` on every request:

```ruby
acn = Acnologia::Client.new(access_password: ENV.fetch("MY_ACCESS_PASSWORD"))
```

With no `access_password:`, the client reads `ACNOLOGIA_ACCESS_PASSWORD`. When neither is set, no such header is sent. `access_password: false` turns it off even when the variable is set. The proxy never forwards this header to the provider, and the client never prints it.

## Errors

Every error from Acnologia is an `Acnologia::Error` with `status`, `code`, `message`, `retry_after` and `body` (the parsed JSON). The class follows the error code, then the status:

| Class | When |
|---|---|
| `Acnologia::Unauthenticated` | 401: token missing, wrong, revoked or expired |
| `Acnologia::Forbidden` | 403: app disabled, or `service_not_allowed`, `proxy_not_allowed` |
| `Acnologia::LeaseNotAllowed` | 403 `lease_not_allowed`: the app is proxy only, or another server shares this service with yours. Use the proxy. A `Forbidden` |
| `Acnologia::ShareRevoked` | 403 `share_revoked`: the owner revoked the share. A `ShareUnavailable` |
| `Acnologia::SharePaused` | 403 `share_paused`: the owner paused the share. A `ShareUnavailable` |
| `Acnologia::ShareExpired` | 403 `share_expired`: the share reached its end date. A `ShareUnavailable` |
| `Acnologia::ShareUnavailable` | Parent of the three above, a `Forbidden`. `share_status` is `revoked`, `paused` or `expired` |
| `Acnologia::AppLimitReached` | 429 `app_limit_reached`: the app used up a request or budget limit. `limit`, `reset_at` (and `reset_time` as a `Time`) and `retry_after` |
| `Acnologia::NoKeyAvailable` | 503 `no_key_available`: every key is cooling down, exhausted or excluded |
| `Acnologia::AllKeysBusy` | 503 `all_keys_busy`: every usable key is at its concurrency limit. A `NoKeyAvailable`, `busy?` is true |
| `Acnologia::ServiceUnavailable` | 503 `service_unavailable`: the provider's circuit breaker is open. Not a `NoKeyAvailable` |
| `Acnologia::UpstreamBlocked` | 502 `upstream_blocked`: the provider's address is one the server refuses to call |
| `Acnologia::LoopDetected` | 508 `loop_detected`: the request passed through more than 3 Acnologia servers |
| `Acnologia::TimeoutError` | The connection or the answer took longer than `open_timeout` or `read_timeout`. `status` 0, `code` `timeout` |
| `Acnologia::Error` | Anything else, for example 404 `lease_not_found`, 409 `report_failed` or a transport failure (`status` 0) |
| `Acnologia::RateLimited`, `QuotaExhausted`, `InvalidKey` | Raised by you inside `with_key` |

API calls (`services`, `me`, `guide`, `guide_doc`, `capabilities`, `forward_proxy`, `forward_proxy_ca`, `lease`, `report`, `renew`) raise these. Other codes to check on `Acnologia::Error#code` include `empty_group`, `bad_params`, `no_route`, `no_route_worked` and `proxy_not_allowed`. Proxy calls return the response; call `res.raise_for_status!` to raise.

```ruby
begin
  acn.lease("agi-labs")
rescue Acnologia::LeaseNotAllowed
  acn.proxy("agi-labs").post("/v1/chat", { "prompt" => "Hello" })
rescue Acnologia::AppLimitReached => e
  warn "#{e.limit} used up until #{e.reset_at}"
rescue Acnologia::ShareUnavailable => e
  warn "the share is #{e.share_status}"
end
```

The token and the access password are replaced with `[key]` in every error message. A report message built from an exception has the lease secret replaced the same way, and is cut to 500 characters (`Acnologia.scrub_message`).

## Tests

```sh
cd sdk/ruby
ruby -Ilib -Itest test/test_acnologia.rb
```

The suite uses minitest, which ships with Ruby, and runs offline. Most tests start a small HTTP server on `127.0.0.1` inside the test process.

To run your own code without a network, pass a transport to the client:

```ruby
acn = Acnologia::Client.new("http://acn.test", "acn_test",
  transport: ->(method, url, headers, body) { Acnologia::Response.new(200, {}, "{}") })
```

## More

- [All SDKs](../README.md)
- [HTTP API and error codes](../../docs/api.md)
- [Request flows](../../docs/flows.md): what the proxy and leases do on the server
