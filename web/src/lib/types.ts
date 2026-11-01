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
  /** Keys currently at their concurrency limit. */
  busy?: number;
}

/** Provider slug. Built-ins are serpapi, elevenlabs and generic; AI or manual providers add more. */
export type ProviderSlug = string;
export type AuthType = "query" | "header" | "bearer";
export type Strategy = "round_robin" | "fill_first" | "least_used" | "most_quota" | "random";

export interface Service {
  id: number;
  slug: string;
  name: string;
  provider: ProviderSlug;
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
  /** Own logo, or the provider logo when the service has none. Null means lettermark. */
  logo_url: string | null;
  /** 0 means unlimited concurrent requests per key. */
  max_concurrency: number;
  queue_timeout_sec: number;
  in_flight: number;
  /** False after a non-superadmin changed where keys are sent; hidden keys pause until a superadmin saves. */
  target_trusted: boolean;
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
> &
  Partial<Pick<Service, "max_concurrency" | "queue_timeout_sec">>;

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
  /** Per key override. Null means the service default applies. */
  max_concurrency: number | null;
  in_flight: number;
  /** Last successful request or passed health check. */
  last_ok_at: string | null;
  validity: Validity;
  /** Plain words, such as "last worked 3m ago". */
  validity_reason: string;
  /** From the provider spec, such as "searches". May be empty. */
  quota_unit: string;
}

export type Validity = "valid" | "invalid" | "unknown";

/** A key on the all keys page, with the service it belongs to. */
export type KeyRow = Key & {
  service_slug: string;
  service_name: string;
  service_logo_url: string | null;
  has_health_check: boolean;
};

export interface KeySummary {
  total: number;
  valid: number;
  invalid: number;
  unknown: number;
  low_quota: number;
  unused_7d: number;
}

export interface DayActivity {
  day: string;
  requests: number;
  errors: number;
  rate_limited: number;
}

export interface KeyInsights {
  key: Key;
  service: { id: number; slug: string; name: string; logo_url: string | null; max_concurrency: number };
  has_health_check: boolean;
  quota_unit: string;
  events: CogitoEvent[];
  daily: DayActivity[];
}

export interface CheckSummary {
  checked: number;
  valid: number;
  /** Keys that work but have no quota left. */
  exhausted: number;
  invalid: number;
  failed: number;
  skipped: number;
  message: string;
  keys: Key[];
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
  /** -1 clears the override (use the service default). */
  max_concurrency: number;
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
  /** Lets the app lease hidden keys. Only a superadmin can turn it on. */
  lease_hidden: boolean;
}

export type AppInput = Pick<App, "name" | "description" | "mode" | "allowed_services"> & { lease_hidden?: boolean };

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
    busy?: number;
  };
  in_flight?: number;
  requests_24h: number;
  rate_limits_24h: number;
  recent_events: CogitoEvent[];
}

// Providers as data. Mirrors docs/superpowers/specs/2026-11-01-providers-as-data-and-ai-builder.md

export type Outcome = "ok" | "rate_limited" | "quota_exhausted" | "invalid" | "error";
export const OUTCOMES: Outcome[] = ["ok", "rate_limited", "quota_exhausted", "invalid", "error"];
export type RetrySource = "header" | "fixed" | "next_hour" | "body_json" | "none";

export interface SpecMatch {
  status?: number[];
  body_contains?: string[];
  body_regex?: string;
  json_path?: string;
  json_equals?: string;
  header_present?: string;
}

export interface SpecRule {
  name: string;
  match: SpecMatch;
  outcome: Outcome;
  retry_after?: { source: RetrySource; value?: string };
}

export interface QuotaWindow {
  limit: string;
  used: string;
  period: string;
}

export interface QuotaPaths {
  limit?: string[];
  remaining?: string[];
  used?: string[];
  reset_at?: string;
  reset_format?: string;
  window?: QuotaWindow;
}

export interface SpecHealth {
  method: string;
  path: string;
  query?: Record<string, string>;
  invalid?: SpecMatch;
  ok_anyway?: SpecMatch;
  quota?: QuotaPaths;
}

export interface SpecExample {
  name: string;
  method: string;
  path: string;
  query?: Record<string, string>;
  body?: unknown;
  safe: boolean;
}

export interface SpecFixture {
  name: string;
  status: number;
  headers?: Record<string, string>;
  body: string;
  expect: Outcome;
}

export interface ProviderSpec {
  base_url: string;
  auth: { type: AuthType; name: string; prefix: string };
  defaults: {
    strategy: Strategy;
    cooldown_sec: number;
    max_attempts: number;
    max_concurrency: number;
    queue_timeout_sec: number;
  };
  quota?: { unit: string; counts_per_request: boolean };
  rules: SpecRule[];
  use_standard_rules: boolean;
  health: SpecHealth | null;
  examples: SpecExample[];
  tests: SpecFixture[];
}

export type ProviderStatus = "active" | "disabled";
export type ProviderSource = "builtin" | "manual" | "ai";

export interface Provider {
  slug: string;
  name: string;
  category: string;
  description: string;
  website: string;
  docs_url: string;
  color: string;
  logo_url: string | null;
  status: ProviderStatus;
  builtin: boolean;
  source: ProviderSource;
  version: number;
  updated_at: string;
  created_at: string;
  services_count: number;
  spec: ProviderSpec;
  base_url: string;
  auth_type: string;
  auth_name: string;
  default_cooldown_sec: number;
  default_max_concurrency: number;
  default_queue_timeout_sec: number;
}

export type ProviderInput = Omit<
  Provider,
  | "logo_url"
  | "builtin"
  | "source"
  | "version"
  | "updated_at"
  | "created_at"
  | "services_count"
  | "base_url"
  | "auth_type"
  | "auth_name"
  | "default_cooldown_sec"
  | "default_max_concurrency"
  | "default_queue_timeout_sec"
>;

export interface ProviderVersion {
  version: number;
  created_at: string;
  created_by: string;
  note: string;
  source: string;
}

export type ProviderDetail = Provider & { versions: ProviderVersion[] };

export interface TestResult {
  name: string;
  expect: string;
  got: string;
  ok: boolean;
  detail: string;
}

export interface TestReport {
  passed: number;
  failed: number;
  results: TestResult[];
}

export interface LiveReport {
  ok: boolean;
  health: {
    ok: boolean;
    status: number;
    detail: string;
    quota_limit: number | null;
    quota_remaining: number | null;
  };
  example: { ran: boolean; ok: boolean; status: number; outcome: string; detail: string };
}

export interface ProviderSaveResult {
  provider: Provider;
  report: TestReport;
}

export interface ValidateResult {
  errors: string[];
  report: TestReport;
}

export interface AiStatus {
  enabled: boolean;
  model: string;
  reason: string;
}

export type AgentRunStatus = "queued" | "running" | "testing" | "needs_review" | "published" | "failed" | "cancelled";

export interface AgentRun {
  id: number;
  kind: string;
  status: AgentRunStatus;
  title: string;
  provider_slug: string;
  input: {
    context: string;
    urls: string[];
    github: string;
    auto_publish: boolean;
    create_service: boolean;
    has_test_key: boolean;
  };
  draft: ProviderInput | null;
  report: TestReport | null;
  live: LiveReport | null;
  error: string;
  summary: string;
  service_id: number | null;
  usage: { input_tokens: number; output_tokens: number; web_searches: number; turns: number; cost_usd: number };
  created_by: string;
  created_at: string;
  finished_at: string | null;
}

export type AgentStepType = "info" | "thinking" | "search" | "fetch" | "tool" | "result" | "error" | "done";

export interface AgentStep {
  seq: number;
  ts: string;
  type: AgentStepType;
  title: string;
  detail: string;
}

export interface AgentRunInput {
  kind: "create_provider" | "update_provider";
  provider_slug?: string;
  context: string;
  urls: string[];
  github: string;
  test_key?: string;
  auto_publish: boolean;
  create_service: boolean;
}

export interface LogoUpload {
  data_base64: string;
  mime: string;
}

export type AssistForm = "app" | "service" | "provider";

export interface AssistResult {
  fields: Record<string, unknown>;
  notes: string;
}

export interface AiRules {
  rules: string;
  updated_at: string | null;
  updated_by: string;
}
