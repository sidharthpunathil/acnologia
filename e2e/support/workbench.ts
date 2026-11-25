import type { Page } from "@playwright/test";
import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { ADMIN } from "./paths";
import { Client, idOf, tokenOf, type Json } from "./seed";
import { startServer, stopServer } from "./server";
import { startUpstream } from "./upstream";

// A server of its own for specs that change what they look at (load demo
// data, send a Try it request, sign in an MCP client) or need data the main
// suite's pages must not show (a trash item, OAuth services, an upstream
// MCP server). Each project starts its own, so the four runs never meet.

export interface Workbench {
  base: string;
  upstream: string;
  api: Client;
  /**
   * Epoch ms the page clock is frozen at: a minute after the data was made,
   * so "1m ago" style text is the same however late the test runs.
   */
  now: number;
  /** The end of the server's log, for a failure message. */
  log: () => string;
  close: () => void;
}

/** Starts an empty server with the demo superadmin and a fake provider API beside it. */
export async function startWorkbench(): Promise<Workbench> {
  const upstream = await startUpstream(Date.now());
  const server = await startServer();
  const api = new Client(server.baseURL);
  try {
    await api.post("/api/auth/setup", ADMIN);
  } catch (e) {
    stopServer(server.pid, server.dataDir);
    upstream.close();
    throw e;
  }
  return {
    base: server.baseURL,
    upstream: upstream.origin,
    api,
    now: Date.now() + 60_000,
    log: () => {
      try {
        return fs.readFileSync(path.join(server.dataDir, "server.log"), "utf8").slice(-4000);
      } catch {
        return "";
      }
    },
    close: () => {
      stopServer(server.pid, server.dataDir);
      upstream.close();
    },
  };
}

/** Signs the page's browser context in to the workbench as the superadmin, on the workbench's clock. */
export async function signIn(page: Page, w: Workbench): Promise<void> {
  await page.clock.setFixedTime(w.now);
  const res = await page.request.post(`${w.base}/api/auth/login`, { data: { email: ADMIN.email, password: ADMIN.password } });
  if (!res.ok()) throw new Error(`login failed with ${res.status()}: ${await res.text()}`);
}

export interface PanelSeed {
  serpapi: number;
  google: number;
  oauthDemo: number;
  docsMcp: number;
  /** The app whose tokens and access controls the specs open. */
  app: { id: number; name: string };
  /** Pending MCP sign ins waiting on /oauth/consent. */
  consent: { app: string; control: string };
}

const REDIRECT = "http://127.0.0.1/callback";

/** A public MCP client, registered the way claude.ai or Cursor would. */
async function registerClient(base: string, name: string): Promise<string> {
  const res = await fetch(`${base}/oauth/register`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ client_name: name, redirect_uris: [REDIRECT], token_endpoint_auth_method: "none" }),
  });
  const body = (await res.json()) as Json;
  if (!res.ok || typeof body.client_id !== "string") throw new Error(`register ${name}: ${res.status} ${JSON.stringify(body)}`);
  return body.client_id;
}

/** Starts a sign in and returns the consent request id and the PKCE verifier. */
async function authorize(base: string, clientId: string, control: boolean): Promise<{ id: string; verifier: string }> {
  const verifier = crypto.randomBytes(32).toString("base64url");
  const challenge = crypto.createHash("sha256").update(verifier).digest("base64url");
  const q = new URLSearchParams({
    response_type: "code",
    client_id: clientId,
    redirect_uri: REDIRECT,
    code_challenge: challenge,
    code_challenge_method: "S256",
    state: "e2e",
    resource: `${base}${control ? "/mcp/control" : "/mcp"}`,
  });
  const res = await fetch(`${base}/oauth/authorize?${q}`, { redirect: "manual" });
  const loc = res.headers.get("location") ?? "";
  const id = new URL(loc, base).searchParams.get("request");
  if (!id) throw new Error(`authorize: no consent request in ${res.status} ${loc}`);
  return { id, verifier };
}

/** Approves a sign in as the signed in admin and trades the code, so the connection is live. */
async function connect(api: Client, base: string, clientId: string, control: boolean, approval: Json): Promise<void> {
  const { id, verifier } = await authorize(base, clientId, control);
  const { redirect } = (await api.post(`/api/oauth/requests/${encodeURIComponent(id)}/approve`, approval)) as { redirect: string };
  const code = new URL(redirect).searchParams.get("code");
  if (!code) throw new Error(`approve: no code in ${redirect}`);
  const res = await fetch(`${base}/oauth/token`, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({ grant_type: "authorization_code", code, redirect_uri: REDIRECT, client_id: clientId, code_verifier: verifier }),
  });
  if (!res.ok) throw new Error(`token: ${res.status} ${await res.text()}`);
}

/**
 * Fills a workbench for the panel specs: a SerpAPI service with traffic, an
 * app with tokens, limits, tag budgets, deny rules and tagged usage, a trash
 * item, a Google service with its OAuth client but no account, an OAuth
 * service with one account signed in against the fake token endpoint, an
 * upstream MCP service, and MCP sign ins (two live, two waiting).
 */
export async function seedPanels(w: Workbench): Promise<PanelSeed> {
  const { api, base, upstream } = w;

  const serpapi = idOf(await api.post("/api/admin/services", { provider: "serpapi", slug: "serpapi", name: "SerpAPI", base_url: `${upstream}/serpapi` }));
  await api.post(`/api/admin/services/${serpapi}/keys`, { keys: [{ label: "acct-1", secret: "demo-serp-aaaaaaa1" }] });

  // An app with two tokens, one with limits and a pinned tag of its own.
  const created = await api.post("/api/admin/apps", { name: "Support bot", mode: "both", limits: { daily_requests: 1000, monthly_budget: 50 } });
  const appId = idOf((created.app ?? created) as Json);
  const token = tokenOf(created);
  await api.post(`/api/admin/apps/${appId}/tokens`, {
    name: "eu-workers",
    limits: { daily_requests: 200, daily_budget: 2 },
    pinned_tags: { region: "eu" },
  });
  await api.put(`/api/admin/apps/${appId}/token-limits`, { limits: { daily_requests: 500, monthly_budget: 10 } });
  await api.put(`/api/admin/apps/${appId}/tag-budgets`, {
    budgets: [
      { key: "customer", daily_requests: 100, daily_budget: 2 },
      { key: "team", monthly_budget: 25 },
    ],
  });
  await api.post(`/api/admin/apps/${appId}/deny-rules`, { method: "POST", service: "serpapi", host: "", path_prefix: "/account*", note: "No account changes" });
  await api.post(`/api/admin/apps/${appId}/deny-rules`, { method: "*", service: "", host: "api.example.com", path_prefix: "/", note: "" });
  // Tagged traffic, one request at a time, for Usage by tag.
  for (const tag of ["customer=cust_A", "customer=cust_A", "customer=cust_B", "team=growth"]) {
    const res = await fetch(`${base}/proxy/serpapi/search.json?engine=google&q=coffee`, {
      headers: { Authorization: `Bearer ${token}`, "X-Acnologia-Meta": tag },
    });
    await res.arrayBuffer();
  }

  // The trash: a deleted service with its key, and a deleted app.
  const brave = idOf(await api.post("/api/admin/services", { provider: "brave-search", slug: "brave", base_url: `${upstream}/brave` }));
  await api.post(`/api/admin/services/${brave}/keys`, { keys: [{ label: "brave-1", secret: "demo-brave-ccccccc1" }] });
  await api.call("DELETE", `/api/admin/services/${brave}`);
  // The trash lists newest first by the second: keep the two apart so the order holds.
  await new Promise((r) => setTimeout(r, 1100));
  const old = await api.post("/api/admin/apps", { name: "Old scraper", mode: "proxy" });
  await api.call("DELETE", `/api/admin/apps/${idOf((old.app ?? old) as Json)}`);

  // Google: the OAuth client is set, no account signed in yet.
  const google = idOf(await api.post("/api/admin/services", { provider: "google", slug: "google", name: "Google" }));
  await api.put(`/api/admin/services/${google}/oauth`, { client_id: "1234567890-demo.apps.googleusercontent.com", client_secret: "demo-google-client-secret" });

  // A provider that signs in with OAuth against the fake token endpoint, with one account connected.
  await api.post("/api/admin/providers", {
    slug: "oauth-demo",
    name: "OAuth Demo",
    spec: {
      base_url: `${upstream}/oauth-demo`,
      auth: {
        type: "oauth2",
        name: "Authorization",
        oauth: { authorize_url: `${upstream}/oauth/authorize`, token_url: `${upstream}/oauth/token`, scopes: ["openid", "email"], pkce: true },
      },
      use_standard_rules: true,
      tests: [{ name: "429", status: 429, body: "", expect: "rate_limited" }],
    },
  });
  const oauthDemo = idOf(await api.post("/api/admin/services", { provider: "oauth-demo", slug: "oauth-demo", base_url: `${upstream}/oauth-demo` }));
  await api.put(`/api/admin/services/${oauthDemo}/oauth`, { client_id: "demo-client", client_secret: "demo-client-secret" });
  const start = (await api.post(`/api/admin/services/${oauthDemo}/oauth/connect`, {})) as { url: string };
  const state = new URL(start.url).searchParams.get("state") ?? "";
  const cb = await fetch(`${base}/oauth/callback?${new URLSearchParams({ state, code: "demo-code" })}`);
  if (!cb.ok) throw new Error(`oauth callback: ${cb.status} ${await cb.text()}`);

  // An upstream MCP server with two tools; an admin marks the write one read-only.
  const docsMcp = idOf(await api.post("/api/admin/services", { provider: "mcp", slug: "docs-mcp", name: "Docs MCP", mcp_url: `${upstream}/mcp` }));
  // With a key: a sync of a service with none panics in upstreamClient (nil key).
  await api.post(`/api/admin/services/${docsMcp}/keys`, { keys: [{ label: "docs-token", secret: "demo-mcp-eeeeeee1" }] });
  await api.post(`/api/admin/services/${docsMcp}/mcp/sync`, {});
  await api.patch(`/api/admin/services/${docsMcp}/mcp/tools`, { name: "create_page", access: "read" });

  // MCP sign ins: Claude on the app (read-only), Cursor on the control MCP.
  const claude = await registerClient(base, "Claude");
  const cursor = await registerClient(base, "Cursor");
  await connect(api, base, claude, false, { app_id: appId, read_only: true });
  await connect(api, base, cursor, true, { scope: "write" });
  const consent = {
    app: (await authorize(base, claude, false)).id,
    control: (await authorize(base, cursor, true)).id,
  };

  w.now = Date.now() + 60_000;
  return { serpapi, google, oauthDemo, docsMcp, app: { id: appId, name: "Support bot" }, consent };
}
