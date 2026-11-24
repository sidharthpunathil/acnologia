import type { TokenLimits } from "./types";

/** Helpers for token limits, tag budgets and pinned tags in the app panels. */

export const LIMIT_FIELDS = ["daily_requests", "monthly_requests", "daily_budget", "monthly_budget"] as const;
export type LimitField = (typeof LIMIT_FIELDS)[number];
/** Raw text per limit; empty means no limit. */
export type LimitsDraft = Record<LimitField, string>;

export const LIMIT_FIELD_LABEL: Record<LimitField, string> = {
  daily_requests: "Daily requests",
  monthly_requests: "Monthly requests",
  daily_budget: "Daily budget (USD)",
  monthly_budget: "Monthly budget (USD)",
};

export const NO_TOKEN_LIMITS: TokenLimits = { daily_requests: null, monthly_requests: null, daily_budget: null, monthly_budget: null };
export const BLANK_LIMITS_DRAFT: LimitsDraft = { daily_requests: "", monthly_requests: "", daily_budget: "", monthly_budget: "" };

export function limitsDraft(l: Partial<TokenLimits> | null | undefined): LimitsDraft {
  const out = { ...BLANK_LIMITS_DRAFT };
  for (const k of LIMIT_FIELDS) {
    const v = l?.[k];
    out[k] = v === null || v === undefined ? "" : String(v);
  }
  return out;
}

/** Parses a draft, or returns what is wrong with it. */
export function parseLimitsDraft(d: LimitsDraft): TokenLimits | string {
  const out: TokenLimits = { ...NO_TOKEN_LIMITS };
  for (const k of LIMIT_FIELDS) {
    const t = d[k].trim();
    if (t === "") continue;
    const n = Number(t);
    const whole = k.endsWith("_requests");
    if (!Number.isFinite(n) || n < 0 || (whole && !Number.isInteger(n))) {
      return `${LIMIT_FIELD_LABEL[k]} must be ${whole ? "a whole number" : "an amount"}, 0 or more, or empty.`;
    }
    out[k] = n;
  }
  return out;
}

export const anyLimit = (l: Partial<TokenLimits> | null | undefined) => !!l && LIMIT_FIELDS.some((k) => l[k] !== null && l[k] !== undefined);

/** "100 a day, $2 a day" */
export function limitsText(l: Partial<TokenLimits> | null | undefined): string {
  if (!l) return "";
  const parts: string[] = [];
  if (l.daily_requests != null) parts.push(`${l.daily_requests} a day`);
  if (l.monthly_requests != null) parts.push(`${l.monthly_requests} a month`);
  if (l.daily_budget != null) parts.push(`$${l.daily_budget} a day`);
  if (l.monthly_budget != null) parts.push(`$${l.monthly_budget} a month`);
  return parts.join(", ");
}

const TAG_KEY = /^[a-z0-9_.-]{1,32}$/;
const TAG_VALUE = /^[A-Za-z0-9_.:@/+-]{1,64}$/;

export const validTagKey = (k: string) => TAG_KEY.test(k);

/** Reads "customer=cust_A, workspace=w1", or returns what is wrong with it. */
export function parseTags(text: string): Record<string, string> | string {
  const out: Record<string, string> = {};
  const parts = text
    .split(",")
    .map((p) => p.trim())
    .filter(Boolean);
  if (parts.length > 5) return "At most 5 tags.";
  for (const p of parts) {
    const i = p.indexOf("=");
    const k = (i < 0 ? p : p.slice(0, i)).trim().toLowerCase();
    const v = i < 0 ? "" : p.slice(i + 1).trim();
    if (!TAG_KEY.test(k) || !TAG_VALUE.test(v)) return `Write tags as key=value, such as customer=cust_A.`;
    if (k in out) return `${k} appears twice.`;
    out[k] = v;
  }
  return out;
}

export const tagsText = (t: Record<string, string> | null | undefined) =>
  Object.keys(t ?? {})
    .sort()
    .map((k) => `${k}=${t![k]}`)
    .join(", ");
