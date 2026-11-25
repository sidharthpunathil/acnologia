import type { ProviderRefreshState } from "./refresh";
// Mirrors the JSON shapes in docs/superpowers/specs/2026-11-01-acnologia-key-pool-design.md

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
  /** Keys whose account the provider suspended. */
  banned?: number;
  disabled: number;
  backup: number;
  /** Keys currently at their concurrency limit. */
  busy?: number;
  /** Of the keys above, how many come from the key source service. */
  shared?: number;
}

/** Provider slug. Built-ins are serpapi, elevenlabs and generic; AI or manual providers add more. */
export type ProviderSlug = string;
export type AuthType = "query" | "header" | "bearer" | "basic" | "fields" | "oauth2";

/** One part of a credential made of several values (a login and a password). */
export interface CredentialPart {
  key: string;
  label: string;
  /** Not secret on its own (a login, an app id): shown as plain text. */
  public?: boolean;
}

/** Where one part of a fields credential goes. */
export interface AuthField {
  key: string;
  label?: string;
  in: "header" | "query";
  name: string;
  prefix?: string;
  public?: boolean;
}
export type Strategy = "round_robin" | "fill_first" | "least_used" | "most_quota" | "random";

export interface Service {
  id: number;
  slug: string;
  name: string;
  provider: ProviderSlug;
  category: string;
  description: string;
  base_url: string;
  /** Optional upstream Streamable HTTP MCP endpoint (absolute URL or path relative to base_url). */
  mcp_url?: string;
  auth_type: AuthType;
  auth_name: string;
  /** The inputs one key takes when the credential has several parts. */
  credential_parts?: CredentialPart[];
  strategy: Strategy;
  cooldown_sec: number;
  max_attempts: number;
  icon: string;
  created_at: string;
  key_counts: KeyCounts;
  requests_24h: number;
  /** Own logo, or the provider logo when the service has none. Null means lettermark. */
  logo_url: string | null;
  /** The provider's logo when the service shows its own, for a small badge. */
  provider_logo_url?: string | null;
  /** 0 means unlimited concurrent requests per key. */
  max_concurrency: number;
  queue_timeout_sec: number;
  in_flight: number;
  /** False after a non-superadmin changed where keys are sent; hidden keys pause until a superadmin saves. */
  target_trusted: boolean;
  /** Egress proxy for this service's traffic; null connects directly. */
  egress_id?: number | null;
  /** Response cache TTL override; null uses the provider default. */
  cache_ttl_sec?: number | null;
  /** USD per request override; null uses the provider default. */
  cost_per_request?: number | null;
  /** Another service of the same account whose keys this one uses as well as its own. */
  key_source_id?: number | null;
  key_source_slug?: string;
  key_source_name?: string;
  /** Set when another Acnologia shares this service; it proxies through that server. */
  peer_link_id?: number | null;
  /** The name of the server that shares it. */
  peer_name?: string;
  /** The owner's provider for a shared service: its name, logo, docs and examples describe it. */
  peer_provider?: string;
  /** Provider rotation: "" is off; with it on, services that serve the same endpoint share requests. */
  rotation?: Rotation;
}

export type Rotation = "" | "fallback" | "round_robin";

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
  Partial<Pick<Service, "mcp_url" | "max_concurrency" | "queue_timeout_sec" | "egress_id" | "cache_ttl_sec" | "cost_per_request" | "target_trusted">> & { rotation?: Rotation | "off" };

export type KeyStatus = "active" | "disabled" | "exhausted" | "invalid" | "banned";
export type EffectiveStatus = KeyStatus | "cooling";
export type Tier = "primary" | "backup";

/** Services one account's key serves (same site, same auth placement), shown as one card. */
export interface ServiceFamily {
  key: string;
  name: string;
  main_id: number;
  service_ids: number[];
  /** Distinct secrets across the family. */
  keys: number;
  available: number;
  /** Members that could use the main service's keys but do not yet. */
  unlinked: number;
}

/** A key in another service with the same secret. */
export interface KeyTwin {
  key_id: number;
  label: string;
  service_id: number;
  service_slug: string;
  service_name: string;
}

/** GET /api/admin/keys/shared. */
export interface SharedKeysReport {
  duplicates: KeyTwin[][];
  suggestions: { service: string; service_name: string; source: string; source_name: string; source_keys: number }[];
}

/** One host the forward proxy opens and puts a key on. */
export interface ForwardProxyHost {
  host: string;
  service: string;
  name: string;
  /** The base path under the host, when the service has one. */
  path?: string;
}

/** GET /api/admin/forward-proxy: how apps use Acnologia as their HTTPS_PROXY. */
export interface ForwardProxySetup {
  /** The proxy address without a token, such as http://keys.example.com:8888. */
  proxy_url: string;
  proxy_url_with_token: string;
  /** Where apps download the CA certificate to trust. */
  ca_url: string;
  ca_fingerprint: string;
  /** HTTPS to hosts that are not services is tunneled untouched. */
  passthrough: boolean;
  hosts: ForwardProxyHost[];
  env: Record<string, string>;
  /** The header that names a service or group, X-Acnologia-Service. */
  service_header: string;
  /** The dedicated listener, such as :8888, or empty for the main port only. */
  listen_addr?: string;
  main_port?: boolean;
  /** ACNOLOGIA_FORWARD_PROXY_URL is set. */
  configured_url?: boolean;
}

export interface Key {
  /** Keys with the same secret in other services. */
  twins?: KeyTwin[];
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
  /** Egress proxy for this key; null falls back to the service's. */
  egress_id?: number | null;
  /** Who the key belongs to. Members get empty strings, since this is personal data. */
  account_name?: string;
  account_email?: string;
  account_phone?: string;
  /** Any other identifier, such as a username, account ID or workspace. */
  account_ref?: string;
  /**
   * TODO(server): the spec's owner side exposure record has no key field yet.
   * Expected as `handed_over: [{share_id, peer_id, peer_name, live, wiped_at}]`
   * on key responses. Until then HandoverChip derives it from the shares list
   * (share.handover.keys).
   */
  handed_over?: KeyHandover[] | null;
  /** People who were shown this key through a reveal share. Read from the shares list when missing. */
  revealed_to?: KeyReveal[] | null;
}

/** One reveal share that showed this key to a person. */
export interface KeyReveal {
  share_id: string;
  share_name?: string;
  /** The person's name or label. */
  label?: string;
  revealed_at?: string | null;
}

/** One share that handed this key to another server. */
export interface KeyHandover {
  share_id: string;
  peer_id: string;
  peer_name: string;
  /** True while the other server holds it (share active, not wiped). */
  live: boolean;
  wiped_at?: string | null;
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
  events: AcnologiaEvent[];
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
  /** The key. A multi part credential sends parts instead. */
  secret?: string;
  /** The parts of a multi part credential by part key. */
  parts?: Record<string, string>;
  tier: Tier;
  hidden: boolean;
  restricted: boolean;
  weight: number;
  priority: number;
  notes: string;
  app_ids: number[];
  account_name?: string;
  account_email?: string;
  account_phone?: string;
  account_ref?: string;
}

export type KeyPatch = Partial<{
  /** Egress proxy id; 0 falls back to the service's proxy. */
  egress_id: number;
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
  account_name: string;
  account_email: string;
  account_phone: string;
  account_ref: string;
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
  /** Request and budget caps; null fields mean no limit. */
  limits?: {
    daily_requests: number | null;
    monthly_requests: number | null;
    daily_budget: number | null;
    monthly_budget: number | null;
  };
  /** Tokens that still work: neither revoked nor expired. */
  token_count?: number;
}

/** One of an app's tokens. Only its prefix hint is ever shown again. */
export interface AppToken {
  id: number;
  app_id: number;
  name: string;
  /** First 8 characters plus "...". */
  prefix: string;
  /** When true, allows only GET/HEAD proxy calls and read-only MCP tools; blocks key leasing. */
  read_only: boolean;
  created_at: string;
  created_by: number | null;
  created_by_email: string;
  last_used_at: string | null;
  expires_at: string | null;
  revoked_at: string | null;
  /** The token's own limits; an empty field falls back to the app's per-token default. */
  limits?: TokenLimits;
  /** Caller tags every call with this token carries. */
  pinned_tags?: Record<string, string>;
}

/** Daily and monthly request counts and USD budgets; null means none. */
export interface TokenLimits {
  daily_requests: number | null;
  monthly_requests: number | null;
  daily_budget: number | null;
  monthly_budget: number | null;
}

/** Caps each value of one caller tag key on its own. */
export interface TagBudget extends TokenLimits {
  key: string;
}

/** Blocks an app's matching requests with 403 denied_by_rule. */
export interface DenyRule {
  id: number;
  app_id: number;
  /** "*" or an HTTP method. */
  method: string;
  /** A service, group or capability slug, or empty. */
  service: string;
  /** An upstream hostname, or empty. */
  host: string;
  path_prefix: string;
  note: string;
  created_at: string;
}

export interface DenyRuleInput {
  method: string;
  service?: string;
  host?: string;
  path_prefix: string;
  note?: string;
}

export interface AppControls {
  token_limits: TokenLimits;
  tag_budgets: TagBudget[];
  deny_rules: DenyRule[];
}

export interface TagUsageRow {
  key: string;
  value: string;
  today_requests: number;
  today_cost: number;
  month_requests: number;
  month_cost: number;
  requests: number;
  cost: number;
}

export interface TagUsageReport {
  days: number;
  tags: TagUsageRow[];
  tag_budgets: TagBudget[];
  truncated: boolean;
}

export interface SpendCeiling {
  ceiling_usd: number;
  source: "env" | "saved";
  env_usd: number;
  saved_usd?: number;
  spent_today_usd: number;
  reset_at: string;
}

export type AppInput =Pick<App, "name" | "description" | "mode" | "allowed_services"> & { lease_hidden?: boolean };

export interface AppWithToken {
  app: App;
  token: string;
  /** The token's record: "Default" for a new app. */
  app_token?: AppToken;
}

export type EventKind =
  | "lease"
  | "proxy"
  | "ok"
  | "rate_limited"
  | "quota_exhausted"
  | "invalid"
  | "error"
  | "health"
  | "busy"
  | "banned"
  | "cache_hit"
  | "service_down"
  | "service_up";

export const EVENT_KINDS: EventKind[] = [
  "lease",
  "proxy",
  "ok",
  "rate_limited",
  "quota_exhausted",
  "invalid",
  "error",
  "health",
  "busy",
  "banned",
  "cache_hit",
  "service_down",
  "service_up",
];

export interface AcnologiaEvent {
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
    banned?: number;
    disabled: number;
    backup: number;
    busy?: number;
  };
  in_flight?: number;
  requests_24h: number;
  rate_limits_24h: number;
  cache_hits_24h?: number;
  spend_24h?: number;
  recent_events: AcnologiaEvent[];
}

// Providers as data. Mirrors docs/superpowers/specs/2026-11-01-providers-as-data-and-ai-builder.md

export type Outcome = "ok" | "rate_limited" | "busy" | "quota_exhausted" | "invalid" | "banned" | "error";
export const OUTCOMES: Outcome[] = ["ok", "rate_limited", "busy", "quota_exhausted", "invalid", "banned", "error"];
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

/** Response headers that carry quota; index picks an entry of a comma list. */
export interface QuotaHeaders {
  limit?: string;
  remaining?: string;
  reset?: string;
  reset_format?: "" | "seconds" | "unix" | "unix_ms" | "iso8601" | "http_date";
  index?: number;
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

export type OfferPer = "second" | "minute" | "hour" | "day" | "month";

/** A published cap: count units per window. */
export interface OfferLimit {
  count: number;
  per: OfferPer;
  unit?: string;
}

/** One priced product or endpoint of a provider, from its pricing page. */
export interface SpecOffer {
  name: string;
  /** Which requests it covers; absent means every request. */
  match?: { method?: string; path?: string; examples?: string[] };
  price?: { amount: number; currency?: string; unit: string; per_request?: number };
  free?: boolean;
  limits?: OfferLimit[];
  concurrency?: number;
  notes?: string;
  source_url?: string;
  checked_at?: string;
}

/** One parameter of a guide endpoint. */
export interface GuideParam {
  name: string;
  in: "query" | "path" | "body" | "header";
  type?: string;
  required?: boolean;
  description: string;
}

/** A call worth knowing, with its parameters and an example body. */
export interface GuideEndpoint {
  /** The product the endpoint belongs to, for providers with many. */
  group?: string;
  name: string;
  method: string;
  path: string;
  description: string;
  params?: GuideParam[];
  body?: unknown;
  response?: string;
}

/** A provider's usage guide, written for AI agents and developers. Part of the spec. */
export interface Guide {
  summary: string;
  endpoints?: GuideEndpoint[];
  notes?: string[];
  markdown?: string;
}

/** The rendered guide for a provider or one service, as structured data. */
export interface GuideDoc {
  provider: string;
  name: string;
  service?: string;
  summary: string;
  generated: boolean;
  proxy_base: string;
  docs_url?: string;
  website?: string;
  endpoints: GuideEndpoint[];
  examples: { name: string; method: string; path: string; query?: Record<string, string>; body?: unknown; curl: string }[];
  limits: string[];
  errors: string[];
  notes: string[];
  markdown?: string;
  /** Measured per endpoint over the last 7 days, where there are enough calls. Service guides only. */
  evidence?: {
    method: string;
    path: string;
    calls: number;
    success_rate: number;
    p50_ms: number;
    p95_ms: number;
    last_ok?: string;
    last_fail?: string;
  }[];
  /** Sample answers from live checks and catalog verifies, cut to 4KB and scrubbed of keys. */
  responses?: { example: string; method: string; path: string; status: number; content_type?: string; body: string; verified_at: string }[];
}

/** GET /api/admin/providers/{slug}/guide and /api/admin/services/{id}/guide. */
export interface GuideResponse {
  markdown: string;
  guide: GuideDoc;
  /** True when the provider has no written guide and this one comes from its examples and offers. */
  generated: boolean;
}

/**
 * POST /api/admin/providers/{slug}/guide/generate. Either an AI run to
 * follow (as `run`, or the run itself with `id` and `status`), or the
 * provider already updated. Every field is optional until the shape settles.
 */
export interface GenerateGuideResult {
  run?: { id: number; status: string };
  id?: number;
  status?: string;
  provider?: { slug: string; version: number };
  /** False when the model could not read the docs and nothing was saved. */
  updated?: boolean;
  summary?: string;
  cost_usd?: number;
}

export interface ProviderSpec {
  base_url: string;
  auth: { type: AuthType; name: string; prefix: string; fields?: AuthField[] };
  defaults: {
    strategy: Strategy;
    cooldown_sec: number;
    max_attempts: number;
    max_concurrency: number;
    queue_timeout_sec: number;
    cache_ttl_sec?: number;
    breaker_threshold?: number;
    breaker_window_sec?: number;
    breaker_reset_sec?: number;
  };
  quota?: {
    unit: string;
    counts_per_request: boolean;
    headers?: QuotaHeaders;
    cost_per_request?: number;
    free_tier?: string;
  };
  offers?: SpecOffer[];
  rules: SpecRule[];
  use_standard_rules: boolean;
  health: SpecHealth | null;
  examples: SpecExample[];
  tests: SpecFixture[];
  /** Usage guide for apps and AI agents; optional. */
  guide?: Guide;
  /** The provider's other API hosts, picked with a "/~name" path prefix. */
  hosts?: SpecHost[];
  /** Optional upstream Streamable HTTP MCP endpoint for this provider. */
  mcp?: SpecMCP;
}

/** Upstream Streamable HTTP MCP server endpoint declared by a provider. */
export interface SpecMCP {
  url: string;
  transport?: string;
  headers?: Record<string, string>;
}

/** One more API host of a provider that serves each product on its own host. */
export interface SpecHost {
  name: string;
  url: string;
  title?: string;
  /** Category, such as "Social & Video". */
  group?: string;
  description?: string;
  docs_url?: string;
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
  /** Visible keys across every service built on this provider. */
  keys_count: number;
  spec: ProviderSpec;
  base_url: string;
  auth_type: string;
  auth_name: string;
  default_cooldown_sec: number;
  default_max_concurrency: number;
  default_queue_timeout_sec: number;
  /** When the offers refresh last checked and changed it; absent when never. */
  refresh?: ProviderRefreshState;
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
  | "keys_count"
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
  /** The effective default model. */
  model: string;
  /** The effective model of each feature (assistant, builder, fill, summary, refresh). */
  models?: Record<string, string>;
  reason: string;
}

export type AgentRunStatus = "queued" | "running" | "testing" | "needs_review" | "published" | "failed" | "cancelled";

export interface AgentRun {
  id: number;
  kind: string;
  status: AgentRunStatus;
  title: string;
  /** One or two sentences on what the run builds. Empty until the server writes one. */
  description?: string;
  provider_slug: string;
  /** The Claude model the run works with, fixed when it was queued. Empty for older runs. */
  model?: string;
  input: {
    context: string;
    urls: string[];
    github: string;
    auto_publish: boolean;
    create_service: boolean;
    has_test_key: boolean;
    /** How many test keys came with the run (test_key plus test_keys). */
    test_key_count?: number;
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
  /** Every pasted key. The first is used for the live check; all are added to a created service. */
  test_keys?: string[];
  auto_publish: boolean;
  create_service: boolean;
}

export interface LogoUpload {
  data_base64: string;
  mime: string;
}

export type AssistForm = "app" | "service" | "provider" | "guide";

export interface AssistResult {
  fields: Record<string, unknown>;
  notes: string;
}

export interface AiRules {
  rules: string;
  updated_at: string | null;
  updated_by: string;
}

export interface ChatThread {
  id: number;
  title: string;
  created_at: string;
  updated_at: string;
}

export interface ChatAi {
  enabled: boolean;
  model: string;
  /** Why the assistant is off, in plain words. Empty when enabled. */
  reason: string;
}

export type ChatItemKind = "user" | "assistant" | "tool" | "confirm" | "error" | "build" | "ask" | "compact" | "stopped";

export type ChatPendingStatus = "waiting" | "approved" | "cancelled" | "expired";

export interface ChatItem {
  id: string;
  kind: ChatItemKind;
  text?: string;
  tool?: string;
  /** Tool input, already redacted by the server. */
  input?: Record<string, unknown>;
  ok: boolean;
  summary?: string;
  /** JSON text of the tool result, cut to 4000 characters. */
  detail?: string;
  /** One time app token. Only in the response that created it. */
  secret?: string;
  pending?: { id: string; status: ChatPendingStatus };
  /** An in app path, such as /services/3. */
  link?: string;
  /** Keys found in a user message. Their secrets stay in the server's vault; text shows only hints. */
  keys?: number;
  /**
   * The keys in a user message, as label and hint only. When present, `text`
   * is just the prose: key lines removed and blank runs collapsed.
   */
  key_list?: { label: string; hint: string }[];
  /** Images a user message carried; load them with api.chatImageURL. */
  images?: { id: string; media_type: string }[];
  /** Candidate logos a find_logo step found. */
  logos?: ChatLogo[];
  /** Data URLs of the images on a message not yet saved, shown until the server copy arrives. */
  local_images?: string[];
  /** A provider build the turn follows (kind build). */
  build?: ChatBuild;
  /** A structured question for the user (kind ask). */
  ask?: ChatAsk;
  /** Rich result of a tool call. */
  card?: ChatCard;
  /** Services, keys, apps and providers the tool result names, with ids, for links. */
  entities?: ChatEntity[];
  /** Short follow ups the model proposed at the end of a turn. */
  suggestions?: string[];
  ts: string;
}

export interface ChatEntity {
  type: "service" | "key" | "app" | "provider";
  id?: number;
  slug?: string;
  name: string;
}

export interface ChatBuild {
  run_id: number;
  title: string;
  status: AgentRunStatus | string;
  /** 0 research, 1 spec, 2 tests, 3 live check, 4 publish. */
  phase: number;
  step: string;
  started_at: string;
  finished_at?: string;
  provider_slug?: string;
  service_id?: number;
  error?: string;
}

export interface ChatAskRow {
  ref: string;
  /** "...c21e" */
  hint: string;
  suggestion: string;
}

/** A candidate logo find_logo found, shown by its id. */
export interface ChatLogo {
  id: string;
  /** "linkedin.com · 180px" */
  label: string;
}

export interface ChatAsk {
  id: string;
  kind: "names" | "choice" | "logo";
  question: string;
  rows?: ChatAskRow[];
  choices?: string[];
  logos?: ChatLogo[];
  status: "waiting" | "answered" | "expired";
  answer?: string;
}

export interface ChatCardKey {
  id: number;
  label: string;
  hint: string;
  status: string;
}

export interface ChatCard {
  type: "keys" | "service" | "provider";
  service?: ChatEntity;
  keys?: ChatCardKey[];
  key_total?: number;
  skipped?: number;
  provider?: { slug: string; name: string; version: number; tests_passed: number; tests_failed: number };
  extra?: Record<string, unknown>;
}

/** A turn runs on the server; while active the client polls the thread. */
export interface ChatTurn {
  active: boolean;
  /** What the assistant is doing right now, such as "Running the tests". */
  step?: string;
  started_at?: string;
  /** Messages sent with Send now that the turn has not read yet. */
  steered?: number;
}

/** Body for POST /api/admin/chat/threads/{id}/answer. */
export interface ChatAnswer {
  ask_id: string;
  /** names: a name per key ref. */
  names?: Record<string, string>;
  /** names: let the assistant pick. */
  auto?: boolean;
  /** choice: the option picked. */
  choice?: string;
}

/** The dashboard page a chat message was sent from. */
export interface ChatPageContext {
  /** Service slug: "this service" in the message means it. */
  service?: string;
}

export interface ChatReply {
  thread: ChatThread;
  /** Only the items this request created or changed. */
  items: ChatItem[];
  /** The full transcript. */
  all: ChatItem[];
  turn?: ChatTurn;
}

/** Tool groups an app's MCP server can expose. */
export type MCPToolSet = "core" | "service_tools" | "insights" | "manage";

/**
 * How tools/list shows the service tools. "all" lists every one, "search"
 * lists the core tools plus search_tools and call_tool and leaves the service
 * tools to be found, "auto" lists all while there are few and searches beyond that.
 */
export type MCPToolMode = "auto" | "all" | "search";

/** Per app MCP settings, from GET /api/admin/apps/{id}/mcp. */
export interface MCPSettings {
  core: boolean;
  service_tools: boolean;
  insights: boolean;
  manage: boolean;
  resources: boolean;
  prompts: boolean;
  /** When true, only read-only tools are listed and callable on this app's /mcp endpoint. */
  read_only: boolean;
  /** get_key reserves the key for the caller unless the call says otherwise. */
  exclusive_default: boolean;
  /** Cap on tool result bodies sent to the model, 1 to 2048. */
  max_result_kb: number;
  /** Appended to the server instructions on initialize, max 8000 characters. */
  instructions: string;
  /** Tool names hidden one by one. */
  disabled_tools: string[];
  /** How tools/list shows the service tools. Empty is saved as "auto". */
  tool_mode: MCPToolMode;
  updated_at: string | null;
}

/** Body for PUT /api/admin/apps/{id}/mcp. */
export type MCPSettingsInput = Omit<MCPSettings, "updated_at">;

export interface MCPTool {
  /** Internal name, the one disabled_tools stores. */
  name: string;
  /** The name agents see in tools/list, such as acnologia_get_key. */
  public_name?: string;
  title: string;
  description: string;
  set: MCPToolSet;
  enabled: boolean;
  /** Shown by tools/list. False for a tool that is on but only found with search_tools. */
  listed: boolean;
  /** True when the tool is safe for read-only mode and read-only tokens. */
  read_only?: boolean;
  /** True when the tool is bridged from an upstream remote MCP server. */
  upstream_mcp?: boolean;
  /** Service slug this tool belongs to, when scoped to one service. */
  service?: string;
}

/** One tool discovered from an upstream Streamable HTTP MCP server. */
export interface ServiceMCPTool {
  name: string;
  public_name: string;
  title?: string;
  description?: string;
  inputSchema?: unknown;
  outputSchema?: unknown;
  annotations?: Record<string, unknown>;
  /** What /mcp enforces: `access` when an admin set it, else `upstream_read_only`. */
  read_only: boolean;
  /** The upstream server's own readOnlyHint. */
  upstream_read_only?: boolean;
  /** Admin override: "" follows the server, "read" or "write" overrides it. */
  access?: ServiceMCPToolAccess;
}

export type ServiceMCPToolAccess = "" | "read" | "write";

/** GET /api/admin/services/{id}/mcp and POST /api/admin/services/{id}/mcp/sync. */
export interface ServiceMCPView {
  service_id: number;
  service_slug: string;
  configured: boolean;
  endpoint: string;
  mcp_url: string;
  from_provider: boolean;
  transport: string;
  server_name: string;
  server_version: string;
  protocol_version: string;
  instructions: string;
  tool_count: number;
  tools: ServiceMCPTool[];
  synced_at: string | null;
  last_error: string;
}

/** What tools/list shows with the saved settings. */
export interface MCPListing {
  /** The effective mode: auto resolves to one of these. */
  mode: "all" | "search";
  /** Tools tools/list shows. */
  listed: number;
  /** Tools that are on, listed or not. */
  total: number;
  /** Auto lists everything while there are this many service tools or fewer. */
  auto_max: number;
}

export interface MCPView {
  settings: MCPSettings;
  /** Every tool in the sets that are on, including ones switched off one by one. */
  tools: MCPTool[];
  listing: MCPListing;
  /** Full URL of the HTTP MCP endpoint, ending in /mcp. */
  endpoint: string;
}

/** What a control MCP tool does: reads, changes, or removes something. */
export type ControlToolKind = "read" | "write" | "destructive";

/** A group of control MCP tools, picked with ?groups= on the endpoint URL. */
export interface ControlToolGroup {
  id: string;
  label: string;
  /** In the tool set a client gets without ?groups=. */
  default: boolean;
}

export interface ControlTool {
  name: string;
  title: string;
  description: string;
  group: string;
  kind: ControlToolKind;
  /** False when the caller's role cannot use it. */
  available: boolean;
}

/** GET /api/admin/mcp/control: the control MCP endpoint and every tool it can expose. */
export interface ControlMcpView {
  /** Full URL of the endpoint, ending in /mcp/control. */
  url: string;
  groups: ControlToolGroup[];
  tools: ControlTool[];
}

/** One account connected to an OAuth service (GET /api/admin/services/{id}/oauth). Never a token. */
export interface OAuthConnection {
  key_id: number;
  label: string;
  account: string;
  status: string;
  hidden: boolean;
  scopes: string[];
  expires_at?: string;
  refreshed_at?: string;
  connected_at: string;
  last_error?: string;
  needs_reconnect: boolean;
}

/** An OAuth service's sign in settings: the client (never its secret), the redirect URI and the accounts. */
export interface OAuthSettings {
  service_id: number;
  service: string;
  provider: string;
  configured: boolean;
  client_id: string;
  has_client_secret: boolean;
  scopes: string[];
  default_scopes: string[];
  allowed_scopes: string[];
  redirect_uri: string;
  public_url_set: boolean;
  pkce: boolean;
  authorize_url: string;
  fields: { key: string; label: string; public?: boolean; optional?: boolean }[];
  connections: OAuthConnection[];
}

export interface OAuthClientInput {
  client_id: string;
  client_secret?: string;
  scopes?: string[];
}

/** A sign in in progress: open url, then poll status_url. */
export interface OAuthConnectState {
  connect_id: string;
  service_id: number;
  service?: string;
  status: "pending" | "exchanging" | "done" | "failed" | "expired";
  url?: string;
  redirect_uri: string;
  expires_at: string;
  status_url: string;
  key_id?: number;
  account?: string;
  error?: string;
}

/** What deleting a key, service or app answers. Items from another server skip the trash, so they have no restore_id. */
export interface DeleteResult {
  ok?: boolean;
  restore_id?: string;
  expires_at?: string;
}

/** One deleted key, service or app in the trash (GET /api/admin/trash). Never a secret. */
export interface TrashItem {
  id: string;
  kind: "key" | "service" | "app";
  target_id: number;
  /** A key's label, a service's slug or an app's name. */
  name: string;
  /** The service a key belongs to. */
  service?: string;
  hint?: string;
  /** Keys a service took with it. */
  keys?: number;
  /** Is or holds a hidden key: superadmins only. */
  hidden: boolean;
  deleted_by: string;
  deleted_at: string;
  expires_at: string;
}

/** GET /api/admin/version. */
export interface ServerVersion {
  current: string;
  /** Null when the release check got no answer. */
  latest: string | null;
  release_url: string | null;
  /** Null when either version is not a release number (a dev build). */
  update_available: boolean | null;
  upgrade_command: string;
  upgrade_doc: string;
  checked_at: string;
}
