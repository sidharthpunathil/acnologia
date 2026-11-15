# Changelog

## Unreleased

### Action needed

- Clients that saved per tool permission rules for the app MCP (`POST /mcp`)
  must update them to the new names. A Claude Code allow rule for `get_key`,
  for example, now needs `acnologia_get_key`. Until the rule is updated, the
  client asks again before each call.

### App MCP

- Built in tools are listed as `acnologia_<name>`: `acnologia_get_key`,
  `acnologia_list_services`, `acnologia_search_tools` and so on, so they no
  longer clash with another server's tools. Generated service tools such as
  `serpapi_google_search` keep their names.
- The old names still work when called, and `disabled_tools` accepts either
  form.
- The dashboard's MCP panel on the Apps page shows the names agents see.
- Every built in tool and every service tool declares an `outputSchema` that
  matches its `structuredContent`.
- `initialize` answers with the client's protocol version when the server
  speaks it (`2025-11-25`, `2025-06-18`, `2025-03-26`, `2024-11-05`), else
  `2025-06-18`.
- Every built in tool has a `title` and states `readOnlyHint`,
  `destructiveHint` and `openWorldHint`.
- `acnologia_list_services` and `acnologia_list_keys` page their results:
  pass `limit` and `offset`, and read `has_more` and `next_offset` (pass it as
  `offset` for the next page).
- Errors name the tool to call next, such as `acnologia_list_services` for an
  unknown service.
- The agent instructions say that apps built by agents call the provider's
  own URL with Acnologia as their `HTTPS_PROXY`, and keep Acnologia out of
  their UI, copy, page titles and user facing errors.

### Control MCP

- Tools are listed as `acnologia_<name>` (`acnologia_list_keys`,
  `acnologia_delete_service`). The old names still work when called, and
  `exclude=` and `batch` accept either form. Instructions, descriptions,
  prompts and errors use the new names, and per tool permission rules saved
  in a client need the new names too.
- The list tools (`list_keys`, `list_services`, `list_events`, `list_apps`,
  `list_providers`, `search` and the other lists) return one page, 100 items
  by default, with `total`, `count`, `offset`, `has_more` and `next_offset`.
  Pass `limit` and `offset` for more. `list_events` and `list_audit` page
  through the newest 500 and 1,000 entries, and their `limit` is now the page
  size.
- `whoami`, `search` and most list tools declare an `outputSchema` that
  matches their `structuredContent`, and every tool states all four hints.
