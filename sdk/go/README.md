# acnologia-go

Go client for [Acnologia](../../README.md), the self-hosted server that holds your API keys. Your code calls with its own `acn_` app token, and Acnologia attaches a real key, rotates to the next one on rate limits and quota errors, and enforces the limits you set. Standard library only.

```go
import acnologia "github.com/sidharthpunathil/acnologia/sdk/go"

c := acnologia.New("http://localhost:8080", os.Getenv("ACNOLOGIA_TOKEN")) // app token, acn_...

resp, err := c.Proxy("weather").Get(ctx, "/v1/forecast", url.Values{"city": {"Lisbon"}})
if err != nil {
	return err
}
defer resp.Body.Close()
fmt.Println(resp.Status, resp.Header.Get("X-Acnologia-Key-Id"))
```

That request goes to `/proxy/weather/v1/forecast?city=Lisbon`. Acnologia adds the key and sends it on, and the key never reaches your process.

## Install

```sh
go get github.com/sidharthpunathil/acnologia/sdk/go
```

Requires Go 1.22 or later. The module is fetched straight from the repository; it is not on any other registry.

## Authenticate

Create an app in the dashboard and copy its token (`acn_...`, shown once). `New(baseURL, token, opts...)` takes both directly. The Go client does not read `ACNOLOGIA_URL` or `ACNOLOGIA_TOKEN` itself, so pass them in with `os.Getenv` as above.

The app must be allowed the mode you use (`proxy`, `lease` or `both`). A lease from a proxy only app, or on a service another Acnologia server shares with yours, fails with an `*acnologia.APIError` (403, `Code: "lease_not_allowed"`). Call those services in proxy mode.

## Choose a mode

| Mode | You call | The secret reaches your process | Rotation |
|---|---|---|---|
| Proxy | Acnologia at `/proxy/{service}/...` | never | automatic, on the server |
| Lease | the provider directly | yes | you report the outcome; `WithKey` does it for you |

Use proxy mode unless the provider's own SDK needs the raw key.

## Proxy mode

`c.Proxy(service)` returns a `*acnologia.Proxy` whose requests go to `/proxy/{service}/{path}`.

```go
p := c.Proxy("weather")

resp, err := p.Get(ctx, "/v1/forecast", url.Values{"city": {"Lisbon"}})
resp, err = p.PostJSON(ctx, "/v1/alerts", map[string]string{"city": "Lisbon"})
resp, err = p.Post(ctx, "/v1/upload", "text/csv", body)
resp, err = p.Request(ctx, http.MethodDelete, "/v1/alerts/42", nil)
```

`Do(req)` sends a request you built yourself. Its URL may be relative, and only the path and query are used:

```go
req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/v1/forecast?city=Lisbon", nil)
resp, err := c.Proxy("weather").Do(req)
```

`p.URL(path, query)` returns the full proxy URL without sending anything.

What to expect from a proxied response:

- It comes back unchanged. A non-2xx status from the provider is a normal response, not an error.
- It carries `X-Acnologia-Key-Id` (which key served it) and `X-Acnologia-Attempts`.
- Redirects are never followed, so your app token is not replayed to another host.
- The client's default timeout does not apply, because bodies may stream. Bound a proxy call with `ctx`.

## Lease mode with WithKey

`WithKey` leases a key, calls your function, reports the outcome and rotates to the next key when you return a rotation error. Use `l.URL` to build the provider URL and `l.Apply` to put the secret where the provider expects it.

```go
var forecast map[string]any
err := c.WithKey(ctx, "weather", func(l *acnologia.Lease) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		l.URL("/v1/forecast", url.Values{"city": {"Lisbon"}}), nil)
	l.Apply(req) // query parameter, header or bearer, per the service
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 429:
		return &acnologia.ErrRateLimited{StatusCode: 429, RetryAfter: time.Minute}
	case resp.StatusCode == 402:
		return &acnologia.ErrQuotaExhausted{StatusCode: 402}
	case resp.StatusCode == 401:
		return &acnologia.ErrInvalidKey{StatusCode: 401}
	case resp.StatusCode >= 400:
		return fmt.Errorf("weather: http %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(&forecast)
}, acnologia.WithMaxAttempts(3))
```

Which status means what depends on the provider. Map its responses to these returns:

| Return | Reported outcome | Rotates |
|---|---|---|
| `nil` | `ok` | no |
| `*acnologia.ErrRateLimited{RetryAfter}` | `rate_limited` | yes |
| `*acnologia.ErrQuotaExhausted{}` | `quota_exhausted` | yes |
| `*acnologia.ErrInvalidKey{}` | `invalid` | yes |
| any other error | `error` | no; the error is returned as is |

Wrapped errors (`fmt.Errorf("...: %w", err)`) are recognized, and each rotation error has an optional `Err` field returned by `Unwrap` for the underlying cause. After `WithMaxAttempts(n)` keys (default 5), `WithKey` returns the last rotation error; the final attempt is reported without asking for a next key. If the pool runs out first, it returns `*acnologia.ErrNoKeyAvailable`.

`WithKey` reports every lease it takes: on success, on any error, when your callback panics (the panic is re-raised after the report) or calls `runtime.Goexit`, and when `ctx` is cancelled while it runs (the report goes out on a detached context). Messages sent with a report have the secret scrubbed out and are capped at 500 bytes.

Options:

- `WithMaxAttempts(n)`: how many keys to try. Default 5.
- `WithWait(d)`: how long the server may wait for a free slot. See [Concurrency limits](#concurrency-limits).
- `WithLeaseOptions(acnologia.LeaseOptions{...})`: options for the first lease. `TTL`, `Wait` and `Exclusive` also apply to every later lease `WithKey` takes.

## Lease and report by hand

```go
lease, err := c.Lease(ctx, "weather", &acnologia.LeaseOptions{TTL: 5 * time.Minute, Wait: 10 * time.Second})
if err != nil {
	return err
}
// Report on every path: an unreported lease holds a concurrency slot until its TTL.
req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
	lease.URL("/v1/forecast", url.Values{"city": {"Lisbon"}}), nil)
lease.Apply(req)
resp, err := http.DefaultClient.Do(req)
// ...
next, err := lease.Report(ctx, acnologia.Report{
	Outcome:    acnologia.OutcomeRateLimited,
	StatusCode: 429,
	RetryAfter: time.Minute,
	Rotate:     true, // ask for the next key in the same call
})
if next != nil {
	// retry with next, and report it too
}
```

`LeaseOptions`:

| Field | Meaning |
|---|---|
| `ExcludeKeyIDs []int64` | keys the server must not return |
| `TTL time.Duration` | lease lifetime. Zero uses the server default: 120 seconds on services with a concurrency limit, 300 otherwise |
| `Wait time.Duration` | how long the server may wait for a free slot, up to `acnologia.MaxWait` (60s). Sent as whole seconds, rounded up |
| `Exclusive bool` | reserve the key for this lease only |

`Report` has `Outcome`, `StatusCode`, `RetryAfter`, `Message` and `Rotate`. An empty `Outcome` is sent as `ok`. `lease.Report` returns the next `*Lease` when `Rotate` is true and another key is free, otherwise `nil`.

Outcomes: `OutcomeOK`, `OutcomeRateLimited`, `OutcomeQuotaExhausted`, `OutcomeInvalid`, `OutcomeError`, plus `OutcomeBusy` (the account is at its concurrency cap, so the key steps aside briefly) and `OutcomeBanned` (the provider suspended the account, so the key leaves rotation).

### Where the secret goes

`Lease` fields: `LeaseID`, `KeyID`, `Service`, `Secret`, `Auth{Type, Name, Prefix}`, `BaseURL`, `ExpiresAt`, `Exclusive`.

`lease.URL(path, query)` joins `BaseURL` with the path and query. It does not add the secret. `lease.Apply(req)` does, according to `Auth.Type`:

| `Auth.Type` | Effect |
|---|---|
| `query` | sets query parameter `Auth.Name` |
| `header` | sets header `Auth.Name` (default `Authorization`) |
| `bearer` | sets header `Auth.Name` (default `Authorization`) to `Bearer <secret>` |

A non-empty `Auth.Prefix` goes in front of the secret for every type. For `bearer` it replaces `Bearer `, so `Prefix: "Token "` gives `Authorization: Token <secret>`.

## Concurrency limits

Some providers cap how many requests one key may run at once, and Acnologia enforces that cap per key. On a service with a limit, each lease holds one of the key's slots until you report it or its TTL runs out.

- **Always report.** `WithKey` does. With a manual `Lease`, report on every return path.
- **Wait instead of failing.** Set `LeaseOptions.Wait`, or `WithWait(d)` for `WithKey`, and the server waits up to that long for a slot when every usable key is busy. If you pass your own `http.Client` with a `Timeout`, keep it longer than the wait.

```go
err := c.WithKey(ctx, "weather", fetchForecast, acnologia.WithWait(20*time.Second))

if errors.Is(err, acnologia.AllKeysBusy) {
	// every usable key is at its limit; retry soon
}
```

A busy pool returns `*acnologia.ErrNoKeyAvailable` with `Busy: true` and `Code: "all_keys_busy"` (HTTP 503, `RetryAfter` usually 1s). It still matches `acnologia.NoKeyAvailable`, and it clears as soon as another caller reports.

Proxy mode handles this on the server: Acnologia queues proxied requests for up to the service's `queue_timeout_sec` and never sends more than the limit in parallel on one key.

## Long jobs: renew and exclusive leases

`lease.Renew(ctx, ttl)` extends an open lease so a long job keeps its key. It returns the new expiry and updates `lease.ExpiresAt`; `c.Renew(ctx, lease, ttl)` does the same. A zero `ttl` uses the server default (120 seconds), and the server caps it at 24 hours. If the lease is no longer open you get an `*acnologia.APIError` with `Code: "lease_not_found"` (HTTP 404).

`LeaseOptions{Exclusive: true}` reserves the key for your lease only: no other lease or proxy call gets it until you report or the lease expires. `lease.Exclusive` tells you whether you got it. With `WithKey`, pass it through `WithLeaseOptions`.

```go
lease, err := c.Lease(ctx, "weather", &acnologia.LeaseOptions{TTL: 10 * time.Minute, Exclusive: true})
if err != nil {
	return err
}
// halfway through a long job:
if _, err := lease.Renew(ctx, 10*time.Minute); err != nil {
	return err
}
```

## Proxy options and response headers

`p.WithIdempotencyKey(key)` and `p.WithNoCache()` return a copy of the `*Proxy` that sends `Idempotency-Key` or `Cache-Control: no-cache` on every request. `acnologia.ProxyInfo(resp)` reads the Acnologia headers of a proxied answer:

```go
resp, err := c.Proxy("weather").WithIdempotencyKey("order-42").PostJSON(ctx, "/v1/alerts", alert)
if err != nil {
	return err
}
info := acnologia.ProxyInfo(resp)
fmt.Println(info.KeyID, info.Attempts, info.Service, info.Cache, info.Replayed)
```

`ProxyMeta` has `KeyID` (0 when absent, as for a share's token), `Attempts`, `Service`, `Cache` (`hit`, `miss`, ...), `Replayed`, `Capability` and `Tried` (`[]Tried{Service, Status}` from `X-Acnologia-Tried`). The service passed to `Proxy` may also be a service group or a capability slug.

## Capabilities

A capability is one call served by whichever provider can. `c.Capabilities(ctx)` lists them, and `c.Capability(ctx, slug, params, opts)` calls one with `params` as the JSON body. The answer comes back as is, like a proxy call.

```go
caps, err := c.Capabilities(ctx) // Slug, Name, Description, Strategy, Params, Routes, URL
resp, err := c.Capability(ctx, "web-search", map[string]any{"q": "coffee"},
	&acnologia.CapabilityOptions{Service: "serpapi"})
info := acnologia.ProxyInfo(resp) // info.Capability, info.Service, info.Tried
```

`Service` adds `?_service=` (only that provider's route) and `Raw` adds `?_raw=1` (the provider's own body, unmapped).

## Forward proxy

Point an app's own HTTP client at Acnologia and call provider URLs as they are. `c.ForwardProxyTransport(ctx)` returns an `*http.Transport` that sends everything through the forward proxy, with the token as proxy credentials and the system roots plus the Acnologia CA as `RootCAs`:

```go
tr, err := c.ForwardProxyTransport(ctx)
if err != nil {
	return err
}
hc := &http.Client{Transport: tr}
resp, err := hc.Get("https://api.openai.com/v1/models") // Acnologia adds the key
```

For another process (a Node or Python tool), save the CA and hand it the environment:

```go
if _, err := c.SaveForwardProxyCA(ctx, "acnologia-ca.pem"); err != nil {
	return err
}
env, err := c.ForwardProxyEnv(ctx, "acnologia-ca.pem")
cmd := exec.Command("node", "agent.js")
cmd.Env = os.Environ()
for k, v := range env {
	cmd.Env = append(cmd.Env, k+"="+v)
}
```

| Call | Returns |
|---|---|
| `c.ForwardProxy(ctx)` | `*ForwardProxy`: `ProxyURL`, `ProxyURLWithToken` (with an `<app token>` placeholder), `CAURL`, `CAFingerprint`, `Passthrough`, `Hosts`, `Env`, `ServiceHeader` |
| `c.ForwardProxyCA(ctx)` | the CA certificate as PEM. Public: the token is not sent |
| `c.SaveForwardProxyCA(ctx, path)` | writes it to `path` (default `acnologia-ca.pem`) |
| `c.ForwardProxyURL(ctx)` | `http://app:acn_...@host:8888`. Contains the token |
| `c.ForwardProxyEnv(ctx, caPath)` | `HTTPS_PROXY`, `HTTP_PROXY`, `NODE_USE_ENV_PROXY=1`, and `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`, `SSL_CERT_FILE` set to `caPath`. Contains the token |
| `c.ForwardProxyTransport(ctx)` | the `*http.Transport` above. Its proxy URL contains the token |

## About this token

`c.Me(ctx)` returns the token's own access: `App`, `Limits` (each a nil pointer when unset), `Usage`, `LimitHit`, `Services` (with `Available` as a count and `LogoURL`) and `Share` (nil for an ordinary app; for a share's token its `Status`, which is still answered after the share stopped).

```go
me, err := c.Me(ctx)
if me.LimitHit != nil {
	fmt.Println("limit", me.LimitHit.Limit, "resets", me.LimitHit.ResetAt)
}
```

## Usage guides

```go
g, err := c.Guide(ctx, "weather")          // Markdown, Generated, Guide (json.RawMessage)
md, err := c.GuideMarkdown(ctx, "weather") // the Markdown alone
```

## List services

`c.Services(ctx)` returns the services this app token may use, each with `Slug`, `Name`, `Provider`, `Category`, `Available`, `BaseURL`, `Auth` and `GuideURL`.

```go
svcs, err := c.Services(ctx)
for _, s := range svcs {
	fmt.Println(s.Slug, s.Available, s.Auth.Type)
}
```

## Errors and retries

| Type | Sentinel | When |
|---|---|---|
| `*acnologia.ErrNoKeyAvailable{RetryAfter, Code, Busy}` | `acnologia.NoKeyAvailable` | 503 `no_key_available`: every key is cooling down, exhausted or excluded |
| `*acnologia.ErrNoKeyAvailable` with `Busy: true` | `acnologia.AllKeysBusy` (and `NoKeyAvailable`) | 503 `all_keys_busy`: every usable key is at its concurrency limit |
| `*acnologia.ErrServiceUnavailable{RetryAfter}` (embeds `APIError`) | `acnologia.ServiceUnavailable` | 503 `service_unavailable`: the provider keeps failing and its circuit breaker is open. Not a `NoKeyAvailable` |
| `*acnologia.ErrAppLimitReached{Limit, ResetAt}` (embeds `APIError`) | `acnologia.AppLimitReached` | 429 `app_limit_reached`: the app used up a daily or monthly request or budget limit. `RetryAfter` says how long |
| `*acnologia.ErrShareUnavailable{Status}` (embeds `APIError`) | `acnologia.ShareUnavailable` | 403 `share_revoked`, `share_paused` or `share_expired`. `Status` is `revoked`, `paused` or `expired` |
| `*acnologia.APIError{StatusCode, Code, Message, RetryAfter}` | | any other non-2xx from the consumer API, such as 401, 403 `lease_not_allowed`, 404 `lease_not_found`, or `empty_group`, `bad_params`, `no_route`, `no_route_worked`, `proxy_not_allowed`, `loop_detected` |
| `*acnologia.ErrRateLimited{RetryAfter}` | `acnologia.RateLimited` | you return it from `WithKey` |
| `*acnologia.ErrQuotaExhausted` | `acnologia.QuotaExhausted` | you return it from `WithKey` |
| `*acnologia.ErrInvalidKey` | `acnologia.InvalidKey` | you return it from `WithKey` |

Each typed error matches its sentinel, so both styles work. Use the `RetryAfter` field to decide when to try again:

```go
if errors.Is(err, acnologia.NoKeyAvailable) { ... }

var nk *acnologia.ErrNoKeyAvailable
if errors.As(err, &nk) {
	time.Sleep(nk.RetryAfter)
}
```

`errors.As(err, &apiErr)` with an `*acnologia.APIError` also matches an `*ErrServiceUnavailable`, `*ErrAppLimitReached` or `*ErrShareUnavailable`.

## Configuration

| Option | Default |
|---|---|
| `acnologia.WithHTTPClient(*http.Client)` | a client with `acnologia.DefaultTimeout` (75 seconds, longer than the 60 second lease wait) |
| `acnologia.WithUserAgent(string)` | `acnologia-go/0.2.0` |
| `acnologia.WithAccessPassword(string)` | none. Sends `X-Acnologia-Access` on every request, for a deploy behind the access password |

The token and lease secrets are redacted when a `Client` or `Lease` is printed with `%v`, `%+v` or `%#v`.

## Tests

```sh
go test ./...
```

The suite runs offline against a stub server.

## More

- [All SDKs](../README.md): install lines for every language and how to run each suite
- [Request flows](../../docs/flows.md), [HTTP API and error codes](../../docs/api.md), [SDK overview](../../docs/sdks.md)
