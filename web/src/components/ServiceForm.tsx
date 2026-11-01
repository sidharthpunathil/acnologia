import type { AuthType, Provider, ServiceInput, Strategy } from "../lib/types";
import { STRATEGY_HELP, STRATEGY_LABEL } from "../lib/format";
import { Field, Input, Select, Textarea } from "./ui";

export const STRATEGIES: Strategy[] = ["round_robin", "fill_first", "least_used", "most_quota", "random"];

export function StrategySelect({ value, onChange, disabled }: { value: Strategy; onChange: (s: Strategy) => void; disabled?: boolean }) {
  return (
    <Select value={value} onChange={(e) => onChange(e.target.value as Strategy)} disabled={disabled}>
      {STRATEGIES.map((s) => (
        <option key={s} value={s}>
          {STRATEGY_LABEL[s]}
        </option>
      ))}
    </Select>
  );
}

export function ServiceForm({
  value,
  onChange,
  slugLocked,
}: {
  value: ServiceInput;
  onChange: (v: ServiceInput) => void;
  slugLocked?: boolean;
}) {
  const set = <K extends keyof ServiceInput>(k: K, v: ServiceInput[K]) => onChange({ ...value, [k]: v });
  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
      <Field label="Name">
        <Input value={value.name} onChange={(e) => set("name", e.target.value)} required />
      </Field>
      <Field label="Slug" hint={slugLocked ? "Apps call this slug, so it is fixed." : "Used in /proxy/<slug>/ and SDK calls."}>
        <Input
          value={value.slug}
          onChange={(e) => set("slug", e.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, ""))}
          required
          disabled={slugLocked}
          className="font-mono"
        />
      </Field>
      <Field label="Base URL" className="sm:col-span-2">
        <Input value={value.base_url} onChange={(e) => set("base_url", e.target.value)} placeholder="https://api.example.com" required className="font-mono" />
      </Field>
      <Field label="Auth type" hint="Where Cogito puts the key on upstream requests.">
        <Select value={value.auth_type} onChange={(e) => set("auth_type", e.target.value as AuthType)}>
          <option value="query">query parameter</option>
          <option value="header">header</option>
          <option value="bearer">bearer token</option>
        </Select>
      </Field>
      <Field label={value.auth_type === "query" ? "Query parameter name" : "Header name"}>
        <Input
          value={value.auth_name}
          onChange={(e) => set("auth_name", e.target.value)}
          placeholder={value.auth_type === "query" ? "api_key" : value.auth_type === "bearer" ? "Authorization" : "xi-api-key"}
          className="font-mono"
        />
      </Field>
      <Field label="Strategy" hint={STRATEGY_HELP[value.strategy]}>
        <StrategySelect value={value.strategy} onChange={(s) => set("strategy", s)} />
      </Field>
      <Field label="Provider" hint="Controls rate limit detection and health checks.">
        <Select value={value.provider} onChange={(e) => set("provider", e.target.value as Provider)}>
          <option value="serpapi">SerpAPI</option>
          <option value="elevenlabs">ElevenLabs</option>
          <option value="generic">Generic</option>
        </Select>
      </Field>
      <Field label="Cooldown (seconds)" hint="Base cooldown after a rate limit, doubled per consecutive failure.">
        <Input type="number" min={0} value={value.cooldown_sec} onChange={(e) => set("cooldown_sec", Number(e.target.value))} />
      </Field>
      <Field label="Max attempts" hint="Proxy retries with the next key up to this many times.">
        <Input type="number" min={1} max={20} value={value.max_attempts} onChange={(e) => set("max_attempts", Number(e.target.value))} />
      </Field>
      <Field label="Category">
        <Input value={value.category} onChange={(e) => set("category", e.target.value)} placeholder="Search" />
      </Field>
      <Field label="Icon" hint="A name like search or audio, an emoji, or an image URL.">
        <Input value={value.icon} onChange={(e) => set("icon", e.target.value)} />
      </Field>
      <Field label="Description" className="sm:col-span-2">
        <Textarea rows={2} value={value.description} onChange={(e) => set("description", e.target.value)} />
      </Field>
    </div>
  );
}
