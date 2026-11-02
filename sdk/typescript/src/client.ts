import {
  AllKeysBusyError,
  AcnologiaError,
  KeyRotationError,
  NoKeyAvailableError,
  ServiceUnavailableError,
} from "./errors.js";
import type {
  AuthSpec,
  AcnologiaOptions,
  FetchLike,
  HeadersLike,
  LeaseJSON,
  LeaseOptions,
  QueryInit,
  ReportOptions,
  Service,
  WithKeyOptions,
} from "./types.js";

export const VERSION = "0.1.0";
const CLIENT_HEADER = `acnologia-ts/${VERSION}`;
const DEFAULT_TIMEOUT_MS = 75_000;
const MAX_REPORT_MESSAGE = 500;
const INSPECT = Symbol.for("nodejs.util.inspect.custom");

/**
 * Replace the lease secret (raw and URL encoded) with `[key]` and truncate. Errors from query
 * auth services often carry the full request URL, key included, and a report must not copy it.
 */
export function scrubMessage(message: string, secret: string): string {
  let out = message;
  if (secret) {
    out = out.split(secret).join("[key]").split(encodeURIComponent(secret)).join("[key]");
  }
  return out.slice(0, MAX_REPORT_MESSAGE);
}

function toSearchParams(query?: QueryInit): URLSearchParams {
  if (!query) return new URLSearchParams();
  if (query instanceof URLSearchParams) return new URLSearchParams(query);
  if (Array.isArray(query)) return new URLSearchParams(query);
  const out = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) {
    if (v === undefined || v === null) continue;
    out.append(k, String(v));
  }
  return out;
}

function toHeaderRecord(headers?: HeadersLike): Record<string, string> {
  const out: Record<string, string> = {};
  if (!headers) return out;
  if (typeof Headers !== "undefined" && headers instanceof Headers) {
    headers.forEach((v, k) => {
      out[k] = v;
    });
    return out;
  }
  if (Array.isArray(headers)) {
    for (const [k, v] of headers) out[k] = v;
    return out;
  }
  return { ...(headers as Record<string, string>) };
}

function appendQuery(url: string, query?: QueryInit): string {
  const qs = toSearchParams(query).toString();
  if (!qs) return url;
  return url + (url.includes("?") ? "&" : "?") + qs;
}

function isPassThroughBody(body: unknown): boolean {
  if (typeof body === "string") return true;
  if (typeof Blob !== "undefined" && body instanceof Blob) return true;
  if (typeof FormData !== "undefined" && body instanceof FormData) return true;
  if (body instanceof URLSearchParams) return true;
  if (body instanceof ArrayBuffer) return true;
  if (ArrayBuffer.isView(body)) return true;
  if (typeof ReadableStream !== "undefined" && body instanceof ReadableStream) return true;
  return false;
}

/** A key handed out by Acnologia. Report how it went with `report()`. */
export class Lease {
  readonly leaseId: string;
  readonly keyId: number;
  readonly service: string;
  readonly secret: string;
  readonly auth: AuthSpec;
  readonly baseUrl: string;
  /** True when the key is reserved for this lease only. */
  readonly exclusive: boolean;
  readonly raw: LeaseJSON;
  private _expiresAt: string;
  private _reported = false;

  constructor(
    private readonly client: Acnologia,
    raw: LeaseJSON,
  ) {
    this.raw = raw;
    this.leaseId = raw.lease_id;
    this.keyId = raw.key_id;
    this.service = raw.service;
    this.secret = raw.secret;
    this.auth = raw.auth ?? { type: "header", name: "" };
    this.baseUrl = (raw.base_url ?? "").replace(/\/+$/, "");
    this._expiresAt = raw.expires_at;
    this.exclusive = raw.exclusive === true;
  }

  /** When the lease expires (RFC 3339). Updated by `renew()`. */
  get expiresAt(): string {
    return this._expiresAt;
  }

  /**
   * Extend this open lease so a long job keeps its key. Omit `ttlSec` for the server default
   * (120 seconds); the server caps it at 24 hours. Returns the new expiry and updates
   * `expiresAt`. Throws `AcnologiaError` with code `lease_not_found` when the lease is no
   * longer open.
   */
  async renew(ttlSec?: number): Promise<string> {
    const body: Record<string, unknown> = {};
    if (ttlSec !== undefined && ttlSec > 0) body.ttl_sec = Math.ceil(ttlSec);
    const res = await this.client._json<{ ok: boolean; expires_at?: string }>(
      "POST",
      `/api/v1/leases/${encodeURIComponent(this.leaseId)}/renew`,
      body,
    );
    if (!res?.expires_at) throw new AcnologiaError("renew response has no expires_at", 200, "bad_response", res);
    this._expiresAt = res.expires_at;
    return res.expires_at;
  }

  /** True once `report()` has been called for this lease. */
  get reported(): boolean {
    return this._reported;
  }

  /** Report the outcome of using this key. Returns the next lease when `rotate` is true and one is available. */
  async report(opts: ReportOptions): Promise<Lease | null> {
    const body: Record<string, unknown> = { outcome: opts.outcome };
    if (opts.statusCode !== undefined) body.status_code = opts.statusCode;
    if (opts.retryAfterSec !== undefined) body.retry_after_sec = opts.retryAfterSec;
    if (opts.message !== undefined) body.message = opts.message;
    if (opts.rotate !== undefined) body.rotate = opts.rotate;
    const res = await this.client._json<{ ok: boolean; next: LeaseJSON | null }>(
      "POST",
      `/api/v1/leases/${encodeURIComponent(this.leaseId)}/report`,
      body,
    );
    this._reported = true;
    return res.next ? new Lease(this.client, res.next) : null;
  }

  /**
   * Put the secret where the service expects it. Returns copies:
   * `query` auth sets `query[auth.name]`, `header` auth sets `headers[auth.name]`,
   * `bearer` auth sets `headers[auth.name || "Authorization"] = "Bearer <secret>"`.
   * A non empty `auth.prefix` goes in front of the secret for every type; for `bearer` it
   * replaces the default `Bearer ` prefix.
   */
  apply(input: { headers?: HeadersLike; query?: QueryInit } = {}): {
    headers: Record<string, string>;
    query: URLSearchParams;
  } {
    const headers = toHeaderRecord(input.headers);
    const query = toSearchParams(input.query);
    const prefix = this.auth.prefix ?? "";
    const value = prefix + this.secret;
    switch (this.auth.type) {
      case "query":
        query.set(this.auth.name || "api_key", value);
        break;
      case "bearer":
        headers[this.auth.name || "Authorization"] = prefix ? value : `Bearer ${this.secret}`;
        break;
      default:
        headers[this.auth.name || "Authorization"] = value;
        break;
    }
    return { headers, query };
  }

  /** Build `baseUrl + path` with the query string, including the secret for `query` auth. */
  url(path: string, query?: QueryInit): string {
    const { query: q } = this.apply({ query });
    const p = path.startsWith("/") ? path : `/${path}`;
    const qs = q.toString();
    return this.baseUrl + p + (qs ? `?${qs}` : "");
  }

  toJSON(): Omit<LeaseJSON, "secret"> & { secret: string } {
    return { ...this.raw, secret: "***" };
  }

  /** Redacted view for `console.log` and `util.inspect` in Node. */
  [INSPECT](): Record<string, unknown> {
    return {
      leaseId: this.leaseId,
      keyId: this.keyId,
      service: this.service,
      secret: "***",
      auth: this.auth,
      baseUrl: this.baseUrl,
      expiresAt: this._expiresAt,
      exclusive: this.exclusive,
      reported: this._reported,
    };
  }
}

/** Calls go to Acnologia's proxy, which injects a key and rotates on rate limits. */
export class Proxy {
  constructor(
    private readonly client: Acnologia,
    readonly service: string,
  ) {}

  /** Send any request. Returns the raw upstream Response, never throws on non-2xx. */
  request(method: string, path: string, init: RequestInit & { query?: QueryInit } = {}): Promise<Response> {
    const { query, ...rest } = init;
    const clean = path.replace(/^\/+/, "");
    const url = appendQuery(
      `${this.client.baseUrl}/proxy/${encodeURIComponent(this.service)}/${clean}`,
      query,
    );
    const headers = { ...toHeaderRecord(rest.headers as HeadersLike | undefined), ...this.client._authHeaders() };
    // Never follow redirects, so the app token is not replayed to another host. No default
    // timeout here because proxied bodies may stream; pass `signal` in init to bound a call.
    return this.client._fetch(url, { ...rest, method: method.toUpperCase(), headers, redirect: "manual" });
  }

  get(path: string, query?: QueryInit, init: RequestInit = {}): Promise<Response> {
    return this.request("GET", path, { ...init, query });
  }

  delete(path: string, query?: QueryInit, init: RequestInit = {}): Promise<Response> {
    return this.request("DELETE", path, { ...init, query });
  }

  post(path: string, body?: unknown, init: RequestInit & { query?: QueryInit } = {}): Promise<Response> {
    return this.withBody("POST", path, body, init);
  }

  put(path: string, body?: unknown, init: RequestInit & { query?: QueryInit } = {}): Promise<Response> {
    return this.withBody("PUT", path, body, init);
  }

  patch(path: string, body?: unknown, init: RequestInit & { query?: QueryInit } = {}): Promise<Response> {
    return this.withBody("PATCH", path, body, init);
  }

  private withBody(
    method: string,
    path: string,
    body: unknown,
    init: RequestInit & { query?: QueryInit },
  ): Promise<Response> {
    if (body === undefined || body === null) return this.request(method, path, init);
    if (isPassThroughBody(body)) {
      return this.request(method, path, { ...init, body: body as BodyInit });
    }
    const headers = toHeaderRecord(init.headers as HeadersLike | undefined);
    const hasType = Object.keys(headers).some((k) => k.toLowerCase() === "content-type");
    if (!hasType) headers["Content-Type"] = "application/json";
    return this.request(method, path, { ...init, headers, body: JSON.stringify(body) });
  }
}

/** Client for an Acnologia key pool. */
export class Acnologia {
  readonly baseUrl: string;
  private readonly token: string;
  private readonly fetchImpl: FetchLike | undefined;
  private readonly extraHeaders: Record<string, string>;
  private readonly timeoutMs: number;

  constructor(opts: AcnologiaOptions) {
    if (!opts || !opts.baseUrl) throw new TypeError("Acnologia: baseUrl is required");
    if (!opts.token) throw new TypeError("Acnologia: token is required");
    this.baseUrl = opts.baseUrl.replace(/\/+$/, "");
    this.token = opts.token;
    this.fetchImpl = opts.fetch;
    this.extraHeaders = opts.headers ?? {};
    this.timeoutMs = opts.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  }

  /** Redacted view for `console.log` and `util.inspect` in Node. */
  [INSPECT](): Record<string, unknown> {
    return { baseUrl: this.baseUrl, token: "***" };
  }

  /** @internal */
  _authHeaders(): Record<string, string> {
    return {
      ...this.extraHeaders,
      Authorization: `Bearer ${this.token}`,
      "X-Acnologia-Client": CLIENT_HEADER,
    };
  }

  /** @internal */
  _fetch(url: string, init: RequestInit): Promise<Response> {
    const f = this.fetchImpl ?? (typeof globalThis.fetch === "function" ? globalThis.fetch.bind(globalThis) : undefined);
    if (!f) {
      throw new Error("Acnologia: no fetch implementation found. Pass { fetch } to the constructor (Node 18+ has one built in).");
    }
    return f(url, init);
  }

  /** @internal */
  async _json<T>(method: string, path: string, body?: unknown): Promise<T> {
    const headers: Record<string, string> = { ...this._authHeaders(), Accept: "application/json" };
    const init: RequestInit = { method, headers };
    if (this.timeoutMs > 0 && typeof AbortSignal !== "undefined" && typeof AbortSignal.timeout === "function") {
      init.signal = AbortSignal.timeout(this.timeoutMs);
    }
    if (body !== undefined) {
      headers["Content-Type"] = "application/json";
      init.body = JSON.stringify(body);
    }
    const res = await this._fetch(this.baseUrl + path, init);
    const text = await res.text();
    let data: any = null;
    if (text) {
      try {
        data = JSON.parse(text);
      } catch {
        data = null;
      }
    }
    if (!res.ok) {
      const code: string = data?.error?.code ?? `http_${res.status}`;
      const message: string = data?.error?.message ?? (text || res.statusText || `HTTP ${res.status}`);
      const ra = data?.retry_after_sec;
      const retryAfter = typeof ra === "number" ? ra : null;
      if (code === "service_unavailable") {
        const header = Number(res.headers?.get?.("retry-after") ?? "");
        const fallback = Number.isFinite(header) && header > 0 ? header : null;
        throw new ServiceUnavailableError(message, retryAfter && retryAfter > 0 ? retryAfter : fallback, data, res.status);
      }
      if (code === "all_keys_busy") {
        throw new AllKeysBusyError(message, retryAfter, data);
      }
      if (code === "no_key_available") {
        throw new NoKeyAvailableError(message, retryAfter, data);
      }
      throw new AcnologiaError(message, res.status, code, data);
    }
    return data as T;
  }

  /** List the services this app may use. */
  async services(): Promise<Service[]> {
    const data = await this._json<{ services: any[] }>("GET", "/api/v1/services");
    return (data?.services ?? []).map((s) => ({
      slug: s.slug,
      name: s.name,
      provider: s.provider,
      category: s.category,
      available: Boolean(s.available),
      baseUrl: s.base_url,
      auth: s.auth,
    }));
  }

  /**
   * Lease a key. Throws `NoKeyAvailableError` when the pool is empty or cooling down, and
   * `AllKeysBusyError` (a subclass) when every usable key is at its concurrency limit and
   * `waitSec` ran out.
   *
   * Always report the lease: on services with a concurrency limit it holds one of the key's
   * slots until it is reported or its TTL expires.
   */
  async lease(service: string, opts: LeaseOptions = {}): Promise<Lease> {
    const body: Record<string, unknown> = { service };
    if (opts.excludeKeyIds !== undefined) body.exclude_key_ids = opts.excludeKeyIds;
    if (opts.ttlSec !== undefined) body.ttl_sec = opts.ttlSec;
    if (opts.waitSec !== undefined && opts.waitSec !== 0) {
      if (!(opts.waitSec > 0 && opts.waitSec <= 60)) {
        throw new RangeError("Acnologia: waitSec must be between 0 and 60");
      }
      body.wait_sec = Math.ceil(opts.waitSec);
    }
    if (opts.exclusive === true) body.exclusive = true;
    const raw = await this._json<LeaseJSON>("POST", "/api/v1/lease", body);
    return new Lease(this, raw);
  }

  /**
   * Lease a key and run `fn` with it. Throw `RateLimitedError`, `QuotaExhaustedError` or
   * `InvalidKeyError` from `fn` to report the key and retry with the next one.
   * Returns whatever `fn` returns. Reports `ok` on success and `error` on any other throw.
   *
   * Every lease is reported, whatever `fn` does. On services with a concurrency limit an
   * unreported lease keeps one of the key's slots busy until its TTL expires. `ttlSec`,
   * `waitSec` and `exclusive` are passed to every lease call.
   */
  async withKey<T>(service: string, fn: (lease: Lease) => T | Promise<T>, opts: WithKeyOptions = {}): Promise<T> {
    const maxAttempts = Math.max(1, opts.maxAttempts ?? 5);
    const tried: number[] = [];
    let lease = await this.lease(service, { ttlSec: opts.ttlSec, waitSec: opts.waitSec, exclusive: opts.exclusive });
    let lastErr: unknown;

    for (let attempt = 1; attempt <= maxAttempts; attempt++) {
      tried.push(lease.keyId);
      let result: T;
      try {
        result = await fn(lease);
      } catch (err) {
        if (err instanceof KeyRotationError) {
          lastErr = err;
          const rotate = attempt < maxAttempts;
          let next: Lease | null = null;
          try {
            next = await lease.report({
              outcome: err.outcome,
              statusCode: err.statusCode,
              retryAfterSec: "retryAfter" in err ? (err as { retryAfter?: number }).retryAfter : undefined,
              message: scrubMessage(err.message, lease.secret),
              rotate,
            });
          } catch (reportErr) {
            if (!rotate) throw err;
            throw reportErr;
          }
          if (!rotate) break;
          lease = next ?? (await this.lease(service, { excludeKeyIds: [...tried], ttlSec: opts.ttlSec, waitSec: opts.waitSec, exclusive: opts.exclusive }));
          continue;
        }
        try {
          await lease.report({
            outcome: "error",
            message: scrubMessage(err instanceof Error ? err.message : String(err), lease.secret),
          });
        } catch {
          // keep the caller's error
        }
        throw err;
      }
      await lease.report({ outcome: "ok" });
      return result;
    }
    throw lastErr;
  }

  /**
   * Extend an open lease and update its `expiresAt`. The same as `lease.renew(ttlSec)`.
   */
  renew(lease: Lease, ttlSec?: number): Promise<string> {
    return lease.renew(ttlSec);
  }

  /** Proxy helper: requests go through Acnologia, which injects a key and rotates for you. */
  proxy(service: string): Proxy {
    return new Proxy(this, service);
  }
}
