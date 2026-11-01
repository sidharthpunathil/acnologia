# cogito-keys (Ruby)

Ruby client for [Cogito](../../docs/llms.txt), a self-hosted API key pool. Cogito
holds many keys per provider and hands out a working one, rotating automatically
when a key is rate limited or out of quota.

Ruby 2.6+, standard library only (`net/http`, `json`).

## Install

```bash
gem install cogito-keys
```

```ruby
require "cogito"

cog = Cogito::Client.new("http://localhost:8080", "cog_your_app_token")
# or set COGITO_URL and COGITO_TOKEN and call Cogito::Client.new
```

## Proxy mode

Send the provider request to Cogito. It injects a key, retries with the next key
on rate limits, and never exposes a secret. Methods return `Cogito::Response`
(`status`, `headers`, `body`, `ok?`, `json`, `res["X-Cogito-Key-Id"]`), including
non 2xx responses.

### SerpAPI search

```ruby
res = cog.proxy("serpapi").get("/search.json", "engine" => "google", "q" => "best espresso grinder")
res.json.fetch("organic_results", []).first(5).each { |r| puts "#{r['title']} #{r['link']}" }
```

### ElevenLabs text to speech

```ruby
res = cog.proxy("elevenlabs").post(
  "/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM",
  { "text" => "Hello from Cogito.", "model_id" => "eleven_multilingual_v2" },
  headers: { "Accept" => "audio/mpeg" }
)
File.binwrite("hello.mp3", res.body) if res.ok?
```

Also `put`, `delete` and `request(method, path, query:, headers:, body:)`.
Hash and Array bodies are sent as JSON.

## Lease mode

`cog.lease(service, exclude_key_ids: nil, ttl_sec: nil, wait_sec: nil)` returns a
`Cogito::Lease` (`lease_id`, `key_id`, `service`, `secret`, `auth`, `base_url`,
`expires_at`). It raises `Cogito::NoKeyAvailable` (`retry_after`) when the pool
is drained.

`lease.apply(query: {}, headers: {})` returns `[query, headers]` with the secret
placed where `auth["type"]` says: `query` (SerpAPI `api_key`), `header`
(ElevenLabs `xi-api-key`) or `bearer` (`Authorization: Bearer ...`).
`lease.url(path, query)` builds a provider URL.

`lease.report(outcome, status_code:, retry_after_sec:, message:, rotate:)`
reports `ok`, `rate_limited`, `quota_exhausted`, `invalid` or `error` and
returns the next lease (or nil) when `rotate: true`.

### with_key

`cog.with_key(service, max_attempts: 5, ttl_sec: nil, wait_sec: nil) { |lease| ... }` leases a key, yields
it, reports the outcome and returns the block's value. Raise
`Cogito::RateLimited`, `Cogito::QuotaExhausted` or `Cogito::InvalidKey` to cool
the key down and retry with the next one. Other exceptions are reported as
`error` and re-raised.

SerpAPI:

```ruby
require "net/http"

data = cog.with_key("serpapi") do |lease|
  query, = lease.apply(query: { "engine" => "google", "q" => "coffee" })
  res = Net::HTTP.get_response(URI(lease.url("/search.json", query)))
  case res.code.to_i
  when 429
    raise Cogito::QuotaExhausted.new(status_code: 429) if res.body.include?("run out of searches")

    raise Cogito::RateLimited.new(retry_after: 3600, status_code: 429)
  when 401 then raise Cogito::InvalidKey.new(status_code: 401)
  end
  JSON.parse(res.body)
end
```

ElevenLabs:

```ruby
audio = cog.with_key("elevenlabs") do |lease|
  _, headers = lease.apply(headers: { "Accept" => "audio/mpeg", "Content-Type" => "application/json" })
  uri = URI(lease.url("/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM"))
  res = Net::HTTP.post(uri, JSON.generate("text" => "Hi", "model_id" => "eleven_multilingual_v2"), headers)
  case res.code.to_i
  when 429 then raise Cogito::RateLimited.new(retry_after: 5, status_code: 429)
  when 401
    raise Cogito::QuotaExhausted.new(status_code: 401) if res.body.include?("quota_exceeded")

    raise Cogito::InvalidKey.new(status_code: 401)
  end
  res.body
end
File.binwrite("hi.mp3", audio)
```

Manual flow (report on every path, see Concurrency limits):

```ruby
lease = cog.lease("serpapi")
begin
  # ... call the provider ...
  next_lease = lease.report("rate_limited", status_code: 429, retry_after_sec: 3600, rotate: true)
ensure
  lease.report("error") unless lease.reported
end
```

## Concurrency limits

Some providers cap how many requests one key may run at once (ElevenLabs
allows 2 per key by default). Cogito enforces this per key, so a lease is
exclusive: on a service with a limit, each lease holds one of the key's slots
until you report it or its `ttl_sec` runs out (default 120 seconds on limited
services, 300 otherwise).

- **Always report.** `with_key` reports every lease, even when the block
  raises, `break`s, returns early or `throw`s. With a manual `lease`, report
  in an `ensure` block, or the slot stays taken until the TTL expires.
- **Wait instead of failing.** Pass `wait_sec:` (0 to 60) and the server waits
  that long for a slot when every usable key is busy. The `net/http` read
  timeout is extended to cover the wait.

```ruby
audio = cog.with_key("elevenlabs", wait_sec: 20) { |lease| speak(lease) }

begin
  lease = cog.lease("elevenlabs")
rescue Cogito::AllKeysBusy => e # e.busy? is true
  sleep(e.retry_after || 1)
  retry
end
```

`Cogito::AllKeysBusy` (503 `all_keys_busy`) means the keys are fine but in
use; it clears as soon as another caller reports. It subclasses
`Cogito::NoKeyAvailable`, so existing rescues still catch it; check `busy?` to
tell them apart.

Proxy mode needs none of this: Cogito queues proxied requests for up to the
service's `queue_timeout_sec` (30 seconds for ElevenLabs) and never sends more
than the limit in parallel on one key.

## Errors

| class | when |
|---|---|
| `Cogito::Error` | non 2xx from Cogito (`status`, `code`), or a transport failure |
| `Cogito::NoKeyAvailable` | 503 `no_key_available`, `retry_after` in seconds, `busy?` |
| `Cogito::AllKeysBusy` | 503 `all_keys_busy`: every usable key is at its concurrency limit (subclass of `NoKeyAvailable`, `busy?` is true) |
| `Cogito::RateLimited`, `QuotaExhausted`, `InvalidKey` | raised by you inside `with_key` (all `Cogito::KeyProblem`) |

## Tests

```bash
ruby -Ilib test/test_cogito.rb
```

Pass `transport: ->(method, url, headers, body) { Cogito::Response.new(...) }`
to the client to replace `net/http`, as the tests do.
