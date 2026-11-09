import { describe, expect, it } from "vitest";
import {
  Acnologia,
  AcnologiaError,
  AppLimitReachedError,
  ShareUnavailableError,
  VERSION,
  proxyInfo,
} from "../src/index.js";

interface Call {
  url: string;
  method: string;
  headers: Record<string, string>;
  body: unknown;
}

const CA = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n";

function json(data: unknown, status = 200, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(data), { status, headers: { "Content-Type": "application/json", ...headers } });
}

const ME = {
  app: { id: 3, name: "bot" },
  limits: { daily_requests: 100, monthly_requests: null, daily_budget: null, monthly_budget: 5.5 },
  usage: { today_requests: 7, today_cost: 0.1, month_requests: 70, month_cost: 1, last_used_at: "2026-11-01T10:00:00Z", limit_hit: null },
  limit_hit: { limit: "daily_requests", reset_at: "2026-11-02T00:00:00Z" },
  services: [{ slug: "serpapi", name: "SerpAPI", provider: "serpapi", category: "search", available: 1, logo_url: null }],
  share: { id: "sh1", name: "Team", owner_name: "Ann", recipient: "bob", label: "x", status: "paused", mode: "proxy", ends_at: null },
};

function stub(opts: { token?: string; accessPassword?: string; proxyUrl?: string } = {}) {
  const calls: Call[] = [];
  const fetch = async (input: string, init: RequestInit = {}) => {
    const headers: Record<string, string> = {};
    new Headers(init.headers).forEach((v, k) => {
      headers[k] = v;
    });
    const body = typeof init.body === "string" ? JSON.parse(init.body) : undefined;
    calls.push({ url: input, method: init.method ?? "GET", headers, body });
    const u = new URL(input);
    const p = u.pathname;
    if (p === "/api/v1/proxy/ca.pem") return new Response(CA, { headers: { "Content-Type": "application/x-pem-file" } });
    if (p === "/api/v1/me") return json(ME);
    if (p === "/api/v1/services")
      return json({
        services: [{ slug: "serpapi", name: "SerpAPI", provider: "serpapi", category: "search", available: true,
          base_url: "https://serpapi.com", auth: { type: "query", name: "api_key" }, guide_url: "http://x/api/v1/services/serpapi/guide" }],
      });
    if (p === "/api/v1/services/serpapi/guide") {
      if ((headers.accept ?? "").includes("text/markdown")) return new Response("# serpapi", { headers: { "Content-Type": "text/markdown" } });
      return json({ markdown: "# serpapi", generated: true, guide: { endpoints: [1] } });
    }
    if (p === "/api/v1/capabilities")
      return json({
        capabilities: [{ slug: "web-search", name: "Web search", description: "d", strategy: "failover",
          params: { q: { type: "string" } }, routes: [{ service: "serpapi", mapped: true }], url: "http://x/proxy/web-search" }],
      });
    if (p === "/api/v1/proxy")
      return json({
        proxy_url: opts.proxyUrl ?? "http://proxy.test:8888",
        proxy_url_with_token: "http://app:%3Capp%20token%3E@proxy.test:8888",
        ca_url: "http://acnologia.test/api/v1/proxy/ca.pem",
        ca_fingerprint: "ab:cd",
        passthrough: true,
        hosts: [{ host: "serpapi.com", service: "serpapi", name: "SerpAPI" }],
        env: { HTTPS_PROXY: "http://app:%3Capp%20token%3E@proxy.test:8888" },
        service_header: "X-Acnologia-Service",
      });
    if (p.startsWith("/proxy/")) {
      const fail = u.searchParams.get("fail");
      if (fail === "limit")
        return json(
          { error: { code: "app_limit_reached", message: "limit" }, limit: "daily_requests", reset_at: "2026-11-02T00:00:00Z", retry_after_sec: 60 },
          429,
          { "Retry-After": "60" },
        );
      if (fail === "share") return json({ error: { code: "share_revoked", message: "revoked" } }, 403, { "X-Acnologia-Share": "revoked" });
      return json({ ok: true }, 200, {
        "X-Acnologia-Key-Id": "7",
        "X-Acnologia-Attempts": "2",
        "X-Acnologia-Service": "serpapi",
        "X-Acnologia-Cache": "miss",
        "Idempotent-Replayed": "true",
        "X-Acnologia-Capability": "web-search",
        "X-Acnologia-Tried": "brave=429, serpapi=200",
      });
    }
    return json({ error: { code: "not_found", message: p } }, 404);
  };
  const client = new Acnologia({
    baseUrl: "http://acnologia.test",
    token: opts.token ?? "acn_test",
    fetch,
    accessPassword: opts.accessPassword,
  });
  return { client, calls, last: () => calls[calls.length - 1]! };
}

describe("0.2.0 surface", () => {
  it("is version 0.2.0", () => {
    expect(VERSION).toBe("0.2.0");
  });

  it("me parses app, limits, usage, services and share", async () => {
    const { client, last } = stub();
    const me = await client.me();
    expect(last().method).toBe("GET");
    expect(last().url).toBe("http://acnologia.test/api/v1/me");
    expect(last().headers.authorization).toBe("Bearer acn_test");
    expect(me.app).toEqual({ id: 3, name: "bot" });
    expect(me.limits).toEqual({ dailyRequests: 100, monthlyRequests: null, dailyBudget: null, monthlyBudget: 5.5 });
    expect(me.usage.todayRequests).toBe(7);
    expect(me.usage.lastUsedAt).toBe("2026-11-01T10:00:00Z");
    expect(me.usage.limitHit).toBeNull();
    expect(me.limitHit).toEqual({ limit: "daily_requests", resetAt: "2026-11-02T00:00:00Z" });
    expect(me.services[0]).toEqual({ slug: "serpapi", name: "SerpAPI", provider: "serpapi", category: "search", available: 1, logoUrl: null });
    expect(me.share?.status).toBe("paused");
    expect(me.share?.ownerName).toBe("Ann");
    expect(me.share?.endsAt).toBeNull();
  });

  it("services carry guideUrl", async () => {
    const { client } = stub();
    expect((await client.services())[0]!.guideUrl).toBe("http://x/api/v1/services/serpapi/guide");
  });

  it("guide and guideMarkdown", async () => {
    const { client, last } = stub();
    const g = await client.guide("serpapi");
    expect(last().url).toBe("http://acnologia.test/api/v1/services/serpapi/guide");
    expect(last().headers.accept).toBe("application/json");
    expect(g).toEqual({ markdown: "# serpapi", generated: true, guide: { endpoints: [1] } });
    expect(await client.guideMarkdown("serpapi")).toBe("# serpapi");
    expect(last().headers.accept).toBe("text/markdown");
    expect(last().headers.authorization).toBe("Bearer acn_test");
  });

  it("capabilities", async () => {
    const { client, last } = stub();
    const caps = await client.capabilities();
    expect(last().method).toBe("GET");
    expect(last().url).toBe("http://acnologia.test/api/v1/capabilities");
    expect(caps).toEqual([
      { slug: "web-search", name: "Web search", description: "d", strategy: "failover", params: { q: { type: "string" } },
        routes: [{ service: "serpapi", mapped: true }], url: "http://x/proxy/web-search" },
    ]);
  });

  it("capability posts params with _service, _raw and per call headers", async () => {
    const { client, last } = stub();
    const res = await client.capability("web-search", { q: "coffee" }, { service: "serpapi", raw: true, idempotencyKey: "k1", noCache: true });
    const c = last();
    expect(c.method).toBe("POST");
    expect(c.url).toBe("http://acnologia.test/proxy/web-search?_service=serpapi&_raw=1");
    expect(c.body).toEqual({ q: "coffee" });
    expect(c.headers["content-type"]).toBe("application/json");
    expect(c.headers["idempotency-key"]).toBe("k1");
    expect(c.headers["cache-control"]).toBe("no-cache");
    const info = proxyInfo(res);
    expect(info.capability).toBe("web-search");
    expect(info.service).toBe("serpapi");
    expect(info.tried).toEqual([
      { service: "brave", status: 429 },
      { service: "serpapi", status: 200 },
    ]);

    await client.capability("web-search");
    expect(last().url).toBe("http://acnologia.test/proxy/web-search");
    expect(last().body).toEqual({});
    expect(last().headers["idempotency-key"]).toBeUndefined();
  });

  it("proxy options and proxyInfo", async () => {
    const { client, last } = stub();
    const res = await client.proxy("serpapi").post("/search", { q: "x" }, { idempotencyKey: "abc", noCache: true });
    expect(last().headers["idempotency-key"]).toBe("abc");
    expect(last().headers["cache-control"]).toBe("no-cache");
    expect(proxyInfo(res)).toMatchObject({ keyId: 7, attempts: 2, service: "serpapi", cache: "miss", replayed: true });
    await client.proxy("serpapi").get("/search", { q: "y" });
    expect(last().headers["idempotency-key"]).toBeUndefined();
    expect(last().headers["cache-control"]).toBeUndefined();
    expect(proxyInfo(new Response("x"))).toEqual({
      keyId: null, attempts: null, service: null, cache: null, replayed: false, capability: null, tried: [],
    });
  });

  it("accessPassword is sent on every request", async () => {
    const { client, last } = stub({ accessPassword: "gate" });
    await client.me();
    expect(last().headers["x-acnologia-access"]).toBe("gate");
    await client.proxy("serpapi").get("/x");
    expect(last().headers["x-acnologia-access"]).toBe("gate");
    await client.forwardProxyCA();
    expect(last().headers["x-acnologia-access"]).toBe("gate");
    const plain = stub();
    await plain.client.me();
    expect(plain.last().headers["x-acnologia-access"]).toBeUndefined();
  });

  it("app_limit_reached throws AppLimitReachedError", async () => {
    const { client } = stub();
    const err: any = await client._json("GET", "/proxy/x?fail=limit").catch((e) => e);
    expect(err).toBeInstanceOf(AppLimitReachedError);
    expect(err).toBeInstanceOf(AcnologiaError);
    expect(err.status).toBe(429);
    expect(err.code).toBe("app_limit_reached");
    expect(err.limit).toBe("daily_requests");
    expect(err.resetAt).toBe("2026-11-02T00:00:00Z");
    expect(err.retryAfter).toBe(60);
  });

  it("share_* throws ShareUnavailableError", async () => {
    const { client } = stub();
    const err: any = await client._json("GET", "/proxy/x?fail=share").catch((e) => e);
    expect(err).toBeInstanceOf(ShareUnavailableError);
    expect(err.shareStatus).toBe("revoked");
    expect(err.status).toBe(403);
    expect(err.code).toBe("share_revoked");

    const paused = new Acnologia({
      baseUrl: "http://x",
      token: "acn_test",
      fetch: async () => json({ error: { code: "share_paused", message: "m" } }, 403),
    });
    const e2: any = await paused.me().catch((e) => e);
    expect(e2).toBeInstanceOf(ShareUnavailableError);
    expect(e2.shareStatus).toBe("paused");
  });

  it("the generic error carries retryAfter from the header", async () => {
    const c = new Acnologia({
      baseUrl: "http://x",
      token: "acn_test",
      fetch: async () => json({ error: { code: "bad_params", message: "q" } }, 400, { "Retry-After": "2" }),
    });
    const err: any = await c.me().catch((e) => e);
    expect(err).toBeInstanceOf(AcnologiaError);
    expect(err.code).toBe("bad_params");
    expect(err.retryAfter).toBe(2);
  });

  it("forwardProxy, forwardProxyURL and forwardProxyEnv", async () => {
    const { client, last } = stub();
    const fp = await client.forwardProxy();
    expect(last().url).toBe("http://acnologia.test/api/v1/proxy");
    expect(last().headers.authorization).toBe("Bearer acn_test");
    expect(fp).toMatchObject({
      proxyUrl: "http://proxy.test:8888",
      caFingerprint: "ab:cd",
      passthrough: true,
      hosts: [{ host: "serpapi.com", service: "serpapi", name: "SerpAPI" }],
      serviceHeader: "X-Acnologia-Service",
    });
    expect(JSON.stringify(fp)).not.toContain("acn_test");

    const url = await client.forwardProxyURL();
    expect(url).toBe("http://app:acn_test@proxy.test:8888");
    expect(await client.forwardProxyEnv()).toEqual({
      HTTPS_PROXY: url,
      HTTP_PROXY: url,
      NODE_USE_ENV_PROXY: "1",
      NODE_EXTRA_CA_CERTS: "acnologia-ca.pem",
      REQUESTS_CA_BUNDLE: "acnologia-ca.pem",
      SSL_CERT_FILE: "acnologia-ca.pem",
    });
    expect((await client.forwardProxyEnv("/etc/ca.pem")).SSL_CERT_FILE).toBe("/etc/ca.pem");
  });

  it("forwardProxyURL percent escapes the token", async () => {
    const { client } = stub({ token: "acn_a/b@c" });
    expect(await client.forwardProxyURL()).toBe("http://app:acn_a%2Fb%40c@proxy.test:8888");
  });

  it("forwardProxyCA sends no token", async () => {
    const { client, last } = stub();
    expect(await client.forwardProxyCA()).toBe(CA);
    expect(last().url).toBe("http://acnologia.test/api/v1/proxy/ca.pem");
    expect(last().headers.authorization).toBeUndefined();
  });
});
