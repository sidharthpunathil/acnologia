import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import {
  RiAlertLine,
  RiArrowDownLine,
  RiChatHistoryLine,
  RiEditBoxLine,
  RiFileUploadLine,
} from "@remixicon/react";
import { useTitle } from "@/hooks/useTitle";
import { ApiError, errorMessage } from "@/lib/api";
import type { ChatItem, ChatThread } from "@/lib/types";
import { NEW_DRAFT, useAssistant, type DraftAttachment } from "@/stores/assistant";
import { useAuth } from "@/stores/auth";
import { toast } from "@/stores/toast";
import { Callout } from "@/components/acn/callout";
import { Button } from "@/components/base/buttons/button";
import { IconButton } from "@/components/base/buttons/icon-button";
import { Drawer, DrawerBody, DrawerHeader } from "@/components/base/drawer/drawer";
import { Kbd } from "@/components/base/kbd/kbd";
import { Modal, ModalBody, ModalHeader } from "@/components/base/modal/modal";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { Tooltip, TooltipTrigger } from "@/components/base/tooltip/tooltip";
import { ScrollFade, useScrollFade } from "@/components/foundations/scroll-fade/scroll-fade";
import { cx } from "@/utils/cx";
import { QUIET_BUTTON } from "@/components/application/agent-chat/agent-chat-message";
import {
  AssistantText,
  ConfirmCard,
  collapseBlank,
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

/**
 * The last chat you had open, per signed in user, so leaving the Assistant
 * and coming back (the top bar links to bare /assistant) lands on it again.
 * Only the id is stored; drafts stay in memory because they can hold keys.
 */
const lastKey = (userId: number | undefined) => (userId === undefined ? null : `acn.assistant.last.${userId}`);
function readLast(key: string | null): number | null {
  if (!key) return null;
  try {
    const v = window.localStorage.getItem(key);
    return v && /^\d+$/.test(v) ? Number(v) : null;
  } catch {
    return null;
  }
}
function writeLast(key: string | null, id: number | null) {
  if (!key) return;
  try {
    if (id === null) window.localStorage.removeItem(key);
    else window.localStorage.setItem(key, String(id));
  } catch {
    // Storage blocked: coming back starts a new chat, as before.
  }
}

/** Route state: `fresh` asks for a blank chat, `restored` marks the redirect to the last one. */
type RouteState = { fresh?: boolean; restored?: boolean } | null;

const isMac = () => /Mac|iPhone|iPad/.test(navigator.platform);

/** Phones get the Chats list as a bottom sheet; wider screens a centred dialog. */
const SHEET_QUERY = "(max-width: 639px)";
function useSheet() {
  const [sheet, setSheet] = useState(() => typeof window !== "undefined" && window.matchMedia(SHEET_QUERY).matches);
  useEffect(() => {
    const mq = window.matchMedia(SHEET_QUERY);
    const on = () => setSheet(mq.matches);
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, []);
  return sheet;
}

export function AssistantPage() {
  const param = useParams().threadId;
  const routeId = param && /^\d+$/.test(param) ? Number(param) : null;
  const navigate = useNavigate();
  const routeState = useLocation().state as RouteState;
  const fresh = !!routeState?.fresh;
  const restored = !!routeState?.restored;
  const storeKey = lastKey(useAuth((s) => s.user?.id));
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
    drafts,
    setDraftText,
    setDraftAttachment,
  } = useAssistant();
  // The draft lives in the store by thread, so it survives leaving the page.
  // Setters write to the thread open at call time: a failed first send
  // restores its text to the chat it just created.
  const draftKey = currentId === null ? NEW_DRAFT : String(currentId);
  const text = drafts[draftKey]?.text ?? "";
  const attachment = drafts[draftKey]?.attachment ?? null;
  const keyNow = () => {
    const id = useAssistant.getState().currentId;
    return id === null ? NEW_DRAFT : String(id);
  };
  const setText = (v: string | ((cur: string) => string)) => setDraftText(keyNow(), v);
  const setAttachment = (v: DraftAttachment | null | ((cur: Attachment | null) => Attachment | null)) =>
    setDraftAttachment(keyNow(), v);
  const [dragging, setDragging] = useState(false);
  // After Edit on a message whose keys only the vault holds: say so under the composer.
  const [vaultNote, setVaultNote] = useState(false);
  useEffect(() => setVaultNote(false), [currentId]);
  const dragDepth = useRef(0);
  const [sheet, setSheet] = useState(false);
  const asSheet = useSheet();
  const fade = useScrollFade();
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

  // Bare /assistant goes back to the last chat you had open; only New chat
  // (route state `fresh`) asks for a blank one.
  const resumeId = routeId === null && !fresh ? readLast(storeKey) : null;
  useEffect(() => {
    if (resumeId !== null) {
      navigate(`/assistant/${resumeId}`, { replace: true, state: { restored: true } });
      return;
    }
    open(routeId).catch((e) => {
      const gone = e instanceof ApiError && e.status === 404;
      if (gone && routeId !== null && readLast(storeKey) === routeId) writeLast(storeKey, null);
      // A remembered chat deleted elsewhere just gives a blank chat; a link to one says so.
      if (!(gone && restored)) toast.error(gone ? "That chat no longer exists." : errorMessage(e));
      navigate("/assistant", { replace: true, state: { fresh: true } });
    });
  }, [routeId, resumeId, restored, storeKey, open, navigate]);

  // Remember the chat on screen: a thread once it has loaded, or nothing for a new chat.
  useEffect(() => {
    if (resumeId !== null || loadingThread) return;
    if (currentId !== null) writeLast(storeKey, currentId);
    else if (routeId === null) writeLast(storeKey, null);
  }, [currentId, loadingThread, routeId, resumeId, storeKey]);

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
  const onScroll = (e: React.UIEvent<HTMLDivElement>) => {
    fade.onScroll(e);
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
    setDraftText(NEW_DRAFT, "");
    setDraftAttachment(NEW_DRAFT, null);
    navigate("/assistant", { state: { fresh: true } });
    window.setTimeout(() => box.current?.focus(), 0);
  };

  const onDelete = async (t: ChatThread) => {
    try {
      await remove(t.id);
      if (readLast(storeKey) === t.id) writeLast(storeKey, null);
      if (routeId === t.id) navigate("/assistant", { replace: true, state: { fresh: true } });
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
    setVaultNote(false);
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
    const hadKeys = !!lastUser.key_list?.length || !!lastUser.keys;
    if (lastRaw === undefined && hadKeys) {
      // The browser never had these keys (only the vault does), so Edit
      // brings back the words alone. Older messages hold key hints inline:
      // drop those lines too.
      const prose = collapseBlank(
        lastUser.key_list?.length
          ? (lastUser.text ?? "")
          : (lastUser.text ?? "")
              .split("\n")
              .filter((l) => !/(?:\.\.\.|\u2026)[A-Za-z0-9_-]{2,8}\b/.test(l))
              .join("\n"),
      );
      setText(prose);
      setVaultNote(true);
      window.setTimeout(() => {
        const el = box.current;
        if (!el) return;
        el.focus();
        el.setSelectionRange(el.value.length, el.value.length);
      }, 0);
      return;
    }
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

  const list = loaded ? (
    <ThreadList threads={threads} currentId={currentId} working={working} onPick={pick} onDelete={onDelete} />
  ) : (
    <ListSkeleton />
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
          <Steps items={row.items} seconds={runSeconds(row)} fresh={delayFor} live={working && i === rows.length - 1} />
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
          keyList={it.key_list}
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
          <ErrorItem text={it.text} onRetry={!rows[i + 1] && !working && lastUser ? retry : undefined} />
        </div>
      );
    if (!body) return null;
    return (
      <div key={row.key} className={cx(spacing, anim)} style={style}>
        {body}
      </div>
    );
  };

  const mod = typeof navigator !== "undefined" && isMac() ? "\u2318" : "Ctrl";

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
            className="chat-drop pointer-events-none absolute inset-0 z-20 flex items-center justify-center bg-background-full/80 p-4 backdrop-blur-[2px]"
            aria-hidden="true"
          >
            <div className="chat-drop-panel flex w-full max-w-sm flex-col items-center rounded-3xl border-2 border-dashed border-border-button-hover bg-background-primary-default px-6 py-8 text-center shadow-xs">
              <span className="flex size-11 items-center justify-center rounded-xl border border-border-button-default bg-background-primary-default text-status-purple-text shadow-xs">
                <RiFileUploadLine className="size-5" aria-hidden />
              </span>
              <div className="mt-3 text-headline-semibold text-text-primary">Drop a file of keys</div>
              <div className="mt-1 text-body-2-regular text-text-secondary">
                .txt, .csv, .env or .json, up to 1 MB. Claude sees only placeholders.
              </div>
            </div>
          </div>
        )}
        {/* History and New chat stay out of the way: a quiet bar above the centred chat. */}
        <div className="mx-auto flex h-12 w-full max-w-[728px] shrink-0 items-center gap-2 px-3 sm:px-6">
          <TooltipTrigger delay={500}>
            <Button
              size="small"
              variant="ghost"
              leadingIcon={RiChatHistoryLine}
              className={cx("-ms-1", QUIET_BUTTON)}
              onClick={() => setSheet(true)}
              aria-haspopup="dialog"
            >
              Chats
            </Button>
            <Tooltip placement="bottom">
              <span className="inline-flex items-center gap-1.5">
                Open chats <Kbd>{mod}</Kbd>
                <Kbd>K</Kbd>
              </span>
            </Tooltip>
          </TooltipTrigger>
          <h1
            id="chat-title"
            className="min-w-0 flex-1 truncate text-center text-body-2-medium text-text-secondary"
          >
            <span className="sr-only">Assistant{title ? ": " : ""}</span>
            {title}
          </h1>
          <TooltipTrigger delay={500}>
            <IconButton
              icon={RiEditBoxLine}
              size="small"
              aria-label="New chat"
              onClick={newChat}
              className="-me-1 border-transparent bg-transparent shadow-none"
            />
            <Tooltip placement="bottom">
              <span className="inline-flex items-center gap-1.5">
                New chat <Kbd>{mod}</Kbd>
                <Kbd>Shift</Kbd>
                <Kbd>O</Kbd>
              </span>
            </Tooltip>
          </TooltipTrigger>
        </div>

        <div className="relative min-h-0 flex-1">
          <div
            ref={scroller}
            onScroll={onScroll}
            className="h-full overflow-y-auto overscroll-contain"
          >
            <div className="mx-auto flex min-h-full w-full max-w-[728px] flex-col px-4 pt-4 pb-6 sm:px-6">
              {error && (
                <Callout
                  tone="error"
                  className="mb-4"
                  action={
                    <Button size="xs" variant="ghost" className={QUIET_BUTTON} onClick={() => fetch().catch(() => {})}>
                      Try again
                    </Button>
                  }
                >
                  {error}
                </Callout>
              )}
              {!loaded || loadingThread || resumeId !== null ? (
                <div className="flex flex-col gap-6" aria-busy="true" aria-label="Loading chat">
                  <Skeleton className="ms-auto h-10 w-1/2 rounded-2xl" />
                  <div className="flex gap-3">
                    <Skeleton className="size-6 rounded-md" />
                    <div className="flex flex-1 flex-col gap-2">
                      <Skeleton shape="text" className="h-4 w-3/4" />
                      <Skeleton shape="text" className="h-4 w-1/2" />
                    </div>
                  </div>
                  <Skeleton className="ms-auto h-10 w-1/3 rounded-2xl" />
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
          <ScrollFade progress={fade.progress} surfaceClassName="from-background-full" />
          {!atBottom && items.length > 0 && (
            <IconButton
              icon={RiArrowDownLine}
              aria-label="Jump to latest message"
              title="Jump to latest"
              onClick={() => {
                stick.current = true;
                toBottom(true);
              }}
              className="swap-in absolute bottom-3 left-1/2 z-10 -translate-x-1/2 rounded-full shadow-sm"
            />
          )}
        </div>

        <div className="shrink-0 pb-2">
          <div className="mx-auto w-full max-w-[728px] px-3 sm:px-6">
            {off && (
              <Callout tone="warning" icon={RiAlertLine} className="mb-2">
                The assistant is off. {ai?.reason}
              </Callout>
            )}
            <Composer
              ref={box}
              value={text}
              onChange={setText}
              onSubmit={(e) => void submit(e)}
              busy={busy}
              working={working}
              note={vaultNote ? "Keys stay in the vault; paste them again to replace them." : undefined}
              off={off}
              model={ai?.model}
              attachment={attachment}
              onAttach={(a) => setAttachment((cur) => mergeAttachments(cur, a))}
              onClearAttachment={() => setAttachment(null)}
            />
          </div>
        </div>
      </section>

      {asSheet ? (
        <Drawer placement="bottom" isOpen={sheet} onOpenChange={setSheet}>
          <DrawerHeader title="Chats" />
          <DrawerBody className="pb-2">
            <ChatsBody onNew={newChat}>{list}</ChatsBody>
          </DrawerBody>
        </Drawer>
      ) : (
        <Modal isOpen={sheet} onOpenChange={setSheet} size="md" overlayClassName="items-start pt-[12vh]" className="max-h-[70vh]">
          <ModalHeader title="Chats" />
          <ModalBody className="pb-1">
            <ChatsBody onNew={newChat}>{list}</ChatsBody>
          </ModalBody>
        </Modal>
      )}
    </div>
  );
}

/** The Chats dialog: New chat, the list, then the shortcuts on keyboard screens. */
function ChatsBody({ onNew, children }: { onNew: () => void; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-3">
      <Button variant="secondary" leadingIcon={RiEditBoxLine} onClick={onNew} className="w-full justify-start">
        New chat
      </Button>
      <div className="-mx-2">{children}</div>
      <Shortcuts />
    </div>
  );
}

function ListSkeleton() {
  return (
    <div className="flex flex-col gap-3 px-3 pt-1" aria-hidden="true">
      {Array.from({ length: 4 }).map((_, i) => (
        <div key={i} className="flex flex-col gap-1.5">
          <Skeleton shape="text" className="h-3.5 w-4/5" />
          <Skeleton shape="text" className="h-2.5 w-12" />
        </div>
      ))}
    </div>
  );
}
