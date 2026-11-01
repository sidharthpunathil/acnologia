import { ADMIN } from "./paths";

// Deterministic demo data, created through the same HTTP API the dashboard
// uses. Every secret is a fixed fake, so masked key suffixes never change.

type Json = Record<string, unknown>;

class Client {
  private cookie = "";
  constructor(private base: string) {}

  async call(method: string, path: string, body?: Json): Promise<Json> {
    const res = await fetch(this.base + path, {
      method,
      headers: {
        "Content-Type": "application/json",
        "X-Requested-With": "e2e",
        ...(this.cookie ? { Cookie: this.cookie } : {}),
      },
      body: body ? JSON.stringify(body) : undefined,
    });
    const set = res.headers.getSetCookie();
    if (set.length) this.cookie = set.map((c) => c.split(";")[0]).join("; ");
    const text = await res.text();
    if (!res.ok) throw new Error(`seed: ${method} ${path} failed with ${res.status}: ${text}`);
    return text ? (JSON.parse(text) as Json) : {};
  }

  post(path: string, body: Json) {
    return this.call("POST", path, body);
  }
}

/** Finds a numeric id in a create response, whatever the envelope looks like. */
function idOf(res: Json): number {
  const inner = (res.service ?? res) as Json;
  if (typeof inner.id !== "number") throw new Error(`seed: no id in ${JSON.stringify(res)}`);
  return inner.id;
}

/** Pulls a token string out of an app create response. */
function tokenOf(res: Json): string {
  const inner = (res.app ?? res) as Json;
  const tok = res.token ?? inner.token;
  if (typeof tok !== "string") throw new Error(`seed: no token in ${JSON.stringify(res)}`);
  return tok;
}

/**
 * Creates the demo workspace. Services point at the fake upstream (see
 * upstream.ts) under a path per slug, so checks and traffic stay local.
 */
export async function seed(base: string, upstream: string): Promise<Record<string, number>> {
  const c = new Client(base);
  await c.post("/api/auth/setup", ADMIN);

  const ids: Record<string, number> = {};
  for (const s of [
    { provider: "serpapi", slug: "serpapi", name: "SerpAPI" },
    { provider: "elevenlabs", slug: "elevenlabs", name: "ElevenLabs" },
    { provider: "brave-search", slug: "brave" },
    { provider: "tavily", slug: "tavily" },
    { provider: "gemini", slug: "gemini", name: "Gemini" },
  ]) {
    ids[s.slug] = idOf(await c.post("/api/admin/services", { ...s, base_url: `${upstream}/${s.slug}` }));
  }

  await c.post(`/api/admin/services/${ids.serpapi}/keys`, {
    keys: [
      { label: "acct-1", secret: "demo-serp-aaaaaaa1" },
      { label: "acct-2", secret: "demo-serp-aaaaaaa2" },
      { label: "acct-3", secret: "demo-serp-aaaaaaa3", tier: "backup" },
      { label: "team-b", secret: "demo-serp-aaaaaaa4", notes: "Owned by team B" },
    ],
  });
  await c.post(`/api/admin/services/${ids.elevenlabs}/keys`, {
    keys: [
      { label: "voice-1", secret: "demo-el-bbbbbbbb1" },
      { label: "voice-2", secret: "demo-el-bbbbbbbb2" },
    ],
  });
  await c.post(`/api/admin/services/${ids.brave}/keys`, { keys: [{ label: "brave-1", secret: "demo-brave-ccccccc1" }] });
  await c.post(`/api/admin/services/${ids.tavily}/keys`, { keys: [{ label: "research", secret: "tvly-demo-ddddddd1" }] });
  await c.post(`/api/admin/services/${ids.gemini}/keys`, {
    keys: [
      { label: "studio-1", secret: "AIzaDemoeeeeeeeeeeeeeeeeeeeeeeeeeee1" },
      { label: "studio-spare", secret: "AIzaDemoeeeeeeeeeeeeeeeeeeeeeeeeeee2", tier: "backup" },
    ],
  });

  await c.post("/api/admin/groups", {
    slug: "search",
    name: "Search",
    description: "SerpAPI first, then Brave",
    members: ["serpapi", "brave"],
  });
  await c.post("/api/admin/webhooks", {
    name: "Ops channel",
    url: "https://hooks.slack.com/services/T000/B000/xyz",
    format: "slack",
    events: ["key.invalid", "pool.empty", "service.down"],
  });
  await c.post("/api/admin/egress", { name: "EU exit", url: "socks5h://user:pw@proxy.example.net:1080", region: "eu-west" });
  const website = tokenOf(
    await c.post("/api/admin/apps", { name: "Website", mode: "both", limits: { daily_requests: 500, monthly_budget: 20 } }),
  );
  await c.post("/api/admin/apps", {
    name: "Research agent",
    mode: "lease",
    allowed_services: ["tavily", "brave"],
    limits: { daily_requests: 2000 },
  });
  await c.post("/api/admin/users", { email: "ops@acnologia.dev", name: "Ops", password: "supersecret1", role: "admin" });
  await c.post("/api/admin/tokens", { name: "CI deploys", scope: "write", expires_in_days: 90 });

  // Check every key now, synchronously, so no background check is still
  // running when the first screenshot is taken.
  for (const slug of ["serpapi", "elevenlabs", "tavily", "gemini"]) {
    await c.post(`/api/admin/services/${ids[slug]}/check`, {});
  }

  // A little proxied traffic, one request at a time so key rotation and the
  // resulting counts are the same on every run.
  const traffic: [string, string, Json?][] = [
    ...Array.from({ length: 9 }, () => ["GET", "/proxy/serpapi/search.json?engine=google&q=coffee"] as [string, string]),
    ...Array.from({ length: 4 }, () => ["POST", "/proxy/tavily/search", { query: "coffee" }] as [string, string, Json]),
    ...Array.from({ length: 3 }, () => ["GET", "/proxy/gemini/v1beta/models"] as [string, string]),
    ["GET", "/proxy/elevenlabs/v1/voices"],
    ["GET", "/proxy/brave/res/v1/web/search?q=coffee"],
  ];
  for (const [method, path, body] of traffic) {
    const res = await fetch(base + path, {
      method,
      headers: { Authorization: `Bearer ${website}`, "Content-Type": "application/json" },
      body: body ? JSON.stringify(body) : undefined,
    });
    await res.arrayBuffer();
  }
  return ids;
}
