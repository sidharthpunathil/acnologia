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
    idempotency_key: "order-42",
    no_cache: true
  )

resp.status      # the provider's status
resp.headers     # [{"content-type", "application/json"}, ...]
resp.body        # binary
resp.key_id      # X-Acnologia-Key-Id
resp.attempts    # X-Acnologia-Attempts
resp.service     # X-Acnologia-Service
resp.cache       # X-Acnologia-Cache: "hit", "miss", ...
resp.replayed    # true when Idempotency-Key replayed a stored answer
resp.capability  # X-Acnologia-Capability, for a capability call
resp.tried       # X-Acnologia-Tried: [%{service: "serpapi", status: 429}, ...]
{:ok, data} = Acnologia.Response.json(resp)
```

`:idempotency_key` sends `Idempotency-Key`, so a retried call is answered
once and replayed after. `no_cache: true` sends `Cache-Control: no-cache`.
The method is an atom or a string. Use `:body` for a raw body and set its
`content-type` in `:headers`. A provider answer with a 4xx or 5xx status is
still `{:ok, resp}`. When Acnologia itself refuses the call, such as no key
available or the app limit reached, you get `{:error, %Acnologia.Error{}}`.

## Services and guides

```elixir
{:ok, services} = Acnologia.services(acn)
# [%Acnologia.Service{slug: "agi-labs", available: true, guide_url: "...", ...}]

{:ok, md} = Acnologia.guide_markdown(acn, "agi-labs")   # Markdown, ready for an agent's prompt
{:ok, guide} = Acnologia.guide(acn, "agi-labs")         # %Acnologia.Guide{markdown:, generated:, guide:}
```

## Capabilities

A capability is one job (such as finding places) that several providers
do. Acnologia picks a provider, translates the params, rotates keys and
moves to the next provider when one fails.

```elixir
{:ok, caps} = Acnologia.capabilities(acn)
# [%Acnologia.Capability{slug: "places", params: [...], routes: [%{service: "serpapi", mapped: true}], ...}]

{:ok, resp} = Acnologia.capability(acn, "places", %{q: "coffee in Lisbon"})
resp.service   # who answered
resp.tried     # [%{service: "serpapi", status: 429}, %{service: "gmaps", status: 200}]
```

`capability/4` sends `POST /proxy/<slug>` with the params as the JSON body.
Options: `service: "gmaps"` pins one provider (`?_service=`), `raw: true`
returns the provider's own body (`?_raw=1`), plus `:headers`,
`:idempotency_key`, `:no_cache` and `:timeout` as for `proxy/5`. A refusal
comes back as `{:error, %Acnologia.Error{reason: :bad_params}}`,
`:no_route` or `:no_route_worked`.

## Forward proxy

An app can use Acnologia as its `HTTPS_PROXY` and call provider URLs as
they are. Acnologia injects the key for the service whose host matches.

```elixir
{:ok, setup} = Acnologia.forward_proxy(acn)   # proxy_url, ca_url, ca_fingerprint, hosts, env, ...
{:ok, pem} = Acnologia.forward_proxy_ca(acn)   # public, no token sent
{:ok, "acnologia-ca.pem"} = Acnologia.save_forward_proxy_ca(acn)

{:ok, env} = Acnologia.forward_proxy_env(acn, "acnologia-ca.pem")
# %{"HTTPS_PROXY" => "http://app:acn_...@host:8888", "HTTP_PROXY" => ...,
#   "NODE_USE_ENV_PROXY" => "1", "NODE_EXTRA_CA_CERTS" => "acnologia-ca.pem",
#   "REQUESTS_CA_BUNDLE" => "acnologia-ca.pem", "SSL_CERT_FILE" => "acnologia-ca.pem"}

# run another program through the proxy
System.cmd("node", ["--use-env-proxy", "app.js"], env: Map.to_list(env))
```

To send your own `:httpc` calls through it, point a profile at the proxy
and trust the CA:

```elixir
%URI{host: host, port: port} = URI.parse(setup.proxy_url)
:inets.start(:httpc, profile: :acnologia)
:httpc.set_options([https_proxy: {{to_charlist(host), port}, []}], :acnologia)

cacerts = for {:Certificate, der, _} <- :public_key.pem_decode(pem), do: der

:httpc.request(:get, {~c"https://agi-labs.example/v1/models", []},
  [proxy_auth: {~c"app", to_charlist(acn.token)},
   ssl: [verify: :verify_peer, cacerts: cacerts ++ :public_key.cacerts_get()]], [], :acnologia)
```

`forward_proxy_url/1` and `forward_proxy_env/2` hold the app token: never
log them. `%Acnologia.ForwardProxy{}` never does (its
`proxy_url_with_token` has an `<app token>` placeholder).

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
me.services   # [%{slug: "agi-labs", available: 1, logo_url: nil, ...}]
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
  {:error, %Acnologia.Error{share: status} = e} when status != nil -> share_stopped(status, e)
  {:error, error} -> raise error
end
```

The error has `reason`, `status`, `code`, `message`, `retry_after`,
`limit`, `reset_at` and `share`. `reason` is the server's error code as an atom:

| Reason | Status | Meaning |
|---|---|---|
| `:unauthenticated` | 401, 403 | Token missing or wrong, or the app is disabled. |
| `:service_not_allowed`, `:proxy_not_allowed`, `:lease_not_allowed` | 403 | Not allowed this service or mode. |
| `:forbidden` | 403 | Any other 403. |
| `:no_key_available` | 503 | Every key is cooling down, used up or disabled. Also any 503 with no code. |
| `:all_keys_busy` | 503 | Every usable key is at its concurrency limit. |
| `:service_unavailable` | 503 | The provider keeps failing and Acnologia paused it. |
| `:app_limit_reached` | 429 | A request or budget limit is used up. See `limit` and `reset_at`. |
| `:share_revoked`, `:share_paused`, `:share_expired` | 403 | The share behind the token ended or paused. `share` is `"revoked"`, `"paused"` or `"expired"` (also read from `X-Acnologia-Share`). |
| `:bad_params`, `:no_route`, `:no_route_worked` | 400, 503 | A capability call had bad params, or no provider route could take or serve it. |
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
`busy?/1`, `app_limit_reached?/1`, `share_unavailable?/1` and
`retryable?/1` help you decide what to do. No message holds
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
