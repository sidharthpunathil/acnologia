// The read only configuration the Settings page shows: what only the
// environment can change. Secrets come back as set or not set, never a value.
import { request } from "./api";

export interface EnvSetting {
  name: string;
  /** The variable has a value in the environment. */
  set: boolean;
  /** A secret: only `set` is known. */
  secret?: boolean;
  /** The value in effect: the default when the variable is unset. */
  value?: string;
}

export interface SettingsSummary {
  version: string;
  /** An access password locks the site, from the environment or the data dir. */
  access_gate_on: boolean;
  env: EnvSetting[];
}

/** Background key health checks (GET and PUT /api/admin/health/settings). */
export interface HealthSettings {
  /** In effect: "off", "15m", "1h", "6h", "7d" and so on. */
  interval: string;
  interval_sec: number;
  /** saved: set on this page. env: from ACNOLOGIA_HEALTH_INTERVAL (or its default). */
  source: "saved" | "env";
  saved: string;
  /** What the environment says, used when nothing is saved. */
  env: string;
  choices: string[];
}

export const settingsApi = {
  summary: () => request<SettingsSummary>("GET", "/api/admin/settings"),
  health: () => request<HealthSettings>("GET", "/api/admin/health/settings"),
  /** "" goes back to the environment's interval. */
  setHealth: (interval: string) => request<HealthSettings>("PUT", "/api/admin/health/settings", { interval }),
};

/** The entry for `name`, or a placeholder when the server did not send it. */
export function envOf(summary: SettingsSummary | null, name: string): EnvSetting | null {
  if (!summary) return null;
  return summary.env.find((e) => e.name === name) ?? { name, set: false };
}

/** The features that can run on their own model, in display order. */
export const MODEL_FEATURES = ["assistant", "builder", "fill", "summary", "refresh"] as const;
export type ModelFeature = (typeof MODEL_FEATURES)[number];

/** One model to pick from (GET /api/admin/ai/models). */
export interface ModelInfo {
  id: string;
  display_name: string;
  /** Input context window in tokens. */
  context_window: number;
  /** USD per million tokens; 0 when pricing_known is false. */
  input_price: number;
  output_price: number;
  pricing_known: boolean;
  /** In Acnologia's own table. */
  builtin: boolean;
  /** Cache prices, USD per million tokens. */
  cache_write_price?: number;
  cache_read_price?: number;
  /** builtin, manual (edited in Settings) or refresh (the price refresh). */
  price_source?: "builtin" | "manual" | "refresh";
  price_url?: string;
  /** YYYY-MM-DD the price was read or edited. */
  price_checked?: string;
  override?: PriceOverride;
}

/** A model's saved price, replacing the built in one. USD per million tokens. */
export interface PriceOverride {
  input: number;
  output: number;
  cache_write: number;
  cache_read: number;
  context_window?: number;
  source_url?: string;
  checked_at?: string;
  source?: string;
  updated_at?: string;
  updated_by?: string;
}

/** The saved choice. Empty means "use the default". */
export type ModelSettings = Record<"default" | ModelFeature, string> & {
  updated_at?: string;
  updated_by?: string;
};

/** Where an effective model comes from, strongest first. */
export type ModelSource = "override" | "saved" | "env" | "builtin";

export interface ModelChoice {
  model: string;
  source: ModelSource;
  context_window: number;
}

export interface ModelsState {
  settings: ModelSettings;
  effective: Record<"default" | ModelFeature, ModelChoice>;
  /** ACNOLOGIA_AI_MODEL, empty when unset. */
  env_model: string;
  builtin_model: string;
  /** The chat compaction threshold now in effect. */
  compact_tokens: number;
  compact_from_env: boolean;
  ai_enabled: boolean;
  /** Only a superadmin may save. */
  can_edit: boolean;
}

export interface ModelsCatalog extends ModelsState {
  models: ModelInfo[];
  /** "api" from Anthropic's Models API, "builtin" from Acnologia's table. */
  source: "api" | "builtin";
  /** Why the list is the built in table, when it is. */
  note?: string;
}

export type ModelsInput = Record<"default" | ModelFeature, string>;

export const modelsApi = {
  list: () => request<ModelsCatalog>("GET", "/api/admin/ai/models"),
  save: (input: ModelsInput) => request<ModelsState & { warnings: string[] | null }>("PUT", "/api/admin/ai/models", input),
  setPrice: (model: string, p: PriceOverride) => request<ModelsCatalog>("PUT", `/api/admin/ai/prices/${encodeURIComponent(model)}`, p),
  resetPrice: (model: string) => request<ModelsCatalog>("DELETE", `/api/admin/ai/prices/${encodeURIComponent(model)}`),
};
