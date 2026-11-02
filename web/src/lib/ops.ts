// Types and endpoints for the operations features: webhooks, egress proxies,
// service groups, admin API tokens, app limits and usage, circuit breakers
// and the response cache.
import { request } from "./api";

export type WebhookFormat = "json" | "slack" | "discord" | "telegram";

export interface Webhook {
  id: number;
  name: string;
  /** Scheme and host only: the path is the secret for Slack, Discord and Telegram. */
  url_hint: string;
  format: WebhookFormat;
  /** Empty means every event. */
  events: string[];
  active: boolean;
  created_at: string;
  last_sent_at: string | null;
  last_status: number;
  last_error: string;
}

export interface WebhookEventType {
  type: string;
  description: string;
}

export interface WebhookInput {
  name?: string;
  url?: string;
  format?: WebhookFormat;
  events?: string[];
  active?: boolean;
}

export interface EgressProxy {
  id: number;
  name: string;
  /** URL without the password. */
  url_hint: string;
  region: string;
  notes: string;
  created_at: string;
  last_checked_at: string | null;
  last_ip: string;
  last_error: string;
  services: number;
  keys: number;
}

export interface EgressInput {
  name?: string;
  url?: string;
  region?: string;
  notes?: string;
}

export interface ServiceGroup {
  id: number;
  slug: string;
  name: string;
  description: string;
  /** Service slugs in fallback order. */
  members: string[];
  created_at: string;
}

export type TokenScope = "read" | "write" | "admin";

/** A pending `acn login`, as the authorize page sees it. */
export interface CliDeviceRequest {
  user_code: string;
  client: string;
  hostname: string;
  os: string;
  created_at: string;
  expires_at: string;
  state: "pending" | "approving" | "approved" | "denied";
}

export interface AdminToken {
  id: number;
  name: string;
  token_prefix: string;
  scope: TokenScope;
  user_id: number;
  user_email: string;
  created_at: string;
  expires_at: string | null;
  last_used_at: string | null;
}

export interface AppLimits {
  daily_requests: number | null;
  monthly_requests: number | null;
  daily_budget: number | null;
  monthly_budget: number | null;
}

export interface AppUsage {
  today_requests: number;
  today_cost: number;
  month_requests: number;
  month_cost: number;
  today_cache_hits: number;
  month_cache_hits: number;
}

export interface UsageDay {
  day: string;
  requests: number;
  cost: number;
  cache_hits: number;
}

export interface AppUsageReport {
  usage: AppUsage;
  limits: AppLimits;
  limit_hit: { limit: string; reset_at: string } | null;
  history: UsageDay[];
}

export type BreakerState = "closed" | "open" | "half_open";

export interface BreakerReport {
  status: {
    state: BreakerState;
    failures: number;
    opened_at: string | null;
    retry_after_sec: number;
    reason: string;
  };
  threshold: number;
  window_sec: number;
  reset_sec: number;
}

export interface CacheStats {
  entries: number;
  bytes: number;
  max_bytes: number;
  hits: number;
  misses: number;
}

export const ops = {
  // Webhooks
  listWebhooks: () =>
    request<{ webhooks: Webhook[]; event_types: WebhookEventType[]; formats: WebhookFormat[] }>("GET", "/api/admin/webhooks"),
  createWebhook: (body: WebhookInput) =>
    request<{ webhook: Webhook; signing_secret?: string }>("POST", "/api/admin/webhooks", body),
  updateWebhook: (id: number, body: WebhookInput) => request<Webhook>("PATCH", `/api/admin/webhooks/${id}`, body),
  deleteWebhook: (id: number) => request<unknown>("DELETE", `/api/admin/webhooks/${id}`),
  testWebhook: (id: number) => request<{ ok: boolean; status: number; error: string }>("POST", `/api/admin/webhooks/${id}/test`),

  // Egress proxies
  listEgress: () => request<{ proxies: EgressProxy[] }>("GET", "/api/admin/egress"),
  createEgress: (body: EgressInput) => request<EgressProxy>("POST", "/api/admin/egress", body),
  updateEgress: (id: number, body: EgressInput) => request<EgressProxy>("PATCH", `/api/admin/egress/${id}`, body),
  deleteEgress: (id: number) => request<unknown>("DELETE", `/api/admin/egress/${id}`),
  testEgress: (id: number) => request<{ ok: boolean; ip: string; error: string }>("POST", `/api/admin/egress/${id}/test`),

  // Service groups
  listGroups: () => request<{ groups: ServiceGroup[] }>("GET", "/api/admin/groups"),
  createGroup: (body: Omit<ServiceGroup, "id" | "created_at">) => request<ServiceGroup>("POST", "/api/admin/groups", body),
  updateGroup: (id: number, body: Partial<Pick<ServiceGroup, "name" | "description" | "members">>) =>
    request<ServiceGroup>("PATCH", `/api/admin/groups/${id}`, body),
  deleteGroup: (id: number) => request<unknown>("DELETE", `/api/admin/groups/${id}`),

  // Admin API tokens
  listTokens: (all = false) => request<{ tokens: AdminToken[] }>("GET", `/api/admin/tokens${all ? "?all=1" : ""}`),
  createToken: (body: { name: string; scope: TokenScope; expires_in_days: number }) =>
    request<{ token: string; info: AdminToken }>("POST", "/api/admin/tokens", body),
  deleteToken: (id: number) => request<unknown>("DELETE", `/api/admin/tokens/${id}`),

  // CLI sign in (acn login): look up, approve or deny a code
  cliLookup: (userCode: string) =>
    request<{ request: CliDeviceRequest }>("GET", `/api/cli/device/lookup?user_code=${encodeURIComponent(userCode)}`),
  cliApprove: (body: { user_code: string; scope: TokenScope; label?: string }) =>
    request<{ request: CliDeviceRequest; token: AdminToken }>("POST", "/api/cli/device/approve", body),
  cliDeny: (userCode: string) => request<{ request: CliDeviceRequest }>("POST", "/api/cli/device/deny", { user_code: userCode }),

  // Apps
  appUsage: (id: number, days = 30) => request<AppUsageReport>("GET", `/api/admin/apps/${id}/usage?days=${days}`),
  setAppLimits: (id: number, limits: AppLimits) => request<unknown>("PATCH", `/api/admin/apps/${id}`, { limits }),

  // Services
  breaker: (serviceId: number) => request<BreakerReport>("GET", `/api/admin/services/${serviceId}/breaker`),
  resetBreaker: (serviceId: number) => request<BreakerReport>("POST", `/api/admin/services/${serviceId}/breaker/reset`),
  purgeCache: (serviceId: number) => request<{ purged: number }>("POST", `/api/admin/services/${serviceId}/cache/purge`),
  cacheStats: () => request<CacheStats>("GET", "/api/admin/cache"),
};

export const usd = (n: number | null | undefined, digits = 2) =>
  n === null || n === undefined ? "" : `$${n.toFixed(n !== 0 && Math.abs(n) < 0.01 ? 4 : digits)}`;
