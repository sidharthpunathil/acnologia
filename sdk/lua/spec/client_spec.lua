local acnologia = require("acnologia")
local json = acnologia.json

local TOKEN = "acn_test_token_123"
local SECRET = "sk/live+secret=1"

-- A fake transport: answers come from a queue, requests are recorded.
local function fake(responses)
  local f = { calls = {}, queue = responses or {} }
  f.transport = function(method, url, headers, body, timeout)
    f.calls[#f.calls + 1] = { method = method, url = url, headers = headers, body = body, timeout = timeout }
    local r = table.remove(f.queue, 1)
    if r == nil then return nil, "no fake response queued" end
    if type(r) == "function" then return r(method, url, headers, body, timeout) end
    if r.fail then return nil, r.fail end
    local b = r.body
    if type(b) == "table" then b = assert(json.encode(b)) end
    return r.status or 200, r.headers or {}, b or ""
  end
  function f.last() return f.calls[#f.calls] end
  function f.body(i) return json.decode(f.calls[i or #f.calls].body) end
  return f
end

local function client(f, extra)
  local opts = { base_url = "http://acn.test/", token = TOKEN, transport = f.transport, getenv = function() return nil end }
  for k, v in pairs(extra or {}) do opts[k] = v end
  return assert(acnologia.new(opts))
end

local LEASE = {
  lease_id = "lease/1", key_id = 7, service = "agi-labs", secret = SECRET,
  auth = { type = "header", name = "X-Api-Key", prefix = "" },
  base_url = "https://api.agi-labs.test/", expires_at = "2026-11-04T10:00:00Z",
}

local function lease_with(auth)
  local l = {}
  for k, v in pairs(LEASE) do l[k] = v end
  l.auth = auth
  return l
end

describe("client config", function()
  it("requires a base URL and token", function()
    local c, err = acnologia.new({ getenv = function() return nil end, transport = function() end })
    assert.is_nil(c)
    assert.are.equal("config", err.code)
    assert.truthy(err.message:find("ACNOLOGIA_URL", 1, true))
    c, err = acnologia.new({ base_url = "http://x", getenv = function() return nil end, transport = function() end })
    assert.is_nil(c)
    assert.truthy(err.message:find("ACNOLOGIA_TOKEN", 1, true))
  end)

  it("defaults from ACNOLOGIA_URL, ACNOLOGIA_TOKEN and ACNOLOGIA_ACCESS_PASSWORD", function()
    local env = { ACNOLOGIA_URL = "http://env.test//", ACNOLOGIA_TOKEN = "acn_env", ACNOLOGIA_ACCESS_PASSWORD = "gatepw" }
    local f = fake({ { body = { app = { id = 1 } } } })
    local c = assert(acnologia.new({ transport = f.transport, getenv = function(k) return env[k] end }))
    assert.are.equal("http://env.test", c.base_url)
    assert(c:me())
    assert.are.equal("http://env.test/api/v1/me", f.last().url)
    assert.are.equal("Bearer acn_env", f.last().headers.Authorization)
    assert.are.equal("gatepw", f.last().headers["X-Acnologia-Access"])
  end)

  it("sends no access header unless one is set", function()
    local f = fake({ { body = {} } })
    local c = client(f)
    assert(c:me())
    assert.is_nil(f.last().headers["X-Acnologia-Access"])
  end)

  it("takes the access password explicitly", function()
    local f = fake({ { body = {} } })
    local c = client(f, { access_password = "pw1" })
    assert(c:me())
    assert.are.equal("pw1", f.last().headers["X-Acnologia-Access"])
  end)

  it("explicit values win over the environment", function()
    local env = { ACNOLOGIA_URL = "http://env.test", ACNOLOGIA_TOKEN = "acn_env" }
    local f = fake({ { body = {} } })
    local c = assert(acnologia.new({ base_url = "http://mine.test", token = "acn_mine", transport = f.transport, getenv = function(k) return env[k] end }))
    assert(c:me())
    assert.are.equal("http://mine.test/api/v1/me", f.last().url)
    assert.are.equal("Bearer acn_mine", f.last().headers.Authorization)
  end)

  it("never prints the token", function()
    local c = client(fake())
    local s = tostring(c)
    assert.truthy(s:find("***", 1, true))
    assert.is_nil(s:find(TOKEN, 1, true))
    for _, v in pairs(c) do
      assert.are_not.equal(TOKEN, v)
    end
  end)

  it("uses the API timeout and lengthens it for a lease wait", function()
    local f = fake({ { body = {} }, { body = LEASE } })
    local c = client(f, { timeout = 5 })
    assert(c:me())
    assert.are.equal(5, f.last().timeout)
    assert(c:lease("agi-labs", { wait_sec = 20 }))
    assert.are.equal(30, f.last().timeout)
  end)
end)

describe("me", function()
  it("GETs /api/v1/me with the app token", function()
    local f = fake({ { body = { app = { id = 3, name = "bot" }, limit_hit = json.null, services = { { slug = "agi-labs" } } } } })
    local c = client(f)
    local me = assert(c:me())
    assert.are.equal("bot", me.app.name)
    assert.are.equal("agi-labs", me.services[1].slug)
    local call = f.last()
    assert.are.equal("GET", call.method)
    assert.are.equal("Bearer " .. TOKEN, call.headers.Authorization)
    assert.are.equal("application/json", call.headers.Accept)
    assert.truthy(call.headers["User-Agent"]:find("acnologia-lua/", 1, true))
    assert.is_nil(call.body)
  end)

  it("lists services", function()
    local f = fake({ { body = { services = { { slug = "agi-labs", available = true } } } } })
    local list = assert(client(f):services())
    assert.are.equal("agi-labs", list[1].slug)
  end)
end)

describe("errors", function()
  local cases = {
    { 401, "unauthenticated" },
    { 403, "unauthenticated" },
    { 403, "service_not_allowed" },
    { 403, "proxy_not_allowed" },
    { 403, "lease_not_allowed" },
    { 503, "no_key_available" },
    { 503, "all_keys_busy" },
    { 503, "service_unavailable" },
    { 429, "app_limit_reached" },
    { 403, "share_revoked" },
    { 403, "share_paused" },
    { 403, "share_expired" },
    { 404, "lease_not_found" },
    { 409, "report_failed" },
    { 410, "report_failed" },
    { 502, "upstream_unreachable" },
    { 502, "upstream_blocked" },
    { 508, "loop_detected" },
    { 400, "bad_path" },
    { 413, "body_too_large" },
  }
  for _, case in ipairs(cases) do
    local status, code = case[1], case[2]
    it("maps " .. status .. " " .. code, function()
      local f = fake({ { status = status, body = { error = { code = code, message = "msg for " .. code } } } })
      local v, err = client(f):me()
      assert.is_nil(v)
      assert.is_true(acnologia.is_error(err))
      assert.are.equal(code, err.code)
      assert.are.equal(status, err.status)
      assert.are.equal("msg for " .. code, err.message)
      assert.are.equal(code == "all_keys_busy", err.busy)
      assert.are.equal(code == "all_keys_busy" or code == "no_key_available", err.no_key)
      assert.truthy(tostring(err):find(code, 1, true))
    end)
  end

  it("carries retry_after from retry_after_sec", function()
    local f = fake({ { status = 503, headers = { ["retry-after"] = "9" }, body = { error = { code = "no_key_available", message = "m" }, retry_after_sec = 4 } } })
    local _, err = client(f):lease("agi-labs")
    assert.are.equal(4, err.retry_after)
  end)

  it("falls back to the Retry-After header", function()
    local f = fake({ { status = 503, headers = { ["retry-after"] = "12" }, body = { error = { code = "service_unavailable", message = "m" }, retry_after_sec = 0 } } })
    local _, err = client(f):lease("agi-labs")
    assert.are.equal("service_unavailable", err.code)
    assert.are.equal(12, err.retry_after)
    assert.is_false(err.no_key)
  end)

  it("reads limit, reset_at and retry_after for app_limit_reached", function()
    local f = fake({ { status = 429, headers = { ["retry-after"] = "3600" }, body = {
      error = { code = "app_limit_reached", message = "this app reached its daily requests limit" },
      limit = "daily_requests", reset_at = "2026-11-05T00:00:00Z", retry_after_sec = 3600,
    } } })
    local _, err = client(f):lease("agi-labs")
    assert.are.equal("app_limit_reached", err.code)
    assert.are.equal(429, err.status)
    assert.are.equal("daily_requests", err.limit)
    assert.are.equal("2026-11-05T00:00:00Z", err.reset_at)
    assert.are.equal(3600, err.retry_after)
  end)

  it("treats a 503 without a code as no_key_available", function()
    local f = fake({ { status = 503, body = "upstream down" } })
    local _, err = client(f):me()
    assert.are.equal("no_key_available", err.code)
    assert.are.equal("upstream down", err.message)
  end)

  it("uses http_<status> when there is no code", function()
    local f = fake({ { status = 500, body = "" } })
    local _, err = client(f):me()
    assert.are.equal("http_500", err.code)
    assert.are.equal("HTTP 500", err.message)
  end)

  it("reports transport failures and timeouts", function()
    local f = fake({ { fail = "connection refused" }, { fail = "timeout" } })
    local c = client(f)
    local _, err = c:me()
    assert.are.equal("transport", err.code)
    assert.are.equal(0, err.status)
    _, err = c:me()
    assert.are.equal("timeout", err.code)
  end)

  it("catches a transport that raises", function()
    local c = client({ transport = function() error("boom " .. TOKEN) end })
    local _, err = c:me()
    assert.are.equal("transport", err.code)
    assert.is_nil(err.message:find(TOKEN, 1, true))
  end)

  it("flags a bad JSON answer", function()
    local f = fake({ { status = 200, body = "{nope" } })
    local _, err = client(f):me()
    assert.are.equal("bad_response", err.code)
  end)

  it("never puts the token, access password or secret in a message", function()
    local f = fake({
      { status = 403, body = { error = { code = "unauthenticated", message = "bad token " .. TOKEN .. " pw gatepw" } } },
      { fail = "failed http://acn.test/?t=" .. TOKEN },
      { status = 500, body = "raw " .. TOKEN },
    })
    local c = client(f, { access_password = "gatepw" })
    for _ = 1, 3 do
      local _, err = c:me()
      assert.is_nil(err.message:find(TOKEN, 1, true))
      assert.is_nil(err.message:find("gatepw", 1, true))
      assert.is_nil(tostring(err):find(TOKEN, 1, true))
    end
  end)

  it("assert raises the error table and passes values through", function()
    local f = fake({ { status = 404, body = { error = { code = "lease_not_found", message = "gone" } } }, { body = { ok = true } } })
    local c = client(f)
    local ok, raised = pcall(function() return acnologia.assert(c:me()) end)
    assert.is_false(ok)
    assert.are.equal("lease_not_found", raised.code)
    assert.are.equal(404, raised.status)
    local v = acnologia.assert(c:me())
    assert.is_true(v.ok)
    assert.are.equal(false, acnologia.assert(false))
  end)
end)

describe("proxy", function()
  it("builds the URL with query, sends our token and returns status, headers and body", function()
    local f = fake({ { status = 200, headers = {
      ["X-Acnologia-Key-Id"] = "7", ["X-Acnologia-Attempts"] = "2", ["X-Acnologia-Service"] = "agi-labs",
      ["X-Acnologia-Cache"] = "miss", ["Content-Type"] = "application/json",
    }, body = '{"reply":"hi"}' } })
    local c = client(f)
    local resp = assert(c:get("agi-labs", "/v1/models", { query = { b = "2 3", a = "x&y", list = { 1, 2 } }, headers = { Authorization = "Bearer theirs" } }))
    local call = f.last()
    assert.are.equal("GET", call.method)
    assert.are.equal("http://acn.test/proxy/agi-labs/v1/models?a=x%26y&b=2%203&list=1&list=2", call.url)
    assert.are.equal("Bearer " .. TOKEN, call.headers.Authorization)
    assert.are.equal(200, resp.status)
    assert.is_true(resp:ok())
    assert.are.equal("application/json", resp.headers["content-type"])
    assert.are.equal("hi", resp:json().reply)
    assert.are.equal(7, resp.acnologia.key_id)
    assert.are.equal(2, resp.acnologia.attempts)
    assert.are.equal("agi-labs", resp.acnologia.service)
    assert.are.equal("miss", resp.acnologia.cache)
    assert.is_false(resp.acnologia.replayed)
    assert.are.equal(120, call.timeout)
  end)

  it("encodes JSON bodies and passes strings through", function()
    local f = fake({ { body = "{}", headers = { ["x-acnologia-service"] = "agi-labs" } }, { body = "" }, { body = "" } })
    local c = client(f)
    assert(c:post("agi-labs", "v1/chat", { json = { prompt = "hello" } }))
    assert.are.equal("POST", f.last().method)
    assert.are.equal("http://acn.test/proxy/agi-labs/v1/chat", f.last().url)
    assert.are.equal('{"prompt":"hello"}', f.last().body)
    assert.are.equal("application/json", f.last().headers["Content-Type"])
    assert(c:put("agi-labs", "/v1/raw", { body = "a=1", headers = { ["Content-Type"] = "text/plain" } }))
    assert.are.equal("a=1", f.last().body)
    assert.are.equal("text/plain", f.last().headers["Content-Type"])
    assert(c:proxy("propfind", "agi-labs", "/x"))
    assert.are.equal("PROPFIND", f.last().method)
  end)

  it("sends Idempotency-Key and reads the replay header", function()
    local f = fake({ { status = 201, headers = { ["Idempotent-Replayed"] = "true" }, body = "{}" } })
    local resp = assert(client(f):post("agi-labs", "/v1/chat", { json = {}, idempotency_key = "order-42" }))
    assert.are.equal("order-42", f.last().headers["Idempotency-Key"])
    assert.is_true(resp.acnologia.replayed)
    assert.are.equal(201, resp.status)
  end)

  it("returns provider errors as responses", function()
    local f = fake({ { status = 429, headers = { ["X-Acnologia-Service"] = "agi-labs", ["Retry-After"] = "5" }, body = { error = { code = "rate_limit", message = "slow" } } } })
    local resp = assert(client(f):get("agi-labs", "/v1/models"))
    assert.are.equal(429, resp.status)
    assert.are.equal("5", resp.headers["retry-after"])
  end)

  it("returns Acnologia's own refusals as errors", function()
    local f = fake({
      { status = 429, headers = { ["Retry-After"] = "60" }, body = { error = { code = "app_limit_reached", message = "limit" }, limit = "monthly_budget", reset_at = "2026-12-01T00:00:00Z", retry_after_sec = 60 } },
      { status = 400, body = { error = { code = "bad_path", message = "bad" } } },
      { status = 413, body = { error = { code = "body_too_large", message = "big" } } },
      { status = 502, body = { error = { code = "upstream_unreachable", message = "down" } } },
      { status = 403, headers = { ["X-Acnologia-Share"] = "paused" }, body = { error = { code = "share_paused", message = "p" } } },
    })
    local c = client(f)
    local resp, err = c:get("agi-labs", "/v1/models")
    assert.is_nil(resp)
    assert.are.equal("app_limit_reached", err.code)
    assert.are.equal("monthly_budget", err.limit)
    assert.are.equal(60, err.retry_after)
    assert.are.equal(429, err.response.status)
    for _, code in ipairs({ "bad_path", "body_too_large", "upstream_unreachable", "share_paused" }) do
      resp, err = c:get("agi-labs", "/x")
      assert.is_nil(resp)
      assert.are.equal(code, err.code)
    end
    assert.are.equal("paused", err.response.acnologia.share)
  end)

  it("escapes the service name and keeps the token out of proxy_url", function()
    local c = client(fake())
    local u = c:proxy_url("a b", "/v1/x")
    assert.are.equal("http://acn.test/proxy/a%20b/v1/x", u)
    assert.is_nil(u:find(TOKEN, 1, true))
  end)

  it("refuses a table body without json", function()
    local _, err = client(fake()):post("agi-labs", "/x", { body = { 1 } })
    assert.are.equal("bad_request", err.code)
  end)
end)

describe("leases", function()
  it("leases with every option in snake case", function()
    local f = fake({ { body = LEASE } })
    local c = client(f)
    local lease = assert(c:lease("agi-labs", { exclude_key_ids = { 1, 2 }, ttl_sec = 30, wait_sec = 2.5, exclusive = true }))
    assert.are.equal("http://acn.test/api/v1/lease", f.last().url)
    assert.are.equal("POST", f.last().method)
    assert.are.equal("application/json", f.last().headers["Content-Type"])
    local body = f.body()
    assert.are.equal("agi-labs", body.service)
    assert.are.same({ 1, 2 }, { body.exclude_key_ids[1], body.exclude_key_ids[2] })
    assert.are.equal(30, body.ttl_sec)
    assert.are.equal(3, body.wait_sec)
    assert.is_true(body.exclusive)
    assert.are.equal("lease/1", lease.lease_id)
    assert.are.equal(7, lease.key_id)
    assert.are.equal(SECRET, lease:secret())
    assert.are.equal("https://api.agi-labs.test", lease.base_url)
  end)

  it("omits unset options", function()
    local f = fake({ { body = LEASE } })
    assert(client(f):lease("agi-labs", { wait_sec = 0, exclude_key_ids = {} }))
    assert.are.equal('{"service":"agi-labs"}', f.last().body)
  end)

  it("refuses wait_sec over 60", function()
    local f = fake()
    local _, err = client(f):lease("agi-labs", { wait_sec = 61 })
    assert.are.equal("bad_request", err.code)
    assert.are.equal(0, #f.calls)
  end)

  it("hides the secret when printed or encoded", function()
    local lease = assert(client(fake({ { body = LEASE } })):lease("agi-labs"))
    assert.is_nil(tostring(lease):find(SECRET, 1, true))
    for _, v in pairs(lease) do assert.are_not.equal(SECRET, v) end
  end)

  it("renews with ttl_sec at the escaped path", function()
    local f = fake({ { body = LEASE }, { body = { ok = true, expires_at = "2026-11-04T11:00:00Z" } }, { body = { ok = true, expires_at = "2026-11-04T12:00:00Z" } } })
    local c = client(f)
    local lease = assert(c:lease("agi-labs"))
    assert.are.equal("2026-11-04T11:00:00Z", assert(lease:renew(299.2)))
    assert.are.equal("http://acn.test/api/v1/leases/lease%2F1/renew", f.last().url)
    assert.are.equal(300, f.body().ttl_sec)
    assert.are.equal("2026-11-04T11:00:00Z", lease.expires_at)
    assert(c:renew(lease))
    assert.are.equal("{}", f.last().body)
    assert.are.equal("2026-11-04T12:00:00Z", lease.expires_at)
  end)

  it("renew returns lease_not_found", function()
    local f = fake({ { body = LEASE }, { status = 404, body = { error = { code = "lease_not_found", message = "lease is not open" } } } })
    local lease = assert(client(f):lease("agi-labs"))
    local v, err = lease:renew(10)
    assert.is_nil(v)
    assert.are.equal("lease_not_found", err.code)
    assert.are.equal(404, err.status)
  end)

  it("reports and returns the next lease when rotating", function()
    local nxt = lease_with({ type = "bearer", name = "", prefix = "" })
    nxt.lease_id, nxt.key_id = "lease-2", 8
    local f = fake({ { body = LEASE }, { body = { ok = true, next = nxt } }, { body = { ok = true, next = json.null } } })
    local lease = assert(client(f):lease("agi-labs"))
    local n = assert(lease:report("rate_limited", { status_code = 429, retry_after_sec = 1.2, message = "429 for key " .. SECRET, rotate = true }))
    assert.are.equal("http://acn.test/api/v1/leases/lease%2F1/report", f.last().url)
    local body = f.body()
    assert.are.equal("rate_limited", body.outcome)
    assert.are.equal(429, body.status_code)
    assert.are.equal(2, body.retry_after_sec)
    assert.is_true(body.rotate)
    assert.are.equal("429 for key [key]", body.message)
    assert.is_true(lease.reported)
    assert.are.equal(8, n.key_id)
    local none = n:report({ outcome = "ok" })
    assert.are.equal(false, none)
    assert.is_false(f.body().rotate)
  end)

  it("scrubs the URL encoded secret and cuts long messages", function()
    local f = fake({ { body = LEASE }, { body = { ok = true } } })
    local lease = assert(client(f):lease("agi-labs"))
    local encoded = "sk%2Flive%2Bsecret%3D1"
    acnologia.assert(lease:report("error", { message = "GET /x?key=" .. encoded .. " " .. string.rep("z", 800) }))
    local msg = f.body().message
    assert.is_nil(msg:find(encoded, 1, true))
    assert.truthy(msg:find("key=[key]", 1, true))
    assert.are.equal(500, #msg)
    assert.are.equal("x [key] y", acnologia.scrub("x " .. SECRET .. " y", SECRET))
  end)

  it("report_failed comes back as an error", function()
    local f = fake({ { body = LEASE }, { status = 409, body = { error = { code = "report_failed", message = "already reported" } } } })
    local lease = assert(client(f):lease("agi-labs"))
    local v, err = lease:report("ok")
    assert.is_nil(v)
    assert.are.equal("report_failed", err.code)
  end)

  describe("apply", function()
    local function lease_for(auth)
      return assert(client(fake({ { body = lease_with(auth) } })):lease("agi-labs"))
    end

    it("puts the secret in a header for header auth without changing the input", function()
      local lease = lease_for({ type = "header", name = "X-Api-Key", prefix = "" })
      local input = { Accept = "text/plain" }
      local h, q = lease:apply(input, { a = "1" })
      assert.are.equal(SECRET, h["X-Api-Key"])
      assert.are.equal("text/plain", h.Accept)
      assert.is_nil(input["X-Api-Key"])
      assert.are.same({ a = "1" }, q)
      assert.are.equal("https://api.agi-labs.test/v1/chat?a=1", lease:url("/v1/chat", { a = "1" }))
    end)

    it("puts the secret in the query for query auth", function()
      local lease = lease_for({ type = "query", name = "api_key", prefix = "" })
      local h, q = lease:apply({}, { q = "x" })
      assert.is_nil(h.Authorization)
      assert.are.equal(SECRET, q.api_key)
      assert.are.equal("https://api.agi-labs.test/v1/chat?api_key=sk%2Flive%2Bsecret%3D1&q=x", lease:url("v1/chat", { q = "x" }))
    end)

    it("uses Bearer on Authorization by default", function()
      local lease = lease_for({ type = "bearer", name = "", prefix = "" })
      local h = lease:apply({ authorization = "old" })
      assert.are.equal("Bearer " .. SECRET, h.Authorization)
      assert.is_nil(h.authorization)
    end)

    it("puts auth.prefix in front of the secret", function()
      assert.are.equal("Token " .. SECRET, (lease_for({ type = "bearer", name = "", prefix = "Token " }):apply()).Authorization)
      assert.are.equal("k-" .. SECRET, (lease_for({ type = "header", name = "X-Key", prefix = "k-" }):apply())["X-Key"])
      local _, q = lease_for({ type = "query", name = "key", prefix = "p:" }):apply()
      assert.are.equal("p:" .. SECRET, q.key)
    end)
  end)
end)

describe("with_key", function()
  local function leases(n)
    local out = {}
    for i = 1, n do
      local l = lease_with(LEASE.auth)
      l.lease_id, l.key_id = "l" .. i, i
      out[i] = l
    end
    return out
  end

  it("reports ok and returns every value", function()
    local L = leases(1)
    local f = fake({ { body = L[1] }, { body = { ok = true } } })
    local a, b, c = client(f):with_key("agi-labs", function(lease) return lease.key_id, nil, "third" end)
    assert.are.equal(1, a)
    assert.is_nil(b)
    assert.are.equal("third", c)
    assert.are.equal("ok", f.body().outcome)
  end)

  it("rotates on a raised rate_limited using the next lease from report", function()
    local L = leases(2)
    local f = fake({ { body = L[1] }, { body = { ok = true, next = L[2] } }, { body = { ok = true } } })
    local seen = {}
    local v = assert(client(f):with_key("agi-labs", function(lease)
      seen[#seen + 1] = lease.key_id
      if lease.key_id == 1 then error(acnologia.rate_limited({ retry_after = 30, status_code = 429, message = "slow " .. SECRET })) end
      return "done"
    end))
    assert.are.equal("done", v)
    assert.are.same({ 1, 2 }, seen)
    local rep = f.body(2)
    assert.are.equal("rate_limited", rep.outcome)
    assert.are.equal(30, rep.retry_after_sec)
    assert.are.equal(429, rep.status_code)
    assert.is_true(rep.rotate)
    assert.are.equal("slow [key]", rep.message)
  end)

  it("rotates on a returned quota_exhausted and leases again excluding tried keys", function()
    local L = leases(2)
    local f = fake({ { body = L[1] }, { body = { ok = true, next = json.null } }, { body = L[2] }, { body = { ok = true } } })
    local v = assert(client(f):with_key("agi-labs", function(lease)
      if lease.key_id == 1 then return nil, acnologia.quota_exhausted() end
      return "second"
    end, { ttl_sec = 60 }))
    assert.are.equal("second", v)
    assert.are.equal("quota_exhausted", f.body(2).outcome)
    local again = f.body(3)
    assert.are.equal(1, again.exclude_key_ids[1])
    assert.are.equal(60, again.ttl_sec)
  end)

  it("gives up after max_attempts", function()
    local L = leases(2)
    local f = fake({ { body = L[1] }, { body = { ok = true, next = L[2] } }, { body = { ok = true } } })
    local v, err = client(f):with_key("agi-labs", function() error(acnologia.invalid_key()) end, { max_attempts = 2 })
    assert.is_nil(v)
    assert.are.equal("invalid", err.outcome)
    assert.is_false(f.body(3).rotate)
  end)

  it("reports error and raises other errors again", function()
    local L = leases(1)
    local f = fake({ { body = L[1] }, { body = { ok = true } } })
    local ok, e = pcall(function()
      return client(f):with_key("agi-labs", function(lease) error("broke with " .. lease:secret(), 0) end)
    end)
    assert.is_false(ok)
    assert.truthy(tostring(e):find("broke", 1, true))
    local rep = f.body(2)
    assert.are.equal("error", rep.outcome)
    assert.are.equal("broke with [key]", rep.message)
  end)

  it("returns nil, err from fn after reporting error", function()
    local L = leases(1)
    local f = fake({ { body = L[1] }, { body = { ok = true } } })
    local v, err = client(f):with_key("agi-labs", function() return nil, "bad input" end)
    assert.is_nil(v)
    assert.are.equal("bad input", err)
    assert.are.equal("error", f.body(2).outcome)
  end)

  it("returns the lease error without calling fn", function()
    local f = fake({ { status = 503, body = { error = { code = "all_keys_busy", message = "busy" }, retry_after_sec = 1 } } })
    local called = false
    local v, err = client(f):with_key("agi-labs", function() called = true end, { wait_sec = 5, exclusive = true })
    assert.is_nil(v)
    assert.is_true(err.busy)
    assert.are.equal(1, err.retry_after)
    assert.is_false(called)
    assert.are.equal(5, f.body(1).wait_sec)
    assert.is_true(f.body(1).exclusive)
  end)
end)
