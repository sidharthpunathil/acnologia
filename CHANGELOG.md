# Changelog

## Unreleased

### Provider rotation

- Requests can move between providers as they move between keys. A service
  with rotation on shares its requests with the other services, also with
  rotation on, that serve the same endpoint: services of the same provider,
  or of providers whose catalog lists an entry of the same name (Omkar
  Cloud's `amazon-scraper` and a self-hosted server's). Each service gets
  the path in its own form.
- Two modes: `fallback` tries the service the request names first, and
  `round_robin` starts each request at the next service.
- Settings on the service page has a Provider rotation card that lists the
  matching services and turns rotation on for all of them in one click.
  New routes: `GET /api/admin/services/{id}/rotation` and
  `POST /api/admin/services/rotation`; `PATCH /services/{id}` takes
  `rotation`.

### Catalogs on self-hosted servers

- A catalog entry's `url` can be a path on the service's base URL, such as
  `/api/scrapers/amazon-scraper/call`, so a server that runs many scrapers
  under one host gets the same `/~name/...` calls as Omkar Cloud. Path
  entries send keys nowhere new and need no named domain.
- A spec with a catalog may document up to 1500 guide endpoints.

### Assistant

- Every service page has an Ask AI button that opens the Assistant in a
  drawer, with the service as the chat's context.
- `call_service` calls a service's endpoint through the proxy and pages a
  long JSON answer with `offset`, `limit`, `select` and `where`, so the
  Assistant can read what a self-hosted server serves and write its catalog
  from it.
- `update_catalog` takes an `offer` per entry and asks for Confirm only
  when an entry gets a host of its own.
- `service_rotation` and `set_rotation` read and set provider rotation;
  `update_service` takes `rotation`.
- A turn may take 40 steps and each reply up to 16,000 tokens, enough to
  write a long catalog in batches.

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
