import { Fragment, useEffect, useRef, useState, type FormEvent, type KeyboardEvent, type ReactNode } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ArrowRight, ArrowUp, ChevronDown, MessageSquare, Plus, Sparkles, TriangleAlert, Wrench, XCircle } from "lucide-react";
import { useTitle } from "../hooks/useTitle";
import { useNow } from "../hooks/useNow";
import { ApiError, errorMessage } from "../lib/api";
import { relTime } from "../lib/format";
import { safeAppPath } from "../lib/url";
import type { ChatItem, ChatThread } from "../lib/types";
import { useAssistant } from "../stores/assistant";
import { toast } from "../stores/toast";
import { Modal } from "../components/Modal";
import { ConfirmButton } from "../components/ConfirmButton";
import { SecretBox } from "../components/CopyButton";
import { Badge, Button, Callout, Empty, PageHeader, PageSkeleton, Skeleton, Spinner, Textarea, cx } from "../components/ui";

const EXAMPLES = [
  "Add these SerpAPI keys: ",
  "Which keys are out of quota?",
  "Create a proxy app for my website",
  "Check every ElevenLabs key",
];

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

/** Renders `inline code` inside a line of plain text. */
function inline(text: string): ReactNode {
  const parts = text.split(/(`[^`\n]+`)/g);
  return parts.map((p, i) =>
    p.length > 2 && p.startsWith("`") && p.endsWith("`") ? (
      <code key={i} className="wrap-anywhere rounded bg-well px-1 py-0.5 font-mono text-[12.5px] text-ink">
        {p.slice(1, -1)}
      </code>
    ) : (
      <Fragment key={i}>{p}</Fragment>
    ),
  );
}

/** Plain text with two small extras: `inline code` and lines starting with "- " as a list. */
function RichText({ text }: { text: string }) {
  const blocks: Array<{ list: boolean; lines: string[] }> = [];
  for (const line of text.split("\n")) {
    const bullet = /^\s*[-*] /.test(line);
    const value = bullet ? line.replace(/^\s*[-*] /, "") : line;
    const last = blocks[blocks.length - 1];
    if (last && last.list === bullet) last.lines.push(value);
    else blocks.push({ list: bullet, lines: [value] });
  }
  return (
    <div className="space-y-2">
      {blocks.map((b, i) =>
        b.list ? (
          <ul key={i} className="list-disc space-y-1 pl-5 marker:text-faint">
            {b.lines.map((l, j) => (
              <li key={j} className="whitespace-pre-wrap">
                {inline(l)}
              </li>
            ))}
          </ul>
        ) : (
          // Blank lines between blocks are already spacing; drop them at the edges.
          b.lines.join("\n").trim() ? (
            <p key={i} className="whitespace-pre-wrap">
              {inline(b.lines.join("\n").replace(/^\n+|\n+$/g, ""))}
            </p>
          ) : null
        ),
      )}
    </div>
  );
}

function ItemLink({ link }: { link?: string }) {
  const to = safeAppPath(link);
  if (!to) return null;
  return (
    <Link to={to} className="tap-inline inline-flex items-center gap-1 text-[12px] font-medium text-ink underline-offset-2 hover:underline">
      Open <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
    </Link>
  );
}

function ToolCard({ item }: { item: ChatItem }) {
  const [open, setOpen] = useState(false);
  const hasInput = !!item.input && Object.keys(item.input).length > 0;
  const hasDetail = !!item.detail?.trim();
  const Icon = item.ok ? Wrench : XCircle;
  return (
    <div className="rounded-xl border border-line bg-surface px-3 py-2.5">
      <div className="flex items-start gap-2.5">
        <span
          className={cx(
            "mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full",
            item.ok ? "bg-emerald-50 text-emerald-700" : "bg-red-50 text-red-700",
          )}
        >
          <Icon className="h-3.5 w-3.5" aria-hidden="true" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span className="text-[13px] font-medium text-ink">{humanize(item.tool ?? "")}</span>
            <Badge tone={item.ok ? "green" : "red"}>{item.ok ? "done" : "failed"}</Badge>
          </div>
          {item.summary && <p className="wrap-anywhere mt-0.5 text-[13px] text-muted">{item.summary}</p>}
          <div className="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1">
            <ItemLink link={item.link} />
            {(hasInput || hasDetail) && (
              <button
                type="button"
                onClick={() => setOpen(!open)}
                aria-expanded={open}
                className="tap-inline inline-flex items-center gap-1 text-[12px] font-medium text-muted hover:text-ink"
              >
                Details
                <ChevronDown className={cx("h-3.5 w-3.5 transition-transform", open && "rotate-180")} aria-hidden="true" />
              </button>
            )}
          </div>
        </div>
      </div>
      {item.secret && (
        <div className="mt-3 space-y-2">
          <Callout tone="amber" icon={<TriangleAlert className="h-4 w-4" aria-hidden="true" />}>
            Copy this token now. It is shown once.
          </Callout>
          <SecretBox value={item.secret} />
        </div>
      )}
      {open && (
        <div className="mt-3 space-y-2">
          {hasInput && (
            <div>
              <div className="mb-1 font-mono text-[11px] text-faint">input</div>
              <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-soft p-2.5 font-mono text-[12px] leading-relaxed text-muted">
                {pretty(item.input)}
              </pre>
            </div>
          )}
          {hasDetail && (
            <div>
              <div className="mb-1 font-mono text-[11px] text-faint">result</div>
              <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-soft p-2.5 font-mono text-[12px] leading-relaxed text-muted">
                {pretty(item.detail)}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

const PENDING_LABEL: Record<string, { label: string; tone: "green" | "neutral" | "amber" }> = {
  approved: { label: "Done", tone: "green" },
  cancelled: { label: "Cancelled", tone: "neutral" },
  expired: { label: "Expired", tone: "amber" },
};

function ConfirmCard({ item, busy, onAnswer }: { item: ChatItem; busy: boolean; onAnswer: (approve: boolean) => Promise<void> }) {
  const [which, setWhich] = useState<boolean | null>(null);
  const status = item.pending?.status ?? "expired";
  const answer = async (approve: boolean) => {
    setWhich(approve);
    try {
      await onAnswer(approve);
    } finally {
      setWhich(null);
    }
  };
  const done = PENDING_LABEL[status];
  return (
    <div className="rounded-xl border border-amber-200 bg-amber-50 px-3.5 py-3 text-amber-900">
      <div className="text-[13px] leading-relaxed">
        <RichText text={item.text || "Confirm this change?"} />
      </div>
      <div className="mt-3 flex flex-wrap items-center gap-2">
        {status === "waiting" ? (
          <>
            <Button size="sm" variant="danger" loading={which === true} disabled={busy} onClick={() => void answer(true)}>
              Confirm
            </Button>
            <Button size="sm" variant="ghost" loading={which === false} disabled={busy} onClick={() => void answer(false)}>
              Cancel
            </Button>
          </>
        ) : (
          done && <Badge tone={done.tone}>{done.label}</Badge>
        )}
        <ItemLink link={item.link} />
      </div>
    </div>
  );
}

function Item({ item, busy, onAnswer }: { item: ChatItem; busy: boolean; onAnswer: (pendingId: string, approve: boolean) => Promise<void> }) {
  switch (item.kind) {
    case "user":
      return (
        <div className="flex justify-end">
          <div className="max-w-[85%] whitespace-pre-wrap break-words rounded-2xl rounded-br-md bg-ink px-3.5 py-2.5 text-[14px] leading-relaxed text-on-ink">
            {item.text}
          </div>
        </div>
      );
    case "assistant":
      return (
        <div className="flex max-w-[92%] gap-2.5">
          <span className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-violet-50 text-violet-700" aria-hidden="true">
            <Sparkles className="h-3.5 w-3.5" />
          </span>
          <div className="wrap-anywhere min-w-0 text-[14px] leading-relaxed text-ink">
            <RichText text={item.text ?? ""} />
            {item.link && (
              <div className="mt-1">
                <ItemLink link={item.link} />
              </div>
            )}
          </div>
        </div>
      );
    case "tool":
      return (
        <div className="pl-[34px]">
          <ToolCard item={item} />
        </div>
      );
    case "confirm":
      return (
        <div className="pl-[34px]">
          <ConfirmCard item={item} busy={busy} onAnswer={(a) => (item.pending ? onAnswer(item.pending.id, a) : Promise.resolve())} />
        </div>
      );
    case "error":
      return (
        <div className="pl-[34px]">
          <Callout tone="red" icon={<XCircle className="h-4 w-4" aria-hidden="true" />}>
            <span className="wrap-anywhere">{item.text || "Something went wrong."}</span>
          </Callout>
        </div>
      );
    default:
      return null;
  }
}

function ThreadList({ threads, currentId, onPick, onDelete }: { threads: ChatThread[]; currentId: number | null; onPick: (id: number) => void; onDelete: (t: ChatThread) => Promise<void> }) {
  const now = useNow(60_000);
  if (!threads.length)
    return (
      <Empty compact icon={<MessageSquare aria-hidden="true" />} title="No chats yet" body="Each one you start is kept here." />
    );
  return (
    <ul className="space-y-0.5">
      {threads.map((t) => {
        const active = t.id === currentId;
        return (
          <li key={t.id} className={cx("group flex items-center gap-1 rounded-xl pr-1 transition-colors", active ? "bg-ink/[0.06]" : "hover:bg-ink/[0.04]")}>
            <button
              type="button"
              onClick={() => onPick(t.id)}
              aria-current={active ? "page" : undefined}
              className="tap min-w-0 flex-1 px-3 py-2 text-left"
            >
              <div className={cx("truncate text-[13px]", active ? "font-medium text-ink" : "text-ink")}>{t.title || "New chat"}</div>
              <div className="font-mono text-[11px] text-faint">{relTime(t.updated_at, now)}</div>
            </button>
            <ConfirmButton iconOnly label={`Delete chat ${t.title || ""}`.trim()} confirmLabel="Delete" onConfirm={() => onDelete(t)} />
          </li>
        );
      })}
    </ul>
  );
}

export function AssistantPage() {
  const param = useParams().threadId;
  const routeId = param && /^\d+$/.test(param) ? Number(param) : null;
  const navigate = useNavigate();
  const { threads, ai, loaded, error, currentId, items, loadingThread, sending, fetch, open, create, remove, send, confirm } = useAssistant();
  const [text, setText] = useState("");
  const [sheet, setSheet] = useState(false);
  const box = useRef<HTMLTextAreaElement>(null);
  const endRef = useRef<HTMLDivElement>(null);
  const current = threads.find((t) => t.id === currentId) ?? null;
  // Items already in a thread when it opens render still; items that arrive
  // after that fade up, staggered when a reply brings several at once.
  const baseline = useRef<{ id: number | null; ids: Set<string> } | null>(null);
  const delays = useRef(new Map<string, number>());
  if (!loadingThread && baseline.current?.id !== currentId) {
    baseline.current = { id: currentId, ids: new Set(items.map((it) => it.id)) };
    delays.current = new Map();
  }
  // The server copy of your own message replaces the local one, which already animated.
  const animates = (it: ChatItem) => !baseline.current?.ids.has(it.id) && !(it.kind === "user" && !it.id.startsWith("local-"));
  let batch = 0;
  for (const it of items) {
    if (animates(it) && !delays.current.has(it.id)) delays.current.set(it.id, Math.min(batch++, 5) * 60);
  }
  const busy = sending !== null;
  const off = !!ai && !ai.enabled;
  useTitle(current?.title && current.title !== "New chat" ? current.title : "Assistant");

  useEffect(() => {
    fetch().catch(() => {});
  }, [fetch]);

  useEffect(() => {
    open(routeId).catch((e) => {
      toast.error(e instanceof ApiError && e.status === 404 ? "That chat no longer exists." : errorMessage(e));
      navigate("/assistant", { replace: true });
    });
  }, [routeId, open, navigate]);

  // Keep the newest item in view.
  useEffect(() => {
    if (!items.length && !busy) return;
    const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    endRef.current?.scrollIntoView({ block: "end", behavior: reduce ? "auto" : "smooth" });
  }, [items, busy]);

  const pick = (id: number) => {
    setSheet(false);
    navigate(`/assistant/${id}`);
  };

  const newChat = () => {
    setSheet(false);
    navigate("/assistant");
    window.setTimeout(() => box.current?.focus(), 0);
  };

  const onDelete = async (t: ChatThread) => {
    try {
      await remove(t.id);
      if (routeId === t.id) navigate("/assistant", { replace: true });
      toast.success("Chat deleted");
    } catch (e) {
      toast.error(errorMessage(e));
    }
  };

  const submit = async (e?: FormEvent) => {
    e?.preventDefault();
    const msg = text.trim();
    if (!msg || busy || off) return;
    setText("");
    try {
      if (currentId === null) {
        const t = await create();
        navigate(`/assistant/${t.id}`, { replace: true });
      }
      await send(msg);
    } catch (err) {
      setText((cur) => cur || msg);
      if (err instanceof ApiError && err.code === "busy") toast.info("Still working on your last message. Try again in a moment.");
      else {
        if (err instanceof ApiError && err.code === "ai_disabled") fetch().catch(() => {});
        toast.error(errorMessage(err));
      }
    } finally {
      window.setTimeout(() => box.current?.focus(), 0);
    }
  };

  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      void submit();
    }
  };

  const answer = async (pendingId: string, approve: boolean) => {
    try {
      await confirm(pendingId, approve);
    } catch (err) {
      if (err instanceof ApiError && err.code === "busy") toast.info("Still working on your last message. Try again in a moment.");
      else toast.error(errorMessage(err));
    }
  };

  const fillExample = (ex: string) => {
    setText(ex);
    window.setTimeout(() => {
      const el = box.current;
      if (!el) return;
      el.focus();
      el.setSelectionRange(ex.length, ex.length);
    }, 0);
  };

  if (!loaded) return <PageSkeleton variant="list" />;

  const list = <ThreadList threads={threads} currentId={currentId} onPick={pick} onDelete={onDelete} />;

  return (
    <div>
      <PageHeader
        title="Assistant"
        meta={`Ask about your keys, services and apps, or have it make changes${ai?.model ? ` · ${ai.model}` : ""}`}
        actions={
          <>
            <Button className="lg:hidden" icon={<MessageSquare className="h-4 w-4" aria-hidden="true" />} onClick={() => setSheet(true)} aria-haspopup="dialog">
              Chats
            </Button>
            <Button variant="primary" icon={<Plus className="h-4 w-4" aria-hidden="true" />} onClick={newChat}>
              New chat
            </Button>
          </>
        }
      />

      {error && (
        <Callout tone="red" className="mb-6">
          {error}{" "}
          <button type="button" className="font-medium underline underline-offset-2" onClick={() => fetch().catch(() => {})}>
            Try again
          </button>
        </Callout>
      )}

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-[240px_minmax(0,1fr)]">
        <aside aria-label="Chats" className="hidden lg:block">
          <div className="sticky top-20 max-h-[calc(100dvh-7rem)] overflow-y-auto">{list}</div>
        </aside>

        <section aria-labelledby="chat-title" className="flex min-w-0 flex-col">
          <h2 id="chat-title" className="mb-4 truncate text-[15px] font-semibold text-ink">
            {current?.title || "New chat"}
          </h2>

          {off && (
            <Callout tone="amber" icon={<TriangleAlert className="h-4 w-4" aria-hidden="true" />} className="mb-4">
              The assistant is off. {ai?.reason}
            </Callout>
          )}

          <div role="log" aria-live="polite" aria-label="Conversation" className={cx("space-y-4 pb-4", (items.length > 0 || loadingThread) && "min-h-[40vh]")}>
            {loadingThread ? (
              <div className="space-y-4" aria-busy="true">
                <Skeleton className="ml-auto h-10 w-1/2 rounded-2xl" />
                <Skeleton className="h-16 w-3/4" />
                <Skeleton className="ml-auto h-10 w-1/3 rounded-2xl" />
              </div>
            ) : items.length === 0 ? (
              <div className="fade-up rounded-card border border-dashed border-line-strong px-4 py-6 text-center sm:px-5 sm:py-10">
                <div className="mx-auto mb-3 flex h-10 w-10 items-center justify-center rounded-xl bg-surface text-muted shadow-card">
                  <Sparkles className="h-5 w-5" aria-hidden="true" />
                </div>
                <div className="text-[15px] font-medium text-ink">What can I help with?</div>
                <p className="mx-auto mt-1 max-w-md text-sm text-muted">
                  I can add and check keys, find problems, and set up apps. I ask before anything that changes or deletes data.
                </p>
                <div className="mt-5 flex flex-wrap justify-center gap-2">
                  {EXAMPLES.map((ex) => (
                    <Button key={ex} size="sm" disabled={off} onClick={() => fillExample(ex)}>
                      {ex.replace(/:\s*$/, "")}
                    </Button>
                  ))}
                </div>
              </div>
            ) : (
              items.map((it) => {
                const delay = animates(it) ? delays.current.get(it.id) : undefined;
                return (
                  <div key={it.id} className={delay !== undefined ? "fade-up" : undefined} style={delay ? { animationDelay: `${delay}ms` } : undefined}>
                    <Item item={it} busy={busy} onAnswer={answer} />
                  </div>
                );
              })
            )}
            {busy && sending === currentId && (
              <div role="status" className="fade-up flex items-center gap-2 pl-[34px] text-[13px] text-muted">
                <Spinner />
                Working
              </div>
            )}
            <div ref={endRef} />
          </div>

          <form onSubmit={submit} className="sticky bottom-[var(--tabbar-h)] z-10 -mx-1 bg-canvas px-1 pb-3 pt-2">
            <label htmlFor="assistant-input" className="sr-only">
              Message
            </label>
            <div className="relative">
              <Textarea
                id="assistant-input"
                ref={box}
                rows={2}
                value={text}
                disabled={busy || off}
                onChange={(e) => setText(e.target.value)}
                onKeyDown={onKey}
                placeholder={off ? "The assistant is off" : "Ask anything, or paste keys to add"}
                className="max-h-60 min-h-[64px] resize-y pr-14"
              />
              <button
                type="submit"
                aria-label="Send"
                title="Send"
                disabled={busy || off || !text.trim()}
                className="tap absolute bottom-2.5 right-2.5 inline-flex h-9 w-9 items-center justify-center rounded-full bg-ink text-on-ink transition-[background-color,transform] duration-150 ease-out hover:bg-ink-strong active:scale-[0.96] disabled:cursor-not-allowed disabled:bg-well disabled:text-faint"
              >
                {busy ? <Spinner className="text-current" /> : <ArrowUp className="h-4 w-4" aria-hidden="true" />}
              </button>
            </div>
            <p className="mt-1.5 text-[11px] leading-relaxed text-faint">
              Keys you paste are sent to Claude to add them, then stored only as their last four characters in this chat.
            </p>
          </form>
        </section>
      </div>

      <Modal open={sheet} onClose={() => setSheet(false)} title="Chats">
        <div className="-mx-2">{list}</div>
        <Button className="mt-4 w-full" icon={<Plus className="h-4 w-4" aria-hidden="true" />} onClick={newChat}>
          New chat
        </Button>
      </Modal>
    </div>
  );
}
