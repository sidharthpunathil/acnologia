# Acnologia docs

| Doc | For | Covers |
|---|---|---|
| [Architecture](architecture.md) | contributors, operators | Components, packages, middleware pipeline, data model, boot, background work, build and deployment. |
| [Request flows](flows.md) | contributors | Sequence and state diagrams: proxy, key selection, lease and report, key states and outcomes, classification, health checks, sign in, MCP, AI builder, Assistant. |
| [Security model](security.md) | operators, reviewers | Roles, key flags, hidden keys, what the model can see, secrets at rest, web protections, limits, outbound requests, checklist. |
| [Configuration](configuration.md) | operators | Environment variables, service settings, self-hosting, backups, upgrading. |
| [HTTP API](api.md) | integrators | Consumer error codes and every dashboard route with its required role. |
| [Provider specs](providers.md) | admins, contributors | Built in providers, the spec format, publishing, versions, the AI builder. |
| [Development](development.md) | contributors | Setup, layout, tests, conventions. |
| [llms.txt](llms.txt) / [llms-full.txt](llms-full.txt) | app and agent authors | Consumer API, proxy, MCP, SDKs, redaction guarantees and errors. |

Design history lives in [`superpowers/specs`](superpowers/specs): the original
key pool design and the providers as data and AI builder design.

```mermaid
flowchart LR
    Apps["apps and agents"] -->|"proxy / lease / MCP"| Acnologia["Acnologia server (one binary)"]
    Admins["dashboard users"] -->|"session"| Acnologia
    Acnologia -->|"key injected"| Providers["provider APIs"]
    Acnologia --- DB[("SQLite")]
```
