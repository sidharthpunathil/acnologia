export type AuthType = "query" | "header" | "bearer";

export interface AuthSpec {
  type: AuthType;
  name: string;
  /**
   * Put in front of the secret for every type when non empty. For `bearer` it replaces the
   * default `Bearer ` prefix. Absent or empty means the raw secret (and `Bearer ` for bearer).
   */
  prefix?: string;
}

export type Outcome = "ok" | "rate_limited" | "busy" | "quota_exhausted" | "invalid" | "banned" | "error";

export interface Service {
  slug: string;
  name: string;
  provider: string;
  category: string;
  available: boolean;
  baseUrl: string;
  auth: AuthSpec;
  /** Where the service's usage guide is (see `guide`). */
  guideUrl: string;
}

/** The limit an app reached and when it resets (RFC 3339). */
export interface LimitHit {
  /** `daily_requests`, `monthly_requests`, `daily_budget` or `monthly_budget`. */
  limit: string;
  resetAt: string;
}

/** The token's own access, from `GET /api/v1/me`. */
export interface Me {
  app: { id: number; name: string };
  /** Each null when the app has no such limit. */
  limits: {
    dailyRequests: number | null;
    monthlyRequests: number | null;
    dailyBudget: number | null;
    monthlyBudget: number | null;
  };
  usage: {
    todayRequests: number;
    todayCost: number;
    monthRequests: number;
    monthCost: number;
    lastUsedAt: string | null;
    limitHit: LimitHit | null;
  };
  limitHit: LimitHit | null;
  services: MeService[];
  /** Null for an ordinary app. */
  share: MeShare | null;
}

export interface MeService {
  slug: string;
  name: string;
  provider: string;
  category: string;
  /** Keys free right now for a share's token; 1 or 0 for an ordinary app. */
  available: number;
  /** The logo as a `data:` URI, or null. */
  logoUrl: string | null;
}

export interface MeShare {
  id: string;
  name: string;
  ownerName: string;
  recipient: string;
  label: string;
  /** `active`, `paused`, `expired` or `revoked`. */
  status: string;
  mode: string;
  endsAt: string | null;
}

/** A service's usage guide. */
export interface Guide {
  markdown: string;
  /** True when nobody wrote the guide and it was built from the provider spec. */
  generated: boolean;
  /** The structured guide, as plain JSON. */
  guide: unknown;
}

/** One call served by whichever provider can. */
export interface Capability {
  slug: string;
  name: string;
  description: string;
  strategy: string;
  /** The params, as plain JSON. */
  params: unknown;
  routes: Array<{ service: string; mapped: boolean }>;
  url: string;
}

export interface CapabilityOptions {
  /** Use only that provider's route (`?_service=`). */
  service?: string;
  /** Return the provider's own body, unmapped (`?_raw=1`). */
  raw?: boolean;
  /** Sent as `Idempotency-Key`. */
  idempotencyKey?: string;
  /** Send `Cache-Control: no-cache`. */
  noCache?: boolean;
  /** Bound the call. */
  signal?: AbortSignal;
}

export interface ForwardHost {
  host: string;
  service: string;
  name: string;
  /** Base path under the host, when the service has one. */
  path?: string;
}

/**
 * How to use the forward proxy, from `GET /api/v1/proxy`. It never holds the token:
 * `proxyUrlWithToken` and `env` carry an `<app token>` placeholder.
 */
export interface ForwardProxy {
  proxyUrl: string;
  proxyUrlWithToken: string;
  caUrl: string;
  caFingerprint: string;
  passthrough: boolean;
  hosts: ForwardHost[];
  env: Record<string, string>;
  serviceHeader: string;
}

/** Per call options for proxy requests, on top of `RequestInit`. */
export interface ProxyOptions {
  query?: QueryInit;
  /** Sent as `Idempotency-Key`, so a retried POST is answered from the first result. */
  idempotencyKey?: string;
  /** Send `Cache-Control: no-cache`, so the server skips its response cache. */
  noCache?: boolean;
}

export type ProxyRequestInit = RequestInit & ProxyOptions;

/** What Acnologia says about a proxied answer in its headers. */
export interface ProxyInfo {
  /** `X-Acnologia-Key-Id`; null when absent, as for a share's token. */
  keyId: number | null;
  /** `X-Acnologia-Attempts`. */
  attempts: number | null;
  /** `X-Acnologia-Service`: who answered. */
  service: string | null;
  /** `X-Acnologia-Cache`: `hit`, `miss` and so on, when caching applies. */
  cache: string | null;
  /** True on `Idempotent-Replayed: true`. */
  replayed: boolean;
  /** `X-Acnologia-Capability`. */
  capability: string | null;
  /** `X-Acnologia-Tried`, parsed: every route a capability call tried. */
  tried: Array<{ service: string; status: number }>;
}

export interface LeaseOptions {
  /** Key ids the server must not hand out (for example keys you already tried). */
  excludeKeyIds?: number[];
  /** Lease lifetime in seconds. Server default applies when omitted (120 on services with a concurrency limit, 300 otherwise). */
  ttlSec?: number;
  /**
   * Seconds (0 to 60) the server may wait for a free slot when every usable key is at its
   * concurrency limit. Omit or 0 to fail fast with `AllKeysBusyError`.
   */
  waitSec?: number;
  /**
   * Reserve the key for this lease only: no other lease or proxy call gets it until the lease
   * is reported or expires. Sent only when true.
   */
  exclusive?: boolean;
}

export interface ReportOptions {
  outcome: Outcome;
  statusCode?: number;
  retryAfterSec?: number;
  message?: string;
  /** Ask the server for a replacement lease in the same call. */
  rotate?: boolean;
}

export interface WithKeyOptions {
  /** Total keys to try before giving up. Default 5. */
  maxAttempts?: number;
  ttlSec?: number;
  /** Passed to every lease call. See `LeaseOptions.waitSec`. */
  waitSec?: number;
  /** Passed to every lease call. See `LeaseOptions.exclusive`. */
  exclusive?: boolean;
}

export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;

export interface AcnologiaOptions {
  /** Where Acnologia runs, for example `http://localhost:8080`. */
  baseUrl: string;
  /** App token, `acn_...`. */
  token: string;
  /** Custom fetch. Defaults to `globalThis.fetch`. */
  fetch?: FetchLike;
  /** Extra headers sent with every request to Acnologia. */
  headers?: Record<string, string>;
  /**
   * Timeout for API calls (lease, report, services) in milliseconds. Default 75000, longer than
   * the 60s maximum lease wait. 0 disables it. Proxy calls are not bounded by it; pass your own
   * `signal` to bound those.
   */
  timeoutMs?: number;
  /** Sent as `X-Acnologia-Access` on every request, for a deploy behind the access password. */
  accessPassword?: string;
}

/** Raw lease JSON as returned by the server. */
export interface LeaseJSON {
  lease_id: string;
  key_id: number;
  service: string;
  secret: string;
  auth: AuthSpec;
  base_url: string;
  expires_at: string;
  /** True when the key is reserved for this lease only. */
  exclusive?: boolean;
}

export type QueryInit =
  | URLSearchParams
  | Record<string, string | number | boolean | null | undefined>
  | Array<[string, string]>;

export type HeadersLike = Headers | Record<string, string> | Array<[string, string]>;
