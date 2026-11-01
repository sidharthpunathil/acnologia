export type AuthType = "query" | "header" | "bearer";

export interface AuthSpec {
  type: AuthType;
  name: string;
}

export type Outcome = "ok" | "rate_limited" | "quota_exhausted" | "invalid" | "error";

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
  /** Lease lifetime in seconds. Server default applies when omitted. */
  ttlSec?: number;
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
}

export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;

export interface CogitoOptions {
  /** Where Cogito runs, for example `http://localhost:8080`. */
  baseUrl: string;
  /** App token, `cog_...`. */
  token: string;
  /** Custom fetch. Defaults to `globalThis.fetch`. */
  fetch?: FetchLike;
  /** Extra headers sent with every request to Cogito. */
  headers?: Record<string, string>;
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
