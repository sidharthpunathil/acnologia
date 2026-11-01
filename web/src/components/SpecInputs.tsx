import { useEffect, useState, type InputHTMLAttributes } from "react";
import { Plus, X } from "lucide-react";
import type { SpecMatch } from "../lib/types";
import { joinList, parseStatusList, splitList } from "../lib/providerSpec";
import { Button, Field, IconButton, Input, Textarea, cx } from "./ui";

type InputProps = Omit<InputHTMLAttributes<HTMLInputElement>, "value" | "onChange">;

/**
 * A comma separated list input that keeps the raw text while typing, so a
 * trailing comma or space is not eaten, and emits the parsed list.
 */
export function ListInput({ value, onChange, ...rest }: { value: string[] | undefined; onChange: (v: string[]) => void } & InputProps) {
  const [text, setText] = useState(joinList(value));
  useEffect(() => {
    if (joinList(splitList(text)) !== joinList(value)) setText(joinList(value));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [joinList(value)]);
  return (
    <Input
      {...rest}
      value={text}
      onChange={(e) => {
        setText(e.target.value);
        onChange(splitList(e.target.value));
      }}
    />
  );
}

export function StatusListInput({ value, onChange, ...rest }: { value: number[] | undefined; onChange: (v: number[]) => void } & InputProps) {
  const [text, setText] = useState(joinList(value));
  useEffect(() => {
    if (joinList(parseStatusList(text)) !== joinList(value)) setText(joinList(value));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [joinList(value)]);
  return (
    <Input
      {...rest}
      inputMode="numeric"
      value={text}
      onChange={(e) => {
        setText(e.target.value);
        onChange(parseStatusList(e.target.value));
      }}
      className={cx("font-mono", rest.className)}
    />
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
    <div className="space-y-2">
      {rows.map(([k, v], i) => (
        <div key={i} className="flex items-center gap-2">
          <Input
            aria-label={keyLabel}
            placeholder={keyLabel}
            value={k}
            disabled={disabled}
            onChange={(e) => emit(rows.map((r, j) => (j === i ? [e.target.value, r[1]] : r)))}
            className="min-w-0 flex-1 font-mono text-[13px]"
          />
          <Input
            aria-label={valueLabel}
            placeholder={valueLabel}
            value={v}
            disabled={disabled}
            onChange={(e) => emit(rows.map((r, j) => (j === i ? [r[0], e.target.value] : r)))}
            className="min-w-0 flex-1 font-mono text-[13px]"
          />
          <IconButton label={`Remove ${k || "row"}`} disabled={disabled} onClick={() => emit(rows.filter((_, j) => j !== i))}>
            <X className="h-4 w-4" />
          </IconButton>
        </div>
      ))}
      <Button size="sm" variant="ghost" icon={<Plus className="h-3.5 w-3.5" />} disabled={disabled} onClick={() => setRows([...rows, ["", ""]])}>
        {addLabel}
      </Button>
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
    <div className={cx("grid grid-cols-1 gap-3", compact ? "sm:grid-cols-2" : "sm:grid-cols-2")}>
      <Field label="Status codes" hint="Comma separated. Empty matches any status of 400 or more.">
        <StatusListInput value={m.status} onChange={(v) => set({ status: v })} placeholder="429, 402" disabled={disabled} />
      </Field>
      <Field label="Body contains" hint="Any of these phrases, comma separated, case insensitive.">
        <ListInput value={m.body_contains} onChange={(v) => set({ body_contains: v })} placeholder="run out of searches" disabled={disabled} />
      </Field>
      <Field label="JSON path" hint="Dot path such as error.code. $. is optional.">
        <Input value={m.json_path ?? ""} onChange={(e) => set({ json_path: e.target.value })} placeholder="detail.status" disabled={disabled} className="font-mono" />
      </Field>
      <Field label="JSON equals" hint="Value at the path, compared as text.">
        <Input value={m.json_equals ?? ""} onChange={(e) => set({ json_equals: e.target.value })} placeholder="quota_exceeded" disabled={disabled} className="font-mono" />
      </Field>
      {!compact && (
        <>
          <Field label="Body regex" hint="Optional regular expression on the raw body.">
            <Input value={m.body_regex ?? ""} onChange={(e) => set({ body_regex: e.target.value })} disabled={disabled} className="font-mono" />
          </Field>
          <Field label="Header present" hint="Matches when the response has this header.">
            <Input value={m.header_present ?? ""} onChange={(e) => set({ header_present: e.target.value })} placeholder="x-ratelimit-reset" disabled={disabled} className="font-mono" />
          </Field>
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
    <Field label={label} hint={hint} error={err}>
      <Textarea
        rows={rows}
        value={text}
        disabled={disabled}
        spellCheck={false}
        onChange={(e) => {
          setText(e.target.value);
          if (!e.target.value.trim()) {
            setErr(null);
            onChange(null);
            return;
          }
          try {
            onChange(JSON.parse(e.target.value));
            setErr(null);
          } catch (x) {
            setErr(x instanceof Error ? `Not valid JSON: ${x.message}` : "Not valid JSON");
          }
        }}
        className="font-mono text-[12.5px] leading-relaxed"
      />
    </Field>
  );
}
