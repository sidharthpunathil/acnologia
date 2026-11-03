# acnologia (Lua)

Lua client for [Acnologia](../../README.md), the self-hosted server that holds your API keys. Your code calls with its own `acn_` app token. Acnologia attaches a real key, rotates on rate limits and quota errors, and enforces the limits you set.

```lua
local acnologia = require("acnologia")

local client = acnologia.assert(acnologia.new({
  base_url = "http://127.0.0.1:8080",
  token = "acn_your_app_token",
}))

local resp = acnologia.assert(client:post("agi-labs", "/v1/chat", { json = { prompt = "hello" } }))
print(resp.status, resp.acnologia.key_id)
print(resp:json().reply)
```

That request goes to `/proxy/agi-labs/v1/chat`. Acnologia adds the key and sends it on. The key never reaches your process.

- Lua 5.1, LuaJIT, 5.2, 5.3, 5.4 and 5.5.
- Pure Lua. A JSON encoder and decoder is built in.
- HTTP goes through luasocket (with luasec for https), lua-curl, or a transport you pass in.

## Install

The rock is not on luarocks.org yet. Install it from the repository:

```sh
luarocks install --local https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/sdk/lua/acnologia-scm-1.rockspec
```

This pulls in luasocket and luasec. From a clone, run `luarocks make --local sdk/lua/acnologia-scm-1.rockspec` at the repository root.

You can also copy the `acnologia` folder into your project. It needs no rock to load. Then bring your own HTTP library or transport.

## Configure

```lua
local client, err = acnologia.new({
  base_url = "http://127.0.0.1:8080", -- default: ACNOLOGIA_URL
  token = "acn_...",                  -- default: ACNOLOGIA_TOKEN
  access_password = nil,              -- default: ACNOLOGIA_ACCESS_PASSWORD
  timeout = 30,                       -- seconds, for API calls
  proxy_timeout = 120,                -- seconds, for proxy calls
  transport = nil,                    -- default: luasocket, then lua-curl
})
```

With the variables set, `acnologia.new()` is enough. A missing URL or token returns `nil` and an error with code `config`.

The access password is sent as `X-Acnologia-Access`. It is sent only when you pass it or set `ACNOLOGIA_ACCESS_PASSWORD`. Use it when the server sits behind its access lock.

`tostring(client)` never shows the token.

## Proxy

`client:proxy(method, service, path, opts)` sends any method. `get`, `post`, `put`, `patch`, `delete`, `head` and `options` are shortcuts.

```lua
local resp, err = client:get("agi-labs", "/v1/models", { query = { limit = 10 } })

resp, err = client:post("agi-labs", "/v1/chat", {
  json = { prompt = "hello" },          -- or body = "raw string"
  headers = { ["X-Trace"] = "abc" },
  idempotency_key = "order-42",
  timeout = 60,
})
```

A response has:

- `status`, `headers` (lower case names) and `body` (a string).
- `resp:json()` and `resp:ok()`.
- `resp.acnologia`: `key_id`, `attempts`, `service`, `cache`, `share` and `replayed`, read from the `X-Acnologia-*` and `Idempotent-Replayed` headers.

A provider error, such as a 429 from the provider, comes back as a response. When Acnologia itself refuses the call, you get `nil` and an error instead. Examples are `app_limit_reached`, `no_key_available`, `upstream_unreachable`, `bad_path` and `body_too_large`. Then `err.response` holds the raw response.

Proxy calls never follow redirects, so your token is never sent to another host.

## Leases

A lease hands you the raw key for a while. Use it when a library needs the key itself.

```lua
local lease = acnologia.assert(client:lease("agi-labs", {
  ttl_sec = 120,         -- lease length
  wait_sec = 10,         -- wait up to 60 seconds for a free slot
  exclusive = false,     -- true keeps the key from every other caller
  exclude_key_ids = {},  -- key ids to skip
}))

-- Put the key where the service expects it: a header, a bearer token or a query parameter.
local headers, query = lease:apply({ ["Content-Type"] = "application/json" }, { model = "small" })
local url = lease:url("/v1/chat", { model = "small" }) -- base URL, path and query (with the key for query auth)

-- A long job can keep the key longer.
local expires_at = acnologia.assert(lease:renew(300))

-- Always report. With rotate, you get the next lease back.
local next_lease, err = lease:report("rate_limited", {
  status_code = 429,
  retry_after_sec = 30,
  message = "provider said slow down",
  rotate = true,
})
```

`report` returns the next lease, `false` when there is none, or `nil` and an error. It replaces the secret in `message` with `[key]` and cuts it to 500 bytes. Outcomes are `ok`, `rate_limited`, `busy`, `quota_exhausted`, `invalid`, `banned` and `error`.

`lease:secret()` returns the key. `tostring(lease)` hides it.

### with_key

`client:with_key(service, fn, opts)` leases a key, calls `fn(lease)`, reports, and returns what `fn` returned. Raise or return `acnologia.rate_limited()`, `acnologia.quota_exhausted()`, `acnologia.invalid_key()` or `acnologia.banned_key()` to rotate to the next key.

```lua
local reply, err = client:with_key("agi-labs", function(lease)
  local status, body = call_agi_labs(lease) -- your own HTTP call
  if status == 429 then
    error(acnologia.rate_limited({ retry_after = 30, status_code = 429 }))
  end
  return body
end, { max_attempts = 5, wait_sec = 10 })
```

Every lease is reported, whatever `fn` does. Another error raised in `fn` is reported as `error` and raised again.

## Me

```lua
local me = acnologia.assert(client:me())
print(me.app.name, me.limit_hit, #me.services)
```

`client:services()` lists the services the token may use.

## Errors

Calls return a value, or `nil` and an error table:

| Field | Meaning |
|---|---|
| `code` | The server's code, such as `lease_not_found`. Also `transport`, `timeout`, `bad_response`, `bad_request`, `config`, or `http_<status>` when there is none. |
| `status` | HTTP status, or 0 when the request did not get an answer. |
| `message` | What went wrong. Never holds the token, the access password or a secret. |
| `retry_after` | Seconds to wait, from `retry_after_sec` or the `Retry-After` header. |
| `limit`, `reset_at` | For `app_limit_reached`: which limit, and when it resets. |
| `busy`, `no_key` | `busy` is true for `all_keys_busy`. `no_key` is true for it and for `no_key_available`. |
| `body` | The decoded error body, when there was one. |

A 503 without a code counts as `no_key_available`. `tostring(err)` gives one line.

`acnologia.assert(v, err)` raises the error table when `v` is nil. Catch it with `pcall` and read the same fields:

```lua
local ok, err = pcall(function()
  local lease = acnologia.assert(client:lease("agi-labs"))
  -- ...
end)
if not ok and err.code == "all_keys_busy" then
  -- wait err.retry_after seconds
end
```

Codes to expect: `unauthenticated`, `service_not_allowed`, `proxy_not_allowed`, `lease_not_allowed`, `no_key_available`, `all_keys_busy`, `service_unavailable`, `app_limit_reached`, `share_revoked`, `share_paused`, `share_expired`, `lease_not_found`, `report_failed`, `upstream_unreachable`, `upstream_blocked`, `loop_detected`, `bad_path` and `body_too_large`. See the [HTTP API](../../docs/api.md#consumer-api).

## Custom transports

A transport is one function:

```lua
-- returns status, headers (lower case names), body; or nil and a message
local function transport(method, url, headers, body, timeout) end
```

It must not follow redirects. The built in ones are `acnologia.transport.luasocket()` and `acnologia.transport.curl()`.

In OpenResty, wrap lua-resty-http:

```lua
local http = require("resty.http")

local function resty_transport(method, url, headers, body, timeout)
  local httpc = http.new()
  httpc:set_timeout(timeout * 1000)
  local res, err = httpc:request_uri(url, { method = method, headers = headers, body = body })
  if not res then return nil, err end
  local out = {}
  for k, v in pairs(res.headers) do
    out[k:lower()] = type(v) == "table" and table.concat(v, ", ") or v
  end
  return res.status, out, res.body
end

local client = acnologia.assert(acnologia.new({ transport = resty_transport }))
```

In Neovim 0.10 or later, `vim.system` with `curl` works the same way. Any library that returns status, headers and body will do.

## Tests

From `sdk/lua`, with busted:

```sh
luarocks install --local busted
busted
```

Without busted, `lua spec/run.lua` runs the same specs. Both need luasocket for the integration spec, which starts a small HTTP server in a child process. When lua-curl is installed, it runs over that too.
