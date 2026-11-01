import { useCallback, useDeferredValue, useId, useMemo, useRef, useState, type DragEvent, type ReactNode } from "react";
import { RiArrowRightSLine, RiCloseLine, RiErrorWarningLine, RiFileTextLine, RiFileUploadLine, RiUploadCloud2Line } from "@remixicon/react";
import { maskLine, maskSecret, MAX_IGNORED, MAX_KEYS, mergeParsed, parseKeys, type ParseResult } from "../lib/keyparse";
import { num, plural } from "../lib/format";
import { Button } from "@/components/base/buttons/button";
import { SpinnerGlyph } from "@/components/base/spinner/spinner";
import { cx } from "@/utils/cx";

const MAX_FILE = 1024 * 1024;
const ACCEPT = ".txt,.csv,.env,.json,text/plain,text/csv,application/json";
const OK_NAME = /(\.(txt|csv|env|json|tsv|text)$)|(^\.env(\.[\w-]+)?$)/i;
const OK_TYPE = /^(text\/|application\/json$)/;

export interface KeyFile {
  id: number;
  name: string;
  size: number;
  parsed: ParseResult;
}

export type DropState = { kind: "idle" } | { kind: "over"; files: number } | { kind: "reading"; files: number } | { kind: "error"; message: string };

function kb(n: number): string {
  return n < 1024 ? `${n} B` : n < MAX_FILE ? `${Math.round(n / 1024)} KB` : `${(n / MAX_FILE).toFixed(1)} MB`;
}

function hasFiles(e: DragEvent) {
  return Array.from(e.dataTransfer?.types ?? []).includes("Files");
}

/**
 * State for a "paste or drop keys" field: the text, the parse of it, and drag
 * and file handling. Spread `dropProps` on any element that should accept a
 * drop (the field itself does already), such as a whole dialog.
 */
export function useKeyInput() {
  const [text, setText] = useState("");
  const [drop, setDrop] = useState<DropState>({ kind: "idle" });
  // Each file is parsed on its own (a JSON or CSV file only parses whole) and
  // its secrets never land in the text area.
  const [files, setFiles] = useState<KeyFile[]>([]);
  const depth = useRef(0);
  const nextId = useRef(1);
  const deferred = useDeferredValue(text);
  const textParsed = useMemo(() => parseKeys(deferred), [deferred]);
  const parsed = useMemo(() => (files.length ? mergeParsed([textParsed, ...files.map((f) => f.parsed)]) : textParsed), [textParsed, files]);

  const readFiles = useCallback(async (list: FileList | File[]) => {
    const all = Array.from(list);
    if (!all.length) return;
    const errors: string[] = [];
    const good = all.filter((f) => {
      if (!OK_NAME.test(f.name) && !OK_TYPE.test(f.type)) {
        errors.push(`${f.name} is not a text file. Use .txt, .csv, .env or .json.`);
        return false;
      }
      if (f.size > MAX_FILE) {
        errors.push(`${f.name} is ${kb(f.size)}. Files can be up to 1 MB.`);
        return false;
      }
      return true;
    });
    if (good.length) setDrop({ kind: "reading", files: good.length });
    const read: KeyFile[] = [];
    for (const f of good) {
      try {
        const body = await f.text();
        const p = parseKeys(body);
        if (!p.keys.length) errors.push(`No keys found in ${f.name}.`);
        read.push({ id: nextId.current++, name: f.name, size: f.size, parsed: p });
      } catch {
        errors.push(`Could not read ${f.name}.`);
      }
    }
    if (read.length) setFiles((prev) => [...prev, ...read]);
    setDrop(errors.length ? { kind: "error", message: errors.join(" ") } : { kind: "idle" });
  }, []);

  const dropProps = {
    onDragEnter: (e: DragEvent) => {
      if (!hasFiles(e)) return;
      e.preventDefault();
      e.stopPropagation();
      depth.current++;
      setDrop({ kind: "over", files: e.dataTransfer.items?.length ?? 0 });
    },
    onDragOver: (e: DragEvent) => {
      if (!hasFiles(e)) return;
      e.preventDefault();
      e.stopPropagation();
      e.dataTransfer.dropEffect = "copy";
    },
    onDragLeave: (e: DragEvent) => {
      if (!hasFiles(e)) return;
      e.stopPropagation();
      depth.current = Math.max(0, depth.current - 1);
      if (depth.current === 0) setDrop((d) => (d.kind === "over" ? { kind: "idle" } : d));
    },
    onDrop: (e: DragEvent) => {
      if (!hasFiles(e)) return;
      e.preventDefault();
      e.stopPropagation();
      depth.current = 0;
      void readFiles(e.dataTransfer.files);
    },
  };

  const reset = useCallback(() => {
    setText("");
    setFiles([]);
    setDrop({ kind: "idle" });
    depth.current = 0;
  }, []);

  const removeFile = useCallback((id: number) => setFiles((fs) => fs.filter((f) => f.id !== id)), []);

  return { text, setText, parsed, stale: deferred !== text, drop, setDrop, files, readFiles, removeFile, dropProps, reset };
}

export type KeyInput = ReturnType<typeof useKeyInput>;

/** The paste area with its drop overlay, file chooser and file list. */
export function KeyPasteArea({
  input,
  id,
  rows = 7,
  placeholder,
  label,
  describedBy,
}: {
  input: KeyInput;
  id?: string;
  rows?: number;
  placeholder?: string;
  label: string;
  describedBy?: string;
}) {
  const fileRef = useRef<HTMLInputElement>(null);
  const hintId = useId();
  const errId = useId();
  const { drop } = input;
  const active = drop.kind === "over" || drop.kind === "reading";

  return (
    <div>
      <div className="relative" {...input.dropProps}>
        <textarea
          id={id}
          rows={rows}
          value={input.text}
          onChange={(e) => {
            input.setText(e.target.value);
            if (drop.kind === "error") input.setDrop({ kind: "idle" });
          }}
          aria-label={label}
          aria-describedby={[hintId, drop.kind === "error" ? errId : "", describedBy ?? ""].filter(Boolean).join(" ")}
          spellCheck={false}
          wrap="off"
          autoComplete="off"
          autoCapitalize="off"
          autoCorrect="off"
          placeholder={placeholder}
          className={cx(
            "block min-h-[140px] w-full resize-y rounded-2lg bg-background-tertiary-default px-3 py-2.5 font-mono text-body-2-regular text-text-primary placeholder:text-text-tertiary",
            "ring-1 ring-inset ring-input-idle-ring outline-none transition-[box-shadow] duration-150 hover:ring-border-button-hover focus:ring-2 focus:ring-border-button-active",
          )}
        />
        {/* Drop overlay. Pointer events stay off so the drag keeps landing on the field below. */}
        <div
          aria-hidden="true"
          className={cx(
            "pointer-events-none absolute inset-0 flex flex-col items-center justify-center gap-1.5 rounded-2lg border-2 border-dashed border-accent-line bg-background-primary-default text-center transition-opacity duration-150 ease-[var(--ease-out)] motion-reduce:transition-none",
            active ? "opacity-100" : "opacity-0",
          )}
        >
          {drop.kind === "reading" ? (
            <SpinnerGlyph size="md" />
          ) : (
            <RiUploadCloud2Line aria-hidden className={cx("size-5 text-accent-fg transition-transform duration-200 ease-[var(--ease-out)] motion-reduce:transition-none", active && "-translate-y-0.5")} />
          )}
          <span className="text-body-2-medium text-text-primary">
            {drop.kind === "reading" ? `Reading ${plural(drop.files, "file")}` : "Drop to read the keys inside"}
          </span>
          <span className="font-mono text-caption-2-regular text-text-secondary">.txt .csv .env .json · up to 1 MB each</span>
        </div>
      </div>

      <div className="mt-2 flex flex-wrap items-center justify-between gap-x-3 gap-y-2">
        <p id={hintId} className="flex min-w-0 items-center gap-1.5 text-caption-1-regular text-text-secondary">
          <RiUploadCloud2Line className="size-4 shrink-0" aria-hidden />
          <span>Or drop .txt, .csv, .env or .json files, 1 MB each.</span>
        </p>
        <Button variant="secondary" size="small" leadingIcon={RiFileUploadLine} onClick={() => fileRef.current?.click()}>
          Choose files
        </Button>
        <input
          ref={fileRef}
          type="file"
          multiple
          accept={ACCEPT}
          tabIndex={-1}
          className="sr-only"
          aria-hidden="true"
          onChange={(e) => {
            if (e.target.files) void input.readFiles(e.target.files);
            e.target.value = "";
          }}
        />
      </div>

      {drop.kind === "error" && (
        <p id={errId} role="alert" className="mt-2 flex items-start gap-1.5 text-caption-1-medium text-text-error-primary">
          <RiErrorWarningLine className="mt-px size-4 shrink-0" aria-hidden />
          {drop.message}
        </p>
      )}

      {input.files.length > 0 && (
        <ul className="mt-2 flex flex-wrap gap-1.5" aria-label="Files added">
          {input.files.map((f) => (
            <li key={f.id} className="flex max-w-full items-center gap-1 rounded-lg bg-background-tertiary-default py-0.5 ps-2 pe-0.5 font-mono text-caption-2-regular text-text-secondary">
              <RiFileTextLine className="size-3.5 shrink-0" aria-hidden />
              <span className="min-w-0 truncate text-text-primary">{f.name}</span>
              <span className="shrink-0">· {plural(f.parsed.keys.length, "key")}</span>
              <button
                type="button"
                onClick={() => input.removeFile(f.id)}
                aria-label={`Remove ${f.name}`}
                className="ms-0.5 inline-flex size-6 shrink-0 cursor-pointer items-center justify-center rounded-md text-foreground-icon-secondary outline-none transition-colors hover:bg-background-tertiary-hover hover:text-text-primary focus-visible:ring-2 focus-visible:ring-border-focus-ring"
              >
                <RiCloseLine className="size-3.5" aria-hidden />
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** Live summary of what the parser found. Never shows a whole secret. */
export function KeyPreview({
  parsed,
  stale,
  show = 8,
  fallbackLabel,
  empty,
}: {
  parsed: ParseResult;
  stale?: boolean;
  show?: number;
  /** Label shown for keys without one, by their index in the list. */
  fallbackLabel?: (i: number) => string;
  empty?: ReactNode;
}) {
  const n = parsed.keys.length;
  const parts: string[] = [];
  if (parsed.duplicates) parts.push(`${plural(parsed.duplicates, "duplicate")} skipped`);
  if (parsed.ignored.length) parts.push(`${num(parsed.ignored.length)}${parsed.ignored.length >= MAX_IGNORED ? "+" : ""} ignored`);
  const rest = n - show;

  return (
    <div className={cx("transition-opacity duration-150", stale && "opacity-60")}>
      <p aria-live="polite" aria-atomic="true" className="font-mono text-caption-1-regular text-text-secondary">
        <span className={n ? "text-caption-1-medium text-text-primary" : ""}>{n ? `${num(n)} ${n === 1 ? "key" : "keys"} found` : "No keys found yet"}</span>
        {parts.map((p) => (
          <span key={p}> · {p}</span>
        ))}
        {parsed.overflow > 0 && <span> · {num(parsed.overflow)} over the {num(MAX_KEYS)} limit left out</span>}
      </p>
      {n === 0 && empty}

      {n > 0 && (
        <ul className="mt-2 divide-y divide-separator-border overflow-hidden rounded-2xl border border-border-button-default bg-background-secondary-default" aria-label="Keys found">
          {parsed.keys.slice(0, show).map((k, i) => {
            const label = k.label || fallbackLabel?.(i);
            return (
              <li key={k.secret} className="flex min-h-[34px] items-center gap-3 px-3 py-1.5 text-body-2-regular">
                <span className="w-6 shrink-0 text-end font-mono text-caption-2-regular tabular-nums text-text-tertiary">{i + 1}</span>
                <span className={cx("min-w-0 flex-1 truncate", k.label ? "text-text-primary" : "text-text-tertiary")}>{label || "no label"}</span>
                <span className="shrink-0 font-mono text-caption-1-regular text-text-secondary">{maskSecret(k.secret)}</span>
              </li>
            );
          })}
          {rest > 0 && <li className="px-3 py-1.5 font-mono text-caption-2-regular text-text-secondary">and {num(rest)} more</li>}
        </ul>
      )}

      {parsed.ignored.length > 0 && (
        <details className="group mt-2">
          <summary className="inline-flex cursor-pointer list-none items-center gap-1 rounded-md py-1 text-caption-1-regular text-text-secondary outline-none hover:text-text-primary focus-visible:ring-2 focus-visible:ring-border-focus-ring [&::-webkit-details-marker]:hidden">
            <RiArrowRightSLine className="size-4 transition-transform rtl:-scale-x-100 duration-150 ease-[var(--ease-out)] group-open:rotate-90 motion-reduce:transition-none" aria-hidden="true" />
            Show what was ignored
          </summary>
          <ul className="mt-1 flex max-h-40 flex-col gap-0.5 overflow-auto rounded-2xl bg-background-tertiary-default px-3 py-2 font-mono text-caption-2-regular text-text-secondary">
            {parsed.ignored.slice(0, 200).map((l, i) => (
              <li key={i} className="truncate">
                {maskLine(l)}
              </li>
            ))}
            {parsed.ignored.length > 200 && <li className="text-text-tertiary">and {num(parsed.ignored.length - 200)} more</li>}
          </ul>
          <p className="mt-1 text-caption-2-regular text-text-tertiary">Long values are shortened here. A key is 16 to 512 letters, digits or _-.:+/= and has a digit or is at least 24 long. A word right before a key becomes its label.</p>
        </details>
      )}
    </div>
  );
}
