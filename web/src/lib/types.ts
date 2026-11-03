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
  /** Egress proxy for this service's traffic; null connects directly. */
  egress_id?: number | null;
  /** Response cache TTL override; null uses the provider default. */
  cache_ttl_sec?: number | null;
  /** USD per request override; null uses the provider default. */
  cost_per_request?: number | null;
  /** Set when another Acnologia shares this service; it proxies through that server. */
  peer_link_id?: number | null;
  /** The name of the server that shares it. */
  peer_name?: string;
  /** The owner's provider for a shared service: its name, logo, docs and examples describe it. */
  peer_provider?: string;
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
  Partial<Pick<Service, "max_concurrency" | "queue_timeout_sec" | "egress_id" | "cache_ttl_sec" | "cost_per_request">>;

export type KeyStatus = "active" | "disabled" | "exhausted" | "invalid" | "banned";
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
  secret: string;
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
  created_at: string;
  created_by: number | null;
  created_by_email: string;
  last_used_at: string | null;
  expires_at: string | null;
  revoked_at: string | null;
}

export type AppInput = Pick<App, "name" | "description" | "mode" | "allowed_services"> & { lease_hidden?: boolean };

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
  auth: { type: AuthType; name: string; prefix: string };
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

export type ChatItemKind = "user" | "assistant" | "tool" | "confirm" | "error" | "build" | "ask" | "compact";

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

export interface ChatAsk {
  id: string;
  kind: "names" | "choice";
  question: string;
  rows?: ChatAskRow[];
  choices?: string[];
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

/** Per app MCP settings, from GET /api/admin/apps/{id}/mcp. */
export interface MCPSettings {
  core: boolean;
  service_tools: boolean;
  insights: boolean;
  manage: boolean;
  resources: boolean;
  prompts: boolean;
  /** get_key reserves the key for the caller unless the call says otherwise. */
  exclusive_default: boolean;
  /** Cap on tool result bodies sent to the model, 1 to 2048. */
  max_result_kb: number;
  /** Appended to the server instructions on initialize, max 8000 characters. */
  instructions: string;
  /** Tool names hidden one by one. */
  disabled_tools: string[];
  updated_at: string | null;
}

/** Body for PUT /api/admin/apps/{id}/mcp. */
export type MCPSettingsInput = Omit<MCPSettings, "updated_at">;

export interface MCPTool {
  name: string;
  title: string;
  description: string;
  set: MCPToolSet;
  enabled: boolean;
}

export interface MCPView {
  settings: MCPSettings;
  /** Every tool in the sets that are on, including ones switched off one by one. */
  tools: MCPTool[];
  /** Full URL of the HTTP MCP endpoint, ending in /mcp. */
  endpoint: string;
}
