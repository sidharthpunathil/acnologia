import { Fragment, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import {
  ArrowRight,
  Ban,
  Check,
  ChevronRight,
  Clock,
  KeyRound,
  Pencil,
  RotateCcw,
  TriangleAlert,
  X,
} from "lucide-react";
import { safeAppPath } from "../../lib/url";
import type { ChatEntity, ChatItem } from "../../lib/types";
import { linkify } from "./cards";
import { CopyButton, SecretBox } from "../../components/CopyButton";
import { Button, Callout, cx } from "../../components/ui";

/** "add_keys" becomes "Add keys". */
function humanize(tool: string): string {
  const s = tool.replace(/[_-]+/g, " ").trim();
  return s ? s[0].toUpperCase() + s.slice(1) : "Tool";
}

function pretty(v: unknown): string {
  if (typeof v === "string") {
    try {
      return JSON.stringify(JSON.parse(v), null, 2);
    } catch {
      return v;
    }
  }
  return JSON.stringify(v, null, 2);
}

/**
 * Word by word reveal for text that just arrived. Each call hands out the next
 * delay, so words fade in reading order across paragraphs and list items.
 */
class Reveal {
  private n = 0;
  constructor(private readonly base: number) {}
  next(): number {
    // Fast at first, and capped so a long reply is fully in within about a second.
    return this.base + Math.min(this.n++ * 14, 900);
  }
}

function words(text: string, reveal: Reveal | null): ReactNode {
  if (!reveal) return text;
  return text.split(/(\s+)/).map((w, i) =>
    /^\s*$/.test(w) ? (
      <Fragment key={i}>{w}</Fragment>
    ) : (
      <span
        key={i}
        className="chat-word"
        style={{ animationDelay: `${reveal.next()}ms` }}
      >
        {w}
      </span>
    ),
  );
}

type Entities = Map<string, ChatEntity> | undefined;

/** Renders `inline code` inside a line of plain text, and names from tool results as chips. */
function inline(text: string, reveal: Reveal | null, entities?: Entities): ReactNode {
  const parts = text.split(/(`[^`\n]+`)/g);
  return parts.map((p, i) =>
    p.length > 2 && p.startsWith("`") && p.endsWith("`") ? (
      <code
        key={i}
        className={cx(
          "wrap-anywhere rounded-md bg-ink/[0.06] px-1.5 py-0.5 font-mono text-[0.86em] text-ink",
          reveal && "chat-word",
        )}
        style={reveal ? { animationDelay: `${reveal.next()}ms` } : undefined}
      >
        {p.slice(1, -1)}
      </code>
    ) : (
      <Fragment key={i}>{linkify(p, entities, (seg, k) => <Fragment key={k}>{words(seg, reveal)}</Fragment>)}</Fragment>
    ),
  );
}

/** Plain text with two small extras: `inline code` and lines starting with "- " as a list. */
export function RichText({ text, reveal, entities }: { text: string; reveal?: number; entities?: Entities }) {
  const r = reveal !== undefined ? new Reveal(reveal) : null;
  const blocks: Array<{ list: boolean; lines: string[] }> = [];
  for (const line of text.split("\n")) {
    const bullet = /^\s*[-*] /.test(line);
    const value = bullet ? line.replace(/^\s*[-*] /, "") : line;
    const last = blocks[blocks.length - 1];
    if (last && last.list === bullet) last.lines.push(value);
    else blocks.push({ list: bullet, lines: [value] });
  }
  return (
    <div className="space-y-2.5">
      {blocks.map((b, i) =>
        b.list ? (
          <ul key={i} className="list-disc space-y-1 pl-5 marker:text-faint">
            {b.lines.map((l, j) => (
              <li key={j} className="whitespace-pre-wrap pl-0.5">
                {inline(l, r, entities)}
              </li>
            ))}
          </ul>
        ) : // Blank lines between blocks are already spacing; drop them at the edges.
        b.lines.join("\n").trim() ? (
          <p key={i} className="whitespace-pre-wrap">
            {inline(b.lines.join("\n").replace(/^\n+|\n+$/g, ""), r, entities)}
          </p>
        ) : null,
      )}
    </div>
  );
}

function OpenLink({ link, className }: { link?: string; className?: string }) {
  const to = safeAppPath(link);
  if (!to) return null;
  return (
    <Link
      to={to}
      className={cx(
        "tap-inline inline-flex shrink-0 items-center gap-1 rounded-md text-[12.5px] font-medium text-ink underline-offset-4 hover:underline",
        className,
      )}
    >
      Open <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
    </Link>
  );
}

/** The product's dragon mark. The rounded violet tile is baked into the image, so nothing is drawn around it. */
export function Mark({
  size = 24,
  className,
}: {
  size?: number;
  className?: string;
}) {
  return (
    <img
      src="/mark.png"
      alt=""
      aria-hidden="true"
      width={size}
      height={size}
      draggable={false}
      className={cx("shrink-0 select-none", className)}
      style={{ width: size, height: size }}
    />
  );
}

/** Width of the avatar column (24px mark plus a 12px gap), so steps and approvals line up with the text. */
export const GUTTER = "pl-9";

/** A quiet text action under a message: Copy, Retry, Edit. */
function MsgAction({ icon, label, onClick }: { icon: ReactNode; label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="press tap-inline inline-flex items-center gap-1 rounded-md px-1 text-[12px] text-faint transition-[color,transform] duration-150 hover:text-ink"
    >
      {icon}
      {label}
    </button>
  );
}

/** Shown on hover or focus with a mouse; always shown on touch screens. */
const ACTIONS_ROW =
  "flex items-center gap-3 transition-opacity duration-150 [@media(hover:hover)]:opacity-0 [@media(hover:hover)]:focus-within:opacity-100";

export function UserBubble({ text, keys, onEdit }: { text: string; keys?: number; onEdit?: () => void }) {
  // A message with many keys stays compact: the server keeps only their hints anyway.
  const long = text.length > 600;
  const [open, setOpen] = useState(false);
  return (
    <div className="group/user flex flex-col items-end">
      <div className="max-w-[85%] whitespace-pre-wrap break-words rounded-[20px] bg-surface px-4 py-2 text-[15px] leading-[1.55] text-ink shadow-card sm:max-w-[75%] sm:text-[14px]">
        {keys ? (
          <span className="mb-1 flex items-center gap-1.5 font-mono text-[11px] text-muted">
            <KeyRound className="h-3 w-3" aria-hidden="true" />
            {keys} {keys === 1 ? "key" : "keys"}, sent to Claude as placeholders
          </span>
        ) : null}
        {long && !open ? `${text.slice(0, 600)}…` : text}
        {long && (
          <button
            type="button"
            onClick={() => setOpen(!open)}
            className="press ml-1 text-[12px] font-medium text-muted underline-offset-4 hover:text-ink hover:underline"
            aria-expanded={open}
          >
            {open ? "Show less" : "Show all"}
          </button>
        )}
      </div>
      {onEdit && (
        <div className={cx(ACTIONS_ROW, "-mr-1 mt-1 [@media(hover:hover)]:group-hover/user:opacity-100")}>
          <MsgAction icon={<Pencil className="h-3 w-3" aria-hidden="true" />} label="Edit" onClick={onEdit} />
        </div>
      )}
    </div>
  );
}

/** A reply: plain text on the page, no bubble. Every reply can be copied; the last one can be retried. */
export function AssistantText({
  item,
  reveal,
  actions,
  entities,
  onRetry,
}: {
  item: ChatItem;
  reveal?: number;
  actions?: boolean;
  entities?: Entities;
  onRetry?: () => void;
}) {
  return (
    <div className="group/msg flex gap-3">
      <Mark className="mt-px" />
      <div className="min-w-0 flex-1">
        <div className="wrap-anywhere text-[15px] leading-[1.65] text-ink sm:text-[14px]">
          <RichText text={item.text ?? ""} reveal={reveal} entities={entities} />
        </div>
        {actions ? (
          <div className={cx(ACTIONS_ROW, "-ml-2 mt-1 [@media(hover:hover)]:group-hover/msg:opacity-100")}>
            <CopyButton text={item.text ?? ""} label="Copy" className="press text-faint" />
            {onRetry && (
              <MsgAction icon={<RotateCcw className="h-3 w-3" aria-hidden="true" />} label="Retry" onClick={onRetry} />
            )}
            <OpenLink link={item.link} />
          </div>
        ) : (
          item.link && (
            <div className="mt-1.5">
              <OpenLink link={item.link} />
            </div>
          )
        )}
      </div>
    </div>
  );
}

function StepRow({ item, delay }: { item: ChatItem; delay?: number }) {
  const [open, setOpen] = useState(false);
  const hasInput = !!item.input && Object.keys(item.input).length > 0;
  const hasDetail = !!item.detail?.trim();
  const panelId = `step-${item.id}`;
  return (
    <li
      className={cx("relative", delay !== undefined && "fade-up")}
      style={delay ? { animationDelay: `${delay}ms` } : undefined}
    >
      <div className="flex items-start gap-2.5">
        <span
          className={cx(
            "relative z-[1] mt-[3px] flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded-full",
            item.ok ? "bg-ink text-on-ink" : "bg-red-600 text-white",
          )}
          aria-hidden="true"
        >
          {item.ok ? (
            <Check className="h-2.5 w-2.5" strokeWidth={3} />
          ) : (
            <X className="h-2.5 w-2.5" strokeWidth={3} />
          )}
        </span>
        <div className="min-w-0 flex-1 text-[13px] leading-snug">
          <span className="font-medium text-ink">
            {humanize(item.tool ?? "")}
          </span>
          {item.ok ? (
            <span className="sr-only">, done</span>
          ) : (
            <span className="font-medium text-red-700"> failed</span>
          )}
          {item.summary && (
            <span className="wrap-anywhere text-muted">
              {" "}
              {linkify(item.summary, entityMap(item.entities), (seg, k) => <Fragment key={k}>{seg}</Fragment>)}
            </span>
          )}
          <span className="ml-2 inline-flex items-center gap-3 align-baseline">
            {(hasInput || hasDetail) && (
              <button
                type="button"
                onClick={() => setOpen(!open)}
                aria-expanded={open}
                aria-controls={panelId}
                className="tap-inline rounded-sm text-[12px] text-faint underline-offset-4 transition-colors hover:text-ink hover:underline"
              >
                {open ? "Hide details" : "Details"}
              </button>
            )}
            <OpenLink link={item.link} className="text-[12px]" />
          </span>
        </div>
      </div>
      {item.secret && (
        <div className="mt-2 space-y-2 pl-6">
          <Callout
            tone="amber"
            icon={<TriangleAlert className="h-4 w-4" aria-hidden="true" />}
          >
            Copy this token now. It is shown once.
          </Callout>
          <SecretBox value={item.secret} />
        </div>
      )}
      {open && (
        <div id={panelId} className="chat-reveal mt-2 space-y-2.5 pl-6">
          {hasInput && <Code label="Input" value={pretty(item.input)} />}
          {hasDetail && <Code label="Result" value={pretty(item.detail)} />}
        </div>
      )}
    </li>
  );
}

function entityMap(list?: ChatEntity[]): Entities {
  if (!list?.length) return undefined;
  const m = new Map<string, ChatEntity>();
  for (const e of list) if (e.name && e.name.length >= 3 && !m.has(e.name)) m.set(e.name, e);
  return m;
}

function Code({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="mb-1 font-mono text-[11px] uppercase tracking-wide text-faint">
        {label}
      </div>
      <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-line bg-surface px-3 py-2 font-mono text-[12px] leading-relaxed text-muted">
        {value}
      </pre>
    </div>
  );
}

function plural(n: number, one: string, many: string) {
  return `${n} ${n === 1 ? one : many}`;
}

/**
 * A run of tool calls folded into one quiet line, "Worked for 6s · 2 actions",
 * that opens to the steps. It starts open when a step holds a one time token.
 */
export function Steps({
  items,
  seconds,
  fresh,
}: {
  items: ChatItem[];
  seconds: number | null;
  fresh: (it: ChatItem) => number | undefined;
}) {
  const hasSecret = items.some((it) => it.secret);
  const [open, setOpen] = useState(hasSecret);
  const failed = items.filter((it) => !it.ok).length;
  const id = `steps-${items[0]?.id}`;
  return (
    <div>
      <button
        type="button"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        aria-controls={id}
        className="tap-inline group/steps -ml-1 inline-flex items-center gap-1 rounded-md px-1 text-[13px] text-muted transition-colors hover:text-ink"
      >
        <span>
          {seconds !== null && seconds >= 1
            ? `Worked for ${seconds}s`
            : "Worked"}
          <span className="text-faint">
            {" "}
            · {plural(items.length, "action", "actions")}
          </span>
          {failed > 0 && (
            <span className="text-red-700"> · {failed} failed</span>
          )}
        </span>
        <ChevronRight
          className={cx(
            "h-3.5 w-3.5 text-faint transition-transform duration-200 ease-out group-hover/steps:text-ink",
            open && "rotate-90",
          )}
          aria-hidden="true"
        />
      </button>
      {open && (
        <ol
          id={id}
          className="chat-reveal relative mt-2 space-y-2.5 pb-1 before:absolute before:bottom-2 before:left-[6.5px] before:top-2 before:w-px before:bg-line-strong"
        >
          {items.map((it) => (
            <StepRow key={it.id} item={it} delay={fresh(it)} />
          ))}
        </ol>
      )}
    </div>
  );
}

const RESOLVED: Record<string, { label: string; icon: ReactNode }> = {
  approved: {
    label: "Approved and done",
    icon: <Check className="h-3.5 w-3.5 text-ink" aria-hidden="true" />,
  },
  cancelled: {
    label: "Cancelled. Nothing was changed.",
    icon: <Ban className="h-3.5 w-3.5 text-faint" aria-hidden="true" />,
  },
  expired: {
    label: "Expired. Nothing was changed.",
    icon: <Clock className="h-3.5 w-3.5 text-faint" aria-hidden="true" />,
  },
};

/** An action waiting for approval. Neutral throughout; only the Confirm button carries the danger color. */
export function ConfirmCard({
  item,
  busy,
  onAnswer,
}: {
  item: ChatItem;
  busy: boolean;
  onAnswer: (approve: boolean) => Promise<void>;
}) {
  const [which, setWhich] = useState<boolean | null>(null);
  const status = item.pending?.status ?? "expired";
  const waiting = status === "waiting";
  const answer = async (approve: boolean) => {
    setWhich(approve);
    try {
      await onAnswer(approve);
    } finally {
      setWhich(null);
    }
  };
  const done = RESOLVED[status];
  return (
    <div
      role="group"
      aria-label={waiting ? "Approval needed" : "Approval"}
      className="rounded-2xl border border-line bg-surface shadow-card"
    >
      <div className="px-4 pb-3 pt-3.5">
        <div className="font-mono text-[11px] uppercase tracking-wide text-faint">
          {waiting ? "Needs your approval" : "Approval"}
        </div>
        <div
          className={cx(
            "mt-1.5 text-[14px] leading-relaxed transition-colors duration-200",
            waiting ? "text-ink" : "text-muted",
          )}
        >
          <RichText text={item.text || "Confirm this change?"} />
        </div>
      </div>
      {waiting ? (
        <div className="flex flex-wrap items-center justify-end gap-2 px-4 pb-3.5">
          <OpenLink link={item.link} className="mr-auto" />
          <Button
            size="sm"
            variant="ghost"
            className="flex-1 sm:flex-none"
            loading={which === false}
            disabled={busy}
            onClick={() => void answer(false)}
          >
            Cancel
          </Button>
          <Button
            size="sm"
            variant="danger"
            className="flex-1 sm:flex-none"
            loading={which === true}
            disabled={busy}
            onClick={() => void answer(true)}
          >
            Confirm
          </Button>
        </div>
      ) : (
        done && (
          <div
            key={status}
            className="chat-reveal flex items-center gap-2 border-t border-line px-4 py-2.5 text-[13px] text-muted"
          >
            {done.icon}
            <span className="flex-1">{done.label}</span>
            <OpenLink link={item.link} />
          </div>
        )
      )}
    </div>
  );
}

export function ErrorItem({ text }: { text?: string }) {
  return (
    <div className="flex items-start gap-2 text-[13px] leading-relaxed text-red-700">
      <X className="mt-[3px] h-3.5 w-3.5 shrink-0" aria-hidden="true" />
      <span className="wrap-anywhere">{text || "Something went wrong."}</span>
    </div>
  );
}
