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
}

export type QueryInit =
  | URLSearchParams
  | Record<string, string | number | boolean | null | undefined>
  | Array<[string, string]>;

export type HeadersLike = Headers | Record<string, string> | Array<[string, string]>;
