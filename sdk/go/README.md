# cogito-go

Go client for [Cogito](../../docs/llms.txt), a self-hosted API key pool. Standard library only.

Two ways to use your pooled keys:

- **Proxy mode**: send requests to Cogito instead of the provider. Cogito injects a key, retries with the next key on rate limits, and never exposes the secret.
- **Lease mode**: lease a key, call the provider yourself, report the outcome. `WithKey` runs that loop and rotates keys for you.

## Install

```sh
go get github.com/cogito-keys/cogito-go
```

Requires Go 1.22+.

```go
import cogito "github.com/cogito-keys/cogito-go"

c := cogito.New("http://localhost:8080", os.Getenv("COGITO_TOKEN")) // app token, cog_...
```

Options: `cogito.WithHTTPClient(*http.Client)`, `cogito.WithUserAgent(string)`.

## Proxy mode

`c.Proxy(service)` returns a helper whose requests go to `/proxy/{service}/{path}`.
Upstream responses come back unchanged (non-2xx is not an error). Each response
carries `X-Cogito-Key-Id` and `X-Cogito-Attempts`.

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
	map[string]string{"text": "Hello from Cogito", "model_id": "eleven_multilingual_v2"})
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
| `*cogito.ErrRateLimited{RetryAfter}` | `rate_limited` | yes |
| `*cogito.ErrQuotaExhausted{}` | `quota_exhausted` | yes |
| `*cogito.ErrInvalidKey{}` | `invalid` | yes |
| any other error | `error` | no, the error is returned as is |

Wrapped errors (`fmt.Errorf("...: %w", err)`) are recognized. After
`WithMaxAttempts(n)` (default 5) keys, the last rotation error is returned; the
final attempt is reported without asking for a next key.

#### SerpAPI search

```go
var results map[string]any
err := c.WithKey(ctx, "serpapi", func(l *cogito.Lease) error {
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
		return &cogito.ErrQuotaExhausted{StatusCode: 429}
	case resp.StatusCode == 429:
		return &cogito.ErrRateLimited{StatusCode: 429, RetryAfter: time.Hour}
	case resp.StatusCode == 401:
		return &cogito.ErrInvalidKey{StatusCode: 401}
	case resp.StatusCode >= 400:
		return fmt.Errorf("serpapi: http %d", resp.StatusCode)
	}
	return json.Unmarshal(body, &results)
}, cogito.WithMaxAttempts(3))
```

#### ElevenLabs text to speech

```go
var audio []byte
err := c.WithKey(ctx, "elevenlabs", func(l *cogito.Lease) error {
	payload := strings.NewReader(`{"text":"Hello from Cogito","model_id":"eleven_multilingual_v2"}`)
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
		return &cogito.ErrRateLimited{StatusCode: 429, RetryAfter: 5 * time.Second}
	case resp.StatusCode == 401 && strings.Contains(string(body), "quota_exceeded"):
		return &cogito.ErrQuotaExhausted{StatusCode: 401}
	case resp.StatusCode == 401:
		return &cogito.ErrInvalidKey{StatusCode: 401}
	case resp.StatusCode >= 400:
		return fmt.Errorf("elevenlabs: http %d", resp.StatusCode)
	}
	audio = body
	return nil
})
```

### Lease and Report by hand

```go
lease, err := c.Lease(ctx, "serpapi", &cogito.LeaseOptions{TTL: 5 * time.Minute})
if err != nil {
	return err
}
req, _ := http.NewRequestWithContext(ctx, "GET",
	lease.URL("/search.json", url.Values{"q": {"coffee"}}), nil)
lease.Apply(req)
resp, err := http.DefaultClient.Do(req)
// ...
next, err := lease.Report(ctx, cogito.Report{
	Outcome:    cogito.OutcomeRateLimited,
	StatusCode: 429,
	RetryAfter: time.Hour,
	Rotate:     true, // ask for the next key in the same call
})
if next != nil {
	// retry with next
}
```

`Lease` fields: `LeaseID`, `KeyID`, `Service`, `Secret`, `Auth{Type, Name}`, `BaseURL`, `ExpiresAt`.
`Apply(req)` places the secret according to `Auth.Type`: `query` sets the query
parameter `Auth.Name`, `header` sets header `Auth.Name`, `bearer` sets
`Auth.Name` (default `Authorization`) to `Bearer <secret>`.

Outcomes: `OutcomeOK`, `OutcomeRateLimited`, `OutcomeQuotaExhausted`, `OutcomeInvalid`, `OutcomeError`.

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
| `*cogito.ErrNoKeyAvailable{RetryAfter}` | `cogito.NoKeyAvailable` | 503 `no_key_available` from the pool |
| `*cogito.ErrRateLimited{RetryAfter}` | `cogito.RateLimited` | you return it from `WithKey` |
| `*cogito.ErrQuotaExhausted` | `cogito.QuotaExhausted` | you return it from `WithKey` |
| `*cogito.ErrInvalidKey` | `cogito.InvalidKey` | you return it from `WithKey` |
| `*cogito.APIError{StatusCode, Code, Message}` | | any other non-2xx from the consumer API |

Each typed error matches its sentinel, so both styles work:

```go
if errors.Is(err, cogito.NoKeyAvailable) { ... }

var nk *cogito.ErrNoKeyAvailable
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
