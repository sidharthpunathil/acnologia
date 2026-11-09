-- Lua client for Acnologia, the self-hosted server that holds your API keys.
--
--   local acnologia = require("acnologia")
--   local client = acnologia.assert(acnologia.new({ base_url = "http://127.0.0.1:8080", token = "acn_..." }))
--   local resp = acnologia.assert(client:post("agi-labs", "/v1/chat", { json = { prompt = "hi" } }))
--
-- Calls return a value, or nil and an error table (see acnologia.errors).
-- Works on Lua 5.1 (and LuaJIT) through 5.5.

local json = require("acnologia.json")
local errors = require("acnologia.errors")
local transports = require("acnologia.transport")

local M = {}

M.VERSION = "0.2.0"
M.USER_AGENT = "acnologia-lua/" .. M.VERSION
M.DEFAULT_TIMEOUT = 30
M.DEFAULT_PROXY_TIMEOUT = 120
M.MAX_WAIT_SEC = 60
-- Where forward_proxy_env and save_forward_proxy_ca put the proxy CA by default.
M.DEFAULT_CA_PATH = "acnologia-ca.pem"
M.OUTCOMES = { "ok", "rate_limited", "busy", "quota_exhausted", "invalid", "banned", "error" }

M.json = json
M.errors = errors
M.transport = transports
M.scrub = errors.scrub
M.is_error = errors.is

local url_encode = errors.url_encode

local function ceil(n) return math.ceil(tonumber(n)) end

local function copy(t)
  local out = {}
  for k, v in pairs(t or {}) do out[k] = v end
  return out
end

-- Set a header, replacing any spelling of the same name.
local function set_header(h, name, value)
  local lname = string.lower(name)
  for k in pairs(h) do
    if string.lower(k) == lname then h[k] = nil end
  end
  h[name] = value
end

local function has_header(h, name)
  local lname = string.lower(name)
  for k in pairs(h) do
    if string.lower(k) == lname then return true end
  end
  return false
end

--- Encode a query table as a string. Keys are sorted; a list value repeats
--- its key. A string is returned as it is.
local function encode_query(query)
  if query == nil then return "" end
  if type(query) == "string" then return (string.gsub(query, "^%?", "")) end
  local keys = {}
  for k in pairs(query) do keys[#keys + 1] = tostring(k) end
  table.sort(keys)
  local parts = {}
  for _, k in ipairs(keys) do
    local v = query[k]
    if v == nil then v = query[tonumber(k)] end
    if type(v) == "table" then
      for _, item in ipairs(v) do parts[#parts + 1] = url_encode(k) .. "=" .. url_encode(tostring(item)) end
    elseif v ~= nil and v ~= false and v ~= json.null then
      parts[#parts + 1] = url_encode(k) .. "=" .. url_encode(tostring(v))
    end
  end
  return table.concat(parts, "&")
end
M.encode_query = encode_query

local function append_query(url, query)
  local qs = encode_query(query)
  if qs == "" then return url end
  return url .. (string.find(url, "?", 1, true) and "&" or "?") .. qs
end

-- Client -------------------------------------------------------------------

local Client = {}
Client.__index = Client
Client.__name = "acnologia.Client"
M.Client = Client

function Client.__tostring(c)
  return "acnologia.Client(" .. c.base_url .. ", token=***)"
end

local function config_error(message)
  return nil, errors.new({ code = "config", message = message })
end

--- Make a client.
---
--- opts:
---   base_url         server URL; default ACNOLOGIA_URL
---   token            app token (acn_...); default ACNOLOGIA_TOKEN
---   access_password  sent as X-Acnologia-Access; default ACNOLOGIA_ACCESS_PASSWORD
---   timeout          seconds for API calls (default 30)
---   proxy_timeout    seconds for proxy calls (default 120)
---   transport        a transport function; default the first one installed
---   headers          extra headers for every request
---   getenv           function used to read the environment (default os.getenv)
---
--- Returns the client, or nil and an error table with code "config".
function M.new(opts)
  opts = opts or {}
  local getenv = opts.getenv or os.getenv
  local base_url = opts.base_url or getenv("ACNOLOGIA_URL") or ""
  local token = opts.token or getenv("ACNOLOGIA_TOKEN") or ""
  if base_url == "" then return config_error("base_url is required (or set ACNOLOGIA_URL)") end
  if token == "" then return config_error("token is required (or set ACNOLOGIA_TOKEN)") end
  local access = opts.access_password
  if access == nil then access = getenv("ACNOLOGIA_ACCESS_PASSWORD") end
  if access == "" then access = nil end
  local transport = opts.transport
  local transport_name = "custom"
  if transport == nil then
    local err
    transport, err = transports.pick()
    if not transport then return config_error(err) end
    transport_name = err
  end
  local self = setmetatable({
    base_url = (string.gsub(base_url, "/+$", "")),
    timeout = opts.timeout or M.DEFAULT_TIMEOUT,
    proxy_timeout = opts.proxy_timeout or M.DEFAULT_PROXY_TIMEOUT,
    transport_name = transport_name,
    headers = copy(opts.headers),
  }, Client)
  -- Kept in a closure so printing or serializing the client never shows them.
  local secrets = { token = token, access = access }
  self._secret = function(name) return secrets[name] end
  self._transport = transport
  return self
end

-- Every string that must never show up in an error message.
function Client:_secrets(extra)
  local list = { self._secret("token") }
  if self._secret("access") then list[#list + 1] = self._secret("access") end
  if extra then list[#list + 1] = extra end
  return list
end

function Client:_auth_headers(base)
  local h = copy(self.headers)
  for k, v in pairs(base or {}) do set_header(h, k, v) end
  set_header(h, "Authorization", "Bearer " .. self._secret("token"))
  set_header(h, "User-Agent", M.USER_AGENT)
  set_header(h, "X-Acnologia-Client", M.USER_AGENT)
  if self._secret("access") then set_header(h, "X-Acnologia-Access", self._secret("access")) end
  return h
end

function Client:_send(method, url, headers, body, timeout)
  local ok, status, rheaders, rbody = pcall(self._transport, method, url, headers, body, timeout)
  if not ok then
    return nil, errors.new({ code = "transport", message = errors.scrub(status, self:_secrets()) })
  end
  if status == nil then
    local msg = tostring(rheaders or "request failed")
    local code = string.find(msg, "timeout", 1, true) and "timeout" or "transport"
    return nil, errors.new({ code = code, message = errors.scrub(msg, self:_secrets()) })
  end
  return tonumber(status), transports.lower_headers(rheaders), rbody or ""
end

-- Call the consumer API and decode the JSON answer.
function Client:_call(method, path, body, timeout)
  local headers = self:_auth_headers({ Accept = "application/json" })
  local payload
  if body ~= nil then
    local err
    payload, err = json.encode(body)
    if not payload then return nil, errors.new({ code = "bad_request", message = err }) end
    set_header(headers, "Content-Type", "application/json")
  end
  local status, rheaders, rbody = self:_send(method, self.base_url .. path, headers, payload, timeout or self.timeout)
  if not status then return nil, rheaders end
  if status < 200 or status > 299 then
    return nil, errors.from_response(status, rheaders, rbody, self:_secrets())
  end
  if rbody == "" then return {} end
  local data, err = json.decode(rbody)
  if data == nil then
    return nil, errors.new({ code = "bad_response", status = status, message = errors.scrub(err, self:_secrets()) })
  end
  return data
end

--- GET /api/v1/me: the token's app, limits, usage and services.
function Client:me()
  return self:_call("GET", "/api/v1/me")
end

--- GET /api/v1/services: the services this token may use. Each has slug,
--- name, provider, category, available, base_url, auth {type, name,
--- prefix} and guide_url.
function Client:services()
  local data, err = self:_call("GET", "/api/v1/services")
  if not data then return nil, err end
  return data.services or json.array({})
end

-- Send a request to a full URL with this client's headers but no token.
function Client:_get_raw(path, accept, with_token)
  local headers
  if with_token then
    headers = self:_auth_headers({ Accept = accept })
  else
    headers = copy(self.headers)
    set_header(headers, "Accept", accept)
    set_header(headers, "User-Agent", M.USER_AGENT)
    set_header(headers, "X-Acnologia-Client", M.USER_AGENT)
    if self._secret("access") then set_header(headers, "X-Acnologia-Access", self._secret("access")) end
  end
  local status, rheaders, rbody = self:_send("GET", self.base_url .. path, headers, nil, self.timeout)
  if not status then return nil, rheaders end
  if status < 200 or status > 299 then
    return nil, errors.from_response(status, rheaders, rbody, self:_secrets())
  end
  return rbody
end

--- GET /api/v1/services/<slug>/guide: the usage guide of a service as a
--- table { markdown = string, generated = boolean, guide = table }. guide is
--- the structured guide as decoded JSON. generated is true when the server
--- built it from the provider spec because nobody wrote one.
function Client:guide(slug)
  local data, err = self:_call("GET", "/api/v1/services/" .. url_encode(slug) .. "/guide")
  if not data then return nil, err end
  return {
    markdown = type(data.markdown) == "string" and data.markdown or "",
    generated = data.generated == true,
    guide = data.guide,
  }
end

--- The usage guide of a service as a Markdown string (the same route with
--- Accept: text/markdown).
function Client:guide_markdown(slug)
  return self:_get_raw("/api/v1/services/" .. url_encode(slug) .. "/guide", "text/markdown", true)
end

--- GET /api/v1/capabilities: the capabilities this token may call. Each has
--- slug, name, description, strategy, params (decoded JSON), routes
--- ({ service, mapped } each) and url. A share's token gets none.
function Client:capabilities()
  local data, err = self:_call("GET", "/api/v1/capabilities")
  if not data then return nil, err end
  return data.capabilities or json.array({})
end

--- GET /api/v1/proxy: how to use the forward proxy, as a table with
--- proxy_url, proxy_url_with_token, ca_url, ca_fingerprint, passthrough,
--- hosts ({ host, service, name, path } each), env and service_header. Its
--- strings carry a "<app token>" placeholder, never the token, so it is
--- safe to log.
function Client:forward_proxy()
  return self:_call("GET", "/api/v1/proxy")
end

--- GET /api/v1/proxy/ca.pem: the forward proxy's CA certificate as PEM
--- text. The route is public: no token is sent.
function Client:forward_proxy_ca()
  return self:_get_raw("/api/v1/proxy/ca.pem", "application/x-pem-file", false)
end

--- Download the forward proxy's CA certificate and write it to path
--- (default "acnologia-ca.pem"). Returns path, or nil and an error.
function Client:save_forward_proxy_ca(path)
  path = path or M.DEFAULT_CA_PATH
  local pem, err = self:forward_proxy_ca()
  if not pem then return nil, err end
  local fh, ferr = io.open(path, "wb")
  if not fh then return nil, errors.new({ code = "io", message = tostring(ferr) }) end
  fh:write(pem)
  fh:close()
  return path
end

--- The forward proxy URL with user "app" and this client's token as the
--- password, percent escaped: http://app:acn_...@host:8888. It contains
--- the token: keep it out of logs.
function Client:forward_proxy_url()
  local fp, err = self:forward_proxy()
  if not fp then return nil, err end
  local raw = type(fp.proxy_url) == "string" and fp.proxy_url or ""
  local scheme, authority, rest = string.match(raw, "^(%a[%w+.%-]*)://([^/?#]+)(.*)$")
  if not scheme then
    return nil, errors.new({ code = "bad_response", status = 200, message = "the server sent no usable proxy_url" })
  end
  authority = string.gsub(authority, "^.*@", "")
  return scheme .. "://app:" .. url_encode(self._secret("token")) .. "@" .. authority .. rest
end

--- Environment for a process that should reach providers through the
--- forward proxy: HTTPS_PROXY and HTTP_PROXY (forward_proxy_url()),
--- NODE_USE_ENV_PROXY=1, and NODE_EXTRA_CA_CERTS, REQUESTS_CA_BUNDLE and
--- SSL_CERT_FILE set to ca_path (default "acnologia-ca.pem"). The proxy
--- values contain the token: keep them out of logs.
function Client:forward_proxy_env(ca_path)
  ca_path = ca_path or M.DEFAULT_CA_PATH
  local url, err = self:forward_proxy_url()
  if not url then return nil, err end
  return {
    HTTPS_PROXY = url,
    HTTP_PROXY = url,
    NODE_USE_ENV_PROXY = "1",
    NODE_EXTRA_CA_CERTS = ca_path,
    REQUESTS_CA_BUNDLE = ca_path,
    SSL_CERT_FILE = ca_path,
  }
end

-- Proxy --------------------------------------------------------------------

local Response = {}
Response.__index = Response
Response.__name = "acnologia.Response"
M.Response = Response

--- Decode the body as JSON. Returns the value, or nil and a message.
function Response:json()
  return json.decode(self.body)
end

--- True for a 2xx status.
function Response:ok()
  return self.status >= 200 and self.status <= 299
end

function Response.__tostring(r)
  return "acnologia.Response(" .. r.status .. ", " .. #r.body .. " bytes)"
end

-- Parse X-Acnologia-Tried ("a=200, b=429") into { service, status } entries.
local function parse_tried(v)
  local out = {}
  for item in string.gmatch(v or "", "[^,]+") do
    local pair = string.match(item, "^%s*(.-)%s*$")
    if pair ~= "" then
      local svc, code = string.match(pair, "^(.*)=(%d+)$")
      if svc then
        out[#out + 1] = { service = svc, status = tonumber(code) }
      else
        out[#out + 1] = { service = pair }
      end
    end
  end
  return out
end
M.parse_tried = parse_tried

local function acnologia_headers(h)
  local function get(name) return h["x-acnologia-" .. name] or h["x-cogito-" .. name] end
  return {
    key_id = tonumber(get("key-id")),
    attempts = tonumber(get("attempts")),
    service = get("service"),
    cache = get("cache"),
    share = get("share"),
    capability = get("capability"),
    tried = parse_tried(get("tried")),
    replayed = h["idempotent-replayed"] == "true",
  }
end

--- The proxy URL for a service and path, without the token.
function Client:proxy_url(service, path, query)
  local clean = string.gsub(path or "", "^/+", "")
  return append_query(self.base_url .. "/proxy/" .. url_encode(service) .. "/" .. clean, query)
end

--- Send any request through /proxy/<service>/<path>.
---
--- opts:
---   query            table or string
---   headers          table of request headers
---   body             string sent as it is
---   json             value encoded as JSON (sets Content-Type)
---   idempotency_key  sent as Idempotency-Key
---   no_cache         true sends Cache-Control: no-cache
---   timeout          seconds (default client.proxy_timeout)
---
--- Returns a response { status, headers, body, acnologia } for anything the
--- provider answered, whatever the status. headers use lower case names, and
--- acnologia holds key_id, attempts, service, cache, share, capability,
--- tried (a list of { service, status }) and replayed.
---
--- Returns nil and an error table when Acnologia itself refused the call
--- (for example app_limit_reached, no_key_available, upstream_unreachable,
--- bad_path or body_too_large), or the network failed. err.response then
--- holds the raw response when there was one.
function Client:proxy(method, service, path, opts)
  opts = opts or {}
  if type(service) ~= "string" or service == "" then
    return nil, errors.new({ code = "bad_request", message = "service is required" })
  end
  return self:_proxy_send(method, self:proxy_url(service, path, opts.query), opts)
end

function Client:_proxy_send(method, url, opts)
  method = string.upper(method or "GET")
  local headers = {}
  for k, v in pairs(opts.headers or {}) do headers[k] = tostring(v) end
  local body = opts.body
  if opts.json ~= nil then
    local err
    body, err = json.encode(opts.json)
    if not body then return nil, errors.new({ code = "bad_request", message = err }) end
    if not has_header(headers, "Content-Type") then headers["Content-Type"] = "application/json" end
  end
  if body ~= nil and type(body) ~= "string" then
    return nil, errors.new({ code = "bad_request", message = "body must be a string; use json for tables" })
  end
  if opts.idempotency_key then set_header(headers, "Idempotency-Key", tostring(opts.idempotency_key)) end
  if opts.no_cache and not has_header(headers, "Cache-Control") then headers["Cache-Control"] = "no-cache" end
  -- Our token always wins over a caller's Authorization header.
  headers = self:_auth_headers(headers)
  local status, rheaders, rbody = self:_send(method, url, headers, body, opts.timeout or self.proxy_timeout)
  if not status then return nil, rheaders end
  local resp = setmetatable({
    status = status,
    headers = rheaders,
    body = rbody,
    acnologia = acnologia_headers(rheaders),
  }, Response)
  -- Answers relayed from the provider carry X-Acnologia-Service (or
  -- Key-Id, Cache, or Idempotent-Replayed on a replay). An error without any
  -- of them was written by Acnologia itself.
  local a = resp.acnologia
  if status >= 400 and not (a.service or a.key_id or a.cache or a.replayed) then
    local err = errors.from_response(status, rheaders, rbody, self:_secrets())
    if err.body and type(err.body.error) == "table" then
      err.response = resp
      return nil, err
    end
  end
  return resp
end

--- Call a capability: POST /proxy/<slug> with params as the JSON body.
---
--- opts:
---   service          use only this provider (?_service=)
---   raw              true returns the provider's own body (?_raw=1)
---   headers, idempotency_key, no_cache, timeout   as for proxy
---
--- Returns a response like proxy, with acnologia.capability and
--- acnologia.tried set, or nil and an error (no_route, no_route_worked,
--- bad_params and so on).
function Client:capability(slug, params, opts)
  opts = opts or {}
  if type(slug) ~= "string" or slug == "" then
    return nil, errors.new({ code = "bad_request", message = "capability slug is required" })
  end
  local query = {}
  if opts.service and opts.service ~= "" then query._service = opts.service end
  if opts.raw then query._raw = "1" end
  local send = copy(opts)
  send.json = params or json.object({})
  send.body = nil
  return self:_proxy_send("POST", append_query(self.base_url .. "/proxy/" .. url_encode(slug), query), send)
end

for _, m in ipairs({ "get", "post", "put", "patch", "delete", "head", "options" }) do
  local method = string.upper(m)
  Client[m] = function(self, service, path, opts)
    return self:proxy(method, service, path, opts)
  end
end

-- Leases -------------------------------------------------------------------

local Lease = {}
Lease.__index = Lease
Lease.__name = "acnologia.Lease"
M.Lease = Lease

function Lease.__tostring(l)
  return string.format("acnologia.Lease(%s, key %s, %s, secret=***)", tostring(l.lease_id), tostring(l.key_id), tostring(l.service))
end

local function new_lease(client, data)
  local auth = type(data.auth) == "table" and data.auth or {}
  local secret = type(data.secret) == "string" and data.secret or ""
  local lease = setmetatable({
    lease_id = tostring(data.lease_id or ""),
    key_id = tonumber(data.key_id) or 0,
    service = data.service,
    auth = {
      type = type(auth.type) == "string" and auth.type or "header",
      name = type(auth.name) == "string" and auth.name or "",
      prefix = type(auth.prefix) == "string" and auth.prefix or "",
    },
    base_url = (string.gsub(type(data.base_url) == "string" and data.base_url or "", "/+$", "")),
    expires_at = data.expires_at,
    exclusive = data.exclusive == true,
    reported = false,
  }, Lease)
  -- The secret lives in a closure so printing or encoding the lease hides it.
  lease.get_secret = function() return secret end
  lease._client = client
  return lease
end

--- The raw key. Keep it out of logs.
function Lease:secret()
  return self.get_secret()
end

--- Borrow a key.
---
--- opts:
---   exclude_key_ids  list of key ids to skip
---   ttl_sec          lease length in seconds
---   wait_sec         wait up to this long (0 to 60) for a free slot
---   exclusive        keep the key from every other caller
---
--- Returns a Lease, or nil and an error (no_key_available, all_keys_busy,
--- lease_not_allowed, app_limit_reached and so on). Always report the lease.
function Client:lease(service, opts)
  opts = opts or {}
  local body = { service = service }
  if opts.exclude_key_ids and #opts.exclude_key_ids > 0 then
    body.exclude_key_ids = json.array(copy(opts.exclude_key_ids))
  end
  if opts.ttl_sec ~= nil then body.ttl_sec = ceil(opts.ttl_sec) end
  local timeout = self.timeout
  if opts.wait_sec ~= nil and opts.wait_sec ~= 0 then
    local w = tonumber(opts.wait_sec)
    if not w or w < 0 or w > M.MAX_WAIT_SEC then
      return nil, errors.new({ code = "bad_request", message = "wait_sec must be between 0 and " .. M.MAX_WAIT_SEC })
    end
    body.wait_sec = ceil(w)
    -- The HTTP call must outlast the server side wait.
    if timeout < w + 10 then timeout = w + 10 end
  end
  if opts.exclusive then body.exclusive = true end
  local data, err = self:_call("POST", "/api/v1/lease", body, timeout)
  if not data then return nil, err end
  return new_lease(self, data)
end

--- Extend an open lease. ttl_sec nil uses the server default (120 seconds).
--- Returns the new expires_at, or nil and an error (lease_not_found when the
--- lease is no longer open).
function Client:renew(lease, ttl_sec)
  local body = json.object({})
  if ttl_sec ~= nil and tonumber(ttl_sec) and tonumber(ttl_sec) > 0 then body.ttl_sec = ceil(ttl_sec) end
  local data, err = self:_call("POST", "/api/v1/leases/" .. url_encode(lease.lease_id) .. "/renew", body)
  if not data then return nil, err end
  if type(data.expires_at) ~= "string" then
    return nil, errors.new({ code = "bad_response", status = 200, message = "renew response has no expires_at" })
  end
  lease.expires_at = data.expires_at
  return data.expires_at
end

function Lease:renew(ttl_sec)
  return self._client:renew(self, ttl_sec)
end

--- Report how the key did.
---
---   lease:report("ok")
---   lease:report("rate_limited", { status_code = 429, retry_after_sec = 30, rotate = true })
---   lease:report({ outcome = "invalid", message = "..." })
---
--- The message is scrubbed of the secret and cut to 500 bytes. Returns the
--- next Lease when rotate is true and another key is free, false when there
--- is none, or nil and an error.
function Lease:report(outcome, opts)
  if type(outcome) == "table" then opts, outcome = outcome, outcome.outcome end
  opts = opts or {}
  local body = { outcome = outcome or "ok", rotate = opts.rotate and true or false }
  if opts.status_code ~= nil then body.status_code = math.floor(tonumber(opts.status_code)) end
  if opts.retry_after_sec ~= nil then body.retry_after_sec = ceil(opts.retry_after_sec) end
  if opts.message ~= nil and opts.message ~= "" then
    body.message = errors.scrub(opts.message, self:secret())
  end
  local data, err = self._client:_call("POST", "/api/v1/leases/" .. url_encode(self.lease_id) .. "/report", body)
  if not data then return nil, err end
  self.reported = true
  if type(data.next) == "table" and data.next ~= json.null then return new_lease(self._client, data.next) end
  return false
end

--- Put the secret where the service expects it. Returns copies of headers
--- and query: "query" auth sets query[auth.name], "header" auth sets
--- headers[auth.name], "bearer" sets headers[auth.name or "Authorization"]
--- to "Bearer <secret>". A non empty auth.prefix goes in front of the
--- secret; for bearer it replaces "Bearer ".
function Lease:apply(headers, query)
  local h, q = copy(headers), copy(query)
  local prefix = self.auth.prefix or ""
  local secret = self:secret()
  local kind = self.auth.type
  if kind == "query" then
    q[self.auth.name ~= "" and self.auth.name or "api_key"] = prefix .. secret
  elseif kind == "bearer" then
    set_header(h, self.auth.name ~= "" and self.auth.name or "Authorization", (prefix ~= "" and prefix or "Bearer ") .. secret)
  else
    set_header(h, self.auth.name ~= "" and self.auth.name or "Authorization", prefix .. secret)
  end
  return h, q
end

--- base_url .. path with the query string, including the secret for query
--- auth. Pass the result of apply() headers with it for header auth.
function Lease:url(path, query)
  local _, q = self:apply(nil, query)
  local p = path or ""
  if p ~= "" and string.sub(p, 1, 1) ~= "/" then p = "/" .. p end
  return append_query(self.base_url .. p, q)
end

-- with_key -----------------------------------------------------------------

--- Errors to raise (or return) from a with_key callback to rotate the key.
function M.rate_limited(opts) return errors.key_problem("rate_limited", opts) end
function M.quota_exhausted(opts) return errors.key_problem("quota_exhausted", opts) end
function M.invalid_key(opts) return errors.key_problem("invalid", opts) end
function M.banned_key(opts) return errors.key_problem("banned", opts) end

local unpack = table.unpack or unpack
local function pack(...) return { n = select("#", ...), ... } end

--- Lease a key, call fn(lease), report, and return what fn returned.
---
--- If fn raises (or returns nil and) acnologia.rate_limited(),
--- quota_exhausted(), invalid_key() or banned_key(), the key is reported
--- with rotate and fn runs again with the next key, up to max_attempts.
--- If fn returns nil and another error, the key is reported as "error" and
--- with_key returns nil and that error. If fn raises another error, the key
--- is reported as "error" and the error is raised again. Every lease is
--- reported.
---
--- opts: max_attempts (default 5), ttl_sec, wait_sec, exclusive.
function Client:with_key(service, fn, opts)
  opts = opts or {}
  local max_attempts = math.max(1, opts.max_attempts or 5)
  local lease_opts = { ttl_sec = opts.ttl_sec, wait_sec = opts.wait_sec, exclusive = opts.exclusive }
  local tried = {}
  local lease, err = self:lease(service, lease_opts)
  if not lease then return nil, err end
  local attempt = 0
  while true do
    attempt = attempt + 1
    tried[#tried + 1] = lease.key_id
    local results = pack(pcall(fn, lease))
    local ok = results[1]
    local problem, other = nil, nil
    if not ok then
      if errors.is_key_problem(results[2]) then problem = results[2] else other = results[2] end
    elseif results[2] == nil and errors.is_key_problem(results[3]) then
      problem = results[3]
    end
    if problem then
      local last = attempt >= max_attempts
      local nxt, rerr = lease:report(problem.outcome, {
        status_code = problem.status_code,
        retry_after_sec = problem.retry_after,
        message = problem.message,
        rotate = not last,
      })
      if last then return nil, problem end
      if nxt == nil then return nil, rerr end
      if nxt then
        lease = nxt
      else
        local lo = copy(lease_opts)
        lo.exclude_key_ids = tried
        lease, err = self:lease(service, lo)
        if not lease then return nil, err end
      end
    elseif not ok then
      lease:report("error", { message = tostring(other) })
      error(other, 0)
    elseif results[2] == nil and results[3] ~= nil then
      lease:report("error", { message = tostring(results[3]) })
      return nil, results[3]
    else
      local _, rerr = lease:report("ok")
      if rerr then return nil, rerr end
      return unpack(results, 2, results.n)
    end
  end
end

--- Raise the error when v is nil, else return everything given, like assert
--- but for (value, err) pairs from this SDK. The raised value is the error
--- table itself, so pcall callers keep code, status and retry_after.
function M.assert(v, err, ...)
  if v == nil or v == false and err ~= nil then
    if errors.is(err) then error(err, 2) end
    error(tostring(err or "acnologia: call failed"), 2)
  end
  return v, err, ...
end

return M
