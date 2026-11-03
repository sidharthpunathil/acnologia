-- HTTP transports for the Acnologia client.
--
-- A transport is a function:
--
--   transport(method, url, headers, body, timeout)
--     -> status, headers, body
--     -> nil, message            on a network failure
--
-- headers going in map a name to a string value. headers coming back must
-- use lower case names. body is a string or nil both ways. timeout is in
-- seconds. A transport must not follow redirects, so the app token is never
-- sent to another host.
--
-- Two are built in: luasocket (with luasec for https) and lua-curl. pick()
-- returns the first one whose library loads.

local M = {}

local function lower_headers(raw)
  local out = {}
  for k, v in pairs(raw or {}) do
    local name = string.lower(tostring(k))
    if type(v) == "table" then v = table.concat(v, ", ") end
    if out[name] then out[name] = out[name] .. ", " .. tostring(v) else out[name] = tostring(v) end
  end
  return out
end
M.lower_headers = lower_headers

--- A transport built on luasocket, with luasec for https URLs.
--- Returns nil and a message when luasocket is not installed.
function M.luasocket()
  local ok_http, http = pcall(require, "socket.http")
  local ok_ltn12, ltn12 = pcall(require, "ltn12")
  local ok_socket, socket = pcall(require, "socket")
  if not (ok_http and ok_ltn12 and ok_socket) then
    return nil, "luasocket is not installed"
  end
  local ok_https, https = pcall(require, "ssl.https")

  return function(method, url, headers, body, timeout)
    local is_https = string.find(string.lower(url), "^https://") ~= nil
    if is_https and not ok_https then
      return nil, "https needs luasec (luarocks install luasec)"
    end
    local chunks = {}
    local hdrs = {}
    for k, v in pairs(headers or {}) do hdrs[k] = v end
    if body ~= nil then hdrs["Content-Length"] = tostring(#body) end
    local req = {
      url = url,
      method = method,
      headers = hdrs,
      source = body ~= nil and ltn12.source.string(body) or nil,
      sink = ltn12.sink.table(chunks),
      redirect = false,
    }
    -- Both libraries read the timeout from a module field when they open the
    -- socket; set it for this call only.
    local mod = is_https and https or http
    local saved = mod.TIMEOUT
    if timeout then mod.TIMEOUT = timeout end
    local ok, result, status, resp_headers = pcall(mod.request, req)
    mod.TIMEOUT = saved
    if not ok then return nil, tostring(result) end
    if not result then
      return nil, tostring(status)
    end
    if type(status) ~= "number" then
      return nil, "bad response: " .. tostring(status)
    end
    return status, lower_headers(resp_headers), table.concat(chunks)
  end
end

--- A transport built on lua-curl (Lua-cURLv3, `require "cURL"`).
--- Returns nil and a message when lua-curl is not installed.
function M.curl()
  local ok, cURL = pcall(require, "cURL")
  if not ok then return nil, "lua-curl is not installed" end

  return function(method, url, headers, body, timeout)
    local chunks, resp_headers = {}, {}
    local list = {}
    for k, v in pairs(headers or {}) do list[#list + 1] = k .. ": " .. v end
    -- curl adds "Expect: 100-continue" to larger bodies; turn it off.
    list[#list + 1] = "Expect:"
    local opts = {
      url = url,
      httpheader = list,
      followlocation = false,
      writefunction = function(s) chunks[#chunks + 1] = s; return #s end,
      headerfunction = function(line)
        local name, value = string.match(line, "^([^:%s]+):%s*(.-)%s*$")
        if name then
          name = string.lower(name)
          if resp_headers[name] then
            resp_headers[name] = resp_headers[name] .. ", " .. value
          else
            resp_headers[name] = value
          end
        elseif string.find(line, "^HTTP/") then
          resp_headers = {} -- a new status line (after 100 Continue) starts over
        end
        return #line
      end,
    }
    if timeout then opts.timeout_ms = math.floor(timeout * 1000) end
    if method == "HEAD" then
      opts.nobody = true
    elseif method ~= "GET" then
      opts.customrequest = method
    end
    if body ~= nil then opts.postfields = body end
    local easy_ok, easy = pcall(cURL.easy, opts)
    if not easy_ok then return nil, tostring(easy) end
    local perf_ok, perr = pcall(easy.perform, easy)
    if not perf_ok then
      easy:close()
      return nil, tostring(perr)
    end
    local status = easy:getinfo_response_code()
    easy:close()
    return status, resp_headers, table.concat(chunks)
  end
end

--- The first transport whose library is installed: luasocket, then lua-curl.
function M.pick()
  local t = M.luasocket()
  if t then return t, "luasocket" end
  t = M.curl()
  if t then return t, "curl" end
  return nil, "no HTTP library found: install luasocket and luasec, or lua-curl, or pass a transport"
end

return M
