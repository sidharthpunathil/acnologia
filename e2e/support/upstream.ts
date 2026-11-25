import http from "node:http";
import type net from "node:net";

// A fake provider API on 127.0.0.1, so the key checks and proxied requests
// the seed triggers never leave the machine and always answer the same way.
// Each seeded secret maps to one fixed behaviour below.

const DAY = 86_400_000;

type Reply = { status: number; body: unknown };

function reply(path: string, secret: string, now: number): Reply {
  const has = (s: string) => secret.endsWith(s);
  // SerpAPI: /account.json carries the quota.
  if (path.startsWith("/serpapi")) {
    if (has("aaaaaaa4")) return { status: 401, body: { error: "Invalid API key. Your API key should be here: https://serpapi.com/manage-api-key" } };
    const left = has("aaaaaaa1") ? 812 : has("aaaaaaa2") ? 37 : 5000;
    if (path.endsWith("/account.json"))
      return {
        status: 200,
        body: {
          searches_per_month: has("aaaaaaa3") ? 5000 : 1000,
          extra_credits: 0,
          total_searches_left: left,
          plan_renewal_date: new Date(now + 12.5 * DAY).toISOString(),
          account_rate_limit_per_hour: 200,
          this_hour_searches: 4,
        },
      };
    return { status: 200, body: { search_metadata: { status: "Success" }, organic_results: [{ title: "Coffee" }] } };
  }
  // ElevenLabs: voice-2 is out of characters.
  if (path.startsWith("/elevenlabs")) {
    if (has("bbbbbbbb2")) return { status: 401, body: { detail: { status: "quota_exceeded", message: "This request exceeds your quota." } } };
    if (path.endsWith("/v1/user/subscription"))
      return { status: 200, body: { character_limit: 100000, character_count: 41250, next_character_count_reset_unix: Math.floor((now + 9.5 * DAY) / 1000) } };
    return { status: 200, body: { voices: [] } };
  }
  if (path.startsWith("/tavily")) {
    if (path.endsWith("/usage")) return { status: 200, body: { account: { plan_limit: 1000, plan_usage: 120 } } };
    return { status: 200, body: { results: [] } };
  }
  if (path.startsWith("/gemini")) return { status: 200, body: { models: [{ name: "models/gemini-2.5-flash" }] } };
  return { status: 200, body: { web: { results: [] } } };
}

/** Finds which seeded secret a request carries, in the query or any header. */
function secretOf(req: http.IncomingMessage): string {
  const hay = [req.url ?? "", ...Object.values(req.headers).flat()].join(" ");
  const m = hay.match(/demo-[a-z]+-[a-z0-9]+|AIzaDemo[a-z0-9]+|tvly-demo-[a-z0-9]+/);
  return m ? m[0] : "";
}

// A Streamable HTTP MCP server at /mcp with two tools, one marked read-only,
// for services whose provider is mcp.
const MCP_TOOLS = [
  {
    name: "search_docs",
    title: "Search docs",
    description: "Finds pages in the product docs that match a query.",
    inputSchema: { type: "object", properties: { query: { type: "string" } }, required: ["query"] },
    annotations: { readOnlyHint: true },
  },
  {
    name: "create_page",
    title: "Create page",
    description: "Adds a new page to the docs.",
    inputSchema: { type: "object", properties: { title: { type: "string" }, body: { type: "string" } }, required: ["title"] },
    annotations: { readOnlyHint: false },
  },
];

function mcpReply(raw: string): { status: number; body?: unknown } {
  let msg: { id?: number | string; method?: string };
  try {
    msg = JSON.parse(raw) as typeof msg;
  } catch {
    return { status: 400, body: { jsonrpc: "2.0", id: null, error: { code: -32700, message: "parse error" } } };
  }
  if (msg.id === undefined) return { status: 202 };
  if (msg.method === "initialize")
    return {
      status: 200,
      body: {
        jsonrpc: "2.0",
        id: msg.id,
        result: { protocolVersion: "2025-06-18", capabilities: { tools: {} }, serverInfo: { name: "docs-mcp", version: "1.0.0" } },
      },
    };
  if (msg.method === "tools/list") return { status: 200, body: { jsonrpc: "2.0", id: msg.id, result: { tools: MCP_TOOLS } } };
  return { status: 200, body: { jsonrpc: "2.0", id: msg.id, error: { code: -32601, message: "method not found" } } };
}

/** An unsigned id token, so the OAuth sign in names the account. */
function idToken(email: string): string {
  const enc = (v: unknown) => Buffer.from(JSON.stringify(v)).toString("base64url");
  return `${enc({ alg: "none" })}.${enc({ email })}.sig`;
}

/**
 * A provider's OAuth token endpoint at /oauth/token: any code is good. The
 * tokens carry no lifetime, so the account shows "no expiry" whatever the
 * clock says.
 */
function oauthReply(path: string): Reply {
  if (path === "/oauth/token")
    return {
      status: 200,
      body: { access_token: "fake-oauth-access-0001", refresh_token: "fake-oauth-refresh-0001", token_type: "Bearer", id_token: idToken("ana@example.com") },
    };
  return { status: 404, body: { error: "not_found" } };
}

export async function startUpstream(now: number): Promise<{ origin: string; close: () => void }> {
  const srv = http.createServer((req, res) => {
    const path = new URL(req.url ?? "/", "http://x").pathname;
    if (path.startsWith("/mcp") || path.startsWith("/oauth/")) {
      let raw = "";
      req.setEncoding("utf8");
      req.on("data", (chunk: string) => (raw += chunk));
      req.on("end", () => {
        const { status, body } = path.startsWith("/mcp") ? mcpReply(raw) : oauthReply(path);
        if (body === undefined) {
          res.writeHead(status);
          res.end();
          return;
        }
        res.writeHead(status, { "Content-Type": "application/json" });
        res.end(JSON.stringify(body));
      });
      return;
    }
    req.resume();
    const { status, body } = reply(path, secretOf(req), now);
    res.writeHead(status, { "Content-Type": "application/json" });
    res.end(JSON.stringify(body));
  });
  await new Promise<void>((r) => srv.listen(0, "127.0.0.1", r));
  srv.unref();
  const { port } = srv.address() as net.AddressInfo;
  return { origin: `http://127.0.0.1:${port}`, close: () => srv.close() };
}
