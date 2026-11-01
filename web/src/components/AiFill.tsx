import { useEffect, useRef, useState, type FormEvent } from "react";
import { Sparkles, X } from "lucide-react";
import { ApiError, api, errorMessage } from "../lib/api";
import type { AssistForm } from "../lib/types";
import { useAi } from "../stores/ai";
import { toast } from "../stores/toast";
import { Button, cx } from "./ui";

export interface AiFillState {
  form: AssistForm;
  open: boolean;
  setOpen: (v: boolean) => void;
  busy: boolean;
  notes: string;
  changed: Set<string>;
  enabled: boolean;
  run: (instruction: string) => Promise<void>;
  /** Class to add to a field wrapper that the AI just filled. */
  flash: (name: string) => string;
}

/**
 * State for "Fill with AI": sends the instruction and the form's current values
 * to /api/admin/ai/assist, then hands the suggested fields to `apply`.
 * Nothing is submitted; the user reviews and saves.
 */
export function useAiFill(form: AssistForm, current: () => object, apply: (fields: Record<string, unknown>) => void): AiFillState {
  const status = useAi((s) => s.status);
  const ensureStatus = useAi((s) => s.ensureStatus);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [notes, setNotes] = useState("");
  const [changed, setChanged] = useState<Set<string>>(new Set());
  const [disabledByServer, setDisabledByServer] = useState(false);
  const timer = useRef<number>();

  useEffect(() => ensureStatus(), [ensureStatus]);
  useEffect(() => () => window.clearTimeout(timer.current), []);

  const run = async (instruction: string) => {
    if (!instruction.trim()) return;
    setBusy(true);
    try {
      const res = await api.aiAssist(form, instruction.trim(), current());
      const fields = res.fields ?? {};
      apply(fields);
      const keys = new Set(Object.keys(fields));
      setChanged(keys);
      setNotes(res.notes ?? "");
      window.clearTimeout(timer.current);
      timer.current = window.setTimeout(() => setChanged(new Set()), 2400);
      if (keys.size === 0) toast.info("Nothing to change. The form already matches.");
      else toast.success(`Filled ${keys.size} field${keys.size === 1 ? "" : "s"}. Review, then save.`);
    } catch (e) {
      if (e instanceof ApiError && e.code === "ai_disabled") {
        setDisabledByServer(true);
        setOpen(false);
        toast.error("AI is not set up on this server. Add the AI key to .env, then restart Acnologia.");
      } else toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return {
    form,
    open,
    setOpen,
    busy,
    notes,
    changed,
    enabled: !!status?.enabled && !disabledByServer,
    run,
    flash: (name) => (changed.has(name) ? "ai-flash" : ""),
  };
}

export function AiFillButton({ ai, className }: { ai: AiFillState; className?: string }) {
  if (!ai.enabled) return null;
  return (
    <button
      type="button"
      onClick={() => ai.setOpen(!ai.open)}
      aria-expanded={ai.open}
      className={cx(
        "tap inline-flex h-7 items-center gap-1.5 rounded-full border px-2.5 text-[12px] font-medium transition-colors",
        ai.open ? "border-brand/30 bg-brand-soft text-brand-ink" : "border-line bg-surface text-muted hover:border-brand/30 hover:text-brand-ink",
        className,
      )}
    >
      <Sparkles className="h-3.5 w-3.5" aria-hidden="true" />
      Fill with AI
    </button>
  );
}

const PLACEHOLDER: Record<AssistForm, string> = {
  app: "marketing site that only needs search",
  service: "search pool for our scraper, gentle on rate limits",
  provider: "a transactional email API with a daily send limit",
};

/** Inline one line prompt plus the AI's notes. Render it at the top of the form. */
export function AiFillPanel({ ai }: { ai: AiFillState }) {
  const [text, setText] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (ai.open) inputRef.current?.focus();
  }, [ai.open]);

  if (!ai.enabled) return null;
  const submit = (e: FormEvent) => {
    e.preventDefault();
    e.stopPropagation();
    void ai.run(text);
  };
  return (
    <>
      {ai.open && (
        <div className="fade-up mb-4 rounded-xl border border-brand/20 bg-brand-soft/60 p-3">
          <label htmlFor={`ai-fill-${ai.form}`} className="mb-1.5 flex items-center gap-1.5 text-[13px] font-medium text-ink">
            <Sparkles className="h-3.5 w-3.5 text-brand" aria-hidden="true" />
            Describe it in a sentence
          </label>
          {/* Not a nested <form>: the panel often sits inside the form it fills. */}
          <div className="flex flex-col gap-2 sm:flex-row">
            <input
              ref={inputRef}
              id={`ai-fill-${ai.form}`}
              value={text}
              onChange={(e) => setText(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") submit(e as unknown as FormEvent);
              }}
              placeholder={`For example: ${PLACEHOLDER[ai.form]}`}
              enterKeyHint="go"
              className="field-input h-10 w-full min-w-0 flex-1 rounded-xl border border-line bg-surface px-3 text-sm text-ink placeholder:text-faint focus:border-ink/60 focus:outline-none focus:ring-2 focus:ring-ink/20"
            />
            <div className="flex gap-2">
              <Button variant="primary" loading={ai.busy} disabled={!text.trim()} onClick={() => void ai.run(text)} icon={<Sparkles className="h-4 w-4" />}>
                Fill
              </Button>
              <Button variant="ghost" onClick={() => ai.setOpen(false)} aria-label="Close AI fill">
                <X className="h-4 w-4" />
              </Button>
            </div>
          </div>
          <p className="mt-1.5 text-xs text-muted">AI fills the fields for you to review. Nothing is saved until you save.</p>
        </div>
      )}
      {ai.notes && (
        <p className="mb-4 flex items-start gap-1.5 rounded-xl bg-soft px-3 py-2 text-xs leading-relaxed text-muted" aria-live="polite">
          <Sparkles className="mt-0.5 h-3 w-3 shrink-0 text-brand" aria-hidden="true" />
          {ai.notes}
        </p>
      )}
    </>
  );
}
