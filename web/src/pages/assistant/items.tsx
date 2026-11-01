import { Fragment, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { motion, useReducedMotion } from "motion/react";
import {
  Button as AriaButton,
  Disclosure,
  DisclosurePanel,
} from "react-aria-components";
import {
  RiArrowDownSLine,
  RiArrowRightLine,
  RiCheckLine,
  RiCloseLine,
  RiFileCopyLine,
  RiForbidLine,
  RiKey2Line,
  RiPencilLine,
  RiRefreshLine,
  RiTimeLine,
} from "@remixicon/react";
import { safeAppPath } from "@/lib/url";
import type { ChatEntity, ChatItem } from "@/lib/types";
import { toast } from "@/stores/toast";
import { Mark as BrandMark } from "@/components/acn/brand";
import { Callout } from "@/components/acn/callout";
import { Button } from "@/components/base/buttons/button";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { CodeBlock } from "@/components/CodeBlock";
import { SecretBox, copyText } from "@/components/CopyButton";
import {
  AgentMessage,
  LINE_HIDDEN,
  LINE_SHOWN,
  LINE_TRANSITION,
  MessageAction,
  QUIET_BUTTON,
} from "@/components/application/agent-chat/agent-chat-message";
import { LogRow } from "@/components/application/agent-log/agent-log";
import { cx } from "@/utils/cx";
import { linkify } from "./cards";

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

type Entities = Map<string, ChatEntity> | undefined;

/** Renders `inline code` inside a line of plain text, and names from tool results as chips. */
function inline(text: string, entities?: Entities): ReactNode {
  const parts = text.split(/(`[^`\n]+`)/g);
  return parts.map((p, i) =>
    p.length > 2 && p.startsWith("`") && p.endsWith("`") ? (
      <code
        key={i}
        className="wrap-anywhere rounded-md bg-background-tertiary-default px-1.5 py-0.5 font-mono text-body-2-regular text-text-primary"
      >
        {p.slice(1, -1)}
      </code>
    ) : (
      <Fragment key={i}>{linkify(p, entities, (seg, k) => <Fragment key={k}>{seg}</Fragment>)}</Fragment>
    ),
  );
}

/**
 * Plain text with two small extras: `inline code` and lines starting with
 * "- " as a list. A reply that just arrived softens in block by block with
 * the Chat Starter's line entrance; `reveal` is the first block's delay in ms.
 */
export function RichText({ text, reveal, entities }: { text: string; reveal?: number; entities?: Entities }) {
  const reduce = useReducedMotion();
  const animate = reveal !== undefined && !reduce;
  const blocks: Array<{ list: boolean; lines: string[] }> = [];
  for (const line of text.split("\n")) {
    const bullet = /^\s*[-*] /.test(line);
    const value = bullet ? line.replace(/^\s*[-*] /, "") : line;
    const last = blocks[blocks.length - 1];
    if (last && last.list === bullet) last.lines.push(value);
    else blocks.push({ list: bullet, lines: [value] });
  }
  // Blank lines between blocks are already spacing; drop empty paragraphs.
  const shown = blocks.filter((b) => b.list || b.lines.join("\n").trim());
  const motionFor = (i: number) =>
    animate
      ? {
          initial: LINE_HIDDEN,
          animate: LINE_SHOWN,
          transition: { ...LINE_TRANSITION, delay: ((reveal ?? 0) + i * 90) / 1000 },
        }
      : { initial: false as const };
  return (
    <div className="flex flex-col gap-2.5">
      {shown.map((b, i) =>
        b.list ? (
          <motion.ul key={i} {...motionFor(i)} className="flex list-disc flex-col gap-1 ps-5 marker:text-text-tertiary">
            {b.lines.map((l, j) => (
              <li key={j} className="whitespace-pre-wrap ps-0.5">
                {inline(l, entities)}
              </li>
            ))}
          </motion.ul>
        ) : (
          <motion.p key={i} {...motionFor(i)} className="whitespace-pre-wrap">
            {inline(b.lines.join("\n").replace(/^\n+|\n+$/g, ""), entities)}
          </motion.p>
        ),
      )}
    </div>
  );
}

/** A quiet "Open" link to the page a reply or step points at. */
export function OpenLink({ link, label = "Open", className }: { link?: string; label?: string; className?: string }) {
  const to = safeAppPath(link);
  if (!to) return null;
  return (
    <Link
      to={to}
      className={cx(
        "inline-flex min-h-6 shrink-0 items-center gap-1 rounded-sm pointer-coarse:min-h-10 text-body-2-medium text-text-primary underline-offset-4 outline-none hover:underline focus-visible:ring-2 focus-visible:ring-border-focus-ring",
        className,
      )}
    >
      {label}
      <RiArrowRightLine className="size-3.5" aria-hidden />
    </Link>
  );
}

/** The product's dragon mark, as the assistant's avatar. The rounded violet tile is baked into the image. */
export function Mark({ size = 24, className }: { size?: number; className?: string }) {
  return <BrandMark size={size} className={className} />;
}

/** Width of the avatar column (24px mark plus a 12px gap), so steps and cards line up with the text. */
export const GUTTER = "ps-9";

/** Copy, with a check for a moment after it worked. */
function CopyAction({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  return (
    <MessageAction
      icon={done ? RiCheckLine : RiFileCopyLine}
      label={done ? "Copied" : "Copy"}
      aria-label={done ? "Copied" : "Copy message"}
      onClick={async () => {
        if (await copyText(text)) {
          setDone(true);
          window.setTimeout(() => setDone(false), 1500);
        } else toast.error("Could not copy. Select the text and copy it yourself.");
      }}
    />
  );
}

export function UserBubble({ text, keys, onEdit }: { text: string; keys?: number; onEdit?: () => void }) {
  // A message with many keys stays compact: the server keeps only their hints anyway.
  const long = text.length > 600;
  const [open, setOpen] = useState(false);
  return (
    <AgentMessage
      role="user"
      actions={onEdit ? <MessageAction icon={RiPencilLine} label="Edit" aria-label="Edit your last message" onClick={onEdit} /> : null}
    >
      {keys ? (
        <span className="mb-1 flex items-center gap-1.5 font-mono text-caption-1-regular text-text-secondary">
          <RiKey2Line className="size-3 shrink-0" aria-hidden />
          {keys} {keys === 1 ? "key" : "keys"}, sent to Claude as placeholders
        </span>
      ) : null}
      {long && !open ? `${text.slice(0, 600)}…` : text}
      {long && (
        <button
          type="button"
          onClick={() => setOpen(!open)}
          className="ms-1 rounded-sm text-caption-1-medium text-text-secondary underline-offset-4 outline-none hover:text-text-primary hover:underline focus-visible:ring-2 focus-visible:ring-border-focus-ring"
          aria-expanded={open}
        >
          {open ? "Show less" : "Show all"}
        </button>
      )}
    </AgentMessage>
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
    <AgentMessage
      role="assistant"
      avatar={<Mark />}
      actions={
        actions ? (
          <>
            <CopyAction text={item.text ?? ""} />
            {onRetry && <MessageAction icon={RiRefreshLine} label="Retry" aria-label="Retry your last message" onClick={onRetry} />}
            <OpenLink link={item.link} className="ms-1" />
          </>
        ) : item.link ? (
          <OpenLink link={item.link} />
        ) : null
      }
    >
      <div className="wrap-anywhere">
        <RichText text={item.text ?? ""} reveal={reveal} entities={entities} />
      </div>
    </AgentMessage>
  );
}

function StepRow({
  item,
  first,
  last,
  fresh,
}: {
  item: ChatItem;
  first: boolean;
  last: boolean;
  fresh: boolean;
}) {
  const reduceMotion = useReducedMotion() ?? false;
  const [open, setOpen] = useState(false);
  const hasInput = !!item.input && Object.keys(item.input).length > 0;
  const hasDetail = !!item.detail?.trim();
  const panelId = `step-${item.id}`;
  return (
    // Steps already in the thread when it opened render still; new ones blur in on the guide.
    <LogRow first={first} last={last} reduce={reduceMotion || !fresh} className="pb-2">
      <div className="flex items-start gap-2 py-1">
        <span
          className={cx(
            "mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-full",
            item.ok ? "bg-background-tertiary-default text-foreground-icon-primary" : "bg-status-rose-background text-status-rose-text",
          )}
          aria-hidden
        >
          {item.ok ? <RiCheckLine className="size-3" /> : <RiCloseLine className="size-3" />}
        </span>
        <div className="min-w-0 flex-1 text-body-2-regular">
          <span className="text-body-2-medium text-text-primary">{humanize(item.tool ?? "")}</span>
          {item.ok ? <span className="sr-only">, done</span> : <span className="text-body-2-medium text-text-error-primary"> failed</span>}
          {item.summary && (
            <span className="wrap-anywhere text-text-secondary">
              {" "}
              {linkify(item.summary, entityMap(item.entities), (seg, k) => <Fragment key={k}>{seg}</Fragment>)}
            </span>
          )}
          <span className="ms-2 inline-flex items-center gap-3 align-baseline">
            {(hasInput || hasDetail) && (
              <button
                type="button"
                onClick={() => setOpen(!open)}
                aria-expanded={open}
                aria-controls={panelId}
                className="rounded-sm text-caption-1-regular text-text-tertiary underline-offset-4 pointer-coarse:-my-3 pointer-coarse:inline-block pointer-coarse:py-3 outline-none transition-colors duration-150 hover:text-text-primary hover:underline focus-visible:ring-2 focus-visible:ring-border-focus-ring"
              >
                {open ? "Hide details" : "Details"}
              </button>
            )}
            <OpenLink link={item.link} className="text-caption-1-medium" />
          </span>
        </div>
      </div>
      {item.secret && (
        <div className="mt-1 flex flex-col gap-2 ps-6">
          <Callout tone="warning">Copy this token now. It is shown once.</Callout>
          <SecretBox value={item.secret} />
        </div>
      )}
      {open && (
        <div id={panelId} className="chat-reveal mt-1 flex flex-col gap-2.5 ps-6">
          {hasInput && <CodeBlock label="input" code={pretty(item.input)} />}
          {hasDetail && <CodeBlock label="result" code={pretty(item.detail)} />}
        </div>
      )}
    </LogRow>
  );
}

function entityMap(list?: ChatEntity[]): Entities {
  if (!list?.length) return undefined;
  const m = new Map<string, ChatEntity>();
  for (const e of list) if (e.name && e.name.length >= 3 && !m.has(e.name)) m.set(e.name, e);
  return m;
}

function plural(n: number, one: string, many: string) {
  return `${n} ${n === 1 ? one : many}`;
}

/**
 * A run of tool calls folded into one quiet line, "Worked for 6s, 2 actions",
 * that opens to the steps on Agent Log's tree guide. It starts open when a
 * step holds a one time token. The panel uses Accordion's height and blur
 * recipe on a React Aria Disclosure.
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
  const ease = "ease-[cubic-bezier(0.22,1,0.36,1)]";
  return (
    <Disclosure isExpanded={open} onExpandedChange={setOpen} className="group/steps">
      <AriaButton
        slot="trigger"
        className="-ms-1 inline-flex min-h-7 cursor-pointer pointer-coarse:min-h-10 items-center gap-1 rounded-md px-1 text-body-2-regular text-text-secondary outline-none transition-colors duration-150 hover:text-text-primary data-[focus-visible]:ring-2 data-[focus-visible]:ring-border-focus-ring"
      >
        <span>
          {seconds !== null && seconds >= 1 ? `Worked for ${seconds}s` : "Worked"}
          <span className="text-text-tertiary">, {plural(items.length, "action", "actions")}</span>
          {failed > 0 && <span className="text-text-error-primary">, {failed} failed</span>}
        </span>
        <RiArrowDownSLine
          className={cx(
            "size-4 text-foreground-icon-tertiary transition-[rotate] duration-300 motion-reduce:transition-none group-data-[expanded]/steps:rotate-180",
            ease,
          )}
          aria-hidden
        />
      </AriaButton>
      <DisclosurePanel
        className={cx("h-(--disclosure-panel-height) overflow-clip transition-[height] duration-300 motion-reduce:transition-none", ease)}
      >
        <ol className="mt-1.5 ms-2">
          {items.map((it, i) => (
            <StepRow key={it.id} item={it} first={i === 0} last={i === items.length - 1} fresh={fresh(it) !== undefined} />
          ))}
        </ol>
      </DisclosurePanel>
    </Disclosure>
  );
}

const RESOLVED: Record<string, { label: string; icon: typeof RiCheckLine; tone: string }> = {
  approved: { label: "Approved and done", icon: RiCheckLine, tone: "text-foreground-icon-primary" },
  cancelled: { label: "Cancelled. Nothing was changed.", icon: RiForbidLine, tone: "text-foreground-icon-tertiary" },
  expired: { label: "Expired. Nothing was changed.", icon: RiTimeLine, tone: "text-foreground-icon-tertiary" },
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
      className="rounded-3xl border border-border-button-default bg-background-primary-default shadow-xs"
    >
      <div className="px-4 pt-3.5 pb-3">
        <div className="text-caption-2-semibold text-text-tertiary uppercase">{waiting ? "Needs your approval" : "Approval"}</div>
        <div className={cx("mt-1.5 text-body-regular transition-colors duration-200", waiting ? "text-text-primary" : "text-text-secondary")}>
          <RichText text={item.text || "Confirm this change?"} />
        </div>
      </div>
      {waiting ? (
        <div className="flex flex-wrap items-center justify-end gap-2 px-4 pb-3.5">
          <OpenLink link={item.link} className="me-auto" />
          <Button
            size="small"
            variant="secondary"
            className="flex-1 sm:flex-none"
            leadingIcon={which === false ? ButtonSpinner : undefined}
            disabled={busy}
            onClick={() => void answer(false)}
          >
            Cancel
          </Button>
          <Button
            size="small"
            variant="danger"
            className="flex-1 sm:flex-none"
            leadingIcon={which === true ? ButtonSpinner : undefined}
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
            className="chat-reveal flex items-center gap-2 border-t border-separator-border px-4 py-2.5 text-body-2-regular text-text-secondary"
          >
            <done.icon className={cx("size-3.5 shrink-0", done.tone)} aria-hidden />
            <span className="flex-1">{done.label}</span>
            <OpenLink link={item.link} />
          </div>
        )
      )}
    </div>
  );
}

export function ErrorItem({ text, onRetry }: { text?: string; onRetry?: () => void }) {
  return (
    <Callout
      tone="error"
      action={onRetry ? <Button size="xs" variant="ghost" className={QUIET_BUTTON} leadingIcon={RiRefreshLine} onClick={onRetry}>Retry</Button> : undefined}
    >
      <span className="wrap-anywhere">{text || "Something went wrong."}</span>
    </Callout>
  );
}
