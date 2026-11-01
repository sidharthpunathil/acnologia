import type { AuthType, ServiceInput, Strategy } from "../lib/types";
import { STRATEGY_HELP, STRATEGY_LABEL } from "../lib/format";
import { useProviders } from "../stores/providers";
import { ProviderLogo } from "./EntityLogo";
import { Field, Input, Select, Textarea } from "./ui";

export const STRATEGIES: Strategy[] = ["round_robin", "fill_first", "least_used", "most_quota", "random"];

export function StrategySelect({
  value,
  onChange,
  disabled,
  id,
  ...rest
}: {
  value: Strategy;
  onChange: (s: Strategy) => void;
  disabled?: boolean;
  id?: string;
  "aria-describedby"?: string;
}) {
  return (
    <Select id={id} value={value} onChange={(e) => onChange(e.target.value as Strategy)} disabled={disabled} {...rest}>
      {STRATEGIES.map((s) => (
        <option key={s} value={s}>
          {STRATEGY_LABEL[s]}
        </option>
      ))}
    </Select>
  );
}

/** Native select with the chosen provider's 24px logo inside the field. */
export function ProviderSelect({
  value,
  onChange,
  id,
  disabled,
  ...rest
}: {
  value: string;
  onChange: (slug: string) => void;
  id?: string;
  disabled?: boolean;
  "aria-describedby"?: string;
}) {
  const providers = useProviders((s) => s.providers);
  const current = providers.find((p) => p.slug === value);
  return (
    <div className="relative">
      <span className="pointer-events-none absolute left-2 top-1/2 -translate-y-1/2">
        <ProviderLogo provider={current ?? { name: value || "?", color: null as unknown as string, logo_url: null }} size="sm" />
      </span>
      <Select id={id} value={value} disabled={disabled} onChange={(e) => onChange(e.target.value)} className="pl-10" {...rest}>
        {!current && <option value={value}>{value}</option>}
        {providers.map((p) => (
          <option key={p.slug} value={p.slug}>
            {p.name}
            {p.status === "disabled" ? " (disabled)" : ""}
          </option>
        ))}
      </Select>
    </div>
  );
}

export type ServiceErrors = Partial<Record<keyof ServiceInput, string>>;

export function validateService(v: ServiceInput): ServiceErrors {
  const e: ServiceErrors = {};
  if (!v.name.trim()) e.name = "Give the service a name.";
  if (!v.slug.trim()) e.slug = "Add a slug. Apps call it in /proxy/<slug>/.";
  else if (!/^[a-z0-9][a-z0-9_-]*$/.test(v.slug)) e.slug = "Use lowercase letters, numbers, dashes or underscores.";
  if (!v.base_url.trim()) e.base_url = "Add the API address Cogito forwards to, such as https://api.example.com.";
  else if (!/^https?:\/\//i.test(v.base_url.trim())) e.base_url = "Start with http:// or https://.";
  if ((v.max_concurrency ?? 0) < 0 || (v.max_concurrency ?? 0) > 1000) e.max_concurrency = "Between 0 (unlimited) and 1000.";
  if ((v.queue_timeout_sec ?? 0) < 0 || (v.queue_timeout_sec ?? 0) > 300) e.queue_timeout_sec = "Between 0 and 300 seconds.";
  if (v.max_attempts < 1 || v.max_attempts > 20) e.max_attempts = "Between 1 and 20.";
  if (v.cooldown_sec < 0) e.cooldown_sec = "Cannot be negative.";
  return e;
}

/** Concurrency fields shared by the service form and the rotation card. */
export function ConcurrencyFields({
  maxConcurrency,
  queueSec,
  onChange,
  disabled,
  errors,
  flash,
}: {
  maxConcurrency: number;
  queueSec: number;
  onChange: (v: { max_concurrency?: number; queue_timeout_sec?: number }) => void;
  disabled?: boolean;
  errors?: ServiceErrors;
  flash?: (k: string) => string;
}) {
  return (
    <>
      <Field
        label="Max concurrent requests per key"
        hint="0 means no limit. A key at its limit is skipped until one of its requests finishes."
        error={errors?.max_concurrency}
        className={flash?.("max_concurrency")}
      >
        <Input
          type="number"
          inputMode="numeric"
          min={0}
          max={1000}
          value={maxConcurrency}
          disabled={disabled}
          onChange={(e) => onChange({ max_concurrency: Math.max(0, Number(e.target.value) || 0) })}
        />
      </Field>
      <Field
        label="Queue wait (sec)"
        hint="How long a caller waits for a free key when every key is busy. 0 fails right away with all_keys_busy."
        error={errors?.queue_timeout_sec}
        className={flash?.("queue_timeout_sec")}
      >
        <Input
          type="number"
          inputMode="numeric"
          min={0}
          max={300}
          value={queueSec}
          disabled={disabled}
          onChange={(e) => onChange({ queue_timeout_sec: Math.max(0, Number(e.target.value) || 0) })}
        />
      </Field>
    </>
  );
}

export function ServiceForm({
  value,
  onChange,
  slugLocked,
  errors,
  flash,
}: {
  value: ServiceInput;
  onChange: (v: ServiceInput) => void;
  slugLocked?: boolean;
  errors?: ServiceErrors;
  flash?: (k: string) => string;
}) {
  const set = <K extends keyof ServiceInput>(k: K, v: ServiceInput[K]) => onChange({ ...value, [k]: v });
  const f = flash ?? (() => "");
  return (
    <div className="space-y-6">
      <fieldset className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <legend className="mb-3 font-mono text-[11px] uppercase tracking-wide text-faint">Identity</legend>
        <Field label="Name" required error={errors?.name} className={f("name")}>
          <Input value={value.name} onChange={(e) => set("name", e.target.value)} autoComplete="off" />
        </Field>
        <Field
          label="Slug"
          required
          error={errors?.slug}
          hint={slugLocked ? "Apps call this slug, so it is fixed." : "Used in /proxy/<slug>/ and SDK calls."}
          className={f("slug")}
        >
          <Input
            value={value.slug}
            onChange={(e) => set("slug", e.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, ""))}
            disabled={slugLocked}
            autoComplete="off"
            spellCheck={false}
            className="font-mono"
          />
        </Field>
        <Field label="Category" hint="Groups services in the catalog." className={f("category")}>
          <Input value={value.category} onChange={(e) => set("category", e.target.value)} placeholder="Search" />
        </Field>
        <Field label="Provider" hint="Sets how Cogito spots rate limits and checks keys." className={f("provider")}>
          <ProviderSelect value={value.provider} onChange={(v) => set("provider", v)} />
        </Field>
        <Field label="Description" className={`sm:col-span-2 ${f("description")}`}>
          <Textarea rows={2} value={value.description} onChange={(e) => set("description", e.target.value)} placeholder="What this pool is for, such as search for the scraper" />
        </Field>
      </fieldset>

      <fieldset className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <legend className="mb-3 font-mono text-[11px] uppercase tracking-wide text-faint">Upstream</legend>
        <Field label="Base URL" required error={errors?.base_url} className={`sm:col-span-2 ${f("base_url")}`}>
          <Input
            type="url"
            inputMode="url"
            value={value.base_url}
            onChange={(e) => set("base_url", e.target.value)}
            placeholder="https://api.example.com"
            spellCheck={false}
            className="font-mono"
          />
        </Field>
        <Field label="Auth type" hint="Where Cogito puts the key on upstream requests." className={f("auth_type")}>
          <Select value={value.auth_type} onChange={(e) => set("auth_type", e.target.value as AuthType)}>
            <option value="query">query parameter</option>
            <option value="header">header</option>
            <option value="bearer">bearer token</option>
          </Select>
        </Field>
        <Field label={value.auth_type === "query" ? "Query parameter name" : "Header name"} className={f("auth_name")}>
          <Input
            value={value.auth_name}
            onChange={(e) => set("auth_name", e.target.value)}
            placeholder={value.auth_type === "query" ? "api_key" : value.auth_type === "bearer" ? "Authorization" : "X-API-Key"}
            spellCheck={false}
            className="font-mono"
          />
        </Field>
      </fieldset>

      <fieldset className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <legend className="mb-3 font-mono text-[11px] uppercase tracking-wide text-faint">Rotation and limits</legend>
        <Field label="Strategy" hint={STRATEGY_HELP[value.strategy]} className={f("strategy")}>
          <StrategySelect value={value.strategy} onChange={(s) => set("strategy", s)} />
        </Field>
        <Field label="Cooldown (sec)" hint="How long a key rests after a rate limit. Doubles with each failure in a row." error={errors?.cooldown_sec} className={f("cooldown_sec")}>
          <Input type="number" inputMode="numeric" min={0} value={value.cooldown_sec} onChange={(e) => set("cooldown_sec", Number(e.target.value))} />
        </Field>
        <Field label="Max attempts" hint="How many keys the proxy tries before giving up." error={errors?.max_attempts} className={f("max_attempts")}>
          <Input type="number" inputMode="numeric" min={1} max={20} value={value.max_attempts} onChange={(e) => set("max_attempts", Number(e.target.value))} />
        </Field>
        <div className="hidden sm:block" />
        <ConcurrencyFields
          maxConcurrency={value.max_concurrency ?? 0}
          queueSec={value.queue_timeout_sec ?? 0}
          onChange={(p) => onChange({ ...value, ...p })}
          errors={errors}
          flash={flash}
        />
      </fieldset>
    </div>
  );
}
