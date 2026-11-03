import { useEffect, useRef, useState } from "react";
import { RiCloseLine, RiSparkling2Line } from "@remixicon/react";
import { ApiError, api, errorMessage } from "../lib/api";
import type { AssistForm } from "../lib/types";
import { useAi } from "../stores/ai";
import { toast } from "../stores/toast";
import { Button } from "@/components/base/buttons/button";
import { Input } from "@/components/base/input/input";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { cx } from "@/utils/cx";

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
  const timer = useRef<number | undefined>(undefined);

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
    <Button
      variant="secondary"
      size="small"
      leadingIcon={RiSparkling2Line}
      onClick={() => ai.setOpen(!ai.open)}
      aria-expanded={ai.open}
      className={cx(ai.open && "border-accent-line bg-accent-subtle text-accent-fg hover:bg-accent-subtle", className)}
    >
      Fill with AI
    </Button>
  );
}

const PLACEHOLDER: Record<AssistForm, string> = {
  app: "marketing site that only needs search",
  service: "search pool for our scraper, gentle on rate limits",
  provider: "a transactional email API with a daily send limit",
  guide: "focus on search and pagination, skip the admin endpoints",
};

/** Inline one line prompt plus the AI's notes. Render it at the top of the form. */
export function AiFillPanel({ ai }: { ai: AiFillState }) {
  const [text, setText] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (ai.open) inputRef.current?.focus();
  }, [ai.open]);

  if (!ai.enabled) return null;
  return (
    <>
      {ai.open && (
        <div className="fade-up mb-4 rounded-2xl border border-accent-line/30 bg-accent-subtle p-3">
          {/* Not a nested <form>: the panel often sits inside the form it fills. */}
          <div className="flex flex-col gap-2 sm:flex-row sm:items-end">
            <Input
              ref={inputRef}
              label={
                <span className="inline-flex items-center gap-1.5">
                  <RiSparkling2Line className="size-4 text-accent-fg" aria-hidden />
                  Describe it in a sentence
                </span>
              }
              value={text}
              onChange={setText}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  e.stopPropagation();
                  void ai.run(text);
                }
              }}
              placeholder={`For example: ${PLACEHOLDER[ai.form]}`}
              enterKeyHint="go"
              className="min-w-0 flex-1"
              fieldClassName="bg-background-primary-default"
            />
            <div className="flex gap-2">
              <Button
                variant="primary"
                disabled={ai.busy || !text.trim()}
                leadingIcon={ai.busy ? ButtonSpinner : RiSparkling2Line}
                onClick={() => void ai.run(text)}
              >
                Fill
              </Button>
              <Button variant="ghost" iconOnly leadingIcon={RiCloseLine} onClick={() => ai.setOpen(false)} aria-label="Close AI fill" />
            </div>
          </div>
          <p className="mt-1.5 text-caption-1-regular text-text-secondary">AI fills the fields for you to review. Nothing is saved until you save.</p>
        </div>
      )}
      {ai.notes && (
        <p
          className="mb-4 flex items-start gap-1.5 rounded-2xl bg-background-secondary-default px-3 py-2 text-caption-1-regular text-text-secondary"
          aria-live="polite"
        >
          <RiSparkling2Line className="mt-0.5 size-3.5 shrink-0 text-accent-fg" aria-hidden />
          {ai.notes}
        </p>
      )}
    </>
  );
}
