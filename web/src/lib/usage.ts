import { request } from "./api";

/** Sums of Claude API calls for one feature, model, user or day. */
export interface AiUsageRow {
  key: string;
  user_id?: number;
  calls: number;
  input_tokens: number;
  cache_write_tokens: number;
  cache_read_tokens: number;
  output_tokens: number;
  web_searches: number;
  cost_usd: number;
}

/** Provider spend through the pool for one service, app or day. */
export interface ProviderUsageRow {
  key: string;
  id?: number;
  name?: string;
  requests: number;
  cost_usd: number;
  cache_hits: number;
}

export interface UsageTotals {
  ai_cost_usd: number;
  ai_calls: number;
  provider_cost_usd: number;
  requests: number;
  cache_hits: number;
}

export type UsageWindow = "today" | "7d" | "30d" | "range";

export interface AiPrice {
  input: number;
  cache_write: number;
  cache_read: number;
  output: number;
  web_search: number;
}

export interface UsageReport {
  days: number;
  /** First UTC day of the range, YYYY-MM-DD. */
  since: string;
  /** Today, UTC. */
  until: string;
  totals: Record<UsageWindow, UsageTotals>;
  ai: {
    by_feature: AiUsageRow[];
    by_model: AiUsageRow[];
    by_user: AiUsageRow[];
    /** One row per day of the range, oldest first. */
    by_day: AiUsageRow[];
  };
  provider: {
    /** One row per day of the range, oldest first. */
    by_day: ProviderUsageRow[];
    by_service: ProviderUsageRow[];
    by_app: ProviderUsageRow[];
  };
  ai_enabled: boolean;
  ai_model: string;
  ai_price: AiPrice;
  /** How far back provider spend goes; 0 keeps events forever. */
  event_retention_days: number;
}

export function fetchUsage(days: number): Promise<UsageReport> {
  return request<UsageReport>("GET", `/api/admin/usage?days=${days}`);
}

const usd2 = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", minimumFractionDigits: 2, maximumFractionDigits: 2 });

/** "$12.34", "$0.0042" for sub-cent amounts, "$0.00" for none. */
export function usd(n: number): string {
  if (!n) return "$0.00";
  if (Math.abs(n) >= 0.01) return usd2.format(n);
  return "$" + n.toPrecision(2).replace(/0+$/, "");
}

/** Short axis label: "$0", "$1.50", "$12", "$1.2K". */
export function usdAxis(n: number): string {
  if (n === 0) return "$0";
  if (n >= 1000) return "$" + new Intl.NumberFormat("en-US", { notation: "compact", maximumFractionDigits: 1 }).format(n);
  if (n >= 10) return "$" + Math.round(n);
  if (n >= 0.1) return "$" + n.toFixed(2).replace(/\.?0+$/, "");
  return usd(n);
}
