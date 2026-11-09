# Acnologia for Haskell

Call the APIs your Acnologia server holds keys for, from Haskell, with your app's own `acn_` token. Send requests through the proxy and the key never reaches your process. Or lease a key, call the provider yourself, and report how it went.

Every call runs in `IO` and returns `Either AcnologiaError a`. Each one has a variant ending in `OrThrow` that throws the error instead.

## Quick start

```haskell
{-# LANGUAGE OverloadedStrings #-}
import Acnologia

main :: IO ()
main = do
  client <- newClientOrThrow defaultConfig
  res <- proxyOrThrow client "agi-labs" (proxyRequest "GET" "/v1/models")
  print (responseStatus res, responseBody res)
```

`defaultConfig` reads `ACNOLOGIA_URL` and `ACNOLOGIA_TOKEN`. `agi-labs` is the slug of a service on your server.

## Install

The package is not on Hackage yet. Add it from the repository in your `cabal.project`:

```cabal
packages: .

source-repository-package
  type: git
  location: https://github.com/sidharthpunathil/acnologia.git
  tag: main
  subdir: sdk/haskell
```

Or point at a local checkout:

```cabal
packages: . path/to/acnologia/sdk/haskell
```

Then add `acnologia` to `build-depends`. It needs GHC 9.4 or later. It is tested with GHC 9.14.1 and cabal 3.18.

## Connect

| Setting | Environment variable | Default |
|---|---|---|
| `configBaseUrl` | `ACNOLOGIA_URL` | required |
| `configToken` | `ACNOLOGIA_TOKEN` | required |
| `configAccessPassword` | `ACNOLOGIA_ACCESS_PASSWORD` | not sent |
| `configTimeoutSec` | | 75 seconds for API calls |
| `configProxyTimeoutSec` | | 300 seconds for proxy calls |
| `configManager` | | a new TLS manager |

A setting you give wins over the environment. The access password is sent as `X-Acnologia-Access` only when you set it or the variable is set. Use it when the server sits behind an access password.

```haskell
client <- newClientOrThrow defaultConfig
  { configBaseUrl = Just "http://127.0.0.1:8080"
  , configToken = Just "acn_your_app_token"
  , configTimeoutSec = 30
  }
```

The client never follows redirects, so your token is not sent to another host. `show` hides the token.

## Proxy

Acnologia attaches a key and moves to the next one on a rate limit. You get the provider's answer as it is, whatever its status.

```haskell
let req = (withJsonBody (object ["prompt" .= ("hello" :: Text)]) (proxyRequest "POST" "/v1/chat"))
      { proxyQuery = [("stream", "false")]
      , proxyHeaders = [("Accept", "application/json")]
      , proxyIdempotencyKey = Just "order-42"
      , proxyNoCache = True   -- Cache-Control: no-cache
      }
r <- proxy client "agi-labs" req
case r of
  Right res -> do
    print (responseStatus res)
    print (acnKeyId (responseAcnologia res), acnAttempts (responseAcnologia res))
    print (acnReplayed (responseAcnologia res))
  Left err -> print err
```

`responseAcnologia` holds the `X-Acnologia-*` headers: `acnKeyId`, `acnAttempts`, `acnService`, `acnCache`, `acnReplayed` (an idempotent call was replayed), and on a capability call `acnCapability` and `acnTried` (each route tried, as `Tried {triedService, triedStatus}`). A retry with the same `proxyIdempotencyKey` replays the first answer instead of calling the provider again.

`Left` means Acnologia itself refused or failed, such as `ProxyNotAllowed` or `UpstreamUnreachable`. A 429 from the provider is a `Right`.

Shortcuts: `proxyGet client "agi-labs" "/v1/models" [("limit", "5")]` and `proxyPostJson client "agi-labs" "/v1/chat" body`.

## Lease, renew and report

Use a lease when your code must hold the key. Always report it. On a service with a concurrency limit an open lease holds a slot until it is reported or expires.

```haskell
l <- leaseOrThrow client "agi-labs" defaultLeaseOptions { leaseTtlSec = Just 120, leaseWaitSec = Just 10 }

-- Put the key where the service expects it.
let url = leaseUrl l "/v1/chat" []          -- adds the key for query auth
    hdrs = applyHeaders l [("Accept", "application/json")]  -- adds it for header and bearer auth
-- Or, with http-client: applyToRequest l request

-- A long job keeps its key.
l' <- renewOrThrow client l (Just 600)

-- Tell the server how it went. With rotate, you get the next key in the same call.
next <- reportOrThrow client l' (outcomeReport OutcomeRateLimited)
  { reportStatusCode = Just 429, reportRetryAfterSec = Just 30, reportRotate = True }
```

`LeaseOptions` also has `leaseExcludeKeyIds` and `leaseExclusiveKey`. The outcomes are `OutcomeOk`, `OutcomeRateLimited`, `OutcomeBusy`, `OutcomeQuotaExhausted`, `OutcomeInvalid`, `OutcomeBanned` and `OutcomeError`. A report message has the secret replaced with `[key]` and is cut to 500 characters. `show` on a lease hides the secret.

`withKey` runs that loop for you. Return `Left (KeyRateLimited ...)`, `Left (KeyQuotaExhausted ...)` or `Left (KeyInvalid ...)` to report the key and try the next one. It reports every lease, even when your function throws.

```haskell
answer <- withKey client "agi-labs" defaultWithKeyOptions $ \l -> do
  status <- callTheProvider l
  pure $ if status == 429 then Left (KeyRateLimited Nothing (Just 429) Nothing) else Right status
```

A service another Acnologia server shares with yours is proxy only. A lease on it fails with `LeaseNotAllowed`.

## Me

```haskell
m <- meOrThrow client
print (meAppName (meApp m))
print (map meServiceSlug (meServices m))
print (usageTodayRequests (meUsage m), meLimitHit m)
```

`meShare` is set when the token belongs to a share. Each service has `meServiceAvailable` (keys free now for a share's token, 1 or 0 for an app) and `meServiceLogoUrl` (a `data:` image URI). `meRaw` has the whole answer. `services client` lists the services with their base URL, auth placement and `serviceGuideUrl`.

## Usage guides

```haskell
md <- guideMarkdownOrThrow client "agi-labs"   -- the guide as Markdown
g <- guideOrThrow client "agi-labs"            -- guideText, guideGenerated, guideStructured (JSON)
```

Both fail with `ServiceNotAllowed` for a service the token may not use.

## Capabilities

A capability is one job several providers can do, behind one slug:

```haskell
caps <- capabilitiesOrThrow client
mapM_ (\k -> print (capabilitySlug k, capabilityParams k)) caps

r <- capabilityOrThrow client "maps-search" (object ["query" .= ("cafes in Kochi" :: Text)])
       defaultCapabilityOptions {capService = Just "serpapi"}   -- capRaw = True adds ?_raw=1
print (acnService (responseAcnologia r), acnTried (responseAcnologia r))
```

`capability` sends `POST /proxy/{slug}` with the params as the body and answers like `proxy`. `capIdempotencyKey` and `capNoCache` work as on a proxy request. `bad_params`, `no_route` and `no_route_worked` come back as `ApiError` with that `errorCode`.

## Forward proxy

An app can use Acnologia as its `HTTPS_PROXY` and call provider URLs as they are. Save the CA, set the environment, then use the app's own HTTP client:

```haskell
saveForwardProxyCAOrThrow client "acnologia-ca.pem"
env <- forwardProxyEnvOrThrow client "acnologia-ca.pem"
mapM_ (\(k, v) -> setEnv (T.unpack k) (T.unpack v)) (Map.toList env)
-- A child process (node, python, curl) now goes through Acnologia.
```

http-client's default manager also reads `HTTPS_PROXY`, but http-client-tls trusts only the system store, so a Haskell client needs the Acnologia CA added there (this SDK adds no TLS dependency to load it for you).

| Call | Returns |
|---|---|
| `forwardProxy` | `ForwardProxy`: `fpProxyUrl`, `fpProxyUrlWithToken` (with an `<app token>` placeholder), `fpCaUrl`, `fpCaFingerprint`, `fpPassthrough`, `fpHosts`, `fpEnv`, `fpServiceHeader` |
| `forwardProxyCA` | The CA as PEM text. Sent without the token |
| `saveForwardProxyCA client path` | Writes the CA to `path` |
| `forwardProxyURL` | The proxy URL with user `app` and the token as the password: `http://app:acn_...@host:8888` |
| `forwardProxyEnv client caPath` | `HTTPS_PROXY`, `HTTP_PROXY`, `NODE_USE_ENV_PROXY=1`, and `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`, `SSL_CERT_FILE` set to `caPath` |

`ForwardProxy` and its `show` never hold the token. `forwardProxyURL` and `forwardProxyEnv` do: keep them out of logs. Each call has an `OrThrow` variant.

## Errors

`AcnologiaError` is a sum type with an `Exception` instance. Server errors map from the status and the error code:

| Constructor | Status and code |
|---|---|
| `Unauthenticated` | 401, or 403 `unauthenticated` |
| `ServiceNotAllowed`, `ProxyNotAllowed`, `LeaseNotAllowed` | 403 with that code |
| `NoKeyAvailable`, `AllKeysBusy`, `ServiceUnavailable` | 503 with that code, plus seconds to wait |
| `AppLimitReached` | 429 `app_limit_reached`, with the limit, its reset time and seconds to wait |
| `ShareRevoked`, `SharePaused`, `ShareExpired` | 403 with that code. `shareUnavailable err` gives `Just "revoked"`, `"paused"` or `"expired"` for these |
| `LeaseNotFound` | 404 `lease_not_found` |
| `ReportFailed` | 409 or 410 `report_failed` |
| `UpstreamUnreachable`, `UpstreamBlocked` | 502 with that code |
| `LoopDetected` | 508 `loop_detected` |
| `BadPath` | 400 `bad_path` |
| `BodyTooLarge` | 413 `body_too_large` |
| `ApiError` | any other error, such as `empty_group`, `bad_params`, `no_route` or `no_route_worked` (read `errorCode`) |
| `RotationExhausted` | `withKey` ran out of attempts |
| `HttpFailure`, `BadResponse`, `InvalidInput` | network failure, an answer that could not be decoded, a bad setting |

The wait comes from `retry_after_sec` in the body, else from the `Retry-After` header. Read it with `errorRetryAfter`. `errorStatus` and `errorCode` work on any error. Messages never contain your token, the access password or a lease secret.

```haskell
r <- lease client "agi-labs" defaultLeaseOptions
case r of
  Left (AllKeysBusy _ wait) -> print ("busy, retry in", wait)
  Left err -> print (errorCode err)
  Right l -> ...
```

## Tests

```sh
cd sdk/haskell
cabal update
cabal build all && cabal test
```

The suite starts a fake server in the test process. Warnings are errors when you build from this folder. `cabal run -fexamples acnologia-smoke -- agi-labs` runs proxy, lease, renew, report and me against a real server from `ACNOLOGIA_URL` and `ACNOLOGIA_TOKEN`.
