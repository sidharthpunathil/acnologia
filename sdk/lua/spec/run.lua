-- A plain test runner for machines without busted. It supports the small
-- part of busted the specs use. Run it from sdk/lua:
--
--   lua spec/run.lua
--
-- It exits 0 when every test passes.

package.path = "./?.lua;./?/init.lua;" .. package.path
-- Find rocks installed with luarocks (luasocket, luasec) when it is around.
pcall(require, "luarocks.loader")

local real_assert = assert

local function deep_equal(a, b)
  if a == b then return true end
  if type(a) ~= "table" or type(b) ~= "table" then return false end
  for k, v in pairs(a) do
    if not deep_equal(v, b[k]) then return false end
  end
  for k in pairs(b) do
    if a[k] == nil then return false end
  end
  return true
end

local function fail(msg, extra)
  error((extra and (extra .. ": ") or "") .. msg, 3)
end

local function show(v) return type(v) == "string" and string.format("%q", v) or tostring(v) end

local A = setmetatable({}, { __call = function(_, ...) return real_assert(...) end })
A.are = {
  equal = function(want, got, msg) if want ~= got then fail("expected " .. show(want) .. ", got " .. show(got), msg) end end,
  same = function(want, got, msg) if not deep_equal(want, got) then fail("tables differ", msg) end end,
}
A.are_not = {
  equal = function(a, b, msg) if a == b then fail("expected values to differ: " .. show(a), msg) end end,
}
A.is_nil = function(v, msg) if v ~= nil then fail("expected nil, got " .. show(v), msg) end end
A.is_true = function(v, msg) if v ~= true then fail("expected true, got " .. show(v), msg) end end
A.is_false = function(v, msg) if v ~= false then fail("expected false, got " .. show(v), msg) end end
A.truthy = function(v, msg) if not v then fail("expected a truthy value, got " .. show(v), msg) end end
assert = A

local tests, stack = {}, {}
local function current() return stack[#stack] end

local function new_block(name, parent)
  return { name = name, parent = parent, children = {}, setup = {}, teardown = {} }
end

local root = new_block("", nil)
stack[1] = root

function describe(name, fn)
  local block = new_block(name, current())
  table.insert(current().children, block)
  stack[#stack + 1] = block
  fn()
  stack[#stack] = nil
end

function it(name, fn)
  table.insert(current().children, { test = true, name = name, fn = fn })
end

function setup(fn) table.insert(current().setup, fn) end
function teardown(fn) table.insert(current().teardown, fn) end

local passed, failed = 0, 0

local function run(block, prefix)
  for _, fn in ipairs(block.setup) do fn() end
  for _, child in ipairs(block.children) do
    local name = (prefix ~= "" and (prefix .. " ") or "") .. child.name
    if child.test then
      local ok, err = pcall(child.fn)
      if ok then
        passed = passed + 1
      else
        failed = failed + 1
        io.stdout:write("FAIL ", name, "\n  ", tostring(err), "\n")
      end
    else
      run(child, name)
    end
  end
  for _, fn in ipairs(block.teardown) do fn() end
end

local files = { "spec/json_spec.lua", "spec/client_spec.lua", "spec/integration_spec.lua" }
for _, file in ipairs(files) do
  local chunk = real_assert(loadfile(file))
  chunk()
end
run(root, "")
io.stdout:write(string.format("%d passed, %d failed\n", passed, failed))
os.exit(failed == 0 and 0 or 1)
