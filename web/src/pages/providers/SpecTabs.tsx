import { cloneElement, useId, useLayoutEffect, useRef, useState, type ReactElement, type ReactNode } from "react";
import {
  RiAddLine,
  RiArrowDownLine,
  RiArrowDownSLine,
  RiArrowRightLine,
  RiArrowUpLine,
  RiCheckboxCircleFill,
  RiCloseCircleFill,
  RiDeleteBinLine,
  RiFileCopyLine,
  RiFlaskLine,
  RiListOrdered2,
} from "@remixicon/react";
import { Focusable } from "react-aria-components";
import type { ComponentType } from "react";
import type { AuthType, OfferLimit, OfferPer, Outcome, ProviderSpec, QuotaHeaders, RetrySource, SpecExample, SpecFixture, SpecHealth, SpecOffer, SpecRule, Strategy, TestReport } from "../../lib/types";
import { LIMIT_PERS, PRICE_UNITS, matchText } from "../../lib/offers";
import { OUTCOMES } from "../../lib/types";
import { STRATEGY_HELP } from "../../lib/format";
import { ConcurrencyFields, StrategySelect } from "../../components/ServiceForm";
import { JsonTextarea, KeyValueEditor, ListInput, MatchEditor, OptionalNumberInput } from "../../components/SpecInputs";
import { Chip, type ChipColor } from "@/components/base/badges/chip";
import { Button } from "@/components/base/buttons/button";
import { IconButton } from "@/components/base/buttons/icon-button";
import { TextField, InputBase, type InputBaseProps } from "@/components/base/input/input";
import { Label } from "@/components/base/input/label";
import { hintTextClassName } from "@/components/base/input/hint-text";
import { Select, SelectItem } from "@/components/base/select/select";
import { Switch } from "@/components/base/switch/switch";
import { TextareaBase } from "@/components/base/textarea/textarea";
import { Tooltip, TooltipTrigger } from "@/components/base/tooltip/tooltip";
import { cx } from "@/utils/cx";
import { EmptyState, Section } from "@/components/acn";

type IconComponent = ComponentType<{ className?: string; "aria-hidden"?: boolean | "true" | "false" }>;

export interface SpecTabProps {
  spec: ProviderSpec;
  onChange: (s: ProviderSpec) => void;
  readOnly?: boolean;
}

const OUTCOME_LABEL: Record<Outcome, string> = {
  ok: "ok (the key is fine)",
  rate_limited: "rate limited (cool down)",
  busy: "busy (at its concurrency cap, steps aside about a second, no backoff)",
  quota_exhausted: "quota exhausted (park until reset)",
  invalid: "invalid (disable key)",
  banned: "banned (account suspended, out of rotation until an operator resets it)",
  error: "error (provider problem, key keeps working)",
};

const OUTCOME_TONE: Record<string, ChipColor> = {
  ok: "lime",
  rate_limited: "yellow",
  busy: "yellow",
  quota_exhausted: "yellow",
  invalid: "rose",
  banned: "rose",
  error: "neutral",
};

export function OutcomeBadge({ outcome }: { outcome: string }) {
  return (
    <Chip variant="caption" color={OUTCOME_TONE[outcome] ?? "neutral"} className="font-mono">
      {outcome.replace("_", " ") || "none"}
    </Chip>
  );
}

// ---------------------------------------------------------------- Fields

/** Mono, small eyebrow above a group of fields. */
const LEGEND = "mb-1 text-caption-2-semibold uppercase text-text-tertiary";

/**
 * A text field: BoardUI TextField, Label, InputBase and hint, composed here so
 * native input attributes (min, step, spellCheck) still reach the input.
 */
function TextInput({
  label,
  hint,
  isRequired,
  mono,
  value,
  onChange,
  disabled,
  className,
  ...input
}: {
  label: ReactNode;
  hint?: ReactNode;
  isRequired?: boolean;
  mono?: boolean;
  value: string | number;
  onChange: (v: string) => void;
  disabled?: boolean;
  className?: string;
} & Omit<InputBaseProps, "value" | "onChange" | "disabled" | "className">) {
  return (
    <TextField
      value={String(value)}
      onChange={onChange}
      isDisabled={disabled}
      isRequired={isRequired}
      inputClassName={mono ? "font-mono" : undefined}
      className={cx("min-w-0", className)}
    >
      <Label isRequired={isRequired}>{label}</Label>
      <InputBase {...input} />
      {hint && <p className={hintTextClassName(false)}>{hint}</p>}
    </TextField>
  );
}

/**
 * Label and hint around a control that is not a BoardUI field (the shared
 * spec inputs, the strategy select). The control gets the id and the hint id.
 */
function FieldShell({ label, hint, children, className }: { label: ReactNode; hint?: ReactNode; children: ReactElement<Record<string, unknown>>; className?: string }) {
  const id = useId();
  const hintId = `${id}-hint`;
  const control = cloneElement(children, { id, "aria-describedby": hint ? hintId : undefined });
  return (
    <div className={cx("flex min-w-0 flex-col gap-1", className)}>
      <Label htmlFor={id}>{label}</Label>
      {control}
      {hint && (
        <p id={hintId} className={hintTextClassName(false)}>
          {hint}
        </p>
      )}
    </div>
  );
}

/** BoardUI Select with string keys, a label and an optional hint. */
function SelectField({
  label,
  hint,
  value,
  onChange,
  options,
  disabled,
  className,
}: {
  label: ReactNode;
  hint?: ReactNode;
  value: string;
  onChange: (v: string) => void;
  options: Array<{ id: string; label: string }>;
  disabled?: boolean;
  className?: string;
}) {
  const hintId = useId();
  return (
    <div className={cx("flex min-w-0 flex-col gap-1", className)}>
      <Select
        label={label}
        selectedKey={value}
        onSelectionChange={(k) => k != null && onChange(String(k))}
        isDisabled={disabled}
        aria-describedby={hint ? hintId : undefined}
        className="w-full"
        popoverClassName="w-(--trigger-width) min-w-[240px]"
      >
        {options.map((o) => (
          <SelectItem key={o.id} id={o.id} textValue={o.label}>
            {o.label}
          </SelectItem>
        ))}
      </Select>
      {hint && (
        <p id={hintId} className={hintTextClassName(false)}>
          {hint}
        </p>
      )}
    </div>
  );
}

/** A labelled switch row with a description under the label. */
function ToggleRow({
  label,
  description,
  checked,
  onChange,
  disabled,
}: {
  label: string;
  description?: string;
  checked: boolean;
  onChange: (v: boolean) => void;
  disabled?: boolean;
}) {
  const descId = useId();
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <Switch
        size="sm"
        isSelected={checked}
        onChange={onChange}
        isDisabled={disabled}
        aria-describedby={description ? descId : undefined}
        className="w-full flex-row-reverse justify-between gap-4"
      >
        {label}
      </Switch>
      {description && (
        <p id={descId} className="max-w-2xl pe-14 text-caption-1-regular text-text-secondary">
          {description}
        </p>
      )}
    </div>
  );
}

/** A small subheading for a sub editor (query parameters, headers). */
function SubLabel({ children }: { children: ReactNode }) {
  return <div className="mb-1.5 text-body-medium text-text-primary">{children}</div>;
}

/** Icon only button with a tooltip and a 44px hit area. */
function RowIconButton({
  icon,
  label,
  onClick,
  disabled,
  danger,
  move,
}: {
  icon: IconComponent;
  label: string;
  onClick: () => void;
  disabled?: boolean;
  danger?: boolean;
  /** Marks the up and down arrows, so focus can return to them after a move. */
  move?: boolean;
}) {
  const button = (
    <IconButton
      icon={icon}
      size="small"
      aria-label={label}
      data-move={move ? "" : undefined}
      disabled={disabled}
      onClick={onClick}
      className={cx("after:absolute after:-inset-1.5 after:content-['']", danger && "hover:text-text-error-primary")}
    />
  );
  // A disabled button cannot take focus, so it gets no tooltip.
  if (disabled) return button;
  return (
    <TooltipTrigger delay={400}>
      <Focusable>{button}</Focusable>
      <Tooltip>{label}</Tooltip>
    </TooltipTrigger>
  );
}

function move<T>(list: T[], i: number, d: -1 | 1): T[] {
  const j = i + d;
  if (j < 0 || j >= list.length) return list;
  const next = list.slice();
  [next[i], next[j]] = [next[j], next[i]];
  return next;
}

/**
 * Stable keys, slide and focus for an ordered list edited with up, down,
 * duplicate and delete. Keys follow items when they move, so an open rule
 * stays open on the rule you moved. Rows that change place slide there
 * (FLIP), and focus returns to the arrow you pressed.
 */
function useOrderedList(length: number) {
  const seq = useRef(0);
  const keys = useRef<number[]>([]);
  // Items added or removed elsewhere (the add button, the JSON tab): grow or trim at the end.
  const added = useRef<number | null>(null);
  const mounted = useRef(false);
  if (keys.current.length < length) {
    const grow = Array.from({ length: length - keys.current.length }, () => ++seq.current);
    // One item added after the first render (the add button): let it fade in.
    if (mounted.current && grow.length === 1) added.current = grow[0];
    keys.current = [...keys.current, ...grow];
  } else if (keys.current.length > length) keys.current = keys.current.slice(0, length);
  const els = useRef(new Map<number, HTMLElement>());
  const tops = useRef<Map<number, number> | null>(null);
  const focus = useRef<{ key: number; dir: -1 | 1 } | null>(null);

  useLayoutEffect(() => {
    mounted.current = true;
    const before = tops.current;
    tops.current = null;
    if (before && !window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      for (const [k, el] of els.current) {
        const was = before.get(k);
        if (was === undefined) continue;
        const dy = was - el.getBoundingClientRect().top;
        if (Math.abs(dy) < 1) continue;
        el.animate([{ transform: `translateY(${dy}px)` }, { transform: "translateY(0)" }], { duration: 240, easing: "cubic-bezier(0.77, 0, 0.175, 1)" });
      }
    }
    const f = focus.current;
    focus.current = null;
    if (f) {
      const btns = els.current.get(f.key)?.querySelectorAll<HTMLButtonElement>("button[data-move]");
      const want = btns?.[f.dir === -1 ? 0 : 1];
      const other = btns?.[f.dir === -1 ? 1 : 0];
      (want && !want.disabled ? want : other)?.focus({ preventScroll: true });
    }
  });

  const snapshot = () => {
    tops.current = new Map([...els.current].map(([k, el]) => [k, el.getBoundingClientRect().top]));
  };
  return {
    key: (i: number) => keys.current[i],
    ref: (i: number) => {
      // Bound to the key at render time, so detaching an old row clears its own entry.
      const k = keys.current[i];
      return (el: HTMLElement | null) => {
        if (el) els.current.set(k, el);
        else els.current.delete(k);
      };
    },
    /** True for the copy a duplicate just made, so it can fade in. */
    isNew: (i: number) => added.current === keys.current[i],
    move: (i: number, d: -1 | 1) => {
      const j = i + d;
      if (j < 0 || j >= keys.current.length) return;
      snapshot();
      const next = keys.current.slice();
      [next[i], next[j]] = [next[j], next[i]];
      keys.current = next;
      focus.current = { key: next[j], dir: d };
    },
    duplicate: (i: number) => {
      snapshot();
      const k = ++seq.current;
      added.current = k;
      keys.current = [...keys.current.slice(0, i + 1), k, ...keys.current.slice(i + 1)];
    },
    remove: (i: number) => {
      snapshot();
      keys.current = keys.current.filter((_, j) => j !== i);
    },
  };
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
    <div className="ms-auto flex shrink-0 items-center gap-1.5">
      <RowIconButton move icon={RiArrowUpLine} label={`Move ${label} up`} disabled={disabled || index === 0} onClick={() => onMove(-1)} />
      <RowIconButton move icon={RiArrowDownLine} label={`Move ${label} down`} disabled={disabled || index === count - 1} onClick={() => onMove(1)} />
      <RowIconButton icon={RiFileCopyLine} label={`Duplicate ${label}`} disabled={disabled} onClick={onDuplicate} />
      <RowIconButton danger icon={RiDeleteBinLine} label={`Delete ${label}`} disabled={disabled} onClick={onRemove} />
    </div>
  );
}

/** The card an ordered list item sits in. */
const ITEM_CARD = "rounded-3xl border border-border-button-default bg-background-primary-default";

// ---------------------------------------------------------------- Connection

export function ConnectionTab({ spec, onChange, readOnly }: SpecTabProps) {
  const set = (p: Partial<ProviderSpec>) => onChange({ ...spec, ...p });
  const d = spec.defaults;
  const setD = (p: Partial<ProviderSpec["defaults"]>) => set({ defaults: { ...d, ...p } });
  const quota = spec.quota ?? { unit: "", counts_per_request: false };
  const qh: QuotaHeaders = quota.headers ?? {};
  // Empty header fields are cleared to undefined so they never reach the server as "".
  const setQH = (p: Partial<QuotaHeaders>) => {
    const next: QuotaHeaders = { ...qh, ...p };
    for (const k of Object.keys(next) as Array<keyof QuotaHeaders>) if (next[k] === "" || next[k] === undefined) delete next[k];
    set({ quota: { ...quota, headers: Object.keys(next).length ? next : undefined } });
  };
  return (
    <div className="flex flex-col gap-5">
      <Section title="Upstream" description="Where requests go and where the key goes on each request.">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <TextInput
            label="Base URL"
            isRequired
            className="sm:col-span-3"
            type="url"
            inputMode="url"
            dir="ltr"
            mono
            value={spec.base_url}
            disabled={readOnly}
            onChange={(v) => set({ base_url: v })}
            placeholder="https://api.example.com"
            spellCheck={false}
          />
          <SelectField
            label="Auth type"
            value={spec.auth.type}
            disabled={readOnly}
            onChange={(v) => set({ auth: { ...spec.auth, type: v as AuthType } })}
            options={[
              { id: "query", label: "query parameter" },
              { id: "header", label: "header" },
              { id: "bearer", label: "bearer token" },
            ]}
          />
          <TextInput
            label={spec.auth.type === "query" ? "Parameter name" : "Header name"}
            mono
            value={spec.auth.name}
            disabled={readOnly}
            onChange={(v) => set({ auth: { ...spec.auth, name: v } })}
            placeholder={spec.auth.type === "query" ? "api_key" : "Authorization"}
            spellCheck={false}
          />
          <TextInput
            label="Value prefix"
            hint={spec.auth.type === "bearer" ? "Bearer adds its own prefix." : "Text before the key, if the API needs any."}
            mono
            value={spec.auth.prefix}
            disabled={readOnly}
            onChange={(v) => set({ auth: { ...spec.auth, prefix: v } })}
            placeholder="Token "
            spellCheck={false}
          />
        </div>
      </Section>

      <Section title="Defaults for new services" description="Services created from this provider start with these values. Existing services keep their own settings.">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <FieldShell label="Strategy" hint={STRATEGY_HELP[d.strategy]}>
            <StrategySelect value={d.strategy as Strategy} disabled={readOnly} onChange={(s) => setD({ strategy: s })} />
          </FieldShell>
          <TextInput
            label="Cooldown (sec)"
            hint="How long a key rests after a rate limit."
            type="number"
            min={0}
            inputMode="numeric"
            mono
            value={d.cooldown_sec}
            disabled={readOnly}
            onChange={(v) => setD({ cooldown_sec: Number(v) })}
          />
          <TextInput
            label="Max attempts"
            hint="How many keys the proxy tries."
            type="number"
            min={1}
            max={20}
            inputMode="numeric"
            mono
            value={d.max_attempts}
            disabled={readOnly}
            onChange={(v) => setD({ max_attempts: Number(v) })}
          />
        </div>
        <div className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2">
          <ConcurrencyFields maxConcurrency={d.max_concurrency} queueSec={d.queue_timeout_sec} disabled={readOnly} onChange={(p) => setD(p)} />
        </div>
        <div className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-3">
          <OptionalNumberInput label="Response cache (sec)" hint="How long identical successful responses are reused. Empty or 0 turns caching off." min={0} value={d.cache_ttl_sec} disabled={readOnly} onChange={(v) => setD({ cache_ttl_sec: v })} placeholder="off" />
        </div>
        <fieldset className="mt-6 grid grid-cols-1 gap-4 border-t border-separator-border pt-5 sm:grid-cols-3">
          <legend className={cx(LEGEND, "float-left w-full")}>Circuit breaker</legend>
          <p className="text-body-2-regular text-text-secondary sm:col-span-3">
            Pauses the whole service after too many provider errors in a row. Leave empty or 0 for the defaults: 10 errors in 60s pause it for 30s. A threshold of -1 turns the breaker off.
          </p>
          <OptionalNumberInput label="Error threshold" min={-1} value={d.breaker_threshold} disabled={readOnly} onChange={(v) => setD({ breaker_threshold: v })} placeholder="10" />
          <OptionalNumberInput label="Window (sec)" min={0} value={d.breaker_window_sec} disabled={readOnly} onChange={(v) => setD({ breaker_window_sec: v })} placeholder="60" />
          <OptionalNumberInput label="Pause (sec)" min={0} value={d.breaker_reset_sec} disabled={readOnly} onChange={(v) => setD({ breaker_reset_sec: v })} placeholder="30" />
        </fieldset>
      </Section>

      <Section title="Quota" description="How Acnologia counts usage between health checks.">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <TextInput
            label="Unit"
            hint="Shows next to quota numbers, such as searches or characters."
            value={quota.unit}
            disabled={readOnly}
            onChange={(v) => set({ quota: { ...quota, unit: v } })}
            placeholder="requests"
          />
          <div className="sm:pt-7">
            <ToggleRow
              label="Each successful request uses one unit"
              description="Counts down the remaining quota on each success. Leave off when the cost changes per request."
              checked={quota.counts_per_request}
              disabled={readOnly}
              onChange={(v) => set({ quota: { ...quota, counts_per_request: v } })}
            />
          </div>
          <OptionalNumberInput label="Cost per request (USD)" hint="Used for spend totals and app budgets when no offer prices the request. Empty means free or unknown." min={0} value={quota.cost_per_request} disabled={readOnly} onChange={(v) => set({ quota: { ...quota, cost_per_request: v } })} placeholder="0.002" />
          <TextInput
            label="Free tier"
            hint="Short note shown in the catalog."
            value={quota.free_tier ?? ""}
            disabled={readOnly}
            onChange={(v) => set({ quota: { ...quota, free_tier: v || undefined } })}
            placeholder="1,000 searches a month"
          />
        </div>
        <fieldset className="mt-6 grid grid-cols-1 gap-4 border-t border-separator-border pt-5 sm:grid-cols-3">
          <legend className={cx(LEGEND, "float-left w-full")}>Quota from response headers (optional)</legend>
          <p className="text-body-2-regular text-text-secondary sm:col-span-3">Header names Acnologia reads on every response to keep the remaining quota fresh.</p>
          <TextInput label="Limit header" mono value={qh.limit ?? ""} disabled={readOnly} onChange={(v) => setQH({ limit: v })} placeholder="X-RateLimit-Limit" spellCheck={false} />
          <TextInput label="Remaining header" mono value={qh.remaining ?? ""} disabled={readOnly} onChange={(v) => setQH({ remaining: v })} placeholder="X-RateLimit-Remaining" spellCheck={false} />
          <TextInput label="Reset header" mono value={qh.reset ?? ""} disabled={readOnly} onChange={(v) => setQH({ reset: v })} placeholder="X-RateLimit-Reset" spellCheck={false} />
          <SelectField
            label="Reset format"
            value={qh.reset_format || "none"}
            disabled={readOnly}
            onChange={(v) => setQH({ reset_format: (v === "none" ? "" : v) as QuotaHeaders["reset_format"] })}
            options={[
              { id: "none", label: "not set" },
              { id: "seconds", label: "seconds from now" },
              { id: "unix", label: "unix seconds" },
              { id: "unix_ms", label: "unix milliseconds" },
              { id: "iso8601", label: "ISO 8601" },
              { id: "http_date", label: "HTTP date" },
            ]}
          />
          <OptionalNumberInput label="Index" hint="Pick entry N of a comma list such as 1, 2000; use the longest window." className="sm:col-span-2" min={0} step={1} value={qh.index} disabled={readOnly} onChange={(v) => setQH({ index: v })} placeholder="0" />
        </fieldset>
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
  rowRef,
  enter,
}: {
  rule: SpecRule;
  index: number;
  count: number;
  onChange: (r: SpecRule) => void;
  onMove: (d: -1 | 1) => void;
  onDuplicate: () => void;
  onRemove: () => void;
  readOnly?: boolean;
  rowRef?: (el: HTMLElement | null) => void;
  enter?: boolean;
}) {
  const [open, setOpen] = useState(!rule.name);
  const panelId = useId();
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
    <li ref={rowRef} className={cx(ITEM_CARD, enter && "fade-up")}>
      <div className="flex flex-wrap items-center gap-2 px-3 py-3 sm:flex-nowrap sm:px-4">
        <span className="flex size-6 shrink-0 items-center justify-center rounded-md bg-background-secondary-default font-mono text-caption-1-medium text-text-secondary">
          {index + 1}
        </span>
        <button
          type="button"
          onClick={() => setOpen(!open)}
          aria-expanded={open}
          aria-controls={panelId}
          className="flex min-h-11 min-w-0 flex-1 basis-[60%] cursor-pointer items-center gap-2 rounded-xl text-start outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring"
        >
          <span className="min-w-0 flex-1">
            <span className="block truncate text-body-medium text-text-primary">{rule.name || "Untitled rule"}</span>
            <span className="block truncate font-mono text-caption-1-regular text-text-secondary">{summary}</span>
          </span>
          <OutcomeBadge outcome={rule.outcome} />
          <RiArrowDownSLine
            aria-hidden
            className={cx("size-4 shrink-0 text-foreground-icon-tertiary transition-transform duration-200 ease-out motion-reduce:transition-none", open && "rotate-180")}
          />
        </button>
        <ItemControls index={index} count={count} label={rule.name || `rule ${index + 1}`} onMove={onMove} onDuplicate={onDuplicate} onRemove={onRemove} disabled={readOnly} />
      </div>
      {open && (
        <div id={panelId} className="fade-up flex flex-col gap-4 border-t border-separator-border px-4 py-4">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <TextInput
              label="Name"
              hint="A short label shown in test reports."
              value={rule.name}
              disabled={readOnly}
              onChange={(v) => onChange({ ...rule, name: v })}
              placeholder="Monthly quota used up"
            />
            <SelectField
              label="Outcome"
              value={rule.outcome}
              disabled={readOnly}
              onChange={(v) => onChange({ ...rule, outcome: v as Outcome })}
              options={OUTCOMES.map((o) => ({ id: o, label: OUTCOME_LABEL[o] }))}
            />
          </div>
          <fieldset>
            <legend className={cx(LEGEND, "mb-2")}>Match when</legend>
            <MatchEditor value={rule.match} disabled={readOnly} onChange={(m) => onChange({ ...rule, match: m })} />
          </fieldset>
          {rule.outcome === "rate_limited" && (
            <fieldset className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <legend className={cx(LEGEND, "mb-2 float-left w-full")}>Retry after</legend>
              <SelectField
                label="Source"
                hint="Where the rest time comes from. None uses the service cooldown."
                value={src}
                disabled={readOnly}
                onChange={(v) => {
                  const s = v as RetrySource;
                  onChange({ ...rule, retry_after: s === "none" ? undefined : { source: s, value: rule.retry_after?.value ?? "" } });
                }}
                options={RETRY_SOURCES.map((s) => ({ id: s.id, label: s.label }))}
              />
              {srcMeta?.value && (
                <TextInput
                  label={srcMeta.value}
                  mono
                  value={rule.retry_after?.value ?? ""}
                  disabled={readOnly}
                  inputMode={src === "fixed" ? "numeric" : undefined}
                  onChange={(v) => onChange({ ...rule, retry_after: { source: src, value: v } })}
                  placeholder={src === "header" ? "Retry-After" : src === "fixed" ? "60" : "error.retry_after"}
                  spellCheck={false}
                />
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
  const order = useOrderedList(rules.length);
  return (
    <div className="flex flex-col gap-5">
      <Section
        title="Classification rules"
        description="Checked top to bottom on every error response, and the first match wins. Every field in a rule must match. Any value in a list can match."
        actions={
          !readOnly && (
            <Button size="small" variant="secondary" leadingIcon={RiAddLine} onClick={add}>
              Add rule
            </Button>
          )
        }
      >
        <ToggleRow
          label="Fall back to standard rules"
          description="Runs after your rules. 429 is rate limited (respects Retry-After), 402 is out of quota, 401 and 403 are invalid, 5xx is an error."
          checked={spec.use_standard_rules}
          disabled={readOnly}
          onChange={(v) => onChange({ ...spec, use_standard_rules: v })}
        />
      </Section>
      {rules.length === 0 ? (
        <EmptyState
          icon={RiListOrdered2}
          title="No custom rules"
          body={
            spec.use_standard_rules
              ? "The standard rules apply. Add a rule when the API signals limits its own way, such as a 200 with an error in the body."
              : "Nothing sorts errors yet. Add a rule, or turn on the standard rules."
          }
          action={
            !readOnly && (
              <Button variant="primary" leadingIcon={RiAddLine} onClick={add}>
                Add a rule
              </Button>
            )
          }
        />
      ) : (
        <ol className="flex flex-col gap-3">
          {rules.map((r, i) => (
            <RuleCard
              key={order.key(i)}
              rowRef={order.ref(i)}
              enter={order.isNew(i)}
              rule={r}
              index={i}
              count={rules.length}
              readOnly={readOnly}
              onChange={(nr) => setRules(rules.map((x, j) => (j === i ? nr : x)))}
              onMove={(d) => {
                order.move(i, d);
                setRules(move(rules, i, d));
              }}
              onDuplicate={() => {
                order.duplicate(i);
                setRules([...rules.slice(0, i + 1), { ...r, name: `${r.name} copy` }, ...rules.slice(i + 1)]);
              }}
              onRemove={() => {
                order.remove(i);
                setRules(rules.filter((_, j) => j !== i));
              }}
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
    <div className="flex flex-col gap-5">
      <Section title="Health check" description="A cheap request Acnologia sends with each key every few hours to catch invalid keys and read the quota left.">
        <ToggleRow
          label="Enable health checks"
          description="When off, Acnologia judges keys by real requests only."
          checked={!!h}
          disabled={readOnly}
          onChange={(v) => onChange({ ...spec, health: v ? { method: "GET", path: "", query: {} } : null })}
        />
        {h && (
          <div className="mt-5 grid grid-cols-1 gap-4 sm:grid-cols-[140px_1fr]">
            <SelectField
              label="Method"
              value={h.method}
              disabled={readOnly}
              onChange={(v) => setH({ method: v })}
              options={["GET", "POST", "HEAD"].map((m) => ({ id: m, label: m }))}
            />
            <TextInput
              label="Path"
              hint="Relative to the base URL."
              mono
              value={h.path}
              disabled={readOnly}
              onChange={(v) => setH({ path: v })}
              placeholder="/account"
              spellCheck={false}
            />
            <div className="sm:col-span-2">
              <SubLabel>Query parameters</SubLabel>
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
              <ListInput label="Limit paths" value={q.limit} disabled={readOnly} onChange={(v) => setQ({ limit: v })} placeholder="plan.limit" />
              <ListInput label="Remaining paths" value={q.remaining} disabled={readOnly} onChange={(v) => setQ({ remaining: v })} placeholder="plan.remaining" />
              <ListInput label="Used paths" hint="When remaining is empty, Acnologia uses limit minus used." value={q.used} disabled={readOnly} onChange={(v) => setQ({ used: v })} placeholder="usage.used" />
              <TextInput
                label="Reset time path"
                mono
                value={q.reset_at ?? ""}
                disabled={readOnly}
                onChange={(v) => setQ({ reset_at: v })}
                placeholder="plan.resets_at"
                spellCheck={false}
              />
              <SelectField
                label="Reset format"
                value={q.reset_format || "unix"}
                disabled={readOnly}
                onChange={(v) => setQ({ reset_format: v })}
                options={[
                  { id: "unix", label: "unix seconds" },
                  { id: "unix_ms", label: "unix milliseconds" },
                  { id: "iso8601", label: "ISO 8601" },
                ]}
              />
            </div>
            <fieldset className="mt-6 grid grid-cols-1 gap-4 border-t border-separator-border pt-5 sm:grid-cols-3">
              <legend className={cx(LEGEND, "float-left w-full")}>Rolling window (optional)</legend>
              <TextInput
                label="Window limit path"
                mono
                value={q.window?.limit ?? ""}
                disabled={readOnly}
                onChange={(v) => setQ({ window: { limit: v, used: q.window?.used ?? "", period: q.window?.period ?? "hour" } })}
                placeholder="rate_limit.limit"
                spellCheck={false}
              />
              <TextInput
                label="Window used path"
                mono
                value={q.window?.used ?? ""}
                disabled={readOnly}
                onChange={(v) => setQ({ window: { limit: q.window?.limit ?? "", used: v, period: q.window?.period ?? "hour" } })}
                placeholder="rate_limit.used"
                spellCheck={false}
              />
              <SelectField
                label="Period"
                value={q.window?.period ?? "hour"}
                disabled={readOnly}
                onChange={(v) => setQ({ window: { limit: q.window?.limit ?? "", used: q.window?.used ?? "", period: v } })}
                options={["minute", "hour", "day"].map((p) => ({ id: p, label: p }))}
              />
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
  const order = useOrderedList(list.length);
  return (
    <div className="flex flex-col gap-5">
      <Section
        title="Examples"
        description="Typical calls. They feed the integration snippets, and the first safe one runs during a live check."
        actions={
          !readOnly && (
            <Button size="small" variant="secondary" leadingIcon={RiAddLine} onClick={add}>
              Add example
            </Button>
          )
        }
      />
      {list.length === 0 ? (
        <EmptyState
          title="No examples yet"
          body="Add one cheap call, such as a search or a list endpoint, so a live check can prove a key works."
          action={
            !readOnly && (
              <Button variant="primary" leadingIcon={RiAddLine} onClick={add}>
                Add an example
              </Button>
            )
          }
        />
      ) : (
        <ol className="flex flex-col gap-3">
          {list.map((ex, i) => {
            const set = (p: Partial<SpecExample>) => setList(list.map((x, j) => (j === i ? { ...x, ...p } : x)));
            return (
              <li key={order.key(i)} ref={order.ref(i)} className={cx(ITEM_CARD, "p-4 sm:p-5", order.isNew(i) && "fade-up")}>
                <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
                  <div className="flex min-w-0 items-center gap-2">
                    <Chip variant="caption" color="gray" className="font-mono">
                      {ex.method}
                    </Chip>
                    <span className="truncate font-mono text-body-2-regular text-text-primary">{ex.path || "/"}</span>
                    {ex.safe && (
                      <Chip variant="caption" color="lime" className="font-mono">
                        safe
                      </Chip>
                    )}
                  </div>
                  <ItemControls
                    index={i}
                    count={list.length}
                    label={ex.name || `example ${i + 1}`}
                    disabled={readOnly}
                    onMove={(d) => {
                      order.move(i, d);
                      setList(move(list, i, d));
                    }}
                    onDuplicate={() => {
                      order.duplicate(i);
                      setList([...list.slice(0, i + 1), { ...ex, name: `${ex.name} copy` }, ...list.slice(i + 1)]);
                    }}
                    onRemove={() => {
                      order.remove(i);
                      setList(list.filter((_, j) => j !== i));
                    }}
                  />
                </div>
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-[1fr_130px_1fr]">
                  <TextInput label="Name" value={ex.name} disabled={readOnly} onChange={(v) => set({ name: v })} placeholder="Search" />
                  <SelectField
                    label="Method"
                    value={ex.method}
                    disabled={readOnly}
                    onChange={(v) => set({ method: v })}
                    options={["GET", "POST", "PUT", "PATCH", "DELETE"].map((m) => ({ id: m, label: m }))}
                  />
                  <TextInput label="Path" mono value={ex.path} disabled={readOnly} onChange={(v) => set({ path: v })} placeholder="/search" spellCheck={false} />
                  <div className="sm:col-span-3">
                    <SubLabel>Query parameters</SubLabel>
                    <KeyValueEditor value={ex.query} disabled={readOnly} onChange={(v) => set({ query: v })} addLabel="Add parameter" />
                  </div>
                  {ex.method !== "GET" && (
                    <div className="sm:col-span-3">
                      <JsonTextarea label="Body (JSON)" rows={4} value={ex.body} disabled={readOnly} onChange={(v) => set({ body: v })} />
                    </div>
                  )}
                  <div className="sm:col-span-3">
                    <ToggleRow
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

// ---------------------------------------------------------------- Offers

const ANY = "any";

/** Drops empty match fields; no method, path or examples means provider wide. */
function cleanMatch(m: NonNullable<SpecOffer["match"]>): SpecOffer["match"] {
  const out: NonNullable<SpecOffer["match"]> = {};
  if (m.method) out.method = m.method;
  if (m.path) out.path = m.path;
  if (m.examples?.length) out.examples = m.examples;
  return Object.keys(out).length ? out : undefined;
}

export function OffersTab({ spec, onChange, readOnly }: SpecTabProps) {
  const list = spec.offers ?? [];
  const setList = (l: SpecOffer[]) => onChange({ ...spec, offers: l });
  const today = new Date().toISOString().slice(0, 10);
  const add = () => setList([...list, { name: "", free: false, limits: [], checked_at: today }]);
  const order = useOrderedList(list.length);
  const exampleNames = spec.examples.map((e) => e.name).filter(Boolean);
  return (
    <div className="flex flex-col gap-5">
      <Section
        title="Offers"
        description="Prices, free allowances and published limits per product or endpoint, from the provider's pricing page. Acnologia keeps every key under an offer's limits, prices requests from it, and shows it to people and AI clients."
        actions={
          !readOnly && (
            <Button size="small" variant="secondary" leadingIcon={RiAddLine} onClick={add}>
              Add offer
            </Button>
          )
        }
      />
      {list.length === 0 ? (
        <EmptyState
          title="No offers yet"
          body="Add what the pricing page says, such as a free search endpoint with 30 requests a minute. Without offers, Acnologia uses the cost per request on the Connection tab."
          action={
            !readOnly && (
              <Button variant="primary" leadingIcon={RiAddLine} onClick={add}>
                Add an offer
              </Button>
            )
          }
        />
      ) : (
        <ol className="flex flex-col gap-3">
          {list.map((o, i) => {
            const set = (p: Partial<SpecOffer>) => setList(list.map((x, j) => (j === i ? { ...x, ...p } : x)));
            const m = o.match ?? {};
            const setMatch = (p: Partial<NonNullable<SpecOffer["match"]>>) => set({ match: cleanMatch({ ...m, ...p }) });
            const price = o.price;
            const setPrice = (p: Partial<NonNullable<SpecOffer["price"]>>) => {
              const next = { amount: 0, unit: "request", ...price, ...p };
              set({ price: next.unit ? next : undefined });
            };
            const limits = o.limits ?? [];
            const setLimits = (l: OfferLimit[]) => set({ limits: l });
            return (
              <li key={order.key(i)} ref={order.ref(i)} className={cx(ITEM_CARD, "p-4 sm:p-5", order.isNew(i) && "fade-up")}>
                <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
                  <div className="flex min-w-0 items-center gap-2">
                    <span className="truncate text-body-medium text-text-primary">{o.name || `Offer ${i + 1}`}</span>
                    {o.free && (
                      <Chip variant="caption" color="lime" className="font-mono">
                        Free
                      </Chip>
                    )}
                    <span className="truncate font-mono text-caption-1-regular text-text-tertiary">{matchText(o)}</span>
                  </div>
                  <ItemControls
                    index={i}
                    count={list.length}
                    label={o.name || `offer ${i + 1}`}
                    disabled={readOnly}
                    onMove={(d) => {
                      order.move(i, d);
                      setList(move(list, i, d));
                    }}
                    onDuplicate={() => {
                      order.duplicate(i);
                      setList([...list.slice(0, i + 1), { ...o, name: `${o.name} copy` }, ...list.slice(i + 1)]);
                    }}
                    onRemove={() => {
                      order.remove(i);
                      setList(list.filter((_, j) => j !== i));
                    }}
                  />
                </div>
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
                  <TextInput label="Name" isRequired value={o.name} disabled={readOnly} onChange={(v) => set({ name: v })} placeholder="TinySearch" />
                  <div className="sm:col-span-2 sm:pt-7">
                    <ToggleRow
                      label="Free"
                      description="Requests under this offer cost nothing, within its limits."
                      checked={!!o.free}
                      disabled={readOnly}
                      onChange={(v) => set(v ? { free: true, price: price ? { ...price, amount: 0 } : undefined } : { free: false })}
                    />
                  </div>

                  <fieldset className="grid grid-cols-1 gap-4 border-t border-separator-border pt-4 sm:col-span-3 sm:grid-cols-[130px_1fr_1fr]">
                    <legend className={cx(LEGEND, "float-left w-full")}>Covers</legend>
                    <p className="text-body-2-regular text-text-secondary sm:col-span-3">
                      Leave all three empty for an offer that covers every request. The path is relative to the base URL and matches as a prefix; * matches inside one segment and a trailing $ makes it exact.
                    </p>
                    <SelectField
                      label="Method"
                      value={m.method || ANY}
                      disabled={readOnly}
                      onChange={(v) => setMatch({ method: v === ANY ? undefined : v })}
                      options={[{ id: ANY, label: "Any" }, ...["GET", "POST", "PUT", "PATCH", "DELETE"].map((x) => ({ id: x, label: x }))]}
                    />
                    <TextInput label="Path" mono value={m.path ?? ""} disabled={readOnly} onChange={(v) => setMatch({ path: v || undefined })} placeholder="/search" spellCheck={false} />
                    <FieldShell label="Examples" hint={exampleNames.length ? `Comma separated: ${exampleNames.join(", ")}` : "Names of examples, comma separated."}>
                      <ListInput value={m.examples} disabled={readOnly} onChange={(v) => setMatch({ examples: v })} placeholder={exampleNames[0] ?? "Search"} />
                    </FieldShell>
                  </fieldset>

                  <fieldset className="grid grid-cols-1 gap-4 border-t border-separator-border pt-4 sm:col-span-3 sm:grid-cols-4">
                    <legend className={cx(LEGEND, "float-left w-full")}>Price</legend>
                    <OptionalNumberInput
                      label="Amount per unit"
                      hint="$5 per 1,000 is 0.005."
                      min={0}
                      value={price?.amount}
                      disabled={readOnly || !!o.free}
                      onChange={(v) => (v === undefined && !price?.unit ? set({ price: undefined }) : setPrice({ amount: v ?? 0 }))}
                      placeholder={o.free ? "0" : "0.016"}
                    />
                    <SelectField
                      label="Unit"
                      value={price?.unit || "none"}
                      disabled={readOnly}
                      onChange={(v) => (v === "none" ? set({ price: undefined }) : setPrice({ unit: v }))}
                      options={[{ id: "none", label: "No price" }, ...PRICE_UNITS.map((u) => ({ id: u, label: u }))]}
                    />
                    <TextInput
                      label="Currency"
                      mono
                      value={price?.currency ?? ""}
                      disabled={readOnly || !price}
                      onChange={(v) => setPrice({ currency: v.toUpperCase() || undefined })}
                      placeholder="USD"
                      maxLength={3}
                    />
                    <OptionalNumberInput
                      label="Units per request"
                      hint="When fixed, such as 1 credit."
                      min={0}
                      value={price?.per_request}
                      disabled={readOnly || !price}
                      onChange={(v) => setPrice({ per_request: v })}
                      placeholder="1"
                    />
                  </fieldset>

                  <fieldset className="flex flex-col gap-3 border-t border-separator-border pt-4 sm:col-span-3">
                    <legend className={cx(LEGEND, "float-left w-full")}>Limits per key</legend>
                    <p className="text-body-2-regular text-text-secondary">Acnologia enforces these on every key, so list only limits that hold for every plan. Put plan specific caps in the notes.</p>
                    {limits.map((l, j) => {
                      const setL = (p: Partial<OfferLimit>) => setLimits(limits.map((x, k) => (k === j ? { ...x, ...p } : x)));
                      return (
                        <div key={j} className="grid grid-cols-[1fr_1fr_auto] items-end gap-3 sm:grid-cols-[160px_160px_1fr_auto]">
                          <OptionalNumberInput label="Count" min={1} value={l.count} disabled={readOnly} onChange={(v) => setL({ count: v ?? 0 })} placeholder="30" />
                          <SelectField label="Per" value={l.per} disabled={readOnly} onChange={(v) => setL({ per: v as OfferPer })} options={LIMIT_PERS.map((x) => ({ id: x, label: x }))} />
                          <TextInput className="max-sm:col-span-2 max-sm:row-start-2" label="Unit" value={l.unit ?? ""} disabled={readOnly} onChange={(v) => setL({ unit: v || undefined })} placeholder="requests" />
                          <div className="pb-1.5 max-sm:col-start-3 max-sm:row-start-1">
                            <RowIconButton danger icon={RiDeleteBinLine} label={`Delete limit ${j + 1}`} disabled={readOnly} onClick={() => setLimits(limits.filter((_, k) => k !== j))} />
                          </div>
                        </div>
                      );
                    })}
                    {!readOnly && (
                      <Button size="small" variant="secondary" leadingIcon={RiAddLine} className="w-fit" onClick={() => setLimits([...limits, { count: 60, per: "minute", unit: "requests" }])}>
                        Add limit
                      </Button>
                    )}
                    <OptionalNumberInput
                      className="sm:max-w-[240px]"
                      label="Concurrent requests per key"
                      hint="Empty when not published."
                      min={0}
                      value={o.concurrency || undefined}
                      disabled={readOnly}
                      onChange={(v) => set({ concurrency: v || undefined })}
                      placeholder="2"
                    />
                  </fieldset>

                  <fieldset className="grid grid-cols-1 gap-4 border-t border-separator-border pt-4 sm:col-span-3 sm:grid-cols-[1fr_200px]">
                    <legend className={cx(LEGEND, "float-left w-full")}>Notes and source</legend>
                    <TextField value={o.notes ?? ""} onChange={(v) => set({ notes: v || undefined })} isDisabled={readOnly} className="min-w-0 sm:col-span-2">
                      <Label>Notes</Label>
                      <TextareaBase rows={2} placeholder="Free up to 12,000 a day, then wallet funds." />
                    </TextField>
                    <TextInput label="Source URL" value={o.source_url ?? ""} disabled={readOnly} onChange={(v) => set({ source_url: v || undefined })} placeholder="https://example.com/pricing" spellCheck={false} />
                    <TextInput label="Checked on" type="date" value={o.checked_at ?? ""} disabled={readOnly} onChange={(v) => set({ checked_at: v || undefined })} />
                  </fieldset>
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
  const order = useOrderedList(list.length);
  const resultFor = (name: string, i: number) => report?.results.find((r) => r.name === name) ?? report?.results[i];
  return (
    <div className="flex flex-col gap-5">
      <Section
        title="Fixture tests"
        description="Real or documented responses, each with the outcome it should produce. Every save runs them through the real engine."
        actions={
          !readOnly && (
            <Button size="small" variant="secondary" leadingIcon={RiAddLine} onClick={add}>
              Add test
            </Button>
          )
        }
      >
        {report && (
          <div className="flex flex-wrap items-center gap-2" role="status">
            <Chip variant="caption" color={report.failed ? "rose" : "lime"} className="font-mono">
              {report.passed} passed, {report.failed} failed
            </Chip>
            <span className="text-caption-1-regular text-text-secondary">from the latest check of this draft</span>
          </div>
        )}
      </Section>
      {list.length === 0 ? (
        <EmptyState
          icon={RiFlaskLine}
          title="No tests yet"
          body="Add at least one response per rule, such as a 429 body that should count as rate limited. Tests are what make auto publishing safe."
          action={
            !readOnly && (
              <Button variant="primary" leadingIcon={RiAddLine} onClick={add}>
                Add a test
              </Button>
            )
          }
        />
      ) : (
        <ol className="flex flex-col gap-3">
          {list.map((t, i) => {
            const set = (p: Partial<SpecFixture>) => setList(list.map((x, j) => (j === i ? { ...x, ...p } : x)));
            const r = resultFor(t.name, i);
            const headerCount = t.headers ? Object.keys(t.headers).length : 0;
            return (
              <li
                key={order.key(i)}
                ref={order.ref(i)}
                className={cx(ITEM_CARD, "p-4 sm:p-5", r && !r.ok && "border-border-error-default", order.isNew(i) && "fade-up")}
              >
                <div className="mb-4 flex flex-wrap items-start justify-between gap-2">
                  <div className="min-w-0">
                    <div className="flex min-h-8 flex-wrap items-center gap-2">
                      {r ? (
                        r.ok ? (
                          <RiCheckboxCircleFill className="size-4 shrink-0 text-status-lime-text" aria-label="Passing" role="img" />
                        ) : (
                          <RiCloseCircleFill className="size-4 shrink-0 text-text-error-primary" aria-label="Failing" role="img" />
                        )
                      ) : null}
                      <span className="truncate text-body-medium text-text-primary">{t.name || `Test ${i + 1}`}</span>
                      <span className="font-mono text-caption-1-regular text-text-secondary">{t.status}</span>
                      <RiArrowRightLine className="size-3.5 shrink-0 text-foreground-icon-quaternary" aria-hidden />
                      <OutcomeBadge outcome={t.expect} />
                    </div>
                    {r && !r.ok && (
                      <p className="mt-1 text-caption-1-regular text-text-error-primary">
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
                    onMove={(d) => {
                      order.move(i, d);
                      setList(move(list, i, d));
                    }}
                    onDuplicate={() => {
                      order.duplicate(i);
                      setList([...list.slice(0, i + 1), { ...t, name: `${t.name} copy` }, ...list.slice(i + 1)]);
                    }}
                    onRemove={() => {
                      order.remove(i);
                      setList(list.filter((_, j) => j !== i));
                    }}
                  />
                </div>
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-[1fr_110px_1fr]">
                  <TextInput label="Name" value={t.name} disabled={readOnly} onChange={(v) => set({ name: v })} placeholder="Monthly quota used up" />
                  <TextInput
                    label="Status"
                    type="number"
                    inputMode="numeric"
                    mono
                    value={t.status}
                    disabled={readOnly}
                    onChange={(v) => set({ status: Number(v) })}
                  />
                  <SelectField
                    label="Expected outcome"
                    value={t.expect}
                    disabled={readOnly}
                    onChange={(v) => set({ expect: v as Outcome })}
                    options={OUTCOMES.map((o) => ({ id: o, label: o.replace("_", " ") }))}
                  />
                  <TextField
                    value={t.body}
                    onChange={(v) => set({ body: v })}
                    isDisabled={readOnly}
                    inputClassName="font-mono text-body-2-regular"
                    className="min-w-0 sm:col-span-3"
                  >
                    <Label>Response body</Label>
                    <TextareaBase rows={3} spellCheck={false} placeholder={'{"error":"Monthly quota exceeded."}'} />
                  </TextField>
                  <details className="group/headers sm:col-span-3">
                    <summary className="flex min-h-11 w-fit cursor-pointer list-none items-center gap-1 rounded-md text-body-2-medium text-text-secondary outline-none transition-colors duration-150 ease hover:text-text-primary focus-visible:ring-2 focus-visible:ring-border-focus-ring [&::-webkit-details-marker]:hidden">
                      <RiArrowDownSLine aria-hidden className="size-4 -rotate-90 transition-transform duration-200 ease-out group-open/headers:rotate-0 motion-reduce:transition-none" />
                      Response headers {headerCount ? `(${headerCount})` : ""}
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
