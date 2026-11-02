# acnologia-go

Go client for [Acnologia](../../docs/llms.txt), a self-hosted API key pool. Standard library only.

Two ways to use your pooled keys:

- **Proxy mode**: send requests to Acnologia instead of the provider. Acnologia injects a key, retries with the next key on rate limits, and never exposes the secret.
- **Lease mode**: lease a key, call the provider yourself, report the outcome. `WithKey` runs that loop and rotates keys for you.

## Install

```sh
go get github.com/sidharthpunathil/acnologia/sdk/go
```

Requires Go 1.22+.

```go
import acnologia "github.com/sidharthpunathil/acnologia/sdk/go"

c := acnologia.New("http://localhost:8080", os.Getenv("ACNOLOGIA_TOKEN")) // app token, acn_...
```

Options: `acnologia.WithHTTPClient(*http.Client)`, `acnologia.WithUserAgent(string)`.

## Proxy mode

`c.Proxy(service)` returns a helper whose requests go to `/proxy/{service}/{path}`.
Upstream responses come back unchanged (non-2xx is not an error). Each response
carries `X-Acnologia-Key-Id` and `X-Acnologia-Attempts`.

### SerpAPI search

```go
resp, err := c.Proxy("serpapi").Get(ctx, "/search.json", url.Values{
	"engine": {"google"},
	"q":      {"coffee"},
})
if err != nil {
	return err
}
defer resp.Body.Close()
var results map[string]any
err = json.NewDecoder(resp.Body).Decode(&results)
```

### ElevenLabs text to speech

```go
resp, err := c.Proxy("elevenlabs").PostJSON(ctx, "/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM",
	map[string]string{"text": "Hello from Acnologia", "model_id": "eleven_multilingual_v2"})
if err != nil {
	return err
}
defer resp.Body.Close()
f, _ := os.Create("hello.mp3")
defer f.Close()
io.Copy(f, resp.Body)
```

Other helpers: `Post(ctx, path, contentType, body)`, `Request(ctx, method, path, body)`,
and `Do(req)`, which accepts a request with a relative URL such as `/search.json?q=x`:

```go
req, _ := http.NewRequestWithContext(ctx, "GET", "/v1/voices", nil)
resp, err := c.Proxy("elevenlabs").Do(req)
```

## Lease mode

### WithKey (recommended)

Return one of the typed errors from your callback to rotate to the next key:

| Return | Reported outcome | Rotates |
|---|---|---|
| `nil` | `ok` | no |
| `*acnologia.ErrRateLimited{RetryAfter}` | `rate_limited` | yes |
| `*acnologia.ErrQuotaExhausted{}` | `quota_exhausted` | yes |
| `*acnologia.ErrInvalidKey{}` | `invalid` | yes |
| any other error | `error` | no, the error is returned as is |

Wrapped errors (`fmt.Errorf("...: %w", err)`) are recognized. After
`WithMaxAttempts(n)` (default 5) keys, the last rotation error is returned; the
final attempt is reported without asking for a next key.

Options: `WithMaxAttempts(n)`, `WithWait(d)` (see [Concurrency limits](#concurrency-limits))
and `WithLeaseOptions(acnologia.LeaseOptions{...})`.

#### SerpAPI search

```go
var results map[string]any
err := c.WithKey(ctx, "serpapi", func(l *acnologia.Lease) error {
	req, _ := http.NewRequestWithContext(ctx, "GET",
		l.URL("/search.json", url.Values{"engine": {"google"}, "q": {"coffee"}}), nil)
	l.Apply(req) // adds ?api_key=<secret>
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch {
	case resp.StatusCode == 429 && strings.Contains(string(body), "run out of searches"):
		return &acnologia.ErrQuotaExhausted{StatusCode: 429}
	case resp.StatusCode == 429:
		return &acnologia.ErrRateLimited{StatusCode: 429, RetryAfter: time.Hour}
	case resp.StatusCode == 401:
		return &acnologia.ErrInvalidKey{StatusCode: 401}
	case resp.StatusCode >= 400:
		return fmt.Errorf("serpapi: http %d", resp.StatusCode)
	}
	return json.Unmarshal(body, &results)
}, acnologia.WithMaxAttempts(3))
```

#### ElevenLabs text to speech

```go
var audio []byte
err := c.WithKey(ctx, "elevenlabs", func(l *acnologia.Lease) error {
	payload := strings.NewReader(`{"text":"Hello from Acnologia","model_id":"eleven_multilingual_v2"}`)
	req, _ := http.NewRequestWithContext(ctx, "POST",
		l.URL("/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM", nil), payload)
	req.Header.Set("Content-Type", "application/json")
	l.Apply(req) // sets xi-api-key: <secret>
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch {
	case resp.StatusCode == 429 && strings.Contains(string(body), "system_busy"):
		return fmt.Errorf("elevenlabs busy") // provider side, key not penalized
	case resp.StatusCode == 429:
		return &acnologia.ErrRateLimited{StatusCode: 429, RetryAfter: 5 * time.Second}
	case resp.StatusCode == 401 && strings.Contains(string(body), "quota_exceeded"):
		return &acnologia.ErrQuotaExhausted{StatusCode: 401}
	case resp.StatusCode == 401:
		return &acnologia.ErrInvalidKey{StatusCode: 401}
	case resp.StatusCode >= 400:
		return fmt.Errorf("elevenlabs: http %d", resp.StatusCode)
	}
	audio = body
	return nil
})
```

### Lease and Report by hand

```go
lease, err := c.Lease(ctx, "serpapi", &acnologia.LeaseOptions{TTL: 5 * time.Minute, Wait: 10 * time.Second})
if err != nil {
	return err
}
// Report on every path: an unreported lease holds a concurrency slot until its TTL.
req, _ := http.NewRequestWithContext(ctx, "GET",
	lease.URL("/search.json", url.Values{"q": {"coffee"}}), nil)
lease.Apply(req)
resp, err := http.DefaultClient.Do(req)
// ...
next, err := lease.Report(ctx, acnologia.Report{
	Outcome:    acnologia.OutcomeRateLimited,
	StatusCode: 429,
	RetryAfter: time.Hour,
	Rotate:     true, // ask for the next key in the same call
})
if next != nil {
	// retry with next
}
```

`Lease` fields: `LeaseID`, `KeyID`, `Service`, `Secret`, `Auth{Type, Name, Prefix}`, `BaseURL`, `ExpiresAt`.
`Apply(req)` places the secret according to `Auth.Type`: `query` sets the query
parameter `Auth.Name`, `header` sets header `Auth.Name`, `bearer` sets
`Auth.Name` (default `Authorization`) to `Bearer <secret>`. When `Auth.Prefix`
is non empty it goes in front of the secret for every type, and for `bearer` it
replaces `Bearer ` (for example `Prefix: "Token "` gives `Authorization: Token <secret>`).

Outcomes: `OutcomeOK`, `OutcomeRateLimited`, `OutcomeQuotaExhausted`, `OutcomeInvalid`, `OutcomeError`.

## Concurrency limits

Some providers cap how many requests one key may run at once (ElevenLabs
allows 2 per key by default). Acnologia enforces this per key, so a lease is
exclusive: on a service with a limit, each lease holds one of the key's slots
until you report it or its `TTL` runs out (default 120 seconds on limited
services, 300 otherwise).

- **Always report.** `WithKey` reports every lease: on success, on any error,
  when your callback panics (the panic is re-raised after the report), and
  when `ctx` is cancelled while it runs (the report goes out on a detached
  context). With a manual `Lease`, report on every return path.
- **Wait instead of failing.** Set `LeaseOptions.Wait` (or `WithWait(d)` for
  `WithKey`), up to `acnologia.MaxWait` (60s), and the server waits that long for
  a slot when every usable key is busy. If your `http.Client` has a `Timeout`,
  keep it longer than the wait.

```go
err := c.WithKey(ctx, "elevenlabs", speak, acnologia.WithWait(20*time.Second))

if errors.Is(err, acnologia.AllKeysBusy) {
	// every usable key is at its limit; retry soon
}
```

A busy pool returns `*acnologia.ErrNoKeyAvailable` with `Busy: true` and
`Code: "all_keys_busy"` (HTTP 503, `RetryAfter` usually 1s). It still matches
`acnologia.NoKeyAvailable`, so existing checks keep working; it clears as soon as
another caller reports.

Proxy mode needs none of this: Acnologia queues proxied requests for up to the
service's `queue_timeout_sec` (30 seconds for ElevenLabs) and never sends more
than the limit in parallel on one key.

## Services

```go
svcs, err := c.Services(ctx)
for _, s := range svcs {
	fmt.Println(s.Slug, s.Available, s.Auth.Type)
}
```

## Errors

| Type | Sentinel | When |
|---|---|---|
| `*acnologia.ErrNoKeyAvailable{RetryAfter, Code, Busy}` | `acnologia.NoKeyAvailable` | 503 `no_key_available` from the pool |
| `*acnologia.ErrNoKeyAvailable` with `Busy: true` | `acnologia.AllKeysBusy` (and `acnologia.NoKeyAvailable`) | 503 `all_keys_busy`: every usable key is at its concurrency limit |
| `*acnologia.ErrRateLimited{RetryAfter}` | `acnologia.RateLimited` | you return it from `WithKey` |
| `*acnologia.ErrQuotaExhausted` | `acnologia.QuotaExhausted` | you return it from `WithKey` |
| `*acnologia.ErrInvalidKey` | `acnologia.InvalidKey` | you return it from `WithKey` |
| `*acnologia.APIError{StatusCode, Code, Message}` | | any other non-2xx from the consumer API |

Each typed error matches its sentinel, so both styles work:

```go
if errors.Is(err, acnologia.NoKeyAvailable) { ... }

var nk *acnologia.ErrNoKeyAvailable
if errors.As(err, &nk) {
	time.Sleep(nk.RetryAfter)
}
```

The rotation errors have an optional `Err` field that is returned by `Unwrap`,
so you can attach the underlying cause.

## Tests

```sh
go test ./...
```
