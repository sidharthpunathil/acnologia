-- Error values for the Acnologia client.
--
-- Every failing call returns nil and an error table:
--
--   { code = "no_key_available", status = 503, message = "...",
--     retry_after = 5, limit = nil, reset_at = nil, busy = false, body = {...} }
--
-- Kinds with their own flags: busy (all_keys_busy), no_key
-- (no_key_available or all_keys_busy), app_limit (app_limit_reached, with
-- limit and reset_at) and share_unavailable (share_revoked, share_paused,
-- share_expired, with share_status "revoked", "paused" or "expired").
--
-- code is the server's error code (such as "lease_not_found"), or
-- "transport", "timeout", "bad_response" or "http_<status>" when the server
-- sent none. tostring(err) gives a one line summary. No error ever carries
-- the app token, the access password or a lease secret.

local json = require("acnologia.json")

local errors = {}

local Error = {}
Error.__index = Error
Error.__name = "acnologia.Error"

function Error.__tostring(e)
  local s = "acnologia: " .. tostring(e.code)
  if e.status and e.status > 0 then s = s .. " (HTTP " .. e.status .. ")" end
  if e.message and e.message ~= "" and e.message ~= e.code then s = s .. ": " .. e.message end
  if e.retry_after then s = s .. " (retry after " .. e.retry_after .. "s)" end
  return s
end

errors.Error = Error

--- True when v is an error table made by this SDK.
function errors.is(v)
  return type(v) == "table" and getmetatable(v) == Error
end

-- Plain text replacement: gsub patterns would treat secret characters as magic.
local function replace_plain(s, needle, repl)
  if needle == nil or needle == "" then return s end
  local out, i = {}, 1
  while true do
    local a, b = string.find(s, needle, i, true)
    if not a then break end
    out[#out + 1] = string.sub(s, i, a - 1)
    out[#out + 1] = repl
    i = b + 1
  end
  out[#out + 1] = string.sub(s, i)
  return table.concat(out)
end

local function url_encode(s)
  return (string.gsub(s, "[^%w%-%._~]", function(c) return string.format("%%%02X", string.byte(c)) end))
end
errors.url_encode = url_encode

local MAX_MESSAGE = 500

--- Replace each secret (raw and URL encoded) with "[key]" and cut the
--- message to 500 bytes. secrets may be a string or a list of strings.
function errors.scrub(message, secrets)
  if message == nil then return nil end
  message = tostring(message)
  if type(secrets) == "string" then secrets = { secrets } end
  for _, secret in ipairs(secrets or {}) do
    if type(secret) == "string" and secret ~= "" then
      message = replace_plain(message, secret, "[key]")
      message = replace_plain(message, url_encode(secret), "[key]")
    end
  end
  if #message > MAX_MESSAGE then message = string.sub(message, 1, MAX_MESSAGE) end
  return message
end

--- Build an error table. fields: code, status, message, retry_after, limit,
--- reset_at, body.
function errors.new(fields)
  local e = {
    code = fields.code or "error",
    status = fields.status or 0,
    message = fields.message or fields.code or "error",
    retry_after = fields.retry_after,
    limit = fields.limit,
    reset_at = fields.reset_at,
    body = fields.body,
    response = fields.response,
  }
  e.busy = e.code == "all_keys_busy"
  e.app_limit = e.code == "app_limit_reached"
  -- share_status is "revoked", "paused" or "expired" for a share's token
  -- whose share stopped (codes share_revoked, share_paused, share_expired).
  local share = string.match(e.code, "^share_(%a+)$")
  if share == "revoked" or share == "paused" or share == "expired" then e.share_status = share end
  e.share_unavailable = e.share_status ~= nil
  -- no_key is true for both 503 codes that mean "no key right now".
  e.no_key = e.code == "no_key_available" or e.code == "all_keys_busy"
  return setmetatable(e, Error)
end

local function positive_number(v)
  local n = tonumber(v)
  if n and n > 0 and n == n and n ~= math.huge then return n end
  return nil
end

local function header_retry_after(headers)
  if not headers then return nil end
  return positive_number(headers["retry-after"])
end
errors.header_retry_after = header_retry_after

--- Map a non 2xx answer from Acnologia to an error table, the same way the
--- reference SDKs do: the code comes from {"error": {"code"}}, a 503 with no
--- code is no_key_available, and anything else without a code is
--- "http_<status>". retry_after comes from retry_after_sec in the body, then
--- from the Retry-After header. secrets are scrubbed from the message.
function errors.from_response(status, headers, body, secrets)
  local data = nil
  if type(body) == "string" and body ~= "" then
    local decoded = json.decode(body)
    if type(decoded) == "table" then data = decoded end
  end
  local code, message = nil, nil
  if data then
    local err = data.error
    if type(err) == "table" then
      if type(err.code) == "string" and err.code ~= "" then code = err.code end
      if type(err.message) == "string" and err.message ~= "" then message = err.message end
    elseif type(err) == "string" and err ~= "" then
      message = err
    end
  end
  if not code and status == 403 and headers then
    local share = headers["x-acnologia-share"]
    if share == "revoked" or share == "paused" or share == "expired" then code = "share_" .. share end
  end
  if not code then
    code = status == 503 and "no_key_available" or ("http_" .. tostring(status))
  end
  if not message then
    if data == nil and type(body) == "string" and body ~= "" then
      message = body
    else
      message = "HTTP " .. tostring(status)
    end
  end
  local retry_after = data and positive_number(data.retry_after_sec) or nil
  if not retry_after then retry_after = header_retry_after(headers) end
  local limit, reset_at
  if data then
    if type(data.limit) == "string" then limit = data.limit end
    if type(data.reset_at) == "string" then reset_at = data.reset_at end
  end
  return errors.new({
    code = code,
    status = status,
    message = errors.scrub(message, secrets),
    retry_after = retry_after,
    limit = limit,
    reset_at = reset_at,
    body = data,
  })
end

--- An error raised by your code inside with_key to make it report the key
--- and rotate to the next one. outcome is "rate_limited", "quota_exhausted",
--- "invalid", "banned" or "busy".
local KeyProblem = {}
KeyProblem.__index = KeyProblem
KeyProblem.__name = "acnologia.KeyProblem"
function KeyProblem.__tostring(k)
  return "acnologia: key problem " .. k.outcome .. (k.message and (": " .. k.message) or "")
end
errors.KeyProblem = KeyProblem

function errors.key_problem(outcome, opts)
  opts = opts or {}
  return setmetatable({
    outcome = outcome,
    status_code = opts.status_code,
    retry_after = opts.retry_after,
    message = opts.message,
  }, KeyProblem)
end

function errors.is_key_problem(v)
  return type(v) == "table" and getmetatable(v) == KeyProblem
end

return errors
