-- A small JSON encoder and decoder in pure Lua, for Lua 5.1 (and LuaJIT)
-- through 5.5. It has no dependencies.
--
-- JSON null decodes to json.null, a unique value that is not nil, so it
-- survives in tables. An empty Lua table encodes as an object ({}); wrap it
-- with json.array(t) to encode it as an array ([]). Decoded arrays carry that
-- mark too, so they encode back as arrays.

local json = {}

local type, pairs, ipairs, tostring, tonumber = type, pairs, ipairs, tostring, tonumber
local setmetatable, getmetatable = setmetatable, getmetatable
local concat, sort = table.concat, table.sort
local byte, char, find, format, sub, gsub = string.byte, string.char, string.find, string.format, string.sub, string.gsub
local floor, huge = math.floor, math.huge

json.null = setmetatable({}, {
  __tostring = function() return "null" end,
  __name = "acnologia.json.null",
})

local array_mt = { __jsontype = "array" }
local object_mt = { __jsontype = "object" }

--- Mark t as a JSON array, so an empty table encodes as [].
function json.array(t)
  return setmetatable(t or {}, array_mt)
end

--- Mark t as a JSON object, so a table with keys 1..n still encodes as {}.
function json.object(t)
  return setmetatable(t or {}, object_mt)
end

-- Encoding ------------------------------------------------------------------

local escapes = {
  ['"'] = '\\"', ["\\"] = "\\\\", ["\b"] = "\\b", ["\f"] = "\\f",
  ["\n"] = "\\n", ["\r"] = "\\r", ["\t"] = "\\t",
}

local function escape_char(c)
  return escapes[c] or format("\\u%04x", byte(c))
end

local function encode_string(s)
  return '"' .. gsub(s, '[%c"\\]', escape_char) .. '"'
end

local function encode_number(n)
  if n ~= n or n == huge or n == -huge then
    error("json: cannot encode " .. tostring(n), 0)
  end
  if math.type and math.type(n) == "integer" then
    return format("%d", n)
  end
  if floor(n) == n and n > -2^53 and n < 2^53 then
    return format("%.0f", n)
  end
  return format("%.17g", n)
end

-- kind returns "array" with its length, or "object".
local function kind(t)
  local mt = getmetatable(t)
  if mt and mt.__jsontype then
    if mt.__jsontype == "array" then return "array", #t end
    return "object"
  end
  local n = 0
  for k in pairs(t) do
    if type(k) ~= "number" or k <= 0 or floor(k) ~= k then return "object" end
    if k > n then n = k end
  end
  if n == 0 then return "object" end
  -- Only a dense sequence is an array; a sparse one would lose its gaps.
  for i = 1, n do
    if t[i] == nil then return "object" end
  end
  return "array", n
end

local encode_value

local function encode_table(t, seen)
  if seen[t] then error("json: cannot encode a table that contains itself", 0) end
  seen[t] = true
  local out = {}
  local k, n = kind(t)
  if k == "array" then
    for i = 1, n do out[i] = encode_value(t[i], seen) end
    seen[t] = nil
    return "[" .. concat(out, ",") .. "]"
  end
  local keys = {}
  for key in pairs(t) do
    local kt = type(key)
    if kt ~= "string" and kt ~= "number" then
      error("json: object keys must be strings or numbers, got " .. kt, 0)
    end
    keys[#keys + 1] = key
  end
  -- Sorted keys keep the output stable, which tests and caches like.
  sort(keys, function(a, b) return tostring(a) < tostring(b) end)
  for i, key in ipairs(keys) do
    local name = type(key) == "number" and encode_number(key) or key
    out[i] = encode_string(name) .. ":" .. encode_value(t[key], seen)
  end
  seen[t] = nil
  return "{" .. concat(out, ",") .. "}"
end

encode_value = function(v, seen)
  local t = type(v)
  if v == nil or v == json.null then return "null" end
  if t == "boolean" then return v and "true" or "false" end
  if t == "number" then return encode_number(v) end
  if t == "string" then return encode_string(v) end
  if t == "table" then return encode_table(v, seen) end
  error("json: cannot encode a " .. t, 0)
end

--- Encode a Lua value as JSON. Returns the string, or nil and an error message.
function json.encode(value)
  local ok, res = pcall(encode_value, value, {})
  if ok then return res end
  return nil, res
end

-- Decoding ------------------------------------------------------------------

local function utf8_char(cp)
  if cp < 0x80 then return char(cp) end
  if cp < 0x800 then
    return char(0xC0 + floor(cp / 0x40), 0x80 + cp % 0x40)
  end
  if cp < 0x10000 then
    return char(0xE0 + floor(cp / 0x1000), 0x80 + floor(cp / 0x40) % 0x40, 0x80 + cp % 0x40)
  end
  return char(0xF0 + floor(cp / 0x40000), 0x80 + floor(cp / 0x1000) % 0x40,
    0x80 + floor(cp / 0x40) % 0x40, 0x80 + cp % 0x40)
end

local function decode_error(str, pos, msg)
  local line, col = 1, 1
  for i = 1, pos - 1 do
    if byte(str, i) == 10 then line, col = line + 1, 1 else col = col + 1 end
  end
  error(format("json: %s at line %d column %d", msg, line, col), 0)
end

local function skip_ws(str, pos)
  local _, e = find(str, "^[ \t\r\n]*", pos)
  return e + 1
end

local decode_value

local unescape = {
  ['"'] = '"', ["\\"] = "\\", ["/"] = "/", b = "\b", f = "\f", n = "\n", r = "\r", t = "\t",
}

local function decode_string(str, pos)
  -- pos is just past the opening quote.
  local parts = {}
  local i = pos
  while true do
    local s, e = find(str, '^[^"\\%c]+', i)
    if s then
      parts[#parts + 1] = sub(str, s, e)
      i = e + 1
    end
    local c = sub(str, i, i)
    if c == '"' then
      return concat(parts), i + 1
    elseif c == "\\" then
      local esc = sub(str, i + 1, i + 1)
      if esc == "u" then
        local hex = sub(str, i + 2, i + 5)
        if not find(hex, "^%x%x%x%x$") then decode_error(str, i, "bad unicode escape") end
        local cp = tonumber(hex, 16)
        i = i + 6
        if cp >= 0xD800 and cp <= 0xDBFF then
          local lo = sub(str, i, i + 5)
          if find(lo, "^\\u[dD][c-fC-F]%x%x$") then
            cp = 0x10000 + (cp - 0xD800) * 0x400 + (tonumber(sub(lo, 3), 16) - 0xDC00)
            i = i + 6
          else
            cp = 0xFFFD
          end
        elseif cp >= 0xDC00 and cp <= 0xDFFF then
          cp = 0xFFFD
        end
        parts[#parts + 1] = utf8_char(cp)
      elseif unescape[esc] then
        parts[#parts + 1] = unescape[esc]
        i = i + 2
      else
        decode_error(str, i, "bad escape")
      end
    elseif c == "" then
      decode_error(str, i, "unterminated string")
    else
      decode_error(str, i, "control character in string")
    end
  end
end

local function decode_number(str, pos)
  local _, e = find(str, "^-?%d+", pos)
  if not e then decode_error(str, pos, "bad number") end
  if find(str, "^-?0%d", pos) then decode_error(str, pos, "leading zero") end
  if sub(str, e + 1, e + 1) == "." then
    local _, fe = find(str, "^%d+", e + 2)
    if not fe then decode_error(str, pos, "bad number") end
    e = fe
  end
  if find(str, "^[eE]", e + 1) then
    local _, xe = find(str, "^[-+]?%d+", e + 2)
    if not xe then decode_error(str, pos, "bad number") end
    e = xe
  end
  local text = sub(str, pos, e)
  local n = tonumber(text)
  if not n then decode_error(str, pos, "bad number") end
  return n, e + 1
end

local function decode_array(str, pos, depth)
  local out = json.array({})
  local n = 0
  pos = skip_ws(str, pos)
  if sub(str, pos, pos) == "]" then return out, pos + 1 end
  while true do
    local v
    v, pos = decode_value(str, pos, depth)
    n = n + 1
    out[n] = v
    pos = skip_ws(str, pos)
    local c = sub(str, pos, pos)
    if c == "]" then return out, pos + 1 end
    if c ~= "," then decode_error(str, pos, "expected ',' or ']'") end
    pos = pos + 1
  end
end

local function decode_object(str, pos, depth)
  local out = {}
  pos = skip_ws(str, pos)
  if sub(str, pos, pos) == "}" then return out, pos + 1 end
  while true do
    pos = skip_ws(str, pos)
    if sub(str, pos, pos) ~= '"' then decode_error(str, pos, "expected a string key") end
    local key
    key, pos = decode_string(str, pos + 1)
    pos = skip_ws(str, pos)
    if sub(str, pos, pos) ~= ":" then decode_error(str, pos, "expected ':'") end
    local v
    v, pos = decode_value(str, pos + 1, depth)
    out[key] = v
    pos = skip_ws(str, pos)
    local c = sub(str, pos, pos)
    if c == "}" then return out, pos + 1 end
    if c ~= "," then decode_error(str, pos, "expected ',' or '}'") end
    pos = pos + 1
  end
end

local MAX_DEPTH = 512

decode_value = function(str, pos, depth)
  pos = skip_ws(str, pos)
  local c = sub(str, pos, pos)
  if c == "{" or c == "[" then
    if depth >= MAX_DEPTH then decode_error(str, pos, "too deeply nested") end
    if c == "{" then return decode_object(str, pos + 1, depth + 1) end
    return decode_array(str, pos + 1, depth + 1)
  end
  if c == '"' then return decode_string(str, pos + 1) end
  if c == "-" or find(c, "^%d$") then return decode_number(str, pos) end
  if sub(str, pos, pos + 3) == "true" then return true, pos + 4 end
  if sub(str, pos, pos + 4) == "false" then return false, pos + 5 end
  if sub(str, pos, pos + 3) == "null" then return json.null, pos + 4 end
  if c == "" then decode_error(str, pos, "unexpected end of input") end
  decode_error(str, pos, "unexpected character '" .. c .. "'")
end

--- Decode a JSON string. Returns the value, or nil and an error message.
function json.decode(str)
  if type(str) ~= "string" then return nil, "json: expected a string, got " .. type(str) end
  local ok, res, pos = pcall(decode_value, str, 1, 0)
  if not ok then return nil, res end
  pos = skip_ws(str, pos)
  if pos <= #str then
    local ok2, err = pcall(decode_error, str, pos, "trailing data")
    return nil, ok2 and "json: trailing data" or err
  end
  return res
end

return json
