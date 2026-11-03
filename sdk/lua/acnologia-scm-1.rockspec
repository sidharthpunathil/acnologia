rockspec_format = "3.0"
package = "acnologia"
version = "scm-1"

source = {
  url = "git+https://github.com/sidharthpunathil/acnologia.git",
  branch = "main",
}

description = {
  summary = "Lua client for Acnologia, the self-hosted server that holds your API keys.",
  detailed = [[
Proxy calls through Acnologia so the provider key never reaches your process,
or lease a key, renew it and report how it did. Pure Lua with a built in JSON
codec. HTTP goes through luasocket and luasec, lua-curl, or a transport you
pass in (for OpenResty or Neovim).
]],
  homepage = "https://github.com/sidharthpunathil/acnologia/tree/main/sdk/lua",
  license = "MIT",
}

dependencies = {
  "lua >= 5.1",
  "luasocket",
  "luasec",
}

-- Paths are relative to the repository root, where luarocks unpacks the source.
build = {
  type = "builtin",
  modules = {
    ["acnologia"] = "sdk/lua/acnologia/init.lua",
    ["acnologia.json"] = "sdk/lua/acnologia/json.lua",
    ["acnologia.errors"] = "sdk/lua/acnologia/errors.lua",
    ["acnologia.transport"] = "sdk/lua/acnologia/transport.lua",
  },
}
