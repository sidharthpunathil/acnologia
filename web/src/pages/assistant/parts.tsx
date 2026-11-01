import {
  forwardRef,
  useLayoutEffect,
  useRef,
  useState,
  type ClipboardEvent,
  type FormEvent,
  type KeyboardEvent,
} from "react";
import {
  ArrowUp,
  Gauge,
  KeyRound,
  Loader2,
  MessageSquare,
  Plus,
  ShieldCheck,
  Workflow,
  X,
} from "lucide-react";
import { maskSecret, parseKeys } from "../../lib/keyparse";
import { useNow } from "../../hooks/useNow";
import { relTime } from "../../lib/format";
import type { ChatThread } from "../../lib/types";
import { ConfirmButton } from "../../components/ConfirmButton";
import { Empty, cx } from "../../components/ui";
import { Mark } from "./items";

const EXAMPLES = [
  {
    text: "Add these SerpAPI keys: ",
    label: "Add these SerpAPI keys",
    hint: "Paste them after the colon",
    icon: KeyRound,
  },
  {
    text: "Which keys are out of quota?",
    label: "Which keys are out of quota?",
    hint: "Across every service",
    icon: Gauge,
  },
  {
    text: "Create a proxy app for my website",
    label: "Create a proxy app",
    hint: "For my website",
    icon: Workflow,
  },
  {
    text: "Check every ElevenLabs key",
    label: "Check every ElevenLabs key",
    hint: "And flag the bad ones",
    icon: ShieldCheck,
  },
];

export function ThreadList({
  threads,
  currentId,
  onPick,
  onDelete,
}: {
  threads: ChatThread[];
  currentId: number | null;
  onPick: (id: number) => void;
  onDelete: (t: ChatThread) => Promise<void>;
}) {
  const now = useNow(60_000);
  if (!threads.length)
    return (
      <Empty
        compact
        icon={<MessageSquare aria-hidden="true" />}
        title="No chats yet"
        body="Each one you start is kept here."
      />
    );
  return (
    <ul className="space-y-px">
      {threads.map((t) => {
        const active = t.id === currentId;
        return (
          <li
            key={t.id}
            className={cx(
              "group relative flex items-center rounded-lg transition-[background-color,transform] duration-150 ease-out active:scale-[0.99]",
              active ? "bg-ink/[0.07]" : "hover:bg-ink/[0.04]",
            )}
          >
            <button
              type="button"
              onClick={() => onPick(t.id)}
              aria-current={active ? "page" : undefined}
              className="tap min-w-0 flex-1 rounded-lg py-2 pl-3 pr-1 text-left"
            >
              <span
                className={cx(
                  "block truncate text-[13.5px] leading-snug",
                  active ? "font-medium text-ink" : "text-ink/90",
                )}
              >
                {t.title || "New chat"}
              </span>
              <span className="mt-0.5 block font-mono text-[11px] text-faint">
                {relTime(t.updated_at, now)}
              </span>
            </button>
            {/* On mouse screens the delete control waits for hover or focus; touch screens always show it. */}
            <span className="shrink-0 pr-1 transition-opacity duration-150 [@media(hover:hover)]:opacity-0 [@media(hover:hover)]:group-hover:opacity-100 [@media(hover:hover)]:focus-within:opacity-100 [&:has(.swap-in)]:!opacity-100">
              <ConfirmButton
                iconOnly
                label={`Delete chat ${t.title || ""}`.trim()}
                confirmLabel="Delete"
                onConfirm={() => onDelete(t)}
              />
            </span>
          </li>
        );
      })}
    </ul>
  );
}

export function NewChatButton({
  onClick,
  className,
}: {
  onClick: () => void;
  className?: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cx(
        "tap inline-flex h-10 w-full items-center gap-2 rounded-xl bg-surface px-3 text-[13.5px] font-medium text-ink shadow-card transition-[background-color,transform] duration-150 ease-out hover:bg-soft active:scale-[0.98]",
        className,
      )}
    >
      <Plus className="h-4 w-4 text-muted" aria-hidden="true" />
      New chat
    </button>
  );
}

export function EmptyState({
  off,
  onPick,
}: {
  off: boolean;
  onPick: (text: string) => void;
}) {
  return (
    <div className="fade-up flex min-h-full flex-col items-center justify-center py-8 text-center">
      <Mark size={44} />
      <h2 className="mt-4 text-[18px] font-semibold tracking-tight text-ink">
        What can I help with?
      </h2>
      <p className="mt-1.5 max-w-sm text-[14px] leading-relaxed text-muted">
        I can add and check keys, find problems and set up apps. I always ask
        before changing or deleting anything.
      </p>
      <ul className="mt-6 grid w-full max-w-xl grid-cols-1 gap-2 sm:grid-cols-2">
        {EXAMPLES.map((ex) => (
          <li key={ex.text}>
            <button
              type="button"
              disabled={off}
              onClick={() => onPick(ex.text)}
              className="tap flex w-full items-start gap-3 rounded-xl border border-line bg-surface px-3.5 py-3 text-left transition-[border-color,background-color,transform] duration-150 ease-out hover:border-line-strong hover:bg-soft active:scale-[0.98] disabled:cursor-not-allowed disabled:opacity-50"
            >
              <ex.icon
                className="mt-0.5 h-4 w-4 shrink-0 text-brand"
                aria-hidden="true"
              />
              <span className="min-w-0">
                <span className="block text-[13.5px] font-medium text-ink">
                  {ex.label}
                </span>
                <span className="block text-[12.5px] text-muted">
                  {ex.hint}
                </span>
              </span>
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

/**
 * The live state of a turn: the mark inside an orbiting ring, the current step
 * from the server (crossfading as it changes) and the elapsed time. With
 * reduced motion the ring is gone and the label is plain text.
 */
export function LiveStatus({ step, startedAt }: { step?: string; startedAt?: string }) {
  const [mounted] = useState(() => Date.now());
  const now = useNow(1000);
  const from = Date.parse(startedAt ?? "");
  const secs = Math.max(0, Math.floor((now - (Number.isNaN(from) ? mounted : from)) / 1000));
  const label = step?.trim() || "Working";
  return (
    <div role="status" className="fade-up flex items-center gap-3">
      <span className="chat-orbit relative inline-flex h-6 w-6 shrink-0 items-center justify-center">
        <Mark />
      </span>
      <span className="min-w-0 text-[14px]">
        <span key={label} className="chat-swap chat-shimmer inline-block max-w-full truncate align-bottom font-medium">
          {label}
        </span>
        <span className="ml-1.5 font-mono text-[12px] tabular-nums text-faint" aria-hidden="true">
          {secs < 60 ? `${secs}s` : `${Math.floor(secs / 60)}m ${String(secs % 60).padStart(2, "0")}s`}
        </span>
      </span>
      <span className="sr-only">Claude is working: {label}</span>
    </div>
  );
}

/** Text that looks like a pasted API key: a long run of key-ish characters. */
const KEYISH = /[A-Za-z0-9_\-]{24,}/;

/** Keys pasted or dropped, held beside the composer instead of inside it. */
export interface Attachment {
  /** The raw text, sent with the message. Never shown. */
  text: string;
  /** Where it came from: "Pasted" or file names. */
  source: string;
  keys: number;
  labeled: number;
  /** Masked first few, such as "acct-1 …c21e". */
  preview: string[];
  /** Hints of every key, for Show all. */
  all: string[];
}

/** Paste this many keys, or a long block holding any, and it becomes an attachment. */
export const ATTACH_MIN_KEYS = 3;

export function toAttachment(text: string, source: string): Attachment | null {
  const { keys } = parseKeys(text);
  if (!keys.length) return null;
  const line = (k: { label?: string; secret: string }) => (k.label ? `${k.label} ${maskSecret(k.secret)}` : maskSecret(k.secret));
  return {
    text,
    source,
    keys: keys.length,
    labeled: keys.filter((k) => k.label).length,
    preview: keys.slice(0, 3).map(line),
    all: keys.slice(0, 500).map(line),
  };
}

export function mergeAttachments(a: Attachment | null, b: Attachment): Attachment {
  if (!a) return b;
  return toAttachment(`${a.text}\n${b.text}`, `${a.source}, ${b.source}`) ?? b;
}

function AttachmentPreview({ att, onClear }: { att: Attachment; onClear: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="chat-reveal mx-1.5 mb-1 mt-1.5 rounded-2xl bg-ink/[0.04] px-3 py-2">
      <div className="flex items-center gap-2.5">
        <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-surface text-brand shadow-card">
          <KeyRound className="h-3.5 w-3.5" aria-hidden="true" />
        </span>
        <div className="min-w-0 flex-1 leading-tight">
          <div className="truncate text-[13px] font-medium text-ink">
            {att.keys} {att.keys === 1 ? "key" : "keys"} detected
            {att.labeled > 0 && <span className="font-normal text-muted">, {att.labeled} labeled</span>}
          </div>
          <div className="truncate text-[11.5px] text-muted">
            Sent to Claude as placeholders <span className="text-faint">· {att.source}</span>
          </div>
        </div>
        <button
          type="button"
          onClick={() => setOpen(!open)}
          aria-expanded={open}
          className="press tap-inline shrink-0 rounded-md px-1 text-[12px] font-medium text-muted transition-colors hover:text-ink"
        >
          {open ? "Hide" : "Show"}
        </button>
        <button
          type="button"
          onClick={onClear}
          aria-label="Remove the keys"
          title="Remove"
          className="press icon-btn inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-full text-faint transition-[background-color,color,transform] duration-150 hover:bg-ink/[0.06] hover:text-ink"
        >
          <X className="h-3.5 w-3.5" aria-hidden="true" />
        </button>
      </div>
      {!open && (
        <div className="mt-1.5 flex flex-wrap gap-1 pl-[38px]">
          {att.preview.map((p, i) => (
            <span key={i} className="max-w-[12rem] truncate rounded-md bg-surface px-1.5 py-0.5 font-mono text-[11px] text-muted shadow-card">
              {p}
            </span>
          ))}
          {att.keys > att.preview.length && (
            <span className="px-1 py-0.5 font-mono text-[11px] text-faint">+{att.keys - att.preview.length} more</span>
          )}
        </div>
      )}
      {open && (
        <ul className="chat-reveal mt-1.5 max-h-40 overflow-y-auto pl-[38px] font-mono text-[11.5px] leading-5 text-muted">
          {att.all.map((p, i) => (
            <li key={i} className="truncate">
              {p}
            </li>
          ))}
          {att.keys > att.all.length && <li className="text-faint">and {att.keys - att.all.length} more</li>}
        </ul>
      )}
    </div>
  );
}

export const Composer = forwardRef<
  HTMLTextAreaElement,
  {
    value: string;
    onChange: (v: string) => void;
    onSubmit: (e?: FormEvent) => void;
    busy: boolean;
    off: boolean;
    model?: string;
    attachment: Attachment | null;
    onAttach: (a: Attachment) => void;
    onClearAttachment: () => void;
  }
>(function Composer({ value, onChange, onSubmit, busy, off, model, attachment, onAttach, onClearAttachment }, ref) {
  const inner = useRef<HTMLTextAreaElement | null>(null);
  const setRef = (el: HTMLTextAreaElement | null) => {
    inner.current = el;
    if (typeof ref === "function") ref(el);
    else if (ref) ref.current = el;
  };
  // Grow with the text up to a cap (a third of the screen, at most 200px), then scroll inside.
  useLayoutEffect(() => {
    const el = inner.current;
    if (!el) return;
    el.style.height = "auto";
    const cap = Math.min(200, Math.round(window.innerHeight / 3));
    el.style.height = `${Math.min(el.scrollHeight, cap)}px`;
  }, [value]);

  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      onSubmit();
    } else if (e.key === "Escape") {
      e.currentTarget.blur();
    }
  };
  // Many keys, or a long block with keys, go to the attachment instead of the textarea.
  const onPaste = (e: ClipboardEvent<HTMLTextAreaElement>) => {
    const text = e.clipboardData.getData("text/plain");
    if (!text) return;
    const att = toAttachment(text, "Pasted");
    if (!att || (att.keys < ATTACH_MIN_KEYS && text.length < 1500)) return;
    e.preventDefault();
    onAttach(att);
    if (!value.trim()) onChange("Add these keys");
  };
  const canSend = !busy && !off && (!!value.trim() || !!attachment);
  const keyish = !!attachment || KEYISH.test(value);

  return (
    <form onSubmit={onSubmit} className="w-full">
      <label htmlFor="assistant-input" className="sr-only">
        Message
      </label>
      <div
        className={cx(
          "rounded-[24px] border border-line bg-surface shadow-card transition-[border-color,box-shadow] duration-150",
          !off && "focus-within:border-line-strong focus-within:shadow-lift",
        )}
      >
        {attachment && <AttachmentPreview att={attachment} onClear={onClearAttachment} />}
        <div className="flex items-end gap-2 py-1.5 pl-4 pr-1.5">
          <textarea
            id="assistant-input"
            ref={setRef}
            rows={1}
            value={value}
            disabled={off}
            onChange={(e) => onChange(e.target.value)}
            onKeyDown={onKey}
            onPaste={onPaste}
            aria-describedby="assistant-hint"
            placeholder={off ? "The assistant is off" : "Ask anything, or paste or drop keys to add"}
            className="block max-h-[200px] min-h-[36px] w-full flex-1 resize-none overflow-y-auto bg-transparent py-[7px] text-[16px] leading-[22px] text-ink placeholder:text-faint focus:outline-hidden disabled:cursor-not-allowed sm:text-[14px]"
          />
          <button
            type="submit"
            aria-label={busy ? "Working on your last message" : "Send"}
            title={busy ? "Working" : "Send"}
            disabled={!canSend}
            className="icon-btn inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-ink text-on-ink transition-[background-color,color,transform] duration-150 ease-out hover:bg-ink-strong active:scale-[0.94] disabled:cursor-not-allowed disabled:bg-well disabled:text-faint disabled:active:scale-100"
          >
            {busy ? <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" /> : <ArrowUp className="h-4 w-4" aria-hidden="true" />}
          </button>
        </div>
      </div>
      <p
        id="assistant-hint"
        className="mt-1.5 px-4 text-center text-[11px] leading-snug text-faint [text-wrap:balance]"
        aria-live="polite"
      >
        {keyish ? (
          <span className="text-muted">
            Claude sees keys only as placeholders. This chat keeps their last 4 characters.
          </span>
        ) : (
          <>
            <span className="hidden [@media(hover:hover)]:inline">
              Enter to send, Shift+Enter for a new line, / to focus
            </span>
            {model && (
              <>
                <span className="mx-2 hidden [@media(hover:hover)]:inline" aria-hidden="true">
                  ·
                </span>
                <span className="font-mono">{model}</span>
              </>
            )}
          </>
        )}
      </p>
    </form>
  );
});

/** Keyboard shortcuts, listed in the Chats dialog. */
export function Shortcuts() {
  const mod = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform) ? "\u2318" : "Ctrl";
  const rows: Array<[string[], string]> = [
    [["/"], "Focus the message box"],
    [[mod, "K"], "Open chats"],
    [[mod, "Shift", "O"], "New chat"],
    [["Esc"], "Leave the message box"],
  ];
  return (
    <ul className="hidden grid-cols-2 gap-x-4 gap-y-1.5 border-t border-line pt-3 text-[12px] text-muted [@media(hover:hover)]:grid">
      {rows.map(([keys, label]) => (
        <li key={label} className="flex items-center gap-2">
          <span className="flex shrink-0 gap-0.5">
            {keys.map((k) => (
              <kbd key={k} className="min-w-[20px] rounded-sm border border-line bg-soft px-1 text-center font-mono text-[10.5px] leading-[18px] text-ink">
                {k}
              </kbd>
            ))}
          </span>
          <span className="truncate">{label}</span>
        </li>
      ))}
    </ul>
  );
}
