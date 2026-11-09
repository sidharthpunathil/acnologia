import { Fragment, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
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
  RiInformationLine,
  RiKey2Line,
  RiPencilLine,
  RiRefreshLine,
  RiShieldKeyholeLine,
  RiTimeLine,
} from "@remixicon/react";
import { safeAppPath } from "@/lib/url";
import type { ChatEntity, ChatItem } from "@/lib/types";
import { toast } from "@/stores/toast";
import { Mark as BrandMark } from "@/components/acn/brand";
import { Callout } from "@/components/acn/callout";
import { Chip } from "@/components/base/badges/chip";
import { Button } from "@/components/base/buttons/button";
import { Popover, PopoverTrigger } from "@/components/base/popover/popover";
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
import { LogRow, ShimmerText } from "@/components/application/agent-log/agent-log";
import { cx } from "@/utils/cx";
import { linkify } from "./cards";
import { formatHint, legacyKeys, type KeyRef } from "./keyhint";

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

/** At most one blank line between paragraphs, and none at the ends. */
export function collapseBlank(text: string): string {
  return text.replace(/\r\n?/g, "\n").replace(/\n[ \t]*(?:\n[ \t]*)+/g, "\n\n").trim();
}

const URL_RE = /\bhttps?:\/\/[^\s<>"'`]+/g;

/** "https://www.app.tavily.com/home/" becomes "app.tavily.com/home", cut to a readable length. */
function shortUrl(href: string): string {
  let shown = href;
  try {
    const u = new URL(href);
    shown = `${u.hostname.replace(/^www\./, "")}${u.pathname === "/" ? "" : u.pathname.replace(/\/$/, "")}${u.search}`;
  } catch {
    // Not a URL after all: show it as typed.
  }
  return shown.length > 40 ? `${shown.slice(0, 39)}\u2026` : shown;
}

/** Plain text with web addresses as links that open in a new tab. */
function autolink(text: string): ReactNode {
  const out: ReactNode[] = [];
  let last = 0;
  for (const m of text.matchAll(URL_RE)) {
    // Trailing punctuation ends the sentence, not the address.
    const href = m[0].replace(/[.,;:!?)\]]+$/, "");
    const at = m.index ?? 0;
    if (at > last) out.push(text.slice(last, at));
    out.push(
      <a
        key={at}
        href={href}
        target="_blank"
        rel="noopener noreferrer"
        title={href}
        className="rounded-sm text-text-primary underline decoration-border-button-hover underline-offset-4 outline-none hover:decoration-text-primary focus-visible:ring-2 focus-visible:ring-border-focus-ring"
      >
        {shortUrl(href)}
      </a>,
    );
    last = at + href.length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

/** Keys shown before "+N more". */
const KEYS_SHOWN = 8;

/**
 * The keys a message carried, under its prose: a shield, how many are in the
 * vault, an explainer, and one chip per key with its label and last
 * characters. The secrets themselves never reach the browser.
 */
const WIDE = "(min-width: 640px)";
function subscribeWide(on: () => void) {
  const mq = window.matchMedia(WIDE);
  mq.addEventListener("change", on);
  return () => mq.removeEventListener("change", on);
}

function SecuredKeys({ keys, count, divided }: { keys: KeyRef[]; count?: number; divided: boolean }) {
  const [all, setAll] = useState(false);
  const block = useRef<HTMLDivElement>(null);
  const wide = useSyncExternalStore(subscribeWide, () => window.matchMedia(WIDE).matches, () => true);
  const shown = all ? keys : keys.slice(0, KEYS_SHOWN);
  const more = keys.length - KEYS_SHOWN;
  const n = count ?? keys.length;
  return (
    <div ref={block} className={cx("whitespace-normal", divided && "mt-2.5 border-t border-separator-border pt-2.5")}>
      <div className="flex items-center gap-1.5 text-caption-1-medium text-text-secondary">
        <RiShieldKeyholeLine className="size-3.5 shrink-0 text-status-purple-text" aria-hidden />
        <span>
          {n} {n === 1 ? "key" : "keys"} secured
        </span>
        <PopoverTrigger>
          <button
            type="button"
            aria-label="How keys are kept"
            className="-m-1 inline-flex size-6 cursor-pointer items-center justify-center rounded-md text-foreground-icon-tertiary outline-none transition-colors duration-150 pointer-coarse:size-8 hover:text-foreground-icon-primary focus-visible:ring-2 focus-visible:ring-border-focus-ring"
          >
            <RiInformationLine className="size-3.5" aria-hidden />
          </button>
          {/*
            User bubbles sit on the right, so the explainer opens into the empty
            space on their left, beside the whole keys block rather than the
            icon, and never over the chips. Phones have no room there: it opens
            under the block instead.
          */}
          <Popover
            triggerRef={block}
            placement={wide ? "start" : "bottom end"}
            offset={wide ? 22 : 8}
            aria-label="How keys are kept"
            className="w-64"
            dialogClassName="px-2.5 py-2"
          >
            <p className="text-caption-1-regular text-text-secondary">
              Stored encrypted. Claude sees a placeholder such as{" "}
              <span className="whitespace-nowrap font-mono text-text-primary">[key k3 ...c21e]</span>, never the key.
            </p>
          </Popover>
        </PopoverTrigger>
      </div>
      {shown.length > 0 && (
        <ul className="mt-1.5 flex flex-wrap gap-1" aria-label="Secured keys">
          {shown.map((k, i) => (
            <li key={i} className="min-w-0 max-w-full">
              <Chip variant="caption" color="soft" className="max-w-full gap-1.5 justify-start">
                <RiKey2Line className="size-3 shrink-0 text-status-purple-text" aria-hidden />
                {k.label.trim() ? (
                  <>
                    <span className="min-w-0 truncate text-text-primary">{k.label.trim()}</span>
                    {formatHint(k.hint) && <span className="shrink-0 font-mono text-text-tertiary">{formatHint(k.hint)}</span>}
                  </>
                ) : (
                  <span className="shrink-0 font-mono text-text-primary">{formatHint(k.hint) || "Key"}</span>
                )}
              </Chip>
            </li>
          ))}
          {more > 0 && (
            <li>
              <button
                type="button"
                onClick={() => setAll(!all)}
                aria-expanded={all}
                className="inline-flex cursor-pointer items-center rounded-md px-1.5 py-1 text-caption-1-medium text-text-secondary outline-none transition-colors duration-150 hover:bg-background-secondary-hover hover:text-text-primary focus-visible:ring-2 focus-visible:ring-border-focus-ring"
              >
                {all ? "Show less" : `+${more} more`}
              </button>
            </li>
          )}
        </ul>
      )}
    </div>
  );
}

/** The pictures of a user message: thumbnails that open full size in a new tab. */
function MessageImages({ urls, spaced }: { urls: string[]; spaced: boolean }) {
  return (
    <ul aria-label="Images" className={cx("flex flex-wrap gap-2", spaced && "mb-2")}>
      {urls.map((u, i) => (
        <li key={u}>
          <a
            href={u}
            target="_blank"
            rel="noreferrer"
            className="block overflow-hidden rounded-xl border border-border-button-default outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring"
          >
            <img src={u} alt={`Image ${i + 1}`} loading="lazy" className="block max-h-48 max-w-[240px] object-cover" />
          </a>
        </li>
      ))}
    </ul>
  );
}

export function UserBubble({
  text,
  keys,
  keyList,
  images,
  onEdit,
}: {
  text: string;
  keys?: number;
  keyList?: KeyRef[];
  /** URLs of the images the message carried. */
  images?: string[];
  onEdit?: () => void;
}) {
  // Older messages carry their keys inline as placeholder lines; newer ones list them in key_list.
  const legacy = !keyList?.length && !!keys;
  const parsed = legacy ? legacyKeys(text) : null;
  const prose = collapseBlank(parsed ? parsed.prose : text);
  // A message with many keys stays compact: the server keeps only their hints anyway.
  const long = prose.length > 600;
  const [open, setOpen] = useState(false);
  const secured = !!keyList?.length || legacy;
  const list = keyList?.length ? keyList : (parsed?.keys ?? []);
  return (
    <AgentMessage
      role="user"
      actions={onEdit ? <MessageAction icon={RiPencilLine} label="Edit" aria-label="Edit your last message" onClick={onEdit} /> : null}
    >
      {images && images.length > 0 && <MessageImages urls={images} spaced={!!prose} />}
      {prose && autolink(long && !open ? `${prose.slice(0, 600)}\u2026` : prose)}
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
      {secured && <SecuredKeys keys={list} count={legacy ? Math.max(keys ?? 0, list.length) : undefined} divided={!!prose} />}
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
 * recipe on a React Aria Disclosure. While the run is still going (`live`),
 * the line reads "Working" with Agent Log's shimmer instead of a past tense
 * total.
 */
export function Steps({
  items,
  seconds,
  fresh,
  live = false,
}: {
  items: ChatItem[];
  seconds: number | null;
  live?: boolean;
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
          {live ? <ShimmerText>Working</ShimmerText> : seconds !== null && seconds >= 1 ? `Worked for ${seconds}s` : "Worked"}
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

/**
 * Where the server summarized the older part of the chat to keep the prompt
 * small. Everything above stays on screen; Claude now works from the summary
 * plus the latest messages. A quiet rule that opens to the summary itself.
 */
export function CompactDivider({ item }: { item: ChatItem }) {
  const [open, setOpen] = useState(false);
  const ease = "ease-[cubic-bezier(0.22,1,0.36,1)]";
  return (
    <Disclosure isExpanded={open} onExpandedChange={setOpen} className="group/compact">
      <div className="flex items-center gap-3">
        <span className="h-px flex-1 bg-separator-border" aria-hidden />
        <AriaButton
          slot="trigger"
          className="inline-flex min-h-7 cursor-pointer pointer-coarse:min-h-10 items-center gap-1 rounded-md px-1 text-caption-1-medium text-text-tertiary outline-none transition-colors duration-150 hover:text-text-primary data-[focus-visible]:ring-2 data-[focus-visible]:ring-border-focus-ring"
        >
          Earlier messages summarized
          <RiArrowDownSLine
            className={cx(
              "size-3.5 text-foreground-icon-tertiary transition-[rotate] duration-300 motion-reduce:transition-none group-data-[expanded]/compact:rotate-180",
              ease,
            )}
            aria-hidden
          />
        </AriaButton>
        <span className="h-px flex-1 bg-separator-border" aria-hidden />
      </div>
      <DisclosurePanel
        className={cx("h-(--disclosure-panel-height) overflow-clip transition-[height] duration-300 motion-reduce:transition-none", ease)}
      >
        <div className="mt-2 rounded-2xl border border-border-button-default bg-background-secondary-default px-4 py-3">
          <p className="text-caption-1-regular text-text-tertiary">
            {item.summary ? `${item.summary}. ` : ""}Claude works from this summary and the latest messages; everything above stays here for you.
          </p>
          <div className="mt-2 text-body-2-regular text-text-secondary">
            <RichText text={item.text || "No summary text."} />
          </div>
        </div>
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

/** Known failures from the model API, in plain words. Null when the error is not one of them. */
function knownError(raw: string): string | null {
  const t = raw.toLowerCase();
  if (/\b529\b|overloaded/.test(t)) return "Claude is overloaded right now. Nothing was changed. Try again in a minute.";
  if (/\b429\b|rate[ _-]?limit/.test(t)) return "Claude's rate limit was hit. Nothing was changed. Try again in a minute.";
  if (/\b401\b|authentication_error|invalid x-api-key|unauthorized/.test(t))
    return "The Anthropic API key was rejected. Check ANTHROPIC_API_KEY and restart Acnologia.";
  if (/timeout|timed out|deadline exceeded/.test(t)) return "Claude took too long to answer. Nothing was changed. Try again.";
  return null;
}

/**
 * The server writes some errors as finished sentences ("That question is no
 * longer open. Send a message instead."). Raw errors from Go or the API start
 * lowercase or carry JSON, status lines or addresses.
 */
function isPlain(raw: string): boolean {
  return /^[A-Z][^{}<>]*[.!?]$/.test(raw) && !/https?:\/\/|\b[45]\d\d\b|_error\b/.test(raw);
}

export function ErrorItem({ text, onRetry }: { text?: string; onRetry?: () => void }) {
  const raw = text?.trim() ?? "";
  const known = raw ? (knownError(raw) ?? (isPlain(raw) ? raw : null)) : null;
  return (
    <Callout
      tone="error"
      action={onRetry ? <Button size="xs" variant="ghost" className={QUIET_BUTTON} leadingIcon={RiRefreshLine} onClick={onRetry}>Retry</Button> : undefined}
    >
      <span className="wrap-anywhere">{known ?? "Something went wrong before Claude answered. Nothing was changed."}</span>
      {!known && raw && (
        <details className="mt-1.5">
          <summary className="w-fit cursor-pointer rounded-sm text-caption-1-medium underline-offset-4 outline-none hover:underline focus-visible:ring-2 focus-visible:ring-border-focus-ring">
            Error details
          </summary>
          <p className="mt-1 wrap-anywhere font-mono text-caption-1-regular opacity-80">{raw}</p>
        </details>
      )}
    </Callout>
  );
}
