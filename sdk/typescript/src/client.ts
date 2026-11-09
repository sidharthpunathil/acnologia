import {
  AllKeysBusyError,
  AcnologiaError,
  AppLimitReachedError,
  KeyRotationError,
  NoKeyAvailableError,
  ServiceUnavailableError,
  ShareUnavailableError,
} from "./errors.js";
import type {
  AuthSpec,
  AcnologiaOptions,
  Capability,
  CapabilityOptions,
  FetchLike,
  ForwardProxy,
  Guide,
  HeadersLike,
  LeaseJSON,
  LeaseOptions,
  LimitHit,
  Me,
  ProxyInfo,
  ProxyRequestInit,
  QueryInit,
  ReportOptions,
  Service,
  WithKeyOptions,
} from "./types.js";

export const VERSION = "0.2.0";
/** Where `forwardProxyEnv` points the CA variables by default. */
export const DEFAULT_CA_PATH = "acnologia-ca.pem";
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

function optNumber(v: string | null | undefined): number | null {
  if (v === null || v === undefined || v.trim() === "") return null;
  const n = Number(v);
  return Number.isFinite(n) ? n : null;
}

function nullableNumber(v: unknown): number | null {
  return typeof v === "number" && Number.isFinite(v) ? v : null;
}

function str(v: unknown): string {
  return typeof v === "string" ? v : v === null || v === undefined ? "" : String(v);
}

function limitHit(v: any): LimitHit | null {
  return v && typeof v === "object" ? { limit: str(v.limit), resetAt: str(v.reset_at) } : null;
}

/** Parse `X-Acnologia-Tried` (`svc=200, other=429`). */
function parseTried(v: string | null): Array<{ service: string; status: number }> {
  if (!v) return [];
  return v
    .split(",")
    .map((p) => p.trim())
    .filter(Boolean)
    .map((p) => {
      const i = p.indexOf("=");
      const service = (i < 0 ? p : p.slice(0, i)).trim();
      const status = i < 0 ? 0 : Number(p.slice(i + 1).trim());
      return { service, status: Number.isFinite(status) ? status : 0 };
    });
}

/** Read the Acnologia headers of a response from a proxy or capability call. */
export function proxyInfo(res: Response): ProxyInfo {
  const h = res.headers;
  return {
    keyId: optNumber(h.get("x-acnologia-key-id")),
    attempts: optNumber(h.get("x-acnologia-attempts")),
    service: h.get("x-acnologia-service"),
    cache: h.get("x-acnologia-cache"),
    replayed: (h.get("idempotent-replayed") ?? "").trim().toLowerCase() === "true",
    capability: h.get("x-acnologia-capability"),
    tried: parseTried(h.get("x-acnologia-tried")),
  };
}

/** Per call headers for proxy and capability requests. */
function callHeaders(opts: { idempotencyKey?: string; noCache?: boolean }): Record<string, string> {
  const out: Record<string, string> = {};
  if (opts.idempotencyKey) out["Idempotency-Key"] = opts.idempotencyKey;
  if (opts.noCache) out["Cache-Control"] = "no-cache";
  return out;
}

/** `proxyUrl` with user `app` and `token` as the password, percent escaped. */
function urlWithToken(proxyUrl: string, token: string): string {
  let u: URL;
  try {
    u = new URL(proxyUrl);
  } catch {
    throw new AcnologiaError(`bad forward proxy URL ${JSON.stringify(proxyUrl)}`, 200, "bad_response");
  }
  u.username = "app";
  u.password = encodeURIComponent(token);
  // URL adds a trailing slash to a bare origin; proxy URLs are written without one.
  const s = u.toString();
  return u.pathname === "/" && !proxyUrl.endsWith("/") && !u.search && !u.hash ? s.replace(/\/$/, "") : s;
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

/** Throw the typed error for a non-2xx answer from the consumer API. */
function throwFor(res: Response, text: string, data: any): never {
  const code: string = data?.error?.code ?? `http_${res.status}`;
  const message: string = data?.error?.message ?? (text || res.statusText || `HTTP ${res.status}`);
  const ra = data?.retry_after_sec;
  const header = Number(res.headers?.get?.("retry-after") ?? "");
  const fallback = Number.isFinite(header) && header > 0 ? header : null;
  const bodyRetry = typeof ra === "number" ? ra : null;
  const retryAfter = bodyRetry && bodyRetry > 0 ? bodyRetry : fallback;
  if (code === "service_unavailable") {
    throw new ServiceUnavailableError(message, retryAfter, data, res.status);
  }
  if (code === "all_keys_busy") {
    throw new AllKeysBusyError(message, bodyRetry, data);
  }
  if (code === "no_key_available") {
    throw new NoKeyAvailableError(message, bodyRetry, data);
  }
  if (code === "app_limit_reached") {
    throw new AppLimitReachedError(
      message,
      { limit: str(data?.limit), resetAt: typeof data?.reset_at === "string" ? data.reset_at : null, retryAfter, status: res.status },
      data,
    );
  }
  if (code === "share_revoked" || code === "share_paused" || code === "share_expired") {
    const st = (res.headers?.get?.("x-acnologia-share") ?? "").trim() || code.slice("share_".length);
    throw new ShareUnavailableError(message, st, code, data, res.status);
  }
  throw new AcnologiaError(message, res.status, code, data, retryAfter);
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

  /**
   * Send any request. Returns the raw upstream Response, never throws on non-2xx. Read the
   * Acnologia headers of the answer with `proxyInfo(res)`. `idempotencyKey` sends
   * `Idempotency-Key`, `noCache` sends `Cache-Control: no-cache`.
   */
  request(method: string, path: string, init: ProxyRequestInit = {}): Promise<Response> {
    const { query, idempotencyKey, noCache, ...rest } = init;
    const clean = path.replace(/^\/+/, "");
    const url = appendQuery(
      `${this.client.baseUrl}/proxy/${encodeURIComponent(this.service)}/${clean}`,
      query,
    );
    const headers = {
      ...callHeaders({ idempotencyKey, noCache }),
      ...toHeaderRecord(rest.headers as HeadersLike | undefined),
      ...this.client._authHeaders(),
    };
    // Never follow redirects, so the app token is not replayed to another host. No default
    // timeout here because proxied bodies may stream; pass `signal` in init to bound a call.
    return this.client._fetch(url, { ...rest, method: method.toUpperCase(), headers, redirect: "manual" });
  }

  get(path: string, query?: QueryInit, init: ProxyRequestInit = {}): Promise<Response> {
    return this.request("GET", path, { ...init, query });
  }

  delete(path: string, query?: QueryInit, init: ProxyRequestInit = {}): Promise<Response> {
    return this.request("DELETE", path, { ...init, query });
  }

  post(path: string, body?: unknown, init: ProxyRequestInit = {}): Promise<Response> {
    return this.withBody("POST", path, body, init);
  }

  put(path: string, body?: unknown, init: ProxyRequestInit = {}): Promise<Response> {
    return this.withBody("PUT", path, body, init);
  }

  patch(path: string, body?: unknown, init: ProxyRequestInit = {}): Promise<Response> {
    return this.withBody("PATCH", path, body, init);
  }

  private withBody(
    method: string,
    path: string,
    body: unknown,
    init: ProxyRequestInit,
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
  private readonly accessPassword: string | undefined;

  constructor(opts: AcnologiaOptions) {
    if (!opts || !opts.baseUrl) throw new TypeError("Acnologia: baseUrl is required");
    if (!opts.token) throw new TypeError("Acnologia: token is required");
    this.baseUrl = opts.baseUrl.replace(/\/+$/, "");
    this.token = opts.token;
    this.fetchImpl = opts.fetch;
    this.extraHeaders = opts.headers ?? {};
    this.timeoutMs = opts.timeoutMs ?? DEFAULT_TIMEOUT_MS;
    this.accessPassword = opts.accessPassword || undefined;
  }

  /** Redacted view for `console.log` and `util.inspect` in Node. */
  [INSPECT](): Record<string, unknown> {
    return { baseUrl: this.baseUrl, token: "***" };
  }

  /** @internal */
  _authHeaders(): Record<string, string> {
    return { ...this._publicHeaders(), Authorization: `Bearer ${this.token}` };
  }

  /** @internal Every header but the token. */
  _publicHeaders(): Record<string, string> {
    const h: Record<string, string> = { ...this.extraHeaders, "X-Acnologia-Client": CLIENT_HEADER };
    if (this.accessPassword) h["X-Acnologia-Access"] = this.accessPassword;
    return h;
  }

  private timeoutSignal(): AbortSignal | undefined {
    if (this.timeoutMs > 0 && typeof AbortSignal !== "undefined" && typeof AbortSignal.timeout === "function") {
      return AbortSignal.timeout(this.timeoutMs);
    }
    return undefined;
  }

  /** @internal GET `path` and return the body as text. With `auth` false the token is not sent. */
  async _text(path: string, accept: string, auth = true): Promise<string> {
    const headers = { ...(auth ? this._authHeaders() : this._publicHeaders()), Accept: accept };
    const init: RequestInit = { method: "GET", headers };
    const signal = this.timeoutSignal();
    if (signal) init.signal = signal;
    const res = await this._fetch(this.baseUrl + path, init);
    const text = await res.text();
    if (!res.ok) {
      let data: any = null;
      try {
        data = text ? JSON.parse(text) : null;
      } catch {
        data = null;
      }
      throwFor(res, text, data);
    }
    return text;
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
    const signal = this.timeoutSignal();
    if (signal) init.signal = signal;
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
    if (!res.ok) throwFor(res, text, data);
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
      guideUrl: str(s.guide_url),
    }));
  }

  /**
   * The token's own access: app, limits, usage, services and, for a share's token, the share.
   * A share that stopped still answers here, with its `status`.
   */
  async me(): Promise<Me> {
    const d = (await this._json<any>("GET", "/api/v1/me")) ?? {};
    const lim = d.limits ?? {};
    const use = d.usage ?? {};
    const sh = d.share;
    return {
      app: { id: Number(d.app?.id ?? 0), name: str(d.app?.name) },
      limits: {
        dailyRequests: nullableNumber(lim.daily_requests),
        monthlyRequests: nullableNumber(lim.monthly_requests),
        dailyBudget: nullableNumber(lim.daily_budget),
        monthlyBudget: nullableNumber(lim.monthly_budget),
      },
      usage: {
        todayRequests: Number(use.today_requests ?? 0),
        todayCost: Number(use.today_cost ?? 0),
        monthRequests: Number(use.month_requests ?? 0),
        monthCost: Number(use.month_cost ?? 0),
        lastUsedAt: typeof use.last_used_at === "string" ? use.last_used_at : null,
        limitHit: limitHit(use.limit_hit),
      },
      limitHit: limitHit(d.limit_hit),
      services: (Array.isArray(d.services) ? d.services : []).map((s: any) => ({
        slug: str(s.slug),
        name: str(s.name),
        provider: str(s.provider),
        category: str(s.category),
        available: Number(s.available ?? 0),
        logoUrl: typeof s.logo_url === "string" ? s.logo_url : null,
      })),
      share:
        sh && typeof sh === "object"
          ? {
              id: str(sh.id),
              name: str(sh.name),
              ownerName: str(sh.owner_name),
              recipient: str(sh.recipient),
              label: str(sh.label),
              status: str(sh.status),
              mode: str(sh.mode),
              endsAt: typeof sh.ends_at === "string" ? sh.ends_at : null,
            }
          : null,
    };
  }

  /** The usage guide of a service the token may use. */
  async guide(slug: string): Promise<Guide> {
    const d = (await this._json<any>("GET", `/api/v1/services/${encodeURIComponent(slug)}/guide`)) ?? {};
    return { markdown: str(d.markdown), generated: d.generated === true, guide: d.guide ?? null };
  }

  /** The usage guide of a service, as Markdown. */
  guideMarkdown(slug: string): Promise<string> {
    return this._text(`/api/v1/services/${encodeURIComponent(slug)}/guide`, "text/markdown");
  }

  /** The capabilities the app may call. Empty for a share's token. */
  async capabilities(): Promise<Capability[]> {
    const d = await this._json<{ capabilities?: any[] }>("GET", "/api/v1/capabilities");
    return (d?.capabilities ?? []).map((c) => ({
      slug: str(c.slug),
      name: str(c.name),
      description: str(c.description),
      strategy: str(c.strategy),
      params: c.params ?? null,
      routes: (Array.isArray(c.routes) ? c.routes : []).map((r: any) => ({ service: str(r.service), mapped: r.mapped === true })),
      url: str(c.url),
    }));
  }

  /**
   * Call a capability: `POST /proxy/{slug}` with `params` as the JSON body. Returns the raw
   * Response like a proxy call; read `capability`, `service` and `tried` with `proxyInfo`.
   */
  capability(slug: string, params: Record<string, unknown> = {}, opts: CapabilityOptions = {}): Promise<Response> {
    const q: Record<string, string> = {};
    if (opts.service) q._service = opts.service;
    if (opts.raw) q._raw = "1";
    const url = appendQuery(`${this.baseUrl}/proxy/${encodeURIComponent(slug)}`, q);
    const headers = { ...callHeaders(opts), ...this._authHeaders(), "Content-Type": "application/json" };
    const init: RequestInit = { method: "POST", headers, body: JSON.stringify(params ?? {}), redirect: "manual" };
    if (opts.signal) init.signal = opts.signal;
    return this._fetch(url, init);
  }

  /** How to use the forward proxy: its URL, the CA, and the hosts it puts keys on. */
  async forwardProxy(): Promise<ForwardProxy> {
    const d = (await this._json<any>("GET", "/api/v1/proxy")) ?? {};
    const env: Record<string, string> = {};
    for (const [k, v] of Object.entries(d.env ?? {})) env[k] = str(v);
    return {
      proxyUrl: str(d.proxy_url),
      proxyUrlWithToken: str(d.proxy_url_with_token),
      caUrl: str(d.ca_url),
      caFingerprint: str(d.ca_fingerprint),
      passthrough: d.passthrough === true,
      hosts: (Array.isArray(d.hosts) ? d.hosts : []).map((h: any) => ({
        host: str(h.host),
        service: str(h.service),
        name: str(h.name),
        ...(h.path ? { path: str(h.path) } : {}),
      })),
      env,
      serviceHeader: str(d.service_header),
    };
  }

  /** The forward proxy's CA certificate as PEM. The route is public, so the token is not sent. */
  forwardProxyCA(): Promise<string> {
    return this._text("/api/v1/proxy/ca.pem", "application/x-pem-file", false);
  }

  /**
   * The forward proxy URL with user `app` and this client's token as the password, such as
   * `http://app:acn_...@host:8888`. It contains the token: do not log it.
   */
  async forwardProxyURL(): Promise<string> {
    return urlWithToken((await this.forwardProxy()).proxyUrl, this.token);
  }

  /**
   * Environment that sends an app's HTTP through the forward proxy: `HTTPS_PROXY` and
   * `HTTP_PROXY` (the `forwardProxyURL`), `NODE_USE_ENV_PROXY=1`, and `NODE_EXTRA_CA_CERTS`,
   * `REQUESTS_CA_BUNDLE` and `SSL_CERT_FILE` set to `caPath`. Save `forwardProxyCA()` there
   * first. It contains the token: do not log it.
   */
  async forwardProxyEnv(caPath: string = DEFAULT_CA_PATH): Promise<Record<string, string>> {
    const url = await this.forwardProxyURL();
    return {
      HTTPS_PROXY: url,
      HTTP_PROXY: url,
      NODE_USE_ENV_PROXY: "1",
      NODE_EXTRA_CA_CERTS: caPath,
      REQUESTS_CA_BUNDLE: caPath,
      SSL_CERT_FILE: caPath,
    };
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

  /**
   * Proxy helper: requests go through Acnologia, which injects a key and rotates for you.
   * `service` may also be a service group or a capability slug.
   */
  proxy(service: string): Proxy {
    return new Proxy(this, service);
  }
}
