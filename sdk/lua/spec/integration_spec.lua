-- Runs the real luasocket transport (and lua-curl when installed) against a
-- fake HTTP server in a child process.

local acnologia = require("acnologia")

local function interpreter()
  if os.getenv("LUA") and os.getenv("LUA") ~= "" then return os.getenv("LUA") end
  local i, exe = -1, nil
  while arg and arg[i] ~= nil do
    exe = arg[i]
    i = i - 1
  end
  return exe or (jit and "luajit" or "lua")
end

local function here()
  local src = debug.getinfo(1, "S").source:gsub("^@", "")
  return (src:match("^(.*)[/\\]") or ".")
end

local function start_server()
  local script = here() .. "/support/fake_server.lua"
  local cmd = string.format('%s -e "package.path=[[%s]] package.cpath=[[%s]] pcall(require, [[luarocks.loader]])" "%s"',
    interpreter(), package.path, package.cpath, script)
  local proc = assert(io.popen(cmd, "r"))
  local port = proc:read("*l")
  assert(port and tonumber(port), "fake server did not start: " .. cmd)
  return proc, "http://127.0.0.1:" .. port
end

local transports = { { "luasocket", acnologia.transport.luasocket() } }
local curl = acnologia.transport.curl()
if curl then transports[#transports + 1] = { "curl", curl } end

for _, t in ipairs(transports) do
  local name, transport = t[1], t[2]

  describe("integration over " .. name, function()
    local proc, base, client

    setup(function()
      proc, base = start_server()
      client = assert(acnologia.new({ base_url = base, token = "acn_integration", transport = transport, getenv = function() return nil end }))
    end)

    teardown(function()
      transport("GET", base .. "/quit", {}, nil, 5)
      proc:close()
    end)

    it("calls me", function()
      local me = acnologia.assert(client:me())
      assert.are.equal("lua", me.app.name)
      assert.are.equal("Bearer acn_integration", me.echo.authorization)
      assert.are.equal("GET", me.echo.method)
    end)

    it("proxies a POST with query, JSON body and Idempotency-Key", function()
      local resp = acnologia.assert(client:post("agi-labs", "/v1/chat", {
        query = { model = "small" }, json = { prompt = "hello" }, idempotency_key = "abc-1",
      }))
      assert.are.equal(200, resp.status)
      assert.are.equal(5, resp.acnologia.key_id)
      assert.are.equal("agi-labs", resp.acnologia.service)
      local echo = resp:json()
      assert.are.equal("POST", echo.method)
      assert.are.equal("/proxy/agi-labs/v1/chat", echo.path)
      assert.are.equal("model=small", echo.query)
      assert.are.equal('{"prompt":"hello"}', echo.body)
      assert.are.equal("abc-1", echo.idempotency_key)
      assert.are.equal("application/json", echo.content_type)
    end)

    it("maps app_limit_reached with Retry-After", function()
      local resp, err = client:get("agi-labs", "/limit")
      assert.is_nil(resp)
      assert.are.equal("app_limit_reached", err.code)
      assert.are.equal(429, err.status)
      assert.are.equal(30, err.retry_after)
      assert.are.equal("daily_requests", err.limit)
    end)

    it("does not follow redirects", function()
      local resp = acnologia.assert(client:get("agi-labs", "/redirect"))
      assert.are.equal(302, resp.status)
      assert.are.equal("http://127.0.0.1:1/elsewhere", resp.headers.location)
    end)

    it("times out", function()
      local resp, err = client:get("agi-labs", "/slow", { timeout = 0.5 })
      assert.is_nil(resp)
      assert.truthy(err.code == "timeout" or err.code == "transport")
    end)
  end)
end
