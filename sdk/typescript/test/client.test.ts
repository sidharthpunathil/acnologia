import { describe, expect, it } from "vitest";
import { scrubMessage } from "../src/client.js";
import {
  AllKeysBusyError,
  Acnologia,
  AcnologiaError,
  InvalidKeyError,
  NoKeyAvailableError,
  QuotaExhaustedError,
  RateLimitedError,
  ServiceUnavailableError,
  type LeaseJSON,
} from "../src/index.js";

interface Call {
  url: string;
  method: string;
  headers: Record<string, string>;
  body: unknown;
  rawBody: unknown;
  init: RequestInit;
}

type Handler = (call: Call) => Response | Promise<Response>;

function json(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function leaseJSON(n: number, over: Partial<LeaseJSON> = {}): LeaseJSON {
  return {
    lease_id: `l${n}`,
    key_id: n,
    service: "serpapi",
    secret: `secret-${n}`,
    auth: { type: "query", name: "api_key" },
    base_url: "https://serpapi.com/",
    expires_at: "2026-11-01T12:00:00Z",
    ...over,
  };
}

function mock(handler: Handler) {
  const calls: Call[] = [];
  const fetch = async (input: string, init: RequestInit = {}) => {
    const headers: Record<string, string> = {};
    new Headers(init.headers).forEach((v, k) => {
      headers[k] = v;
    });
    let body: unknown = undefined;
    if (typeof init.body === "string") {
      try {
        body = JSON.parse(init.body);
      } catch {
        body = init.body;
      }
    }
    const call = { url: input, method: init.method ?? "GET", headers, body, rawBody: init.body, init };
    calls.push(call);
    return handler(call);
  };
  const client = new Acnologia({ baseUrl: "http://acnologia.test/", token: "acn_test", fetch });
  return { client, calls };
}

describe("Acnologia client", () => {
  it("requires baseUrl and token", () => {
    expect(() => new Acnologia({ baseUrl: "", token: "x" })).toThrow(/baseUrl/);
    expect(() => new Acnologia({ baseUrl: "http://x", token: "" })).toThrow(/token/);
  });

  it("lists services with auth headers and camelCase fields", async () => {
    const { client, calls } = mock(() =>
      json({
        services: [
          {
            slug: "serpapi",
            name: "SerpAPI",
            provider: "serpapi",
            category: "search",
            available: true,
            base_url: "https://serpapi.com",
            auth: { type: "query", name: "api_key" },
          },
        ],
      }),
    );
    const services = await client.services();
    expect(calls[0].url).toBe("http://acnologia.test/api/v1/services");
    expect(calls[0].method).toBe("GET");
    expect(calls[0].headers.authorization).toBe("Bearer acn_test");
    expect(calls[0].headers["x-acnologia-client"]).toBe("acnologia-ts/0.2.0");
    expect(services).toEqual([
      {
        slug: "serpapi",
        name: "SerpAPI",
        provider: "serpapi",
        category: "search",
        available: true,
        baseUrl: "https://serpapi.com",
        auth: { type: "query", name: "api_key" },
        guideUrl: "",
      },
    ]);
  });

  it("leases a key and omits undefined options", async () => {
    const { client, calls } = mock(() => json(leaseJSON(7)));
    const lease = await client.lease("serpapi");
    expect(calls[0].url).toBe("http://acnologia.test/api/v1/lease");
    expect(calls[0].method).toBe("POST");
    expect(calls[0].body).toEqual({ service: "serpapi" });
    expect(lease.leaseId).toBe("l7");
    expect(lease.keyId).toBe(7);
    expect(lease.secret).toBe("secret-7");
    expect(lease.baseUrl).toBe("https://serpapi.com");
    expect(lease.expiresAt).toBe("2026-11-01T12:00:00Z");
    expect(lease.reported).toBe(false);
  });

  it("sends exclude_key_ids and ttl_sec in snake_case", async () => {
    const { client, calls } = mock(() => json(leaseJSON(2)));
    await client.lease("serpapi", { excludeKeyIds: [1, 3], ttlSec: 60 });
    expect(calls[0].body).toEqual({ service: "serpapi", exclude_key_ids: [1, 3], ttl_sec: 60 });
  });

  it("throws NoKeyAvailableError with retryAfter on 503", async () => {
    const { client } = mock(() =>
      json({ error: { code: "no_key_available", message: "all keys cooling" }, retry_after_sec: 42 }, 503),
    );
    const err = await client.lease("serpapi").catch((e) => e);
    expect(err).toBeInstanceOf(NoKeyAvailableError);
    expect(err).toBeInstanceOf(AcnologiaError);
    expect(err.retryAfter).toBe(42);
    expect(err.status).toBe(503);
    expect(err.message).toBe("all keys cooling");
  });

  it("sends wait_sec and omits it when 0", async () => {
    const { client, calls } = mock(() => json(leaseJSON(2)));
    await client.lease("elevenlabs", { waitSec: 20, ttlSec: 60 });
    expect(calls[0].body).toEqual({ service: "elevenlabs", ttl_sec: 60, wait_sec: 20 });
    await client.lease("elevenlabs", { waitSec: 0 });
    expect(calls[1].body).toEqual({ service: "elevenlabs" });
    await expect(client.lease("elevenlabs", { waitSec: 61 })).rejects.toBeInstanceOf(RangeError);
    expect(calls).toHaveLength(2);
  });

  it("throws AllKeysBusyError on all_keys_busy, which is also a NoKeyAvailableError", async () => {
    const { client } = mock(() =>
      json({ error: { code: "all_keys_busy", message: "every key is at its limit" }, retry_after_sec: 1 }, 503),
    );
    const err = await client.lease("elevenlabs", { waitSec: 5 }).catch((e) => e);
    expect(err).toBeInstanceOf(AllKeysBusyError);
    expect(err).toBeInstanceOf(NoKeyAvailableError);
    expect(err.busy).toBe(true);
    expect(err.code).toBe("all_keys_busy");
    expect(err.status).toBe(503);
    expect(err.retryAfter).toBe(1);
    expect(err.name).toBe("AllKeysBusyError");
  });

  it("no_key_available is not busy", async () => {
    const { client } = mock(() => json({ error: { code: "no_key_available", message: "drained" } }, 503));
    const err = await client.lease("serpapi").catch((e) => e);
    expect(err).not.toBeInstanceOf(AllKeysBusyError);
    expect(err.busy).toBe(false);
    expect(err.retryAfter).toBeNull();
  });

  it("throws AcnologiaError with code and status for other errors", async () => {
    const { client } = mock(() => json({ error: { code: "unauthorized", message: "bad token" } }, 401));
    const err = await client.services().catch((e) => e);
    expect(err).toBeInstanceOf(AcnologiaError);
    expect(err).not.toBeInstanceOf(NoKeyAvailableError);
    expect(err.status).toBe(401);
    expect(err.code).toBe("unauthorized");
    expect(err.message).toBe("bad token");
  });

  it("handles non JSON error bodies", async () => {
    const { client } = mock(() => new Response("upstream down", { status: 502 }));
    const err = await client.services().catch((e) => e);
    expect(err.code).toBe("http_502");
    expect(err.message).toBe("upstream down");
  });
});

describe("Lease", () => {
  it("reports in snake_case and returns the next lease", async () => {
    const { client, calls } = mock((c) =>
      c.url.endsWith("/report") ? json({ ok: true, next: leaseJSON(9) }) : json(leaseJSON(1, { lease_id: "a/b" })),
    );
    const lease = await client.lease("serpapi");
    const next = await lease.report({
      outcome: "rate_limited",
      statusCode: 429,
      retryAfterSec: 60,
      message: "slow down",
      rotate: true,
    });
    expect(calls[1].url).toBe("http://acnologia.test/api/v1/leases/a%2Fb/report");
    expect(calls[1].body).toEqual({
      outcome: "rate_limited",
      status_code: 429,
      retry_after_sec: 60,
      message: "slow down",
      rotate: true,
    });
    expect(lease.reported).toBe(true);
    expect(next?.keyId).toBe(9);
  });

  it("returns null when the server has no next lease", async () => {
    const { client } = mock((c) => (c.url.endsWith("/report") ? json({ ok: true, next: null }) : json(leaseJSON(1))));
    const lease = await client.lease("serpapi");
    expect(await lease.report({ outcome: "ok" })).toBeNull();
  });

  it("apply puts the secret in the query for query auth", async () => {
    const { client } = mock(() => json(leaseJSON(1)));
    const lease = await client.lease("serpapi");
    const { headers, query } = lease.apply({ query: { q: "coffee", engine: "google" }, headers: { A: "1" } });
    expect(query.get("api_key")).toBe("secret-1");
    expect(query.get("q")).toBe("coffee");
    expect(headers).toEqual({ A: "1" });
    expect(lease.url("/search.json", { q: "coffee" })).toBe(
      "https://serpapi.com/search.json?q=coffee&api_key=secret-1",
    );
  });

  it("apply puts the secret in a header for header auth and does not mutate input", async () => {
    const { client } = mock(() => json(leaseJSON(1, { auth: { type: "header", name: "xi-api-key" } })));
    const lease = await client.lease("elevenlabs");
    const input = new Headers({ Accept: "audio/mpeg" });
    const { headers, query } = lease.apply({ headers: input });
    expect(headers["xi-api-key"]).toBe("secret-1");
    expect(headers.accept).toBe("audio/mpeg");
    expect(input.has("xi-api-key")).toBe(false);
    expect(query.toString()).toBe("");
  });

  it("apply uses Bearer with Authorization by default", async () => {
    const { client } = mock(() => json(leaseJSON(1, { auth: { type: "bearer", name: "" } })));
    const lease = await client.lease("x");
    expect(lease.apply().headers).toEqual({ Authorization: "Bearer secret-1" });
  });

  it("apply puts auth.prefix in front of the secret for every type", async () => {
    const cases: Array<[LeaseJSON["auth"], Record<string, string>, string]> = [
      [{ type: "query", name: "key", prefix: "pk_" }, {}, "key=pk_secret-1"],
      [{ type: "header", name: "Authorization", prefix: "Token " }, { Authorization: "Token secret-1" }, ""],
      [{ type: "bearer", name: "", prefix: "Key " }, { Authorization: "Key secret-1" }, ""],
      [{ type: "bearer", name: "X-Token", prefix: "Token " }, { "X-Token": "Token secret-1" }, ""],
      [{ type: "bearer", name: "", prefix: "" }, { Authorization: "Bearer secret-1" }, ""],
      [{ type: "header", name: "x-key", prefix: "" }, { "x-key": "secret-1" }, ""],
    ];
    for (const [auth, wantHeaders, wantQuery] of cases) {
      const { client } = mock(() => json(leaseJSON(1, { auth })));
      const lease = await client.lease("x");
      expect(lease.auth.prefix).toBe(auth.prefix);
      const { headers, query } = lease.apply();
      expect(headers).toEqual(wantHeaders);
      expect(query.toString()).toBe(wantQuery);
    }
  });

  it("toJSON hides the secret", async () => {
    const { client } = mock(() => json(leaseJSON(1)));
    const lease = await client.lease("serpapi");
    expect(JSON.stringify(lease)).not.toContain("secret-1");
  });
});

describe("withKey", () => {
  function pool(opts: { nextOnReport?: boolean; leaseFails?: boolean } = {}) {
    let leased = 0;
    let rotated = 10;
    return mock((c) => {
      if (c.url.endsWith("/api/v1/lease")) {
        if (opts.leaseFails && leased > 0) {
          return json({ error: { code: "no_key_available", message: "none" }, retry_after_sec: 5 }, 503);
        }
        leased++;
        return json(leaseJSON(leased));
      }
      const body = c.body as { rotate?: boolean };
      if (body.rotate && opts.nextOnReport !== false) {
        rotated++;
        return json({ ok: true, next: leaseJSON(rotated) });
      }
      return json({ ok: true, next: null });
    });
  }

  it("reports ok and returns the callback result", async () => {
    const { client, calls } = pool();
    const result = await client.withKey("serpapi", (lease) => `used ${lease.keyId}`);
    expect(result).toBe("used 1");
    expect(calls.map((c) => c.body)).toEqual([{ service: "serpapi" }, { outcome: "ok" }]);
  });

  it("rotates on RateLimitedError using the next lease from report", async () => {
    const { client, calls } = pool();
    const seen: number[] = [];
    const result = await client.withKey("serpapi", async (lease) => {
      seen.push(lease.keyId);
      if (seen.length === 1) throw new RateLimitedError(30, { statusCode: 429 });
      return "done";
    });
    expect(result).toBe("done");
    expect(seen).toEqual([1, 11]);
    expect(calls[1].body).toEqual({
      outcome: "rate_limited",
      status_code: 429,
      retry_after_sec: 30,
      message: "key was rate limited",
      rotate: true,
    });
    expect(calls[2].url).toBe("http://acnologia.test/api/v1/leases/l11/report");
    expect(calls[2].body).toEqual({ outcome: "ok" });
  });

  it("maps QuotaExhaustedError and InvalidKeyError to outcomes", async () => {
    const { client, calls } = pool();
    let n = 0;
    await client.withKey("serpapi", () => {
      n++;
      if (n === 1) throw new QuotaExhaustedError();
      if (n === 2) throw new InvalidKeyError({ message: "revoked" });
      return n;
    });
    expect((calls[1].body as any).outcome).toBe("quota_exhausted");
    expect((calls[2].body as any).outcome).toBe("invalid");
    expect((calls[2].body as any).message).toBe("revoked");
  });

  it("leases again excluding tried keys when report returns no next lease", async () => {
    const { client, calls } = pool({ nextOnReport: false });
    let n = 0;
    await client.withKey("serpapi", () => {
      if (++n === 1) throw new RateLimitedError();
      return n;
    });
    expect(calls[2].url).toBe("http://acnologia.test/api/v1/lease");
    expect(calls[2].body).toEqual({ service: "serpapi", exclude_key_ids: [1] });
  });

  it("passes waitSec to every lease call", async () => {
    const { client, calls } = pool({ nextOnReport: false });
    let n = 0;
    await client.withKey(
      "elevenlabs",
      () => {
        if (++n === 1) throw new RateLimitedError();
        return n;
      },
      { waitSec: 15 },
    );
    expect(calls[0].body).toEqual({ service: "elevenlabs", wait_sec: 15 });
    expect(calls[2].body).toEqual({ service: "elevenlabs", exclude_key_ids: [1], wait_sec: 15 });
    expect(calls[3].body).toEqual({ outcome: "ok" });
  });

  it("throws AllKeysBusyError without calling fn or reporting", async () => {
    const { client, calls } = mock(() =>
      json({ error: { code: "all_keys_busy", message: "busy" }, retry_after_sec: 1 }, 503),
    );
    let called = false;
    const err = await client
      .withKey("elevenlabs", () => {
        called = true;
      }, { waitSec: 2 })
      .catch((e) => e);
    expect(err).toBeInstanceOf(AllKeysBusyError);
    expect(called).toBe(false);
    expect(calls).toHaveLength(1);
  });

  it("throws NoKeyAvailableError when the pool runs dry", async () => {
    const { client } = pool({ nextOnReport: false, leaseFails: true });
    const err = await client
      .withKey("serpapi", () => {
        throw new RateLimitedError();
      })
      .catch((e) => e);
    expect(err).toBeInstanceOf(NoKeyAvailableError);
    expect(err.retryAfter).toBe(5);
  });

  it("gives up after maxAttempts and rethrows the last rotation error", async () => {
    const { client, calls } = pool();
    let n = 0;
    const err = await client
      .withKey(
        "serpapi",
        () => {
          n++;
          throw new RateLimitedError(n);
        },
        { maxAttempts: 3 },
      )
      .catch((e) => e);
    expect(n).toBe(3);
    expect(err).toBeInstanceOf(RateLimitedError);
    expect(err.retryAfter).toBe(3);
    const reports = calls.filter((c) => c.url.endsWith("/report")).map((c) => (c.body as any).rotate);
    expect(reports).toEqual([true, true, false]);
  });

  it("reports error and rethrows other errors", async () => {
    const { client, calls } = pool();
    const boom = new Error("boom");
    const err = await client
      .withKey("serpapi", () => {
        throw boom;
      })
      .catch((e) => e);
    expect(err).toBe(boom);
    expect(calls[1].body).toEqual({ outcome: "error", message: "boom" });
  });

  it("still rethrows the user error when reporting fails", async () => {
    const { client } = mock((c) =>
      c.url.endsWith("/report") ? json({ error: { code: "internal", message: "x" } }, 500) : json(leaseJSON(1)),
    );
    const boom = new Error("boom");
    const err = await client
      .withKey("serpapi", () => {
        throw boom;
      })
      .catch((e) => e);
    expect(err).toBe(boom);
  });
});

describe("proxy", () => {
  it("GET builds the proxy URL with query and token", async () => {
    const { client, calls } = mock(() => json({ organic_results: [] }));
    const res = await client.proxy("serpapi").get("/search.json", { engine: "google", q: "coffee" });
    expect(res.status).toBe(200);
    expect(calls[0].url).toBe("http://acnologia.test/proxy/serpapi/search.json?engine=google&q=coffee");
    expect(calls[0].method).toBe("GET");
    expect(calls[0].headers.authorization).toBe("Bearer acn_test");
  });

  it("POST JSON encodes plain objects", async () => {
    const { client, calls } = mock(() => new Response(new Uint8Array([1, 2, 3]), { headers: { "Content-Type": "audio/mpeg" } }));
    const res = await client
      .proxy("elevenlabs")
      .post("v1/text-to-speech/voice123", { text: "hi", model_id: "eleven_multilingual_v2" });
    expect(calls[0].url).toBe("http://acnologia.test/proxy/elevenlabs/v1/text-to-speech/voice123");
    expect(calls[0].method).toBe("POST");
    expect(calls[0].headers["content-type"]).toBe("application/json");
    expect(calls[0].body).toEqual({ text: "hi", model_id: "eleven_multilingual_v2" });
    expect(new Uint8Array(await res.arrayBuffer())).toEqual(new Uint8Array([1, 2, 3]));
  });

  it("passes string and binary bodies through untouched", async () => {
    const { client, calls } = mock(() => new Response("ok"));
    const p = client.proxy("generic");
    await p.post("raw", "hello", { headers: { "Content-Type": "text/plain" } });
    const bytes = new Uint8Array([9]);
    await p.put("bin", bytes);
    expect(calls[0].rawBody).toBe("hello");
    expect(calls[0].headers["content-type"]).toBe("text/plain");
    expect(calls[1].rawBody).toBe(bytes);
    expect(calls[1].method).toBe("PUT");
  });

  it("does not throw on non-2xx and request() supports any method", async () => {
    const { client, calls } = mock(() => json({ error: "rate limited" }, 429));
    const res = await client.proxy("serpapi").request("patch", "/a/b", { query: { x: 1 } });
    expect(res.status).toBe(429);
    expect(calls[0].method).toBe("PATCH");
    expect(calls[0].url).toBe("http://acnologia.test/proxy/serpapi/a/b?x=1");
    const del = await client.proxy("serpapi").delete("thing");
    expect(del.status).toBe(429);
    expect(calls[1].method).toBe("DELETE");
  });

  it("our token wins over a caller Authorization header", async () => {
    const { client, calls } = mock(() => new Response("ok"));
    await client.proxy("s").get("x", undefined, { headers: { Authorization: "Bearer nope", "X-Extra": "1" } });
    expect(calls[0].headers.authorization).toBe("Bearer acn_test");
    expect(calls[0].headers["x-extra"]).toBe("1");
  });
});

describe("fetch resolution", () => {
  it("uses globalThis.fetch lazily when none is passed", async () => {
    const original = globalThis.fetch;
    const client = new Acnologia({ baseUrl: "http://c", token: "acn_x" });
    let hit = "";
    globalThis.fetch = (async (url: string) => {
      hit = url;
      return json({ services: [] });
    }) as typeof fetch;
    try {
      expect(await client.services()).toEqual([]);
      expect(hit).toBe("http://c/api/v1/services");
    } finally {
      globalThis.fetch = original;
    }
  });
});

describe("secret hygiene", () => {
  it("scrubs the raw and URL encoded secret from report messages and truncates", async () => {
    expect(scrubMessage("q?k=a%2Bb%2Fc%3Dd and a+b/c=d", "a+b/c=d")).toBe("q?k=[key] and [key]");
    expect(scrubMessage("x".repeat(600), "s")).toHaveLength(500);

    const { client, calls } = mock((c) =>
      c.url.endsWith("/report") ? json({ ok: true, next: null }) : json(leaseJSON(1)),
    );
    await client
      .withKey("serpapi", (lease) => {
        throw new Error(`fetch failed for https://serpapi.com/search.json?api_key=${lease.secret}`);
      })
      .catch(() => undefined);
    await client
      .withKey(
        "serpapi",
        (lease) => {
          throw new RateLimitedError(null, { message: `429 for ${lease.secret}` });
        },
        { maxAttempts: 1 },
      )
      .catch(() => undefined);
    const reports = calls.filter((c) => c.url.endsWith("/report")).map((c) => (c.body as any).message);
    expect(reports[0]).toBe("fetch failed for https://serpapi.com/search.json?api_key=[key]");
    expect(reports[1]).toBe("429 for [key]");
  });

  it("proxy requests never follow redirects", async () => {
    const { client, calls } = mock(() => new Response(null, { status: 302, headers: { Location: "http://evil.test" } }));
    const res = await client.proxy("serpapi").get("x", undefined, { redirect: "follow" });
    expect(res.status).toBe(302);
    expect(calls[0].init.redirect).toBe("manual");
  });

  it("API calls carry a timeout signal, proxy calls do not", async () => {
    const { client, calls } = mock(() => json({ services: [] }));
    await client.services();
    await client.proxy("s").get("x");
    expect(calls[0].init.signal).toBeInstanceOf(AbortSignal);
    expect(calls[1].init.signal).toBeUndefined();
  });

  it("inspect redacts the lease secret and the client token", async () => {
    const { client } = mock(() => json(leaseJSON(1)));
    const lease = await client.lease("serpapi");
    // Node's util.inspect (console.log) calls this symbol; no @types/node here to import it.
    const view = (o: object) => JSON.stringify((o as any)[Symbol.for("nodejs.util.inspect.custom")]());
    expect(view(lease)).not.toContain("secret-1");
    expect(view(client)).not.toContain("acn_test");
  });
});

describe("renew, exclusive and service_unavailable", () => {
  it("renew posts ttl_sec to the escaped lease path and updates expiresAt", async () => {
    const { client, calls } = mock((c) =>
      c.url.endsWith("/api/v1/lease")
        ? json(leaseJSON(1, { lease_id: "a/b c" }))
        : json({ ok: true, expires_at: "2026-11-01T13:00:00Z" }),
    );
    const lease = await client.lease("serpapi");
    expect(lease.expiresAt).toBe("2026-11-01T12:00:00Z");
    const exp = await lease.renew(1.5);
    expect(exp).toBe("2026-11-01T13:00:00Z");
    expect(lease.expiresAt).toBe(exp);
    expect(calls[1].url).toBe("http://acnologia.test/api/v1/leases/a%2Fb%20c/renew");
    expect(calls[1].method).toBe("POST");
    expect(calls[1].body).toEqual({ ttl_sec: 2 });
    await client.renew(lease);
    expect(calls[2].body).toEqual({});
  });

  it("renew surfaces 404 lease_not_found as AcnologiaError", async () => {
    const { client } = mock((c) =>
      c.url.endsWith("/api/v1/lease")
        ? json(leaseJSON(1))
        : json({ error: { code: "lease_not_found", message: "lease not found" } }, 404),
    );
    const lease = await client.lease("serpapi");
    const err = await lease.renew(60).catch((e) => e);
    expect(err).toBeInstanceOf(AcnologiaError);
    expect(err).not.toBeInstanceOf(ServiceUnavailableError);
    expect(err.status).toBe(404);
    expect(err.code).toBe("lease_not_found");
  });

  it("sends exclusive only when true and exposes it on the lease", async () => {
    const { client, calls } = mock((c) => json(leaseJSON(1, { exclusive: (c.body as any).exclusive === true })));
    const plain = await client.lease("serpapi", { exclusive: false });
    expect(calls[0].body).toEqual({ service: "serpapi" });
    expect(plain.exclusive).toBe(false);
    const held = await client.lease("serpapi", { exclusive: true });
    expect(calls[1].body).toEqual({ service: "serpapi", exclusive: true });
    expect(held.exclusive).toBe(true);
  });

  it("withKey passes exclusive to every lease it takes", async () => {
    let n = 0;
    const { client, calls } = mock((c) => {
      if (c.url.endsWith("/api/v1/lease")) {
        n++;
        return json(leaseJSON(n, { exclusive: (c.body as any).exclusive === true }));
      }
      return json({ ok: true, next: null });
    });
    const seen: boolean[] = [];
    await client.withKey(
      "serpapi",
      (lease) => {
        seen.push(lease.exclusive);
        if (lease.keyId === 1) throw new RateLimitedError();
        return lease.keyId;
      },
      { exclusive: true },
    );
    expect(seen).toEqual([true, true]);
    const leases = calls.filter((c) => c.url.endsWith("/api/v1/lease"));
    expect(leases.map((c) => (c.body as any).exclusive)).toEqual([true, true]);
  });

  it("throws ServiceUnavailableError with retryAfter, not a NoKeyAvailableError", async () => {
    const { client } = mock(() =>
      json({ error: { code: "service_unavailable", message: "circuit open" }, retry_after_sec: 30 }, 503),
    );
    const err = await client.lease("serpapi").catch((e) => e);
    expect(err).toBeInstanceOf(ServiceUnavailableError);
    expect(err).toBeInstanceOf(AcnologiaError);
    expect(err).not.toBeInstanceOf(NoKeyAvailableError);
    expect(err.status).toBe(503);
    expect(err.code).toBe("service_unavailable");
    expect(err.retryAfter).toBe(30);
  });

  it("ServiceUnavailableError falls back to the Retry-After header", async () => {
    const { client } = mock(
      () =>
        new Response(JSON.stringify({ error: { code: "service_unavailable", message: "circuit open" } }), {
          status: 503,
          headers: { "Content-Type": "application/json", "Retry-After": "9" },
        }),
    );
    const err = await client.lease("serpapi").catch((e) => e);
    expect(err).toBeInstanceOf(ServiceUnavailableError);
    expect(err.retryAfter).toBe(9);
  });
});
