import { useState } from "react";
import { ArrowDown, ArrowUp, CheckCircle2, ChevronDown, Copy, FlaskConical, ListOrdered, Plus, Trash2, XCircle } from "lucide-react";
import type { AuthType, Outcome, ProviderSpec, RetrySource, SpecExample, SpecFixture, SpecHealth, SpecRule, Strategy, TestReport } from "../../lib/types";
import { OUTCOMES } from "../../lib/types";
import { STRATEGY_HELP } from "../../lib/format";
import { ConcurrencyFields, StrategySelect } from "../../components/ServiceForm";
import { JsonTextarea, KeyValueEditor, ListInput, MatchEditor } from "../../components/SpecInputs";
import { Badge, Button, Empty, Field, IconButton, Input, Section, Select, Textarea, Toggle, cx } from "../../components/ui";

export interface SpecTabProps {
  spec: ProviderSpec;
  onChange: (s: ProviderSpec) => void;
  readOnly?: boolean;
}

const OUTCOME_LABEL: Record<Outcome, string> = {
  ok: "ok (the key is fine)",
  rate_limited: "rate limited (cool down)",
  quota_exhausted: "quota exhausted (park until reset)",
  invalid: "invalid (disable key)",
  error: "error (provider problem, key keeps working)",
};

const OUTCOME_TONE: Record<string, "green" | "amber" | "red" | "neutral"> = {
  ok: "green",
  rate_limited: "amber",
  quota_exhausted: "amber",
  invalid: "red",
  error: "neutral",
};

export function OutcomeBadge({ outcome }: { outcome: string }) {
  return <Badge tone={OUTCOME_TONE[outcome] ?? "neutral"}>{outcome.replace("_", " ") || "none"}</Badge>;
}

function move<T>(list: T[], i: number, d: -1 | 1): T[] {
  const j = i + d;
  if (j < 0 || j >= list.length) return list;
  const next = list.slice();
  [next[i], next[j]] = [next[j], next[i]];
  return next;
}

/** Up, down, duplicate and delete controls for an ordered list item. */
function ItemControls({
  index,
  count,
  label,
  onMove,
  onDuplicate,
  onRemove,
  disabled,
}: {
  index: number;
  count: number;
  label: string;
  onMove: (d: -1 | 1) => void;
  onDuplicate: () => void;
  onRemove: () => void;
  disabled?: boolean;
}) {
  return (
    <div className="ml-auto flex shrink-0 items-center gap-0.5">
      <IconButton label={`Move ${label} up`} disabled={disabled || index === 0} onClick={() => onMove(-1)}>
        <ArrowUp className="h-4 w-4" />
      </IconButton>
      <IconButton label={`Move ${label} down`} disabled={disabled || index === count - 1} onClick={() => onMove(1)}>
        <ArrowDown className="h-4 w-4" />
      </IconButton>
      <IconButton label={`Duplicate ${label}`} disabled={disabled} onClick={onDuplicate}>
        <Copy className="h-4 w-4" />
      </IconButton>
      <IconButton label={`Delete ${label}`} disabled={disabled} onClick={onRemove} className="hover:text-red-700">
        <Trash2 className="h-4 w-4" />
      </IconButton>
    </div>
  );
}

// ---------------------------------------------------------------- Connection

export function ConnectionTab({ spec, onChange, readOnly }: SpecTabProps) {
  const set = (p: Partial<ProviderSpec>) => onChange({ ...spec, ...p });
  const d = spec.defaults;
  const setD = (p: Partial<ProviderSpec["defaults"]>) => set({ defaults: { ...d, ...p } });
  const quota = spec.quota ?? { unit: "", counts_per_request: false };
  return (
    <div className="space-y-5">
      <Section title="Upstream" description="Where requests go and where the key goes on each request.">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <Field label="Base URL" required className="sm:col-span-3">
            <Input
              type="url"
              inputMode="url"
              value={spec.base_url}
              disabled={readOnly}
              onChange={(e) => set({ base_url: e.target.value })}
              placeholder="https://api.example.com"
              spellCheck={false}
              className="font-mono"
            />
          </Field>
          <Field label="Auth type">
            <Select value={spec.auth.type} disabled={readOnly} onChange={(e) => set({ auth: { ...spec.auth, type: e.target.value as AuthType } })}>
              <option value="query">query parameter</option>
              <option value="header">header</option>
              <option value="bearer">bearer token</option>
            </Select>
          </Field>
          <Field label={spec.auth.type === "query" ? "Parameter name" : "Header name"}>
            <Input
              value={spec.auth.name}
              disabled={readOnly}
              onChange={(e) => set({ auth: { ...spec.auth, name: e.target.value } })}
              placeholder={spec.auth.type === "query" ? "api_key" : "Authorization"}
              className="font-mono"
            />
          </Field>
          <Field label="Value prefix" hint={spec.auth.type === "bearer" ? "Bearer adds its own prefix." : "Text before the key, if the API needs any."}>
            <Input value={spec.auth.prefix} disabled={readOnly} onChange={(e) => set({ auth: { ...spec.auth, prefix: e.target.value } })} placeholder="Token " className="font-mono" />
          </Field>
        </div>
      </Section>

      <Section title="Defaults for new services" description="Services created from this provider start with these values. Existing services keep their own settings.">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <Field label="Strategy" hint={STRATEGY_HELP[d.strategy]}>
            <StrategySelect value={d.strategy as Strategy} disabled={readOnly} onChange={(s) => setD({ strategy: s })} />
          </Field>
          <Field label="Cooldown (sec)" hint="How long a key rests after a rate limit.">
            <Input type="number" min={0} inputMode="numeric" value={d.cooldown_sec} disabled={readOnly} onChange={(e) => setD({ cooldown_sec: Number(e.target.value) })} />
          </Field>
          <Field label="Max attempts" hint="How many keys the proxy tries.">
            <Input type="number" min={1} max={20} inputMode="numeric" value={d.max_attempts} disabled={readOnly} onChange={(e) => setD({ max_attempts: Number(e.target.value) })} />
          </Field>
        </div>
        <div className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2">
          <ConcurrencyFields
            maxConcurrency={d.max_concurrency}
            queueSec={d.queue_timeout_sec}
            disabled={readOnly}
            onChange={(p) => setD(p)}
          />
        </div>
      </Section>

      <Section title="Quota" description="How Cogito counts usage between health checks.">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Field label="Unit" hint="Shows next to quota numbers, such as searches or characters.">
            <Input value={quota.unit} disabled={readOnly} onChange={(e) => set({ quota: { ...quota, unit: e.target.value } })} placeholder="requests" />
          </Field>
          <div className="sm:pt-7">
            <Toggle
              label="Each successful request uses one unit"
              description="Counts down the remaining quota on each success. Leave off when the cost changes per request."
              checked={quota.counts_per_request}
              disabled={readOnly}
              onChange={(v) => set({ quota: { ...quota, counts_per_request: v } })}
            />
          </div>
        </div>
      </Section>
    </div>
  );
}

// ---------------------------------------------------------------- Rules

const RETRY_SOURCES: { id: RetrySource; label: string; value?: string }[] = [
  { id: "none", label: "none" },
  { id: "header", label: "response header", value: "Header name (default Retry-After)" },
  { id: "fixed", label: "fixed seconds", value: "Seconds" },
  { id: "next_hour", label: "top of the next hour" },
  { id: "body_json", label: "JSON field in the body", value: "Path to seconds" },
];

function RuleCard({
  rule,
  index,
  count,
  onChange,
  onMove,
  onDuplicate,
  onRemove,
  readOnly,
}: {
  rule: SpecRule;
  index: number;
  count: number;
  onChange: (r: SpecRule) => void;
  onMove: (d: -1 | 1) => void;
  onDuplicate: () => void;
  onRemove: () => void;
  readOnly?: boolean;
}) {
  const [open, setOpen] = useState(!rule.name);
  const src = rule.retry_after?.source ?? "none";
  const srcMeta = RETRY_SOURCES.find((s) => s.id === src);
  const summary = [
    rule.match.status?.length ? `status ${rule.match.status.join("|")}` : "any error",
    rule.match.body_contains?.length ? `body has "${rule.match.body_contains.join('" or "')}"` : "",
    rule.match.json_path ? `${rule.match.json_path} = ${rule.match.json_equals ?? ""}` : "",
  ]
    .filter(Boolean)
    .join(" and ");
  return (
    <li className="rounded-card bg-surface shadow-card">
      <div className="flex flex-wrap items-center gap-2 px-3 py-2.5 sm:flex-nowrap sm:px-4">
        <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md bg-soft font-mono text-[11px] text-muted">{index + 1}</span>
        <button
          type="button"
          onClick={() => setOpen(!open)}
          aria-expanded={open}
          className="tap flex min-w-0 flex-1 basis-[60%] items-center gap-2 text-left"
        >
          <span className="min-w-0 flex-1">
            <span className="block truncate text-[14px] font-medium text-ink">{rule.name || "Untitled rule"}</span>
            <span className="block truncate font-mono text-[11px] text-muted">{summary}</span>
          </span>
          <OutcomeBadge outcome={rule.outcome} />
          <ChevronDown className={cx("h-4 w-4 shrink-0 text-faint transition-transform", open && "rotate-180")} />
        </button>
        <ItemControls index={index} count={count} label={rule.name || `rule ${index + 1}`} onMove={onMove} onDuplicate={onDuplicate} onRemove={onRemove} disabled={readOnly} />
      </div>
      {open && (
        <div className="space-y-4 border-t border-line px-4 py-4">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field label="Name" hint="A short label shown in test reports.">
              <Input value={rule.name} disabled={readOnly} onChange={(e) => onChange({ ...rule, name: e.target.value })} placeholder="Monthly quota used up" />
            </Field>
            <Field label="Outcome">
              <Select value={rule.outcome} disabled={readOnly} onChange={(e) => onChange({ ...rule, outcome: e.target.value as Outcome })}>
                {OUTCOMES.map((o) => (
                  <option key={o} value={o}>
                    {OUTCOME_LABEL[o]}
                  </option>
                ))}
              </Select>
            </Field>
          </div>
          <fieldset>
            <legend className="mb-2 font-mono text-[11px] uppercase tracking-wide text-faint">Match when</legend>
            <MatchEditor value={rule.match} disabled={readOnly} onChange={(m) => onChange({ ...rule, match: m })} />
          </fieldset>
          {rule.outcome === "rate_limited" && (
            <fieldset className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <legend className="mb-2 font-mono text-[11px] uppercase tracking-wide text-faint">Retry after</legend>
              <Field label="Source" hint="Where the rest time comes from. None uses the service cooldown.">
                <Select
                  value={src}
                  disabled={readOnly}
                  onChange={(e) => {
                    const s = e.target.value as RetrySource;
                    onChange({ ...rule, retry_after: s === "none" ? undefined : { source: s, value: rule.retry_after?.value ?? "" } });
                  }}
                >
                  {RETRY_SOURCES.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.label}
                    </option>
                  ))}
                </Select>
              </Field>
              {srcMeta?.value && (
                <Field label={srcMeta.value}>
                  <Input
                    value={rule.retry_after?.value ?? ""}
                    disabled={readOnly}
                    inputMode={src === "fixed" ? "numeric" : undefined}
                    onChange={(e) => onChange({ ...rule, retry_after: { source: src, value: e.target.value } })}
                    placeholder={src === "header" ? "Retry-After" : src === "fixed" ? "60" : "error.retry_after"}
                    className="font-mono"
                  />
                </Field>
              )}
            </fieldset>
          )}
        </div>
      )}
    </li>
  );
}

export function RulesTab({ spec, onChange, readOnly }: SpecTabProps) {
  const rules = spec.rules;
  const setRules = (r: SpecRule[]) => onChange({ ...spec, rules: r });
  const add = () => setRules([...rules, { name: "", match: { status: [429] }, outcome: "rate_limited" }]);
  return (
    <div className="space-y-5">
      <Section
        title="Classification rules"
        description="Checked top to bottom on every error response, and the first match wins. Every field in a rule must match. Any value in a list can match."
        actions={
          !readOnly && (
            <Button size="sm" icon={<Plus className="h-3.5 w-3.5" />} onClick={add}>
              Add rule
            </Button>
          )
        }
      >
        <Toggle
          label="Fall back to standard rules"
          description="Runs after your rules. 429 is rate limited (respects Retry-After), 402 is out of quota, 401 and 403 are invalid, 5xx is an error."
          checked={spec.use_standard_rules}
          disabled={readOnly}
          onChange={(v) => onChange({ ...spec, use_standard_rules: v })}
        />
      </Section>
      {rules.length === 0 ? (
        <Empty
          icon={<ListOrdered className="h-5 w-5" />}
          title="No custom rules"
          body={spec.use_standard_rules ? "The standard rules apply. Add a rule when the API signals limits its own way, such as a 200 with an error in the body." : "Nothing sorts errors yet. Add a rule, or turn on the standard rules."}
          action={!readOnly && <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={add}>Add a rule</Button>}
        />
      ) : (
        <ol className="space-y-2.5">
          {rules.map((r, i) => (
            <RuleCard
              key={i}
              rule={r}
              index={i}
              count={rules.length}
              readOnly={readOnly}
              onChange={(nr) => setRules(rules.map((x, j) => (j === i ? nr : x)))}
              onMove={(d) => setRules(move(rules, i, d))}
              onDuplicate={() => setRules([...rules.slice(0, i + 1), { ...r, name: `${r.name} copy` }, ...rules.slice(i + 1)])}
              onRemove={() => setRules(rules.filter((_, j) => j !== i))}
            />
          ))}
        </ol>
      )}
    </div>
  );
}

// ---------------------------------------------------------------- Health

export function HealthTab({ spec, onChange, readOnly }: SpecTabProps) {
  const h = spec.health;
  const setH = (p: Partial<SpecHealth>) => onChange({ ...spec, health: { ...(h ?? { method: "GET", path: "" }), ...p } });
  const q = h?.quota ?? {};
  const setQ = (p: Partial<NonNullable<SpecHealth["quota"]>>) => setH({ quota: { ...q, ...p } });
  return (
    <div className="space-y-5">
      <Section
        title="Health check"
        description="A cheap request Cogito sends with each key every few hours to catch invalid keys and read the quota left."
      >
        <Toggle
          label="Enable health checks"
          description="When off, Cogito judges keys by real requests only."
          checked={!!h}
          disabled={readOnly}
          onChange={(v) => onChange({ ...spec, health: v ? { method: "GET", path: "", query: {} } : null })}
        />
        {h && (
          <div className="mt-5 grid grid-cols-1 gap-4 sm:grid-cols-[140px_1fr]">
            <Field label="Method">
              <Select value={h.method} disabled={readOnly} onChange={(e) => setH({ method: e.target.value })}>
                <option>GET</option>
                <option>POST</option>
                <option>HEAD</option>
              </Select>
            </Field>
            <Field label="Path" hint="Relative to the base URL.">
              <Input value={h.path} disabled={readOnly} onChange={(e) => setH({ path: e.target.value })} placeholder="/account" className="font-mono" />
            </Field>
            <div className="sm:col-span-2">
              <div className="mb-1.5 text-[13px] font-medium text-ink">Query parameters</div>
              <KeyValueEditor value={h.query} disabled={readOnly} onChange={(v) => setH({ query: v })} addLabel="Add parameter" />
            </div>
          </div>
        )}
      </Section>

      {h && (
        <>
          <Section title="Key is invalid when" description="A match marks the key invalid. Any other failure counts as temporary.">
            <MatchEditor compact value={h.invalid} disabled={readOnly} onChange={(m) => setH({ invalid: Object.keys(m).length ? m : undefined })} />
          </Section>
          <Section title="Key works anyway when" description="For keys that cannot read account details but can still make calls. Quota stays unknown.">
            <MatchEditor compact value={h.ok_anyway} disabled={readOnly} onChange={(m) => setH({ ok_anyway: Object.keys(m).length ? m : undefined })} />
          </Section>
          <Section title="Quota from the response" description="JSON paths in the health check response. Several paths in one field are added together.">
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
              <Field label="Limit paths">
                <ListInput value={q.limit} disabled={readOnly} onChange={(v) => setQ({ limit: v })} placeholder="plan.limit" className="font-mono" />
              </Field>
              <Field label="Remaining paths">
                <ListInput value={q.remaining} disabled={readOnly} onChange={(v) => setQ({ remaining: v })} placeholder="plan.remaining" className="font-mono" />
              </Field>
              <Field label="Used paths" hint="When remaining is empty, Cogito uses limit minus used.">
                <ListInput value={q.used} disabled={readOnly} onChange={(v) => setQ({ used: v })} placeholder="usage.used" className="font-mono" />
              </Field>
              <Field label="Reset time path">
                <Input value={q.reset_at ?? ""} disabled={readOnly} onChange={(e) => setQ({ reset_at: e.target.value })} placeholder="plan.resets_at" className="font-mono" />
              </Field>
              <Field label="Reset format">
                <Select value={q.reset_format || "unix"} disabled={readOnly} onChange={(e) => setQ({ reset_format: e.target.value })}>
                  <option value="unix">unix seconds</option>
                  <option value="unix_ms">unix milliseconds</option>
                  <option value="iso8601">ISO 8601</option>
                </Select>
              </Field>
            </div>
            <fieldset className="mt-5 grid grid-cols-1 gap-4 sm:grid-cols-3">
              <legend className="mb-2 font-mono text-[11px] uppercase tracking-wide text-faint">Rolling window (optional)</legend>
              <Field label="Window limit path">
                <Input
                  value={q.window?.limit ?? ""}
                  disabled={readOnly}
                  onChange={(e) => setQ({ window: { limit: e.target.value, used: q.window?.used ?? "", period: q.window?.period ?? "hour" } })}
                  placeholder="rate_limit.limit"
                  className="font-mono"
                />
              </Field>
              <Field label="Window used path">
                <Input
                  value={q.window?.used ?? ""}
                  disabled={readOnly}
                  onChange={(e) => setQ({ window: { limit: q.window?.limit ?? "", used: e.target.value, period: q.window?.period ?? "hour" } })}
                  placeholder="rate_limit.used"
                  className="font-mono"
                />
              </Field>
              <Field label="Period">
                <Select
                  value={q.window?.period ?? "hour"}
                  disabled={readOnly}
                  onChange={(e) => setQ({ window: { limit: q.window?.limit ?? "", used: q.window?.used ?? "", period: e.target.value } })}
                >
                  <option value="minute">minute</option>
                  <option value="hour">hour</option>
                  <option value="day">day</option>
                </Select>
              </Field>
            </fieldset>
          </Section>
        </>
      )}
    </div>
  );
}

// ---------------------------------------------------------------- Examples

export function ExamplesTab({ spec, onChange, readOnly }: SpecTabProps) {
  const list = spec.examples;
  const setList = (l: SpecExample[]) => onChange({ ...spec, examples: l });
  const add = () => setList([...list, { name: "", method: "GET", path: "/", query: {}, body: null, safe: true }]);
  return (
    <div className="space-y-5">
      <Section
        title="Examples"
        description="Typical calls. They feed the integration snippets, and the first safe one runs during a live check."
        actions={!readOnly && <Button size="sm" icon={<Plus className="h-3.5 w-3.5" />} onClick={add}>Add example</Button>}
      />
      {list.length === 0 ? (
        <Empty title="No examples yet" body="Add one cheap call, such as a search or a list endpoint, so a live check can prove a key works." action={!readOnly && <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={add}>Add an example</Button>} />
      ) : (
        <ol className="space-y-2.5">
          {list.map((ex, i) => {
            const set = (p: Partial<SpecExample>) => setList(list.map((x, j) => (j === i ? { ...x, ...p } : x)));
            return (
              <li key={i} className="rounded-card bg-surface p-4 shadow-card">
                <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
                  <div className="flex min-w-0 items-center gap-2">
                    <Badge tone="dark">{ex.method}</Badge>
                    <span className="truncate font-mono text-[13px] text-ink">{ex.path || "/"}</span>
                    {ex.safe && <Badge tone="green">safe</Badge>}
                  </div>
                  <ItemControls
                    index={i}
                    count={list.length}
                    label={ex.name || `example ${i + 1}`}
                    disabled={readOnly}
                    onMove={(d) => setList(move(list, i, d))}
                    onDuplicate={() => setList([...list.slice(0, i + 1), { ...ex, name: `${ex.name} copy` }, ...list.slice(i + 1)])}
                    onRemove={() => setList(list.filter((_, j) => j !== i))}
                  />
                </div>
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-[1fr_120px_1fr]">
                  <Field label="Name">
                    <Input value={ex.name} disabled={readOnly} onChange={(e) => set({ name: e.target.value })} placeholder="Search" />
                  </Field>
                  <Field label="Method">
                    <Select value={ex.method} disabled={readOnly} onChange={(e) => set({ method: e.target.value })}>
                      {["GET", "POST", "PUT", "PATCH", "DELETE"].map((m) => (
                        <option key={m}>{m}</option>
                      ))}
                    </Select>
                  </Field>
                  <Field label="Path">
                    <Input value={ex.path} disabled={readOnly} onChange={(e) => set({ path: e.target.value })} placeholder="/search" className="font-mono" />
                  </Field>
                  <div className="sm:col-span-3">
                    <div className="mb-1.5 text-[13px] font-medium text-ink">Query parameters</div>
                    <KeyValueEditor value={ex.query} disabled={readOnly} onChange={(v) => set({ query: v })} addLabel="Add parameter" />
                  </div>
                  {ex.method !== "GET" && (
                    <div className="sm:col-span-3">
                      <JsonTextarea label="Body (JSON)" rows={4} value={ex.body} disabled={readOnly} onChange={(v) => set({ body: v })} />
                    </div>
                  )}
                  <div className="sm:col-span-3">
                    <Toggle
                      label="Safe to run in a live check"
                      description="Only for calls that are cheap and change nothing."
                      checked={ex.safe}
                      disabled={readOnly}
                      onChange={(v) => set({ safe: v })}
                    />
                  </div>
                </div>
              </li>
            );
          })}
        </ol>
      )}
    </div>
  );
}

// ---------------------------------------------------------------- Tests

export function TestsTab({ spec, onChange, readOnly, report }: SpecTabProps & { report: TestReport | null }) {
  const list = spec.tests;
  const setList = (l: SpecFixture[]) => onChange({ ...spec, tests: l });
  const add = () => setList([...list, { name: "", status: 429, headers: {}, body: "", expect: "rate_limited" }]);
  const resultFor = (name: string, i: number) => report?.results.find((r) => r.name === name) ?? report?.results[i];
  return (
    <div className="space-y-5">
      <Section
        title="Fixture tests"
        description="Real or documented responses, each with the outcome it should produce. Every save runs them through the real engine."
        actions={!readOnly && <Button size="sm" icon={<Plus className="h-3.5 w-3.5" />} onClick={add}>Add test</Button>}
      >
        {report && (
          <div className="flex flex-wrap items-center gap-2 font-mono text-xs">
            <Badge tone={report.failed ? "red" : "green"}>
              {report.passed} passed · {report.failed} failed
            </Badge>
            <span className="text-muted">from the latest check of this draft</span>
          </div>
        )}
      </Section>
      {list.length === 0 ? (
        <Empty
          icon={<FlaskConical className="h-5 w-5" />}
          title="No tests yet"
          body="Add at least one response per rule, such as a 429 body that should count as rate limited. Tests are what make auto publishing safe."
          action={!readOnly && <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={add}>Add a test</Button>}
        />
      ) : (
        <ol className="space-y-2.5">
          {list.map((t, i) => {
            const set = (p: Partial<SpecFixture>) => setList(list.map((x, j) => (j === i ? { ...x, ...p } : x)));
            const r = resultFor(t.name, i);
            return (
              <li key={i} className={cx("rounded-card bg-surface p-4 shadow-card", r && !r.ok && "ring-1 ring-red-200")}>
                <div className="mb-4 flex flex-wrap items-start justify-between gap-2">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      {r ? (
                        r.ok ? (
                          <CheckCircle2 className="h-4 w-4 text-emerald-700" aria-label="Passing" />
                        ) : (
                          <XCircle className="h-4 w-4 text-red-700" aria-label="Failing" />
                        )
                      ) : null}
                      <span className="truncate text-[14px] font-medium text-ink">{t.name || `Test ${i + 1}`}</span>
                      <span className="font-mono text-[11px] text-muted">{t.status}</span>
                      <span className="text-faint" aria-hidden="true">→</span>
                      <OutcomeBadge outcome={t.expect} />
                    </div>
                    {r && !r.ok && (
                      <p className="mt-1 text-xs text-red-700">
                        Got <span className="font-mono">{r.got || "nothing"}</span>
                        {r.detail ? `: ${r.detail}` : ""}
                      </p>
                    )}
                  </div>
                  <ItemControls
                    index={i}
                    count={list.length}
                    label={t.name || `test ${i + 1}`}
                    disabled={readOnly}
                    onMove={(d) => setList(move(list, i, d))}
                    onDuplicate={() => setList([...list.slice(0, i + 1), { ...t, name: `${t.name} copy` }, ...list.slice(i + 1)])}
                    onRemove={() => setList(list.filter((_, j) => j !== i))}
                  />
                </div>
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-[1fr_110px_1fr]">
                  <Field label="Name">
                    <Input value={t.name} disabled={readOnly} onChange={(e) => set({ name: e.target.value })} placeholder="Monthly quota used up" />
                  </Field>
                  <Field label="Status">
                    <Input type="number" inputMode="numeric" value={t.status} disabled={readOnly} onChange={(e) => set({ status: Number(e.target.value) })} className="font-mono" />
                  </Field>
                  <Field label="Expected outcome">
                    <Select value={t.expect} disabled={readOnly} onChange={(e) => set({ expect: e.target.value as Outcome })}>
                      {OUTCOMES.map((o) => (
                        <option key={o} value={o}>
                          {o.replace("_", " ")}
                        </option>
                      ))}
                    </Select>
                  </Field>
                  <Field label="Response body" className="sm:col-span-3">
                    <Textarea
                      rows={3}
                      value={t.body}
                      disabled={readOnly}
                      spellCheck={false}
                      onChange={(e) => set({ body: e.target.value })}
                      placeholder={'{"error":"Monthly quota exceeded."}'}
                      className="font-mono text-[12.5px]"
                    />
                  </Field>
                  <details className="sm:col-span-3">
                    <summary className="tap cursor-pointer py-1 text-[13px] font-medium text-muted hover:text-ink">
                      Response headers {t.headers && Object.keys(t.headers).length ? `(${Object.keys(t.headers).length})` : ""}
                    </summary>
                    <div className="mt-2">
                      <KeyValueEditor value={t.headers} disabled={readOnly} onChange={(v) => set({ headers: v })} keyLabel="Header" addLabel="Add header" />
                    </div>
                  </details>
                </div>
              </li>
            );
          })}
        </ol>
      )}
    </div>
  );
}

// ---------------------------------------------------------------- Raw JSON

export function JsonTab({ spec, onChange, readOnly }: SpecTabProps) {
  return (
    <Section title="Raw spec" description="The whole spec as JSON. Edits apply as soon as the JSON is valid, and the other tabs pick them up.">
      <JsonTextarea label="Spec JSON" rows={28} value={spec} disabled={readOnly} onChange={(v) => v && typeof v === "object" && onChange(v as ProviderSpec)} />
    </Section>
  );
}
