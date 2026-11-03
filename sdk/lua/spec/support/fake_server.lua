-- A tiny HTTP/1.1 server for the integration test, built on luasocket. It
-- prints its port on the first line, answers one request at a time, and
-- exits on GET /quit (or after 60 idle seconds).

local socket = require("socket")
local json = require("acnologia.json")

local server = assert(socket.bind("127.0.0.1", 0))
local _, port = server:getsockname()
io.stdout:write(port, "\n")
io.stdout:flush()
server:settimeout(60)

local function respond(conn, status, headers, body)
  body = body or ""
  local lines = { "HTTP/1.1 " .. status .. " X" }
  headers["Content-Length"] = tostring(#body)
  headers["Connection"] = "close"
  for k, v in pairs(headers) do lines[#lines + 1] = k .. ": " .. v end
  conn:send(table.concat(lines, "\r\n") .. "\r\n\r\n" .. body)
end

local running = true
while running do
  local conn = server:accept()
  if not conn then break end
  conn:settimeout(5)
  local line = conn:receive("*l")
  if line then
    local method, target = line:match("^(%S+)%s+(%S+)")
    local headers = {}
    while true do
      local h = conn:receive("*l")
      if not h or h == "" then break end
      local k, v = h:match("^([^:]+):%s*(.*)$")
      if k then headers[k:lower()] = v end
    end
    local body = ""
    local n = tonumber(headers["content-length"] or "0")
    if n and n > 0 then body = conn:receive(n) or "" end
    local path, query = target:match("^([^?]*)%??(.*)$")
    local echo = {
      method = method, path = path, query = query, body = body,
      authorization = headers["authorization"], idempotency_key = headers["idempotency-key"],
      content_type = headers["content-type"],
    }
    if path == "/quit" then
      respond(conn, 200, {}, "bye")
      running = false
    elseif path == "/api/v1/me" then
      respond(conn, 200, { ["Content-Type"] = "application/json" }, json.encode({ app = { id = 1, name = "lua" }, echo = echo }))
    elseif path == "/proxy/agi-labs/limit" then
      respond(conn, 429, { ["Retry-After"] = "30", ["Content-Type"] = "application/json" }, json.encode({
        error = { code = "app_limit_reached", message = "this app reached its daily requests limit" },
        limit = "daily_requests", reset_at = "2026-11-05T00:00:00Z", retry_after_sec = 30,
      }))
    elseif path == "/proxy/agi-labs/redirect" then
      respond(conn, 302, { Location = "http://127.0.0.1:1/elsewhere", ["X-Acnologia-Service"] = "agi-labs" }, "")
    elseif path == "/proxy/agi-labs/slow" then
      socket.sleep(2)
      respond(conn, 200, { ["X-Acnologia-Service"] = "agi-labs" }, "late")
    elseif path:match("^/proxy/") then
      respond(conn, 200, {
        ["Content-Type"] = "application/json", ["X-Acnologia-Service"] = "agi-labs",
        ["X-Acnologia-Key-Id"] = "5", ["X-Acnologia-Attempts"] = "1",
      }, json.encode(echo))
    else
      respond(conn, 404, { ["Content-Type"] = "application/json" }, json.encode({ error = { code = "not_found", message = "no route" } }))
    end
  end
  conn:close()
end
server:close()
