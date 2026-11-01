import type { AuthType, ServiceInput, Strategy } from "../lib/types";
import { STRATEGY_HELP, STRATEGY_LABEL } from "../lib/format";
import { useProviders } from "../stores/providers";
import { Input } from "@/components/base/input/input";
import { Label } from "@/components/base/input/label";
import { hintTextClassName } from "@/components/base/input/hint-text";
import { NumberField } from "@/components/base/number-field/number-field";
import { Select as BoardSelect, SelectItem } from "@/components/base/select/select";
import { Textarea } from "@/components/base/textarea/textarea";
import { cx } from "@/utils/cx";
import { ProviderLogo } from "./EntityLogo";
// Native selects (BoardUI field look) keep `id` and `onChange` for callers that wire their own label.
import { Select } from "./ui";

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
      <span className="pointer-events-none absolute start-2 top-1/2 -translate-y-1/2">
        <ProviderLogo provider={current ?? { name: value || "?", color: null as unknown as string, logo_url: null }} size="sm" />
      </span>
      <Select id={id} value={value} disabled={disabled} onChange={(e) => onChange(e.target.value)} className="ps-10" {...rest}>
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
  if (!v.base_url.trim()) e.base_url = "Add the API address Acnologia forwards to, such as https://api.example.com.";
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
      <NumberField
        label="Max concurrent requests per key"
        hint={errors?.max_concurrency ?? "0 means no limit. A key at its limit is skipped until one of its requests finishes."}
        isInvalid={!!errors?.max_concurrency}
        minValue={0}
        maxValue={1000}
        value={maxConcurrency}
        isDisabled={disabled}
        onChange={(n) => onChange({ max_concurrency: Math.max(0, Number.isFinite(n) ? n : 0) })}
        className={flash?.("max_concurrency")}
      />
      <NumberField
        label="Queue wait (sec)"
        hint={errors?.queue_timeout_sec ?? "How long a caller waits for a free key when every key is busy. 0 fails right away with all_keys_busy."}
        isInvalid={!!errors?.queue_timeout_sec}
        minValue={0}
        maxValue={300}
        value={queueSec}
        isDisabled={disabled}
        onChange={(n) => onChange({ queue_timeout_sec: Math.max(0, Number.isFinite(n) ? n : 0) })}
        className={flash?.("queue_timeout_sec")}
      />
    </>
  );
}

const AUTH_TYPES: Array<{ id: AuthType; label: string }> = [
  { id: "query", label: "query parameter" },
  { id: "header", label: "header" },
  { id: "bearer", label: "bearer token" },
];

const LEGEND = "mb-3 font-mono text-caption-2-semibold uppercase text-text-tertiary";

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
  const providerId = "service-form-provider";
  return (
    <div className="flex flex-col gap-6">
      <fieldset className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <legend className={LEGEND}>Identity</legend>
        <Input
          label="Name"
          isRequired
          value={value.name}
          onChange={(v) => set("name", v)}
          isInvalid={!!errors?.name}
          errorMessage={errors?.name}
          autoComplete="off"
          className={f("name")}
        />
        <Input
          label="Slug"
          isRequired
          value={value.slug}
          onChange={(v) => set("slug", v.toLowerCase().replace(/[^a-z0-9_-]/g, ""))}
          isDisabled={slugLocked}
          isInvalid={!!errors?.slug}
          errorMessage={errors?.slug}
          hint={slugLocked ? "Apps call this slug, so it is fixed." : "Used in /proxy/<slug>/ and SDK calls."}
          autoComplete="off"
          spellCheck="false"
          inputDir="ltr"
          inputClassName="font-mono"
          className={f("slug")}
        />
        <Input
          label="Category"
          hint="Groups services in the catalog."
          value={value.category}
          onChange={(v) => set("category", v)}
          placeholder="Search"
          className={f("category")}
        />
        <div className={cx("flex min-w-0 flex-col gap-1", f("provider"))}>
          <Label htmlFor={providerId}>Provider</Label>
          <ProviderSelect id={providerId} value={value.provider} onChange={(v) => set("provider", v)} aria-describedby={`${providerId}-hint`} />
          <p id={`${providerId}-hint`} className={hintTextClassName(false)}>
            Sets how Acnologia spots rate limits and checks keys.
          </p>
        </div>
        <Textarea
          label="Description"
          rows={2}
          value={value.description}
          onChange={(v) => set("description", v)}
          placeholder="What this pool is for, such as search for the scraper"
          className={cx("sm:col-span-2", f("description"))}
        />
      </fieldset>

      <fieldset className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <legend className={LEGEND}>Upstream</legend>
        <Input
          label="Base URL"
          isRequired
          type="url"
          inputMode="url"
          value={value.base_url}
          onChange={(v) => set("base_url", v)}
          isInvalid={!!errors?.base_url}
          errorMessage={errors?.base_url}
          placeholder="https://api.example.com"
          spellCheck="false"
          inputDir="ltr"
          inputClassName="font-mono"
          className={cx("sm:col-span-2", f("base_url"))}
        />
        <BoardSelect
          label="Auth type"
          hint="Where Acnologia puts the key on upstream requests."
          selectedKey={value.auth_type}
          onSelectionChange={(k) => k != null && set("auth_type", k as AuthType)}
          triggerClassName="w-full"
          className={f("auth_type")}
        >
          {AUTH_TYPES.map((a) => (
            <SelectItem key={a.id} id={a.id}>
              {a.label}
            </SelectItem>
          ))}
        </BoardSelect>
        <Input
          label={value.auth_type === "query" ? "Query parameter name" : "Header name"}
          value={value.auth_name}
          onChange={(v) => set("auth_name", v)}
          placeholder={value.auth_type === "query" ? "api_key" : value.auth_type === "bearer" ? "Authorization" : "X-API-Key"}
          spellCheck="false"
          inputDir="ltr"
          inputClassName="font-mono"
          className={f("auth_name")}
        />
      </fieldset>

      <fieldset className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <legend className={LEGEND}>Rotation and limits</legend>
        <BoardSelect
          label="Strategy"
          hint={STRATEGY_HELP[value.strategy]}
          selectedKey={value.strategy}
          onSelectionChange={(k) => k != null && set("strategy", k as Strategy)}
          triggerClassName="w-full"
          className={f("strategy")}
        >
          {STRATEGIES.map((s) => (
            <SelectItem key={s} id={s}>
              {STRATEGY_LABEL[s]}
            </SelectItem>
          ))}
        </BoardSelect>
        <NumberField
          label="Cooldown (sec)"
          hint={errors?.cooldown_sec ?? "How long a key rests after a rate limit. Doubles with each failure in a row."}
          isInvalid={!!errors?.cooldown_sec}
          minValue={0}
          value={value.cooldown_sec}
          onChange={(n) => set("cooldown_sec", Number.isFinite(n) ? n : 0)}
          className={f("cooldown_sec")}
        />
        <NumberField
          label="Max attempts"
          hint={errors?.max_attempts ?? "How many keys the proxy tries before giving up."}
          isInvalid={!!errors?.max_attempts}
          minValue={1}
          maxValue={20}
          value={value.max_attempts}
          onChange={(n) => set("max_attempts", Number.isFinite(n) ? n : 1)}
          className={f("max_attempts")}
        />
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
