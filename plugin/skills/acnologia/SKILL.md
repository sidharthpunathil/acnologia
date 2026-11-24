---
name: acnologia
description: Call third party APIs (search, LLMs, scraping, voice, maps and the rest) through the user's Acnologia server, which holds the API keys, picks one per call and rotates on rate limits. Use when a task needs a provider API, when the user mentions Acnologia or acn, when you need an API key you do not have, or when you build an app that calls a provider.
---

# Acnologia

Acnologia is a self-hosted server that keeps a pool of API keys per provider.
You call the provider through it: it picks a key, injects it, retries with the
next key on a rate limit and records usage. You never see the key, and you
should never ask the user to paste one into the chat.

## Check where you stand

Work down this list and stop at the first step that holds.

1. **The MCP tools are loaded.** Look for tools whose names end in
   `acnologia_list_services`. There are two kinds of connection:
   - The control MCP (this plugin, or `acn mcp`) acts as the signed in user.
     It has `acnologia_whoami`, `acnologia_call_service`,
     `acnologia_list_service_examples` and the admin tools (services, keys,
     apps).
   - The app MCP (an app token, added from the app's MCP panel in the
     dashboard) has `acnologia_search_tools`, `acnologia_call_tool`,
     `acnologia_proxy_request` and `acnologia_get_key`.
   Call `acnologia_list_services` to see what is there, then skip to
   "Find the right tool".
2. **`acn` is installed but the tools are missing.** Run `command -v acn`
   (`Get-Command acn` on Windows). If it is there, run `acn whoami`. If it
   says `not logged in`, ask the user to run `acn login`; it asks for the
   server URL and opens the browser to approve. If it prints an account,
   `acn status` shows the pool. Then ask the user to restart the session so
   the MCP server starts. If the user did not install this plugin, `acn mcp
   install` adds the server to Claude Code, Cursor and Codex in one step.
3. **Nothing is installed.** Ask the user before installing anything.
   - Only the client, for a server that already runs somewhere:
     `curl -fsSL https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/install.sh | sh`
     (Windows: `irm https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/install.ps1 | iex`).
   - A server on this machine, plus the client:
     `curl -fsSL https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/setup.sh | sh -s -- --with-cli`.
     It listens on `http://127.0.0.1:8080`. The user opens that once to
     create the first account and add a provider key.
   - Then `acn login` and a new session.

If `acnologia_list_services` returns nothing, the server has no services yet.
The user adds one in the dashboard (Catalog, pick a provider, paste a key), or
you add it with `acnologia_create_service` and `acnologia_add_keys` on the
control MCP when the user gives you the key out of band. Never echo a key
back.

## Find the right tool

- App MCP: `acnologia_search_tools` with a plain query such as
  `linkedin jobs`, `google maps reviews` or `generate image`. It returns the
  best tools with their input schemas. Run one with `acnologia_call_tool`
  (name and arguments) or by its name. An empty query lists every service and
  how many tools it has.
- Control MCP: `acnologia_list_services` for the slugs, `acnologia_search`
  to find services, keys and apps by text, and
  `acnologia_list_service_examples` for ready to use call arguments.
- From a terminal: `acn services` lists the services with key counts.

## Read the guide first

Before the first call to a service, read `acnologia_service_guide` with its
slug. It lists the endpoints with parameters and example bodies, the response
fields, the limits and pricing, the errors and the real proxy URL. Do not
guess paths from memory: the guide is what this server actually routes.

## Make the call

- Control MCP: `acnologia_call_service` with `service`, `method`, `path`,
  and `query` or `body`. It needs the admin role and runs as the app
  "Control MCP", so that app's limits apply.
- App MCP: a generated tool, `acnologia_call_tool`, or
  `acnologia_proxy_request` with the same fields.
- Plain HTTP with an app token: `ANY /proxy/<service>/<path>` with
  `Authorization: Bearer acn_...`.

The answer carries the provider's status and body. Check the status before
you trust the body: a 4xx from the provider means your request was wrong,
so fix the parameters from the guide rather than retrying.

## Rate limits and errors

Acnologia already moves to the next key when one is rate limited. A key that
was told to wait rests for as long as the provider asked, so do not loop
quickly on your own. These answers come from Acnologia itself:

- 503 `no_key_available`: every key is cooling down or out of quota. Wait
  `retry_after_sec`, then try once more.
- 503 `all_keys_busy`: keys exist but are at their limit. Wait
  `retry_after_sec`, or lease with `wait_sec`.
- 503 `service_unavailable`: the provider keeps failing and its breaker is
  open. Wait, and tell the user if it persists.
- 429 `app_limit_reached`: the app spent its daily or monthly limit;
  `reset_at` says when. Tell the user rather than retrying.
- 403 `service_not_allowed`: the token may not use that service. The user
  allows it in Apps.

## Leases, when you must call the provider yourself

Prefer the proxy. Lease a key only when a library must hold it (a streaming
SDK, a websocket). On the app MCP: `acnologia_get_key` with the service
returns the secret, where it goes and a `lease_id`. Pass `exclusive: true` to
keep the key to yourself, `acnologia_renew_key` for long jobs, and always
finish with `acnologia_report_key`: `ok`, or `rate_limited` with
`retry_after_sec` and `rotate: true` to get the next key. Never print, log or
save the secret.

## Apps you build: the forward proxy

When you write an app that calls a provider, keep keys and Acnologia out of
its code. Give it an app token (the user creates one in Apps, or you use
`acnologia_create_app` and `acnologia_create_app_token` on the control MCP)
and point its HTTPS proxy at Acnologia. The app then calls the provider's own
URL and the key is added on the way.

```sh
acn proxy ca                     # saves acnologia-ca.pem, the CA to trust
acn proxy env --token acn_...    # prints HTTPS_PROXY, NODE_EXTRA_CA_CERTS and friends
```

`acn proxy` lists the hosts that get keys. `acnologia_forward_proxy_setup`
gives the same on the control MCP. Put the token in the app's environment,
never in source control, and keep Acnologia out of the app's UI, copy and
user facing errors: its users never see it.
