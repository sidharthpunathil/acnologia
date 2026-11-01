import { useEffect, useState, type ReactNode } from "react";
import { RiAddLine, RiCloseLine } from "@remixicon/react";
import { Focusable } from "react-aria-components";
import type { SpecMatch } from "../lib/types";
import { joinList, parseStatusList, splitList } from "../lib/providerSpec";
import { Button } from "@/components/base/buttons/button";
import { IconButton } from "@/components/base/buttons/icon-button";
import { Input } from "@/components/base/input/input";
import { Textarea } from "@/components/base/textarea/textarea";
import { Tooltip, TooltipTrigger } from "@/components/base/tooltip/tooltip";
import { cx } from "@/utils/cx";

/**
 * Props every spec input takes. `label` and `hint` render through the
 * BoardUI field; without a label, pass `aria-label` (the placeholder is the
 * fallback name). `min` and `step` are accepted for call site compatibility.
 */
export interface SpecFieldProps {
  label?: ReactNode;
  hint?: ReactNode;
  placeholder?: string;
  disabled?: boolean;
  className?: string;
  id?: string;
  "aria-label"?: string;
  min?: number;
  step?: number;
  autoFocus?: boolean;
}

function fieldProps({ label, hint, placeholder, disabled, id, autoFocus, ...rest }: SpecFieldProps) {
  return {
    label,
    hint,
    placeholder,
    id,
    autoFocus,
    isDisabled: disabled,
    "aria-label": rest["aria-label"],
  };
}

/**
 * A comma separated list input that keeps the raw text while typing, so a
 * trailing comma or space is not eaten, and emits the parsed list.
 */
export function ListInput({ value, onChange, className, ...rest }: { value: string[] | undefined; onChange: (v: string[]) => void } & SpecFieldProps) {
  const [text, setText] = useState(joinList(value));
  useEffect(() => {
    if (joinList(splitList(text)) !== joinList(value)) setText(joinList(value));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [joinList(value)]);
  return (
    <Input
      {...fieldProps(rest)}
      className={cx("min-w-0", className)}
      inputClassName={cx(className?.includes("font-mono") && "font-mono")}
      value={text}
      onChange={(v) => {
        setText(v);
        onChange(splitList(v));
      }}
    />
  );
}

export function StatusListInput({ value, onChange, className, ...rest }: { value: number[] | undefined; onChange: (v: number[]) => void } & SpecFieldProps) {
  const [text, setText] = useState(joinList(value));
  useEffect(() => {
    if (joinList(parseStatusList(text)) !== joinList(value)) setText(joinList(value));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [joinList(value)]);
  return (
    <Input
      {...fieldProps(rest)}
      className={cx("min-w-0", className)}
      inputClassName="font-mono"
      inputMode="numeric"
      value={text}
      onChange={(v) => {
        setText(v);
        onChange(parseStatusList(v));
      }}
    />
  );
}

/**
 * A number input where empty means "not set". Keeps the raw text while typing
 * (so "-" or "0." survive) and emits undefined instead of an empty string.
 */
export function OptionalNumberInput({
  value,
  onChange,
  className,
  ...rest
}: { value: number | undefined | null; onChange: (v: number | undefined) => void } & SpecFieldProps) {
  const show = (v: number | undefined | null) => (v === undefined || v === null ? "" : String(v));
  const [text, setText] = useState(show(value));
  useEffect(() => {
    const parsed = text.trim() === "" ? undefined : Number(text);
    if (parsed !== (value ?? undefined)) setText(show(value));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value]);
  return (
    <Input
      {...fieldProps(rest)}
      className={cx("min-w-0", className)}
      inputClassName="font-mono tabular-nums"
      inputMode="decimal"
      value={text}
      onChange={(v) => {
        setText(v);
        const t = v.trim();
        if (t === "") onChange(undefined);
        else if (Number.isFinite(Number(t))) onChange(Number(t));
      }}
    />
  );
}

/** Icon button with a tooltip carrying its name. */
function RemoveButton({ label, disabled, onPress }: { label: string; disabled?: boolean; onPress: () => void }) {
  return (
    <TooltipTrigger delay={300}>
      <Focusable>
        <IconButton icon={RiCloseLine} size="small" aria-label={label} disabled={disabled} onClick={onPress} className="mt-0.5" />
      </Focusable>
      <Tooltip>{label}</Tooltip>
    </TooltipTrigger>
  );
}

/** Editable string map, used for query params and fixture headers. */
export function KeyValueEditor({
  value,
  onChange,
  keyLabel = "Name",
  valueLabel = "Value",
  addLabel = "Add",
  disabled,
}: {
  value: Record<string, string> | undefined;
  onChange: (v: Record<string, string>) => void;
  keyLabel?: string;
  valueLabel?: string;
  addLabel?: string;
  disabled?: boolean;
}) {
  // Rows are kept locally so an empty or duplicate key can exist while typing.
  const [rows, setRows] = useState<Array<[string, string]>>(() => Object.entries(value ?? {}));
  const serialized = JSON.stringify(value ?? {});
  useEffect(() => {
    const local = JSON.stringify(Object.fromEntries(rows.filter(([k]) => k.trim())));
    if (local !== serialized) setRows(Object.entries(value ?? {}));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [serialized]);

  const emit = (next: Array<[string, string]>) => {
    setRows(next);
    onChange(Object.fromEntries(next.filter(([k]) => k.trim()).map(([k, v]) => [k.trim(), v])));
  };

  return (
    <div className="flex flex-col gap-2">
      {rows.map(([k, v], i) => (
        <div key={i} className="flex items-start gap-2">
          <Input
            aria-label={keyLabel}
            placeholder={keyLabel}
            value={k}
            isDisabled={disabled}
            onChange={(t) => emit(rows.map((r, j) => (j === i ? [t, r[1]] : r)))}
            className="min-w-0 flex-1"
            inputClassName="font-mono"
          />
          <Input
            aria-label={valueLabel}
            placeholder={valueLabel}
            value={v}
            isDisabled={disabled}
            onChange={(t) => emit(rows.map((r, j) => (j === i ? [r[0], t] : r)))}
            className="min-w-0 flex-1"
            inputClassName="font-mono"
          />
          <RemoveButton label={`Remove ${k || "row"}`} disabled={disabled} onPress={() => emit(rows.filter((_, j) => j !== i))} />
        </div>
      ))}
      <div>
        <Button size="small" variant="ghost" leadingIcon={RiAddLine} disabled={disabled} onClick={() => setRows([...rows, ["", ""]])}>
          {addLabel}
        </Button>
      </div>
    </div>
  );
}

function clean(m: SpecMatch): SpecMatch {
  const out: SpecMatch = {};
  if (m.status?.length) out.status = m.status;
  if (m.body_contains?.length) out.body_contains = m.body_contains;
  if (m.body_regex) out.body_regex = m.body_regex;
  if (m.json_path) out.json_path = m.json_path;
  if (m.json_equals) out.json_equals = m.json_equals;
  if (m.header_present) out.header_present = m.header_present;
  return out;
}

/** Match conditions. Fields are ANDed; values inside a list are ORed. */
export function MatchEditor({ value, onChange, disabled, compact }: { value: SpecMatch | undefined; onChange: (m: SpecMatch) => void; disabled?: boolean; compact?: boolean }) {
  const m = value ?? {};
  const set = (patch: Partial<SpecMatch>) => onChange(clean({ ...m, ...patch }));
  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
      <StatusListInput
        label="Status codes"
        hint="Comma separated. Leave empty to match any status of 400 or more."
        value={m.status}
        onChange={(v) => set({ status: v })}
        placeholder="429, 402"
        disabled={disabled}
      />
      <ListInput
        label="Body contains"
        hint="Any of these phrases, comma separated. Case does not matter."
        value={m.body_contains}
        onChange={(v) => set({ body_contains: v })}
        placeholder="quota exceeded"
        disabled={disabled}
      />
      <Input
        label="JSON path"
        hint="A dot path such as error.code. The $. prefix is optional."
        value={m.json_path ?? ""}
        onChange={(v) => set({ json_path: v })}
        placeholder="detail.status"
        isDisabled={disabled}
        inputClassName="font-mono"
        inputDir="ltr"
      />
      <Input
        label="JSON equals"
        hint="Value at the path, compared as text."
        value={m.json_equals ?? ""}
        onChange={(v) => set({ json_equals: v })}
        placeholder="quota_exceeded"
        isDisabled={disabled}
        inputClassName="font-mono"
        inputDir="ltr"
      />
      {!compact && (
        <>
          <Input
            label="Body regex"
            hint="Optional. A regular expression run on the raw body."
            value={m.body_regex ?? ""}
            onChange={(v) => set({ body_regex: v })}
            isDisabled={disabled}
            inputClassName="font-mono"
            inputDir="ltr"
          />
          <Input
            label="Header present"
            hint="Matches when the response has this header."
            value={m.header_present ?? ""}
            onChange={(v) => set({ header_present: v })}
            placeholder="x-ratelimit-reset"
            isDisabled={disabled}
            inputClassName="font-mono"
            inputDir="ltr"
          />
        </>
      )}
    </div>
  );
}

/** A JSON textarea that only emits when the text parses. Shows the parse error inline. */
export function JsonTextarea({
  value,
  onChange,
  rows = 8,
  label,
  hint,
  disabled,
}: {
  value: unknown;
  onChange: (v: unknown) => void;
  rows?: number;
  label: string;
  hint?: string;
  disabled?: boolean;
}) {
  const pretty = (v: unknown) => (v === undefined || v === null ? "" : typeof v === "string" ? v : JSON.stringify(v, null, 2));
  const [text, setText] = useState(pretty(value));
  const [err, setErr] = useState<string | null>(null);
  const serialized = JSON.stringify(value ?? null);
  useEffect(() => {
    try {
      if (JSON.stringify(text.trim() ? JSON.parse(text) : null) === serialized) return;
    } catch {
      /* keep the user's text while it is invalid */
      return;
    }
    setText(pretty(value));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [serialized]);
  return (
    <Textarea
      label={label}
      // The parse error replaces the hint and reads in the error colour.
      hint={err ?? hint}
      isInvalid={!!err}
      rows={rows}
      value={text}
      isDisabled={disabled}
      spellCheck="false"
      onChange={(next) => {
        setText(next);
        if (!next.trim()) {
          setErr(null);
          onChange(null);
          return;
        }
        try {
          onChange(JSON.parse(next));
          setErr(null);
        } catch (x) {
          setErr(x instanceof Error ? `This is not valid JSON yet. ${x.message}` : "This is not valid JSON yet.");
        }
      }}
      inputClassName="font-mono text-caption-1-regular"
    />
  );
}
