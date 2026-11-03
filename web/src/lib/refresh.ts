// The provider refresh: re-reading providers' offers (and Claude's own model
// prices) on a schedule, by hand, or for one provider.
import { request } from "./api";
import type { SpecOffer } from "./types";

export type RefreshSchedule = "off" | "weekly" | "biweekly" | "monthly" | "days";
export type RefreshScope = "with_keys" | "with_services" | "all";
export type RefreshApply = "auto" | "review";

export interface RefreshSettings {
  schedule: RefreshSchedule;
  every_days: number;
  /** UTC hour a scheduled run starts at. */
  hour: number;
  scope: RefreshScope;
  include: string[];
  exclude: string[];
  include_peer: boolean;
  claude_prices: boolean;
  apply: RefreshApply;
  max_run_usd: number;
  max_month_usd: number;
  max_providers: number;
  stale_days: number;
  updated_at?: string;
  updated_by?: string;
}

export type RefreshTrigger = "schedule" | "manual" | "provider";
export type RunStatus = "running" | "done" | "stopped" | "failed";
export type ItemStatus = "pending" | "running" | "unchanged" | "applied" | "review" | "failed" | "skipped";

export interface RefreshRun {
  id: number;
  trigger: RefreshTrigger;
  status: RunStatus;
  started_by: string;
  model: string;
  skipped: { slug: string; reason: string }[];
  stop_reason: string;
  started_at: string;
  finished_at: string | null;
  checked: number;
  changed: number;
  reviews: number;
  failed: number;
  pending: number;
  providers: number;
  cost_usd: number;
}

export interface FieldChange {
  field: string;
  from: string;
  to: string;
}

export interface OffersDiff {
  added: SpecOffer[];
  removed: SpecOffer[];
  changed: { name: string; fields: FieldChange[]; source_url: string }[];
}

export interface RefreshItem {
  id: number;
  run_id: number;
  slug: string;
  status: ItemStatus;
  progress: string;
  detail: string;
  diff?: OffersDiff;
  version: number | null;
  review_id: number | null;
  cost_usd: number;
  started_at: string | null;
  finished_at: string | null;
}

export interface RefreshReview {
  id: number;
  slug: string;
  run_id: number;
  status: "pending" | "applied" | "dismissed" | "superseded";
  base_version: number;
  diff: OffersDiff;
  summary: string;
  reason: string;
  created_at: string;
  decided_at: string | null;
  decided_by: string;
  version: number | null;
}

export interface RefreshState {
  settings: RefreshSettings;
  defaults: RefreshSettings;
  ai_enabled: boolean;
  model: { model: string; source: string; input_price: number; output_price: number; pricing_known: boolean };
  next_run: string | null;
  last_run: RefreshRun | null;
  running: RefreshRun | null;
  month_spend_usd: number;
  pending_reviews: number;
  can_edit: boolean;
}

/** A provider's refresh state, on the provider list and detail. */
export interface ProviderRefreshState {
  checked_at: string;
  trigger: RefreshTrigger;
  outcome: ItemStatus;
  run_id: number | null;
  changed_at: string | null;
  changed_version: number | null;
  pending_review: number | null;
}

/** The item and review slug of Claude's own model prices. */
export const PRICES_TARGET = "_claude_prices";

const base = "/api/admin/providers/refresh";

export const refreshApi = {
  state: () => request<RefreshState>("GET", `${base}/settings`),
  save: (s: RefreshSettings) => request<RefreshState>("PUT", `${base}/settings`, s),
  runNow: () => request<{ run: RefreshRun }>("POST", `${base}/run`),
  runs: () => request<{ runs: RefreshRun[] }>("GET", `${base}/runs`),
  run: (id: number) => request<{ run: RefreshRun; items: RefreshItem[] }>("GET", `${base}/runs/${id}`),
  reviews: () => request<{ reviews: RefreshReview[] }>("GET", `${base}/reviews`),
  apply: (id: number) => request<{ review: RefreshReview }>("POST", `${base}/reviews/${id}/apply`),
  dismiss: (id: number) => request<{ review: RefreshReview }>("POST", `${base}/reviews/${id}/dismiss`),
  checkProvider: (slug: string) => request<{ run: RefreshRun }>("POST", `/api/admin/providers/${encodeURIComponent(slug)}/refresh`),
};

export const TRIGGER_LABEL: Record<RefreshTrigger, string> = {
  schedule: "scheduled refresh",
  manual: "a refresh run by hand",
  provider: "Check now",
};

/** The name of a run item: a provider slug, or Claude's own prices. */
export function targetName(slug: string, names?: Record<string, string>): string {
  if (slug === PRICES_TARGET) return "Claude model prices";
  return names?.[slug] ?? slug;
}

export function usd(n: number): string {
  if (n === 0) return "$0";
  if (n < 0.01) return "<$0.01";
  return `$${n.toFixed(2)}`;
}

export function diffCount(d: OffersDiff | undefined): number {
  if (!d) return 0;
  return (d.added?.length ?? 0) + (d.removed?.length ?? 0) + (d.changed?.length ?? 0);
}
