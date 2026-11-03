# Acnologia for Elixir

Elixir client for [Acnologia](../../README.md), a self-hosted API key server.
Your code holds an app token (`acn_...`). It never stores a provider key.

It needs Elixir 1.18 or newer on Erlang/OTP 25 or newer. It has no
dependencies: it uses the `JSON` module built into Elixir and HTTP from OTP.

## Quick start

```elixir
{:ok, acn} = Acnologia.new(base_url: "https://keys.example.com", token: "acn_...")

{:ok, resp} = Acnologia.proxy(acn, :post, "agi-labs", "/v1/chat", json: %{prompt: "hello"})
IO.puts(resp.body)
```

## Install

It is not on Hex yet. Add it from the repository as a git dependency:

```elixir
# mix.exs
defp deps do
  [
    {:acnologia,
     git: "https://github.com/sidharthpunathil/acnologia.git", sparse: "sdk/elixir", branch: "main"}
  ]
end
```

Or from a local checkout:

```elixir
{:acnologia, path: "../acnologia/sdk/elixir"}
```

Then run `mix deps.get`.

## Client

```elixir
{:ok, acn} = Acnologia.new()
```

With no options it reads `ACNOLOGIA_URL` and `ACNOLOGIA_TOKEN`. Options:

| Option | Default | Meaning |
|---|---|---|
| `:base_url` | `ACNOLOGIA_URL` | Where Acnologia runs. |
| `:token` | `ACNOLOGIA_TOKEN` | The app token. |
| `:access_password` | `ACNOLOGIA_ACCESS_PASSWORD` | Sent as `X-Acnologia-Access`. Not sent when neither is set. |
| `:timeout` | `30000` | Milliseconds to wait for an answer. |
| `:connect_timeout` | `10000` | Milliseconds to wait for a connection. |
| `:ssl` | system trust store | `:ssl` options that replace the defaults. |

HTTPS checks the server certificate against the system trust store.
Redirects are not followed, so the token never goes to another host.
`inspect/1` hides the token and the access password.

## Proxy

Acnologia adds a key, moves to the next key on a rate limit, and relays
the provider's answer. The key never reaches your process.

```elixir
{:ok, resp} =
  Acnologia.proxy(acn, :post, "agi-labs", "/v1/chat",
    query: [model: "small"],
    headers: [{"x-request-id", "abc"}],
    json: %{prompt: "hello"},
    idempotency_key: "order-42"
  )

resp.status      # the provider's status
resp.headers     # [{"content-type", "application/json"}, ...]
resp.body        # binary
resp.key_id      # X-Acnologia-Key-Id
resp.attempts    # X-Acnologia-Attempts
resp.service     # X-Acnologia-Service
resp.replayed    # true when Idempotency-Key replayed a stored answer
{:ok, data} = Acnologia.Response.json(resp)
```

The method is an atom or a string. Use `:body` for a raw body and set its
`content-type` in `:headers`. A provider answer with a 4xx or 5xx status is
still `{:ok, resp}`. When Acnologia itself refuses the call, such as no key
available or the app limit reached, you get `{:error, %Acnologia.Error{}}`.

## Leases

Lease a key when your code has to hold it. Report every lease when you are
done.

```elixir
{:ok, lease} = Acnologia.lease(acn, "agi-labs", ttl_sec: 120, wait_sec: 10)

headers = Acnologia.Lease.apply_headers(lease, [{"accept", "application/json"}])
url = Acnologia.Lease.url(lease, "/v1/chat")
# call the provider at url with headers, using your HTTP client

{:ok, lease} = Acnologia.renew(acn, lease, ttl_sec: 600)  # a long job keeps its key
{:ok, nil} = Acnologia.report(acn, lease, :ok)
```

`lease/3` takes `:exclude_key_ids`, `:ttl_sec`, `:wait_sec` (0 to 60) and
`:exclusive`. `lease.auth` says where the key goes: `"header"`, `"bearer"`
or `"query"`. `apply_headers/2` adds it to headers, `apply_query/2` adds it
to a query, and `url/3` builds the full URL. Each one does nothing when the
key goes in the other place. `inspect/1` hides the secret.

Report the outcome: `:ok`, `:rate_limited`, `:busy`, `:quota_exhausted`,
`:invalid`, `:banned` or `:error`. With `rotate: true` you get the next
lease back in the same call:

```elixir
{:ok, next} =
  Acnologia.report(acn, lease, :rate_limited,
    status_code: 429,
    retry_after_sec: 30,
    message: "slow down",
    rotate: true
  )
```

`next` is `nil` when no other key is free. The secret is replaced with
`[key]` in `:message` before it is sent.

`with_key/4` runs that loop for you. Return `{:rotate, outcome}` to move
to the next key. Every lease is reported, even when your function raises.

```elixir
Acnologia.with_key(acn, "agi-labs", fn lease ->
  case call_provider(lease) do
    {:ok, %{status: 429}} -> {:rotate, :rate_limited, status_code: 429}
    {:ok, %{status: 200, body: body}} -> {:ok, body}
    {:error, reason} -> {:error, reason}
  end
end)
```

## Me

```elixir
{:ok, me} = Acnologia.me(acn)
me.app        # %{id: 3, name: "Website"}
me.limits     # %{daily_requests: 500, monthly_budget: 20, ...}
me.usage      # %{today_requests: 12, today_cost: 0.5, ...}
me.limit_hit  # nil, or %{limit: "daily_requests", reset_at: ~U[...]}
me.services   # [%{slug: "agi-labs", available: 1, ...}]
me.share      # nil, or the share for a share's token
```

## Errors

Every function returns `{:ok, value}` or `{:error, %Acnologia.Error{}}`.
Each one has a bang variant, such as `lease!/3`, that returns the value or
raises the error.

```elixir
case Acnologia.lease(acn, "agi-labs") do
  {:ok, lease} -> use(lease)
  {:error, %Acnologia.Error{reason: :all_keys_busy, retry_after: s}} -> retry_in(s)
  {:error, %Acnologia.Error{reason: :app_limit_reached, limit: l, reset_at: t}} -> wait(l, t)
  {:error, error} -> raise error
end
```

The error has `reason`, `status`, `code`, `message`, `retry_after`,
`limit` and `reset_at`. `reason` is the server's error code as an atom:

| Reason | Status | Meaning |
|---|---|---|
| `:unauthenticated` | 401, 403 | Token missing or wrong, or the app is disabled. |
| `:service_not_allowed`, `:proxy_not_allowed`, `:lease_not_allowed` | 403 | Not allowed this service or mode. |
| `:forbidden` | 403 | Any other 403. |
| `:no_key_available` | 503 | Every key is cooling down, used up or disabled. Also any 503 with no code. |
| `:all_keys_busy` | 503 | Every usable key is at its concurrency limit. |
| `:service_unavailable` | 503 | The provider keeps failing and Acnologia paused it. |
| `:app_limit_reached` | 429 | A request or budget limit is used up. See `limit` and `reset_at`. |
| `:share_revoked`, `:share_paused`, `:share_expired` | 403 | The share behind the token ended or paused. |
| `:lease_not_found` | 404 | Renewing a lease that is not open. |
| `:lease_revoked` | 403 | The app may no longer hold the key. |
| `:report_failed` | 409, 410 | The lease was already reported or expired. |
| `:unknown_service` | 404 | No service has that slug. |
| `:upstream_unreachable`, `:upstream_blocked` | 502 | Acnologia could not reach the provider. |
| `:loop_detected` | 508 | The request passed through too many Acnologia servers. |
| `:bad_path`, `:bad_body`, `:body_too_large`, `:method_not_allowed` | 400, 413, 405 | The proxy refused the request. |
| `:api` | any | Any other error. Check `status` and `code`. |

Errors without a server answer use `:transport`, `:timeout`, `:decode`,
`:config` and `:invalid_argument`. `with_key/4` gives
`:attempts_exhausted` after its last key.

`retry_after` is in seconds. It comes from `retry_after_sec` in the body,
or from the `Retry-After` header. `Acnologia.Error.no_key?/1`,
`busy?/1` and `retryable?/1` help you decide what to do. No message holds
the token, the access password or a leased secret.

## HTTP

On OTP releases with inets 9.6 or newer the client uses `:httpc` with its
automatic retry turned off. Older `:httpc` retries a 503 that carries
`Retry-After` on its own, which would hide `no_key_available` and
`service_unavailable`. On those releases a small built in HTTP/1.1 client
on `:gen_tcp` and `:ssl` is used instead.

## Tests

```sh
mix test
mix format --check-formatted
mix compile --warnings-as-errors
```

The tests start a fake server on a local port and need no network. Set
`ACNOLOGIA_TEST_HTTP=raw` or `ACNOLOGIA_TEST_HTTP=httpc` to test one HTTP
path.
