// Mirrors the JSON shapes in docs/superpowers/specs/2026-11-01-cogito-key-pool-design.md

export type Role = "superadmin" | "admin" | "member";

export interface User {
  id: number;
  email: string;
  name: string;
  role: Role;
  disabled: boolean;
  created_at: string;
  last_login_at: string | null;
}

export interface KeyCounts {
  total: number;
  available: number;
  cooling: number;
  exhausted: number;
  invalid: number;
  disabled: number;
  backup: number;
}

export type Provider = "serpapi" | "elevenlabs" | "generic";
export type AuthType = "query" | "header" | "bearer";
export type Strategy = "round_robin" | "fill_first" | "least_used" | "most_quota" | "random";

export interface Service {
  id: number;
  slug: string;
  name: string;
  provider: Provider;
  category: string;
  description: string;
  base_url: string;
  auth_type: AuthType;
  auth_name: string;
  strategy: Strategy;
  cooldown_sec: number;
  max_attempts: number;
  icon: string;
  created_at: string;
  key_counts: KeyCounts;
  requests_24h: number;
}

export type ServiceInput = Pick<
  Service,
  | "slug"
  | "name"
  | "provider"
  | "category"
  | "description"
  | "base_url"
  | "auth_type"
  | "auth_name"
  | "strategy"
  | "cooldown_sec"
  | "max_attempts"
  | "icon"
>;

export type KeyStatus = "active" | "disabled" | "exhausted" | "invalid";
export type EffectiveStatus = KeyStatus | "cooling";
export type Tier = "primary" | "backup";

export interface Key {
  id: number;
  service_id: number;
  label: string;
  hint: string;
  tier: Tier;
  hidden: boolean;
  restricted: boolean;
  app_ids: number[];
  status: KeyStatus;
  effective_status: EffectiveStatus;
  weight: number;
  priority: number;
  cooldown_until: string | null;
  consecutive_fails: number;
  quota_limit: number | null;
  quota_remaining: number | null;
  quota_reset_at: string | null;
  usage_total: number;
  usage_today: number;
  last_used_at: string | null;
  last_error: string;
  last_checked_at: string | null;
  notes: string;
  created_at: string;
}

export interface NewKeyInput {
  label: string;
  secret: string;
  tier: Tier;
  hidden: boolean;
  restricted: boolean;
  weight: number;
  priority: number;
  notes: string;
  app_ids: number[];
}

export type KeyPatch = Partial<{
  label: string;
  secret: string;
  tier: Tier;
  hidden: boolean;
  restricted: boolean;
  app_ids: number[];
  status: KeyStatus;
  weight: number;
  priority: number;
  notes: string;
  quota_limit: number | null;
}>;

export type AppMode = "proxy" | "lease" | "both";

export interface App {
  id: number;
  name: string;
  description: string;
  token_prefix: string;
  allowed_services: string[];
  mode: AppMode;
  active: boolean;
  created_at: string;
  last_used_at: string | null;
}

export type AppInput = Pick<App, "name" | "description" | "mode" | "allowed_services">;

export interface AppWithToken {
  app: App;
  token: string;
}

export type EventKind =
  | "lease"
  | "proxy"
  | "ok"
  | "rate_limited"
  | "quota_exhausted"
  | "invalid"
  | "error"
  | "health";

export const EVENT_KINDS: EventKind[] = [
  "lease",
  "proxy",
  "ok",
  "rate_limited",
  "quota_exhausted",
  "invalid",
  "error",
  "health",
];

export interface CogitoEvent {
  id: number;
  ts: string;
  kind: EventKind;
  service_id: number | null;
  service_slug: string;
  key_id: number | null;
  key_label: string;
  app_id: number | null;
  app_name: string;
  status_code: number;
  latency_ms: number;
  detail: string;
}

export interface AuditEntry {
  id: number;
  ts: string;
  user_email: string;
  action: string;
  target: string;
  detail: string;
}

export interface ProviderTemplate {
  provider: string;
  name: string;
  category: string;
  description: string;
  base_url: string;
  auth_type: string;
  auth_name: string;
  icon: string;
  default_cooldown_sec: number;
  docs_url: string;
}

export interface AuthState {
  setup_required: boolean;
  user: User | null;
}

export interface Overview {
  services: number;
  keys: {
    total: number;
    active: number;
    cooling: number;
    exhausted: number;
    invalid: number;
    disabled: number;
    backup: number;
  };
  requests_24h: number;
  rate_limits_24h: number;
  recent_events: CogitoEvent[];
}
