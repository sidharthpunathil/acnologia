import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { useNavigate, useParams } from "react-router-dom";
import {
  ArrowDown,
  FileUp,
  MessagesSquare,
  SquarePen,
  TriangleAlert,
} from "lucide-react";
import { useTitle } from "../hooks/useTitle";
import { ApiError, errorMessage } from "../lib/api";
import type { ChatItem, ChatThread } from "../lib/types";
import { useAssistant } from "../stores/assistant";
import { toast } from "../stores/toast";
import { Modal } from "../components/Modal";
import { Button, Callout, IconButton, Skeleton, cx } from "../components/ui";
import {
  AssistantText,
  ConfirmCard,
  ErrorItem,
  GUTTER,
  Steps,
  UserBubble,
} from "./assistant/items";
import { AskCard, BuildCard, Suggestions, ToolCards, useEntities } from "./assistant/cards";
import {
  Composer,
  EmptyState,
  LiveStatus,
  NewChatButton,
  Shortcuts,
  ThreadList,
  mergeAttachments,
  toAttachment,
  type Attachment,
} from "./assistant/parts";
import "./assistant/assistant.css";

/** Rows of the transcript: a run of tool calls folds into one "Worked for Ns" line. */
type Row =
  | { key: string; kind: "item"; item: ChatItem }
  | { key: string; kind: "tools"; items: ChatItem[]; since?: string };

function toRows(items: ChatItem[]): Row[] {
  const rows: Row[] = [];
  items.forEach((it, i) => {
    const last = rows[rows.length - 1];
    if (it.kind === "tool" && last?.kind === "tools") last.items.push(it);
    else if (it.kind === "tool")
      rows.push({
        key: it.id,
        kind: "tools",
        items: [it],
        since: items[i - 1]?.ts,
      });
    else rows.push({ key: it.id, kind: "item", item: it });
  });
  return rows;
}

/** Seconds from the item before a run of steps to its last step, when the timestamps allow. */
function runSeconds(row: { items: ChatItem[]; since?: string }): number | null {
  const a = Date.parse(row.since ?? "");
  const b = Date.parse(row.items[row.items.length - 1]?.ts ?? "");
  if (Number.isNaN(a) || Number.isNaN(b) || b < a) return null;
  return Math.round((b - a) / 1000);
}

const reducedMotion = () =>
  window.matchMedia("(prefers-reduced-motion: reduce)").matches;

/** Files the composer takes: plain text exports of keys. */
const DROP_TYPES = /\.(txt|csv|env|json)$/i;
const DROP_MAX = 1 << 20;

async function readDropped(files: File[]): Promise<{ text: string; names: string[]; skipped: string[] }> {
  const parts: string[] = [];
  const names: string[] = [];
  const skipped: string[] = [];
  for (const f of files) {
    const textual = DROP_TYPES.test(f.name) || f.name.startsWith(".env") || f.type.startsWith("text/") || f.type === "application/json";
    if (!textual) {
      skipped.push(`${f.name} is not a .txt, .csv, .env or .json file`);
      continue;
    }
    if (f.size > DROP_MAX) {
      skipped.push(`${f.name} is over 1 MB`);
      continue;
    }
    parts.push(await f.text());
    names.push(f.name);
  }
  return { text: parts.join("\n"), names, skipped };
}

const isMac = () => /Mac|iPhone|iPad/.test(navigator.platform);

export function AssistantPage() {
  const param = useParams().threadId;
  const routeId = param && /^\d+$/.test(param) ? Number(param) : null;
  const navigate = useNavigate();
  const {
    threads,
    ai,
    loaded,
    error,
    currentId,
    items,
    loadingThread,
    sending,
    turn,
    lastSent,
    fetch,
    open,
    create,
    remove,
    send,
    confirm,
    answer: answerAsk,
  } = useAssistant();
  const [text, setText] = useState("");
  const [attachment, setAttachment] = useState<Attachment | null>(null);
  const [dragging, setDragging] = useState(false);
  const dragDepth = useRef(0);
  const [sheet, setSheet] = useState(false);
  const entities = useEntities(items);
  const box = useRef<HTMLTextAreaElement>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const current = threads.find((t) => t.id === currentId) ?? null;
  const title =
    current?.title && current.title !== "New chat" ? current.title : null;

  // Items already in a thread when it opens render still; items that arrive
  // after that fade up, staggered when a reply brings several at once.
  const baseline = useRef<{ id: number | null; ids: Set<string> } | null>(null);
  const delays = useRef(new Map<string, number>());
  if (!loadingThread && baseline.current?.id !== currentId) {
    baseline.current = {
      id: currentId,
      ids: new Set(items.map((it) => it.id)),
    };
    delays.current = new Map();
  }
  // The server copy of your own message replaces the local one, which already animated.
  const animates = (it: ChatItem) =>
    !baseline.current?.ids.has(it.id) &&
    !(it.kind === "user" && !it.id.startsWith("local-"));
  let batch = 0;
  for (const it of items) {
    if (animates(it) && !delays.current.has(it.id))
      delays.current.set(it.id, Math.min(batch++, 5) * 60);
  }
  const delayFor = (it: ChatItem) =>
    animates(it) ? delays.current.get(it.id) : undefined;

  const active = !!turn?.active;
  const busy = sending !== null || active;
  const working = (sending !== null && sending === currentId) || active;
  const off = !!ai && !ai.enabled;
  useTitle(title ?? "Assistant");

  // The chat fills the viewport and scrolls inside, so the page itself must not scroll.
  useEffect(() => {
    const html = document.documentElement;
    const prev = html.style.overflow;
    html.style.overflow = "hidden";
    window.scrollTo(0, 0);
    return () => {
      html.style.overflow = prev;
    };
  }, []);

  useEffect(() => {
    fetch().catch(() => {});
  }, [fetch]);

  useEffect(() => {
    open(routeId).catch((e) => {
      toast.error(
        e instanceof ApiError && e.status === 404
          ? "That chat no longer exists."
          : errorMessage(e),
      );
      navigate("/assistant", { replace: true });
    });
  }, [routeId, open, navigate]);

  // On a mouse and keyboard screen, start with the cursor in the composer. Deferred
  // past the layout's own focus move on navigation.
  useEffect(() => {
    if (
      !loaded ||
      !window.matchMedia("(hover: hover) and (pointer: fine)").matches
    )
      return;
    const t = window.setTimeout(
      () => box.current?.focus({ preventScroll: true }),
      0,
    );
    return () => window.clearTimeout(t);
  }, [loaded, routeId]);

  // Scrolling: follow new items while the reader is at the bottom; leave them be if they scrolled up.
  const stick = useRef(true);
  const [atBottom, setAtBottom] = useState(true);
  const toBottom = (smooth: boolean) => {
    const el = scroller.current;
    if (!el) return;
    el.scrollTo({
      top: el.scrollHeight,
      behavior: smooth && !reducedMotion() ? "smooth" : "auto",
    });
  };
  const onScroll = () => {
    const el = scroller.current;
    if (!el) return;
    const near = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
    stick.current = near;
    if (near !== atBottom) setAtBottom(near);
  };
  // A thread that just opened starts at its latest message, without a scroll animation.
  useLayoutEffect(() => {
    if (loadingThread) return;
    stick.current = true;
    setAtBottom(true);
    toBottom(false);
  }, [currentId, loadingThread]);
  useEffect(() => {
    const last = items[items.length - 1];
    if (last?.id.startsWith("local-")) stick.current = true;
    if (stick.current) toBottom(true);
  }, [items, working]);
  // The composer growing or the window resizing keeps the latest message in view.
  useEffect(() => {
    const el = scroller.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => {
      if (stick.current) toBottom(false);
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, [loaded]);

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

  const submit = async (e?: FormEvent, override?: string) => {
    e?.preventDefault();
    const typed = override ?? text.trim();
    const att = override === undefined ? attachment : null;
    const msg = [typed, att?.text.trim()].filter(Boolean).join("\n\n");
    if (!msg || busy || off) return;
    if (override === undefined) {
      setText("");
      setAttachment(null);
    }
    try {
      if (currentId === null) {
        const t = await create();
        navigate(`/assistant/${t.id}`, { replace: true });
      }
      await send(msg);
    } catch (err) {
      if (override === undefined) {
        setText((cur) => cur || typed);
        if (att) setAttachment((cur) => cur ?? att);
      }
      if (err instanceof ApiError && err.code === "busy")
        toast.info(
          "Still working on your last message. Try again in a moment.",
        );
      else {
        if (err instanceof ApiError && err.code === "ai_disabled")
          fetch().catch(() => {});
        toast.error(errorMessage(err));
      }
    } finally {
      window.setTimeout(() => box.current?.focus({ preventScroll: true }), 0);
    }
  };

  const answer = async (pendingId: string, approve: boolean) => {
    try {
      await confirm(pendingId, approve);
    } catch (err) {
      if (err instanceof ApiError && err.code === "busy")
        toast.info(
          "Still working on your last message. Try again in a moment.",
        );
      else toast.error(errorMessage(err));
    }
  };

  const onAnswerAsk = async (body: Parameters<typeof answerAsk>[0]) => {
    try {
      await answerAsk(body);
    } catch (err) {
      toast.error(errorMessage(err));
    }
  };

  // The last message you sent this session, as typed (keys and all), for Retry and Edit.
  const lastUser = [...items].reverse().find((it) => it.kind === "user");
  const lastRaw = currentId !== null ? lastSent[currentId] : undefined;
  const retry = () => {
    if (!lastUser) return;
    void submit(undefined, lastRaw ?? lastUser.text ?? "");
  };
  const edit = () => {
    if (!lastUser) return;
    const raw = lastRaw ?? lastUser.text ?? "";
    const att = toAttachment(raw, "Your last message");
    if (att && att.keys >= 3) {
      // Keep the keys out of the textarea, as when they were pasted.
      const prose = raw
        .split("\n")
        .filter((l) => !toAttachment(l, ""))
        .join("\n")
        .trim();
      setText(prose || "Add these keys");
      setAttachment(att);
    } else setText(raw);
    window.setTimeout(() => {
      const el = box.current;
      if (!el) return;
      el.focus();
      el.setSelectionRange(el.value.length, el.value.length);
    }, 0);
  };

  // ---- drop files of keys anywhere on the chat ----
  const hasFiles = (e: React.DragEvent) => Array.from(e.dataTransfer?.types ?? []).includes("Files");
  const onDragEnter = (e: React.DragEvent) => {
    if (!hasFiles(e) || off) return;
    e.preventDefault();
    dragDepth.current += 1;
    setDragging(true);
  };
  const onDragOver = (e: React.DragEvent) => {
    if (!hasFiles(e) || off) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "copy";
  };
  const onDragLeave = (e: React.DragEvent) => {
    if (!hasFiles(e)) return;
    dragDepth.current = Math.max(0, dragDepth.current - 1);
    if (dragDepth.current === 0) setDragging(false);
  };
  const onDrop = async (e: React.DragEvent) => {
    if (!hasFiles(e) || off) return;
    e.preventDefault();
    dragDepth.current = 0;
    setDragging(false);
    const { text: body, names, skipped } = await readDropped(Array.from(e.dataTransfer.files));
    for (const s of skipped) toast.error(s);
    if (!names.length) return;
    const att = toAttachment(body, names.join(", "));
    if (att) {
      setAttachment((cur) => mergeAttachments(cur, att));
      setText((cur) => cur.trim() || "Add these keys");
    } else {
      toast.info(`No keys found in ${names.join(", ")}. Its text is in the message box.`);
      setText((cur) => (cur ? `${cur}\n${body}` : body).slice(0, 20000));
    }
    window.setTimeout(() => box.current?.focus({ preventScroll: true }), 0);
  };

  // ---- keyboard ----
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const mod = isMac() ? e.metaKey : e.ctrlKey;
      const target = e.target as HTMLElement | null;
      const typing = !!target && (target.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName));
      if (mod && !e.shiftKey && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setSheet((v) => !v);
      } else if (mod && e.shiftKey && e.key.toLowerCase() === "o") {
        e.preventDefault();
        newChat();
      } else if (e.key === "/" && !typing && !mod && !e.altKey) {
        e.preventDefault();
        box.current?.focus();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // newChat only navigates; a stale copy is fine.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const fillExample = (ex: string) => {
    setText(ex);
    window.setTimeout(() => {
      const el = box.current;
      if (!el) return;
      el.focus();
      el.setSelectionRange(ex.length, ex.length);
    }, 0);
  };

  const list = (
    <ThreadList
      threads={threads}
      currentId={currentId}
      onPick={pick}
      onDelete={onDelete}
    />
  );
  const rows = toRows(items);

  const renderRow = (row: Row, i: number) => {
    // A new turn (your message, or the reply after it) gets more room than steps inside one.
    const prev = rows[i - 1];
    const turn =
      i > 0 &&
      (row.kind === "item" && row.item.kind === "user") !==
        (prev.kind === "item" && prev.item.kind === "user");
    // A reply that ends a turn already carries its copy row below it, so the next message needs less room.
    const afterActions = prev?.kind === "item" && prev.item.kind === "assistant";
    const spacing = i === 0 ? "" : turn ? (afterActions ? "mt-3" : "mt-6") : "mt-3";
    if (row.kind === "tools")
      return (
        <div key={row.key} className={cx(spacing, GUTTER)}>
          <Steps items={row.items} seconds={runSeconds(row)} fresh={delayFor} />
          <ToolCards items={row.items} />
        </div>
      );
    const it = row.item;
    const delay = delayFor(it);
    const anim =
      delay !== undefined && it.kind !== "assistant" ? "fade-up" : undefined;
    const style = delay && anim ? { animationDelay: `${delay}ms` } : undefined;
    let body = null;
    if (it.kind === "user")
      body = (
        <UserBubble
          text={it.text ?? ""}
          keys={it.keys}
          onEdit={it.id === lastUser?.id && !working ? edit : undefined}
        />
      );
    else if (it.kind === "assistant") {
      const next = rows[i + 1];
      const last = !next;
      const endOfTurn = last || (next.kind === "item" && next.item.kind === "user");
      body = (
        <>
          <AssistantText
            item={it}
            reveal={delay}
            entities={entities}
            actions={!(working && last)}
            onRetry={last && !working && lastUser ? retry : undefined}
          />
          {endOfTurn && last && !working && it.suggestions?.length ? (
            <Suggestions items={it.suggestions} disabled={busy || off} onPick={(s) => void submit(undefined, s)} />
          ) : null}
        </>
      );
    } else if (it.kind === "build")
      body = (
        <div className={GUTTER}>
          <BuildCard item={it} />
        </div>
      );
    else if (it.kind === "ask")
      body = (
        <div className={GUTTER}>
          <AskCard item={it} busy={busy} onAnswer={onAnswerAsk} />
        </div>
      );
    else if (it.kind === "confirm")
      body = (
        <div className={GUTTER}>
          <ConfirmCard
            item={it}
            busy={busy}
            onAnswer={(a) =>
              it.pending ? answer(it.pending.id, a) : Promise.resolve()
            }
          />
        </div>
      );
    else if (it.kind === "error")
      body = (
        <div className={GUTTER}>
          <ErrorItem text={it.text} />
        </div>
      );
    if (!body) return null;
    return (
      <div key={row.key} className={cx(spacing, anim)} style={style}>
        {body}
      </div>
    );
  };

  return (
    <div
      className="-mx-4 -mb-24 -mt-8 flex sm:-mx-6 sm:-mt-14 lg:mx-0"
      style={{
        height:
          "calc(100dvh - 3.5rem - env(safe-area-inset-top, 0px) - var(--tabbar-h))",
      }}
    >
      <section
        aria-labelledby="chat-title"
        className="relative flex min-w-0 flex-1 flex-col"
        onDragEnter={onDragEnter}
        onDragOver={onDragOver}
        onDragLeave={onDragLeave}
        onDrop={(e) => void onDrop(e)}
      >
        {dragging && (
          <div
            className="chat-drop pointer-events-none absolute inset-0 z-20 flex items-center justify-center bg-canvas/80 p-4 backdrop-blur-[2px]"
            aria-hidden="true"
          >
            <div className="chat-drop-panel flex w-full max-w-sm flex-col items-center rounded-3xl border-2 border-dashed border-line-strong bg-surface px-6 py-8 text-center shadow-lift">
              <span className="flex h-11 w-11 items-center justify-center rounded-2xl bg-brand-soft text-brand">
                <FileUp className="h-5 w-5" />
              </span>
              <div className="mt-3 text-[15px] font-semibold text-ink">Drop a file of keys</div>
              <div className="mt-1 text-[13px] leading-relaxed text-muted">
                .txt, .csv, .env or .json, up to 1 MB. Claude sees only placeholders.
              </div>
            </div>
          </div>
        )}
        {/* History and New chat stay out of the way: a quiet bar above the centred chat. */}
        <div className="mx-auto flex h-12 w-full max-w-[728px] shrink-0 items-center gap-2 px-3 sm:px-6">
          <Button
            size="sm"
            variant="ghost"
            className="press -ml-2"
            icon={<MessagesSquare className="h-4 w-4" aria-hidden="true" />}
            onClick={() => setSheet(true)}
            aria-haspopup="dialog"
          >
            Chats
          </Button>
          <h1
            id="chat-title"
            className="min-w-0 flex-1 truncate text-center text-[13px] font-medium text-muted"
          >
            <span className="sr-only">Assistant{title ? ": " : ""}</span>
            {title}
          </h1>
          <IconButton label="New chat" onClick={newChat} className="-mr-2">
            <SquarePen className="h-[18px] w-[18px]" aria-hidden="true" />
          </IconButton>
        </div>

        <div className="relative min-h-0 flex-1">
          <div
            ref={scroller}
            onScroll={onScroll}
            className="h-full overflow-y-auto overscroll-contain"
          >
            <div className="mx-auto flex min-h-full w-full max-w-[728px] flex-col px-4 pb-6 pt-4 sm:px-6">
              {error && (
                <Callout tone="red" className="mb-4">
                  {error}{" "}
                  <button
                    type="button"
                    className="font-medium underline underline-offset-2"
                    onClick={() => fetch().catch(() => {})}
                  >
                    Try again
                  </button>
                </Callout>
              )}
              {!loaded || loadingThread ? (
                <div
                  className="space-y-6"
                  aria-busy="true"
                  aria-label="Loading chat"
                >
                  <Skeleton className="ml-auto h-10 w-1/2 rounded-2xl" />
                  <div className="flex gap-2.5">
                    <Skeleton className="h-6 w-6 rounded-md" />
                    <div className="flex-1 space-y-2">
                      <Skeleton className="h-4 w-3/4" />
                      <Skeleton className="h-4 w-1/2" />
                    </div>
                  </div>
                  <Skeleton className="ml-auto h-10 w-1/3 rounded-2xl" />
                </div>
              ) : items.length === 0 && !working ? (
                <EmptyState off={off} onPick={fillExample} />
              ) : (
                <div
                  role="log"
                  aria-label="Conversation"
                  aria-live="polite"
                  aria-relevant="additions"
                >
                  {rows.map(renderRow)}
                </div>
              )}
              {working && (
                <div className="mt-6">
                  <LiveStatus step={turn?.step} startedAt={turn?.started_at} />
                </div>
              )}
            </div>
          </div>
          {!atBottom && items.length > 0 && (
            <button
              type="button"
              onClick={() => {
                stick.current = true;
                toBottom(true);
              }}
              className="swap-in icon-btn absolute bottom-3 left-1/2 inline-flex h-9 w-9 -translate-x-1/2 items-center justify-center rounded-full bg-surface text-ink shadow-lift transition-colors hover:bg-soft"
              aria-label="Jump to latest message"
              title="Jump to latest"
            >
              <ArrowDown className="h-4 w-4" aria-hidden="true" />
            </button>
          )}
        </div>

        <div className="shrink-0 pb-2">
          <div className="mx-auto w-full max-w-[728px] px-3 sm:px-6">
            {off && (
              <Callout
                tone="amber"
                icon={<TriangleAlert className="h-4 w-4" aria-hidden="true" />}
                className="mb-2"
              >
                The assistant is off. {ai?.reason}
              </Callout>
            )}
            <Composer
              ref={box}
              value={text}
              onChange={setText}
              onSubmit={(e) => void submit(e)}
              busy={busy}
              off={off}
              model={ai?.model}
              attachment={attachment}
              onAttach={(a) => setAttachment((cur) => mergeAttachments(cur, a))}
              onClearAttachment={() => setAttachment(null)}
            />
          </div>
        </div>
      </section>

      <Modal open={sheet} onClose={() => setSheet(false)} title="Chats">
        <NewChatButton onClick={newChat} className="mb-3" />
        <div className="-mx-2">{loaded ? list : <ListSkeleton />}</div>
        <div className="mt-4">
          <Shortcuts />
        </div>
      </Modal>
    </div>
  );
}

function ListSkeleton() {
  return (
    <div className="space-y-3 px-3 pt-1" aria-hidden="true">
      {Array.from({ length: 4 }).map((_, i) => (
        <div key={i}>
          <Skeleton className="h-3.5 w-4/5" />
          <Skeleton className="mt-1.5 h-2.5 w-12" />
        </div>
      ))}
    </div>
  );
}
