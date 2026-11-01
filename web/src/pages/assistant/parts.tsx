import { useState, type ClipboardEvent, type FormEvent, type Ref } from "react";
import {
  RiDashboard3Line,
  RiFlowChart,
  RiKey2Line,
  RiShieldCheckLine,
} from "@remixicon/react";
import { maskSecret, parseKeys } from "@/lib/keyparse";
import { useNow } from "@/hooks/useNow";
import type { ChatThread } from "@/lib/types";
import { CloseButton } from "@/components/base/buttons/close-button";
import { Kbd } from "@/components/base/kbd/kbd";
import { AgentChatHistory, compactAge } from "@/components/application/agent-chat/agent-chat-history";
import { AgentComposer, ComposerModel } from "@/components/application/agent-chat/agent-composer";
import { AgentThinking } from "@/components/application/agent-thinking/agent-thinking";
import { cx } from "@/utils/cx";
import { Mark } from "./items";

const EXAMPLES = [
  { text: "Add these SerpAPI keys: ", label: "Add these SerpAPI keys", hint: "Paste them after the colon", icon: RiKey2Line },
  { text: "Which keys are out of quota?", label: "Which keys are out of quota?", hint: "Across every service", icon: RiDashboard3Line },
  { text: "Create a proxy app for my website", label: "Create a proxy app", hint: "For my website", icon: RiFlowChart },
  { text: "Check every ElevenLabs key", label: "Check every ElevenLabs key", hint: "And flag the bad ones", icon: RiShieldCheckLine },
];

/** The chats in the Chats dialog. The open thread shows a working dot while its turn runs. */
export function ThreadList({
  threads,
  currentId,
  working,
  onPick,
  onDelete,
}: {
  threads: ChatThread[];
  currentId: number | null;
  working: boolean;
  onPick: (id: number) => void;
  onDelete: (t: ChatThread) => Promise<void>;
}) {
  const now = useNow(60_000);
  return (
    <AgentChatHistory
      threads={threads.map((t) => ({
        id: t.id,
        title: t.title || "New chat",
        age: compactAge(t.updated_at, now),
        running: working && t.id === currentId,
      }))}
      activeId={currentId}
      onSelect={onPick}
      onDelete={async (id) => {
        const t = threads.find((x) => x.id === id);
        if (t) await onDelete(t);
      }}
    />
  );
}

/** A new chat: the mark, one line on what the assistant does, and four starters. */
export function EmptyState({ off, onPick }: { off: boolean; onPick: (text: string) => void }) {
  return (
    <div className="fade-up flex min-h-full flex-col items-center justify-center py-8 text-center">
      <Mark size={44} />
      <h2 className="mt-4 text-title-2-medium text-text-primary">What can I help with?</h2>
      <p className="mt-1.5 max-w-sm text-body-regular text-text-secondary">
        I can add and check keys, find problems and set up apps. I always ask before changing or deleting anything.
      </p>
      <ul className="mt-6 grid w-full max-w-xl grid-cols-1 gap-2 sm:grid-cols-2">
        {EXAMPLES.map((ex) => (
          <li key={ex.text}>
            <button
              type="button"
              disabled={off}
              onClick={() => onPick(ex.text)}
              className={cx(
                "button-press-motion flex w-full cursor-pointer items-start gap-3 rounded-3xl border border-border-button-default bg-background-primary-default px-4 py-3.5 text-start shadow-xs",
                "transition-[background-color,border-color] duration-150 ease-out hover:border-border-button-hover hover:bg-background-primary-hover",
                "outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring",
                "disabled:cursor-not-allowed disabled:opacity-50",
              )}
            >
              <ex.icon className="mt-0.5 size-4 shrink-0 text-status-purple-text" aria-hidden />
              <span className="min-w-0">
                <span className="block text-body-medium text-text-primary">{ex.label}</span>
                <span className="block text-caption-1-regular text-text-secondary">{ex.hint}</span>
              </span>
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

/**
 * The live state of a turn: the mark inside an orbiting ring, and Agent
 * Thinking's dot wave with the current step from the server (crossfading as
 * it changes) and the time since the turn started. With reduced motion the
 * ring is gone and the label is plain text.
 */
export function LiveStatus({ step, startedAt }: { step?: string; startedAt?: string }) {
  const label = step?.trim() || "Working";
  const from = Date.parse(startedAt ?? "");
  return (
    <div className="fade-up flex items-center gap-3">
      <span className="chat-orbit relative inline-flex size-6 shrink-0 items-center justify-center">
        <Mark />
      </span>
      <span key={label} className="chat-swap min-w-0">
        <AgentThinking variant="wave" tone="default" label={label} since={Number.isNaN(from) ? undefined : from} />
      </span>
      <span className="sr-only" aria-live="polite">
        Claude is working: {label}
      </span>
    </div>
  );
}

/** Text that looks like a pasted API key: a long run of key-ish characters. */
const KEYISH = /[A-Za-z0-9_-]{24,}/;

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

/** The attachment tile above the prompt: key count, source, masked hints and a remove control. */
function AttachmentPreview({ att, onClear }: { att: Attachment; onClear: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="chat-reveal mx-2 mt-2 rounded-2xl border border-border-button-default bg-background-secondary-default px-3 py-2">
      <div className="flex items-center gap-2.5">
        <span className="flex size-8 shrink-0 items-center justify-center rounded-xl bg-status-purple-background text-status-purple-text">
          <RiKey2Line className="size-4" aria-hidden />
        </span>
        <div className="min-w-0 flex-1">
          <div className="truncate text-body-2-medium text-text-primary">
            {att.keys} {att.keys === 1 ? "key" : "keys"} detected
            {att.labeled > 0 && <span className="text-body-2-regular text-text-secondary">, {att.labeled} labeled</span>}
          </div>
          <div className="truncate text-caption-1-regular text-text-secondary">
            Sent to Claude as placeholders <span className="text-text-tertiary">· {att.source}</span>
          </div>
        </div>
        <button
          type="button"
          onClick={() => setOpen(!open)}
          aria-expanded={open}
          className="inline-flex h-7 shrink-0 cursor-pointer pointer-coarse:h-10 items-center rounded-md px-1.5 text-caption-1-medium text-text-secondary outline-none transition-colors duration-150 hover:text-text-primary focus-visible:ring-2 focus-visible:ring-border-focus-ring"
        >
          {open ? "Hide" : "Show"}
        </button>
        <CloseButton size="sm" aria-label="Remove the keys" title="Remove" onClick={onClear} className="pointer-coarse:size-10" />
      </div>
      {!open && (
        <div className="mt-1.5 flex flex-wrap gap-1 ps-[42px]">
          {att.preview.map((p, i) => (
            <span
              key={i}
              className="max-w-[12rem] truncate rounded-md border border-border-button-default bg-background-primary-default px-1.5 py-0.5 font-mono text-caption-2-regular text-text-secondary"
            >
              {p}
            </span>
          ))}
          {att.keys > att.preview.length && (
            <span className="px-1 py-0.5 font-mono text-caption-2-regular text-text-tertiary">+{att.keys - att.preview.length} more</span>
          )}
        </div>
      )}
      {open && (
        <ul className="chat-reveal mt-1.5 max-h-40 overflow-y-auto ps-[42px] font-mono text-caption-1-regular text-text-secondary">
          {att.all.map((p, i) => (
            <li key={i} className="truncate">
              {p}
            </li>
          ))}
          {att.keys > att.all.length && <li className="text-text-tertiary">and {att.keys - att.all.length} more</li>}
        </ul>
      )}
    </div>
  );
}

/**
 * The Assistant's composer: BoardUI's Agent Composer with the key paste
 * attachment, Enter to send, Shift+Enter for a new line, and the hint and
 * model underneath.
 */
export function Composer({
  ref,
  value,
  onChange,
  onSubmit,
  busy,
  off,
  model,
  attachment,
  onAttach,
  onClearAttachment,
}: {
  ref?: Ref<HTMLTextAreaElement>;
  value: string;
  onChange: (v: string) => void;
  onSubmit: (e?: FormEvent) => void;
  busy: boolean;
  off: boolean;
  model?: string;
  attachment: Attachment | null;
  onAttach: (a: Attachment) => void;
  onClearAttachment: () => void;
}) {
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
  const keyish = !!attachment || KEYISH.test(value);

  return (
    <AgentComposer
      id="assistant-input"
      textareaRef={ref}
      value={value}
      onValueChange={onChange}
      onSubmit={onSubmit}
      busy={busy}
      disabled={off}
      hasContent={!!attachment}
      placeholder={off ? "The assistant is off" : "Ask anything, or paste or drop keys to add"}
      onPaste={onPaste}
      attachment={attachment ? <AttachmentPreview att={attachment} onClear={onClearAttachment} /> : null}
      statusId="assistant-hint"
      status={
        <p className="text-caption-1-regular text-text-tertiary [text-wrap:balance]">
          {keyish ? (
            <span className="text-text-secondary">Claude sees keys only as placeholders. This chat keeps their last 4 characters.</span>
          ) : (
            <>
              <span className="hidden [@media(hover:hover)]:inline">Enter to send, Shift+Enter for a new line, / to focus</span>
              {model && (
                <>
                  <span className="mx-2 hidden [@media(hover:hover)]:inline" aria-hidden>
                    ·
                  </span>
                  <ComposerModel model={model} />
                </>
              )}
            </>
          )}
        </p>
      }
    />
  );
}

/** Keyboard shortcuts, listed in the Chats dialog. Hidden on touch screens. */
export function Shortcuts() {
  const mod = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform) ? "⌘" : "Ctrl";
  const rows: Array<[string[], string]> = [
    [["/"], "Focus the message box"],
    [[mod, "K"], "Open chats"],
    [[mod, "Shift", "O"], "New chat"],
    [["Esc"], "Leave the message box"],
  ];
  return (
    <ul
      aria-label="Keyboard shortcuts"
      className="hidden grid-cols-2 gap-x-4 gap-y-2 border-t border-separator-border pt-4 text-caption-1-regular text-text-secondary [@media(hover:hover)]:grid"
    >
      {rows.map(([keys, label]) => (
        <li key={label} className="flex items-center gap-2">
          <span className="flex shrink-0 gap-0.5">
            {keys.map((k) => (
              <Kbd key={k} className="min-w-5 px-1.5">
                {k}
              </Kbd>
            ))}
          </span>
          <span className="truncate">{label}</span>
        </li>
      ))}
    </ul>
  );
}
