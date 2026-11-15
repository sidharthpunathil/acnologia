# 004: App MCP public names and output schemas, follow ups

Shipped in 946867f (merged to main as 5450dcf), after a8516ba brought both
MCP servers in line with the MCP builder guide
(github.com/anthropics/skills, `skills/mcp-builder`).

## What shipped

- **Prefixed names.** Agents see the app MCP's built in tools as
  `acnologia_get_key`, `acnologia_list_services`, `acnologia_search_tools`
  and so on, so they cannot clash with another server's tools. Generated
  provider tools keep their names, since they already start with the service
  slug. The mapping lives in `server/internal/api/mcp_names.go`.
- **Old names still work.** Agents and SDKs that call `get_key` keep working
  (`canonicalTool`). A tool hidden in an app's MCP settings stays hidden
  under either name (`disabledSet`). The dashboard still shows the short
  names.
- **Agent facing text.** The instructions, tool descriptions, prompts,
  service guides, error messages and `search_tools` hints use the new names
  (`publicText`).
- **Output schemas.** Every built in tool and every provider tool declares
  the shape of its result. `TestMCPResultsMatchOutputSchemas` runs each built
  in tool and the service tool path for real and checks the result against
  its schema, because clients reject a result that does not match.
- **Docs.** The app MCP sections of `llms.txt`, `llms-full.txt`, `api.md` and
  `flows.md` use the new names, with a note that the old ones still work.

## To update

1. **Client permission rules.** The listed names changed, so a client that
   saved per tool permissions (a Claude Code allow rule for `get_key`, say)
   asks again for `acnologia_get_key` until the rule is updated. That
   includes the Acnologia server the Claude Code sessions here use
   (`acnologia-god-access`). Update the allow rules, or tell users in the
   release notes.
2. **Dashboard names.** `web/src/pages/AppMcpPanel.tsx` lists tools by their
   short name (`t.name`), while agents see `acnologia_<name>`. Have
   `mcpView` (`server/internal/api/mcp_admin.go`) return the public name next
   to the internal one and show it in the panel. Keep saving the internal
   name in `disabled_tools`.
3. **Other docs.** These still use the short names in MCP context:
   `docs/security.md` (lines on `get_key` and `proxy_request`),
   `docs/providers.md` (`proxy_request`, `service_guide`),
   `docs/architecture.md` (the App MCP row) and the rows of `docs/api.md`
   outside the `POST /mcp` line. Update them and their copies in
   `server/internal/docsmd/files/`.
4. **Docs screenshots.** The docs pages changed, so the e2e screenshots
   `docs-api`, `docs-flows` and `docs` under `e2e/__screenshots__/` are
   stale. Regenerate them.
5. **Control MCP.** `/mcp/control` keeps its names. It was left out because
   its tools are separate from the app MCP's and none of these changes needed
   it. Decide whether it gets the same treatment: a prefix (its tools such as
   `list_keys` and `list_services` share names with the app MCP's), output
   schemas, and paging on its list tools.
6. **Release note.** Say in the next release that the app MCP's built in
   tools are listed as `acnologia_<name>` and that the old names still work.
