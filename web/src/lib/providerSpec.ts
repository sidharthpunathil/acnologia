import type { Provider, ProviderInput, ProviderSpec, ProviderTemplate, Strategy, AuthType } from "./types";

export const DEFAULT_COLOR = "#6b6b66";

export function blankSpec(): ProviderSpec {
  return {
    base_url: "",
    auth: { type: "header", name: "Authorization", prefix: "" },
    defaults: { strategy: "round_robin", cooldown_sec: 60, max_attempts: 3, max_concurrency: 0, queue_timeout_sec: 0 },
    quota: { unit: "requests", counts_per_request: false },
    rules: [],
    use_standard_rules: true,
    health: null,
    examples: [],
    tests: [],
  };
}

export function blankProviderInput(): ProviderInput {
  return {
    slug: "",
    name: "",
    category: "",
    description: "",
    website: "",
    docs_url: "",
    color: "#3b6ef5",
    status: "active",
    spec: blankSpec(),
  };
}

/** Fill in anything a stored or AI written spec left out, so editors never hit undefined. */
export function normalizeSpec(s: Partial<ProviderSpec> | null | undefined): ProviderSpec {
  const b = blankSpec();
  const src = s ?? {};
  return {
    ...b,
    ...src,
    auth: { ...b.auth, ...(src.auth ?? {}) },
    defaults: { ...b.defaults, ...(src.defaults ?? {}) },
    quota: { ...b.quota!, ...(src.quota ?? {}) },
    rules: src.rules ?? [],
    examples: src.examples ?? [],
    tests: src.tests ?? [],
    health: src.health ?? null,
    use_standard_rules: src.use_standard_rules ?? true,
  };
}

export function inputOf(p: Provider): ProviderInput {
  return {
    slug: p.slug,
    name: p.name,
    category: p.category,
    description: p.description,
    website: p.website,
    docs_url: p.docs_url,
    color: p.color || DEFAULT_COLOR,
    status: p.status,
    spec: normalizeSpec(p.spec),
  };
}

/** Older servers answer /providers with templates. Map them so the UI has one shape. */
export function fromTemplate(t: ProviderTemplate): Provider {
  const spec = blankSpec();
  spec.base_url = t.base_url;
  spec.auth = { type: (["query", "header", "bearer"].includes(t.auth_type) ? t.auth_type : "header") as AuthType, name: t.auth_name, prefix: "" };
  spec.defaults.cooldown_sec = t.default_cooldown_sec || 60;
  return {
    slug: t.provider,
    name: t.name,
    category: t.category,
    description: t.description,
    website: "",
    docs_url: t.docs_url,
    color: "",
    logo_url: null,
    status: "active",
    builtin: true,
    source: "builtin",
    version: 1,
    updated_at: "",
    created_at: "",
    services_count: 0,
    spec,
    base_url: t.base_url,
    auth_type: t.auth_type,
    auth_name: t.auth_name,
    default_cooldown_sec: t.default_cooldown_sec,
    default_max_concurrency: 0,
    default_queue_timeout_sec: 0,
  };
}

export function normalizeProvider(raw: Provider | ProviderTemplate): Provider {
  if ("slug" in raw && "spec" in raw) return { ...raw, spec: normalizeSpec(raw.spec) };
  return fromTemplate(raw as ProviderTemplate);
}

export const STRATEGY_VALUES: Strategy[] = ["round_robin", "fill_first", "least_used", "most_quota", "random"];

/** Comma separated text to list and back, used by the matcher editors. */
export function splitList(s: string): string[] {
  return s
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);
}

export function joinList(a: Array<string | number> | undefined): string {
  return (a ?? []).join(", ");
}

export function parseStatusList(s: string): number[] {
  return splitList(s)
    .map((x) => Number(x))
    .filter((n) => Number.isInteger(n) && n > 0);
}
