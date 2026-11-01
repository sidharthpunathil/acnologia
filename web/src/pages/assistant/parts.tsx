import {
  forwardRef,
  useLayoutEffect,
  useRef,
  useState,
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
} from "lucide-react";
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
              "group relative flex items-center rounded-lg transition-colors duration-150",
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

/** Shown while a reply is pending: a shimmering label that counts seconds, so a long tool run does not look stuck. */
export function Working() {
  const [start] = useState(() => Date.now());
  const now = useNow(1000);
  const secs = Math.max(0, Math.floor((now - start) / 1000));
  return (
    <div role="status" className="fade-up flex items-center gap-3">
      <Mark />
      <span className="text-[14px]">
        <span className="chat-shimmer font-medium">Working</span>
        <span
          className="ml-1.5 font-mono text-[12px] tabular-nums text-faint"
          aria-hidden="true"
        >
          {secs}s
        </span>
      </span>
      <span className="sr-only">Claude is working on your message</span>
    </div>
  );
}

/** Text that looks like a pasted API key: a long run of key-ish characters. */
const KEYISH = /[A-Za-z0-9_\-]{24,}/;

export const Composer = forwardRef<
  HTMLTextAreaElement,
  {
    value: string;
    onChange: (v: string) => void;
    onSubmit: (e?: FormEvent) => void;
    busy: boolean;
    off: boolean;
    model?: string;
  }
>(function Composer({ value, onChange, onSubmit, busy, off, model }, ref) {
  const inner = useRef<HTMLTextAreaElement | null>(null);
  const setRef = (el: HTMLTextAreaElement | null) => {
    inner.current = el;
    if (typeof ref === "function") ref(el);
    else if (ref) ref.current = el;
  };
  // Grow with the text up to a cap, then scroll inside.
  useLayoutEffect(() => {
    const el = inner.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 200)}px`;
  }, [value]);

  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      onSubmit();
    }
  };
  const canSend = !busy && !off && !!value.trim();
  const keyish = KEYISH.test(value);

  return (
    <form onSubmit={onSubmit} className="w-full">
      <label htmlFor="assistant-input" className="sr-only">
        Message
      </label>
      <div
        className={cx(
          "flex items-end gap-2 rounded-[24px] border border-line bg-surface py-1.5 pl-4 pr-1.5 shadow-card transition-[border-color,box-shadow] duration-150",
          !off && "focus-within:border-line-strong focus-within:shadow-lift",
        )}
      >
        <textarea
          id="assistant-input"
          ref={setRef}
          rows={1}
          value={value}
          disabled={off}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={onKey}
          aria-describedby="assistant-hint"
          placeholder={
            off ? "The assistant is off" : "Ask anything, or paste keys to add"
          }
          className="block max-h-[200px] min-h-[36px] w-full flex-1 resize-none bg-transparent py-[7px] text-[16px] leading-[22px] text-ink placeholder:text-faint focus:outline-none disabled:cursor-not-allowed sm:text-[14px]"
        />
        <button
          type="submit"
          aria-label={busy ? "Working on your last message" : "Send"}
          title={busy ? "Working" : "Send"}
          disabled={!canSend}
          className="icon-btn inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-ink text-on-ink transition-[background-color,color,transform] duration-150 ease-out hover:bg-ink-strong active:scale-[0.94] disabled:cursor-not-allowed disabled:bg-well disabled:text-faint disabled:active:scale-100"
        >
          {busy ? (
            <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
          ) : (
            <ArrowUp className="h-4 w-4" aria-hidden="true" />
          )}
        </button>
      </div>
      <p
        id="assistant-hint"
        className="mt-1.5 px-4 text-center text-[11px] leading-snug text-faint [text-wrap:balance]"
        aria-live="polite"
      >
        {keyish ? (
          <span className="text-muted">
            Keys go to Claude to be added. This chat keeps only their last 4
            characters.
          </span>
        ) : (
          <>
            <span className="hidden [@media(hover:hover)]:inline">
              Enter to send, Shift+Enter for a new line
            </span>
            {model && (
              <>
                <span
                  className="mx-2 hidden [@media(hover:hover)]:inline"
                  aria-hidden="true"
                >
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
