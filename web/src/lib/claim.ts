// Public endpoints for a person a share was made for: the claim link
// (preview, redeem) and the "my access" page (GET /api/v1/me with the
// person's token). Every field is optional and read through the normalizers
// below, so the pages still render when the server leaves something out.
import { ApiError, request } from "./api";
import type { AppLimits } from "./ops";
import type { ShareStatus } from "./sharing";

export interface ClaimService {
  slug: string;
  name?: string;
  provider?: string;
  logo_url?: string | null;
  /** Keys the share can use right now (my access page). */
  available?: number | null;
  /** How the service takes its key upstream: "bearer", "header" or "query", or an object naming it. */
  auth?: string | { type?: string; name?: string } | null;
  /** Example calls from the provider spec, used to build runnable snippets. */
  examples?: { method?: string; path?: string; query?: Record<string, string> | null }[] | null;
}

export type ClaimMode = "proxy" | "reveal";

/** What a claim link gives, before it is used. No secrets. */
export interface ClaimPreview {
  owner?: { name?: string; url?: string } | null;
  owner_name?: string;
  share?: {
    name?: string;
    label?: string;
    mode?: ClaimMode;
    services?: ClaimService[] | null;
    ends_at?: string | null;
    limits?: Partial<AppLimits> | null;
  } | null;
  name?: string;
  label?: string;
  mode?: ClaimMode;
  services?: ClaimService[] | null;
  ends_at?: string | null;
  limits?: Partial<AppLimits> | null;
  claim_expires_at?: string | null;
}

export interface RedeemedKey {
  service: string;
  label?: string;
  secret: string;
  provider?: string;
  base_url?: string;
  auth?: ClaimService["auth"];
}

export interface RedeemProxy {
  token: string;
  base_url?: string;
  services?: ClaimService[] | null;
  mcp_url?: string;
  limits?: Partial<AppLimits> | null;
  ends_at?: string | null;
}

export interface RedeemReveal {
  keys: RedeemedKey[];
  ends_at?: string | null;
}

export type RedeemResult = RedeemProxy | RedeemReveal;

export function isReveal(r: RedeemResult): r is RedeemReveal {
  return Array.isArray((r as RedeemReveal).keys) && !(r as RedeemProxy).token;
}

/** The normalized preview the claim page renders. */
export interface PreviewView {
  owner: string;
  ownerUrl: string;
  name: string;
  mode: ClaimMode;
  services: ClaimService[];
  endsAt: string | null;
  limits: AppLimits;
  linkExpiresAt: string | null;
}

export function limitsOf(l: Partial<AppLimits> | null | undefined): AppLimits {
  const n = (v: unknown) => (typeof v === "number" && Number.isFinite(v) ? v : null);
  return {
    daily_requests: n(l?.daily_requests),
    monthly_requests: n(l?.monthly_requests),
    daily_budget: n(l?.daily_budget),
    monthly_budget: n(l?.monthly_budget),
  };
}

export function hasLimits(l: AppLimits): boolean {
  return l.daily_requests != null || l.monthly_requests != null || l.daily_budget != null || l.monthly_budget != null;
}

export function previewView(p: ClaimPreview): PreviewView {
  const sh = p.share ?? {};
  return {
    owner: p.owner?.name || p.owner_name || "An Acnologia server",
    ownerUrl: p.owner?.url || "",
    name: sh.name || p.name || sh.label || p.label || "",
    mode: (sh.mode || p.mode) === "reveal" ? "reveal" : "proxy",
    services: (sh.services ?? p.services ?? []).filter((s) => s && s.slug),
    endsAt: sh.ends_at ?? p.ends_at ?? null,
    limits: limitsOf(sh.limits ?? p.limits),
    linkExpiresAt: p.claim_expires_at ?? null,
  };
}

/** The upstream auth type of a service, as a snippet builder wants it. */
export function authType(a: ClaimService["auth"]): { type: "bearer" | "header" | "query"; name: string } {
  const t = typeof a === "string" ? a : a?.type;
  const name = typeof a === "object" && a?.name ? a.name : "";
  return { type: t === "query" ? "query" : t === "header" ? "header" : "bearer", name };
}

export type ClaimErrorKind = "already_claimed" | "expired" | "invalid" | "rate_limited" | "other";

/** 409 already claimed, 410 expired, 404 invalid, 429 rate limited. */
export function claimErrorKind(e: unknown): ClaimErrorKind {
  if (!(e instanceof ApiError)) return "other";
  if (e.status === 409 || e.code === "already_claimed") return "already_claimed";
  if (e.status === 410 || e.code === "claim_expired") return "expired";
  if (e.status === 404 || e.code === "claim_invalid") return "invalid";
  if (e.status === 429) return "rate_limited";
  return "other";
}

export const claim = {
  preview: (code: string) => request<ClaimPreview>("POST", "/api/claim/v1/preview", { code }),
  redeem: (code: string) => request<RedeemResult>("POST", "/api/claim/v1/redeem", { code }),
};

// ---- my access ----

export type MeStatus = ShareStatus;

export interface MeResponse {
  app?: { id?: number; name?: string } | null;
  share?: {
    id?: string;
    name?: string;
    label?: string;
    mode?: string;
    status?: MeStatus;
    ends_at?: string | null;
    end_action?: string;
    owner?: { name?: string; url?: string } | null;
    owner_name?: string;
  } | null;
  name?: string;
  owner?: { name?: string; url?: string } | null;
  owner_name?: string;
  status?: MeStatus;
  mode?: string;
  ends_at?: string | null;
  services?: ClaimService[] | null;
  limits?: Partial<AppLimits> | null;
  usage?: {
    today_requests?: number;
    month_requests?: number;
    today_cost?: number;
    month_cost?: number;
    last_used_at?: string | null;
  } | null;
  limit_hit?: { limit: string; reset_at: string } | null;
}

export interface MeView {
  name: string;
  owner: string;
  status: MeStatus;
  mode: string;
  endsAt: string | null;
  services: ClaimService[];
  limits: AppLimits;
  usage: { today_requests: number; month_requests: number; today_cost: number; month_cost: number };
  lastUsedAt: string | null;
  limitHit: { limit: string; reset_at: string } | null;
  isShare: boolean;
}

export function meView(m: MeResponse): MeView {
  const sh = m.share ?? null;
  const u = m.usage ?? {};
  const num = (v: unknown) => (typeof v === "number" && Number.isFinite(v) ? v : 0);
  return {
    name: sh?.name || sh?.label || m.name || m.app?.name || "Your access",
    owner: sh?.owner?.name || sh?.owner_name || m.owner?.name || m.owner_name || "",
    status: sh?.status || m.status || "active",
    mode: sh?.mode || m.mode || "proxy",
    endsAt: sh?.ends_at ?? m.ends_at ?? null,
    services: (m.services ?? []).filter((s) => s && s.slug),
    limits: limitsOf(m.limits),
    usage: { today_requests: num(u.today_requests), month_requests: num(u.month_requests), today_cost: num(u.today_cost), month_cost: num(u.month_cost) },
    lastUsedAt: u.last_used_at ?? null,
    limitHit: m.limit_hit ?? null,
    isShare: !!sh,
  };
}

/**
 * GET /api/v1/me with the person's token. Not through `request`: no cookies,
 * and a 401 here means a bad token, not a signed out dashboard.
 */
export async function fetchMe(token: string): Promise<MeResponse> {
  let res: Response;
  try {
    res = await fetch("/api/v1/me", { method: "GET", credentials: "omit", headers: { Accept: "application/json", Authorization: `Bearer ${token}` } });
  } catch {
    throw new ApiError(0, "network", "Cannot reach the server.");
  }
  const text = await res.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = null;
  }
  if (!res.ok) {
    const err = (data as { error?: { code?: string; message?: string } | string } | null)?.error;
    const code = typeof err === "object" && err?.code ? err.code : `http_${res.status}`;
    const message = typeof err === "string" ? err : err?.message || res.statusText || `Request failed (${res.status})`;
    const retry = Number(res.headers.get("Retry-After"));
    throw new ApiError(res.status, code, message, Number.isFinite(retry) && retry > 0 ? retry : undefined, data);
  }
  return (data ?? {}) as MeResponse;
}

// ---- remembered token ----

const ME_KEY = "acn.me.token";

export function readSavedToken(): string {
  try {
    return window.localStorage.getItem(ME_KEY) ?? "";
  } catch {
    return "";
  }
}

export function saveToken(token: string) {
  try {
    window.localStorage.setItem(ME_KEY, token);
  } catch {
    // Storage blocked: the token works for this visit only.
  }
}

export function forgetToken() {
  try {
    window.localStorage.removeItem(ME_KEY);
  } catch {
    // Nothing stored, nothing to forget.
  }
}
