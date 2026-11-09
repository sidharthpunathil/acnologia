import { create } from "zustand";
import { api } from "../lib/api";
import { maskSecret, parseKeys } from "../lib/keyparse";
import { toast } from "./toast";
import type { ChatAi, ChatAnswer, ChatItem, ChatReply, ChatThread, ChatTurn } from "../lib/types";

/** Keys pasted or dropped, held beside the composer instead of inside it. */
export interface DraftAttachment {
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

/** An unsent message: the text in the composer and any keys attached to it. */
export interface ChatDraft {
  text: string;
  attachment: DraftAttachment | null;
}

/** A message waiting to be sent after the running turn ends. */
export interface QueuedMessage {
  id: string;
  /** What you typed, keys and all. Memory only, never stored or shown. */
  text: string;
  /** The same text with keys masked, for the queue row. */
  shown: string;
}

/** The draft slot for a chat not created yet. */
export const NEW_DRAFT = "new";

type Update<T> = T | ((cur: T) => T);

interface AssistantStore {
  threads: ChatThread[];
  ai: ChatAi | null;
  loaded: boolean;
  error: string | null;
  /** The open thread. Null shows the empty state, and the first message creates a thread. */
  currentId: number | null;
  items: ChatItem[];
  loadingThread: boolean;
  /** Thread id with a request in flight. */
  sending: number | null;
  /** Pending id of a confirm in flight. */
  confirming: string | null;
  /** The open thread's server side turn. While active the store polls the thread. */
  turn: ChatTurn | null;
  /**
   * One time app tokens by item id. The server sends each one once and never
   * again, so they are kept here for the rest of the session.
   */
  secrets: Record<string, string>;
  /**
   * What you typed, by thread, for the last message you sent this session, so
   * Retry and Edit can bring back keys the transcript shows only as hints.
   * Memory only, never stored.
   */
  lastSent: Record<number, string>;
  /**
   * Unsent composer text and attachments by thread id (or NEW_DRAFT), so
   * leaving the page or switching chats keeps them. Memory only, never
   * stored: a draft can hold pasted API keys.
   */
  drafts: Record<string, ChatDraft>;
  setDraftText: (key: string, text: Update<string>) => void;
  /**
   * Messages queued by thread while a turn runs, sent one at a time as each
   * turn ends. Memory only, like drafts: a queued message can hold keys.
   */
  queues: Record<number, QueuedMessage[]>;
  enqueue: (threadId: number, text: string) => void;
  /** Takes a queued message out, for Remove or Edit; returns it. */
  unqueue: (threadId: number, id: string) => QueuedMessage | undefined;
  setDraftAttachment: (key: string, attachment: Update<DraftAttachment | null>) => void;
  fetch: () => Promise<void>;
  open: (id: number | null) => Promise<void>;
  create: () => Promise<ChatThread>;
  remove: (id: number) => Promise<void>;
  send: (text: string) => Promise<void>;
  confirm: (pendingId: string, approve: boolean) => Promise<void>;
  answer: (body: ChatAnswer) => Promise<void>;
  /** Thread id with a Compact now request in flight. */
  compacting: number | null;
  /** Summarizes the open thread's older messages now, whatever its size. */
  compact: () => Promise<void>;
}

let localSeq = 0;
const POLL_MS = 1200;

/**
 * Masks the keys in a message the way the server will (…last4), so the
 * raw secret is never shown, even before the server answers.
 */
export function maskMessage(text: string): { text: string; keys: number } {
  const { keys } = parseKeys(text);
  if (!keys.length) return { text, keys: 0 };
  let out = text;
  for (const k of [...keys].sort((a, b) => b.secret.length - a.secret.length)) {
    out = out.split(k.secret).join(maskSecret(k.secret));
  }
  return { text: out, keys: keys.length };
}

export const useAssistant = create<AssistantStore>((set, get) => {
  /** Puts the thread first, since the list is sorted by last update. */
  const putThread = (t: ChatThread) => {
    set({ threads: [t, ...get().threads.filter((x) => x.id !== t.id)] });
  };

  const withSecrets = (items: ChatItem[]) => {
    const secrets = get().secrets;
    return items.map((it) => (!it.secret && secrets[it.id] ? { ...it, secret: secrets[it.id] } : it));
  };

  const keepSecrets = (items: ChatItem[]) => {
    const fresh = Object.fromEntries(items.filter((it) => it.secret).map((it) => [it.id, it.secret as string]));
    if (Object.keys(fresh).length) set({ secrets: { ...get().secrets, ...fresh } });
  };

  /**
   * Says so when the server summarized older messages on its own during a
   * turn (a new "compact" item). Compact now has its own confirmation.
   */
  const noteCompaction = (threadId: number, next: ChatItem[]) => {
    if (get().compacting === threadId || get().currentId !== threadId) return;
    const seen = new Set(get().items.map((it) => it.id));
    if (next.some((it) => it.kind === "compact" && !seen.has(it.id)))
      toast.info("Earlier messages were summarized to keep this chat fast.");
  };

  /** Merges a reply into local state by id: changed items replace old ones, secrets are kept. */
  const apply = (threadId: number, res: ChatReply) => {
    keepSecrets(res.items);
    keepSecrets(res.all ?? []);
    putThread(res.thread);
    if (get().currentId !== threadId) return;
    noteCompaction(threadId, res.all ?? res.items);
    const base = res.all ?? get().items.filter((it) => !it.id.startsWith("local-"));
    const changed = new Map(res.items.map((it) => [it.id, it]));
    const merged = base.map((it) => changed.get(it.id) ?? it);
    for (const it of res.items) if (!merged.some((m) => m.id === it.id)) merged.push(it);
    set({ items: withSecrets(merged), turn: res.turn ?? { active: false } });
    if (res.turn?.active) poll(threadId);
  };

  // One poll loop at a time, for the open thread only.
  let polling: number | null = null;
  const poll = (threadId: number) => {
    if (polling === threadId) return;
    polling = threadId;
    const tick = async () => {
      if (get().currentId !== threadId || polling !== threadId) {
        if (polling === threadId) polling = null;
        return;
      }
      try {
        const res = await api.getChatThread(threadId);
        if (get().currentId !== threadId) {
          polling = null;
          return;
        }
        keepSecrets(res.items ?? []);
        noteCompaction(threadId, res.items ?? []);
        const turn = res.turn ?? { active: false };
        set({ items: withSecrets(res.items ?? []), turn });
        if (!turn.active) {
          polling = null;
          putThread({ ...res.thread, updated_at: new Date().toISOString() });
          return;
        }
      } catch {
        // A blip: keep polling; a deleted thread stops when it is closed.
      }
      window.setTimeout(tick, POLL_MS);
    };
    window.setTimeout(tick, POLL_MS);
  };

  /** Runs one request that starts or continues a turn. */
  const turnRequest = async (call: (id: number) => Promise<ChatReply>) => {
    const id = get().currentId;
    if (id === null) return;
    set({ sending: id });
    try {
      apply(id, await call(id));
    } finally {
      if (get().sending === id) set({ sending: null });
    }
  };

  return {
    threads: [],
    ai: null,
    loaded: false,
    error: null,
    currentId: null,
    items: [],
    loadingThread: false,
    sending: null,
    confirming: null,
    compacting: null,
    turn: null,
    secrets: {},
    lastSent: {},
    drafts: {},
    queues: {},

    enqueue: (threadId, text) => {
      const q = get().queues[threadId] ?? [];
      const item = { id: `queued-${++localSeq}`, text, shown: maskMessage(text).text };
      set({ queues: { ...get().queues, [threadId]: [...q, item] } });
    },

    unqueue: (threadId, id) => {
      const q = get().queues[threadId] ?? [];
      const hit = q.find((m) => m.id === id);
      if (hit) set({ queues: { ...get().queues, [threadId]: q.filter((m) => m.id !== id) } });
      return hit;
    },

    setDraftText: (key, text) => {
      const cur = get().drafts[key] ?? { text: "", attachment: null };
      const next = typeof text === "function" ? text(cur.text) : text;
      set({ drafts: { ...get().drafts, [key]: { ...cur, text: next } } });
    },

    setDraftAttachment: (key, attachment) => {
      const cur = get().drafts[key] ?? { text: "", attachment: null };
      const next = typeof attachment === "function" ? attachment(cur.attachment) : attachment;
      set({ drafts: { ...get().drafts, [key]: { ...cur, attachment: next } } });
    },

    fetch: async () => {
      try {
        const res = await api.listChatThreads();
        set({ threads: res.threads ?? [], ai: res.ai ?? null, loaded: true, error: null });
      } catch (e) {
        set({ loaded: true, error: e instanceof Error ? e.message : "Failed to load chats" });
        throw e;
      }
    },

    open: async (id) => {
      // A thread made by create() is already current and has nothing to load
      // yet. The same applies when you come back to the open thread from
      // another page: it is still in memory, and a turn that was running then
      // is still followed (polling restarts if it had stopped).
      if (id === get().currentId) {
        if (id === null || get().sending === id || get().loadingThread) return;
        if (get().turn?.active) {
          poll(id);
          return;
        }
        // Idle: catch up quietly on anything that changed while you were away.
        api
          .getChatThread(id)
          .then((res) => {
            if (get().currentId !== id || get().sending === id || get().turn?.active) return;
            keepSecrets(res.items ?? []);
            set({ items: withSecrets(res.items ?? []), turn: res.turn ?? null });
            if (res.turn?.active) poll(id);
          })
          .catch(() => {});
        return;
      }
      set({ currentId: id, items: [], turn: null, loadingThread: id !== null });
      if (id === null) return;
      try {
        const res = await api.getChatThread(id);
        if (get().currentId !== id) return;
        keepSecrets(res.items ?? []);
        set({ items: withSecrets(res.items ?? []), turn: res.turn ?? null, loadingThread: false });
        // A turn still running (a provider build, say) keeps going: follow it.
        if (res.turn?.active) poll(id);
      } catch (e) {
        if (get().currentId === id) set({ currentId: null, loadingThread: false });
        throw e;
      }
    },

    create: async () => {
      const res = await api.createChatThread();
      putThread(res.thread);
      set({ currentId: res.thread.id, items: res.items ?? [], turn: null, loadingThread: false });
      return res.thread;
    },

    remove: async (id) => {
      await api.deleteChatThread(id);
      const { [id]: _, ...rest } = get().lastSent;
      const { [String(id)]: _draft, ...drafts } = get().drafts;
      const { [id]: _queue, ...queues } = get().queues;
      set({ threads: get().threads.filter((t) => t.id !== id), lastSent: rest, drafts, queues });
      if (get().currentId === id) set({ currentId: null, items: [], turn: null });
    },

    send: async (text) => {
      let id = get().currentId;
      if (id === null) id = (await get().create()).id;
      // Your own bubble shows keys masked from the first frame.
      const shown = maskMessage(text);
      const local: ChatItem = {
        id: `local-${++localSeq}`,
        kind: "user",
        text: shown.text,
        keys: shown.keys || undefined,
        ok: true,
        ts: new Date().toISOString(),
      };
      set({
        sending: id,
        items: [...get().items, local],
        turn: { active: true, step: "Thinking", started_at: new Date().toISOString() },
        lastSent: { ...get().lastSent, [id]: text },
      });
      try {
        const res = await api.sendChatMessage(id, text);
        apply(id, res);
      } catch (e) {
        if (get().currentId === id) set({ items: get().items.filter((it) => it.id !== local.id), turn: null });
        throw e;
      } finally {
        if (get().sending === id) set({ sending: null });
      }
    },

    confirm: async (pendingId, approve) => {
      set({ confirming: pendingId });
      try {
        await turnRequest((id) => api.confirmChat(id, pendingId, approve));
      } finally {
        set({ confirming: null });
      }
    },

    answer: async (body) => {
      await turnRequest((id) => api.answerChat(id, body));
    },

    compact: async () => {
      const id = get().currentId;
      if (id === null) return;
      set({
        compacting: id,
        sending: id,
        turn: { active: true, step: "Summarizing earlier messages", started_at: new Date().toISOString() },
      });
      try {
        apply(id, await api.compactChat(id));
      } catch (e) {
        if (get().currentId === id) set({ turn: null });
        throw e;
      } finally {
        if (get().sending === id) set({ sending: null });
        if (get().compacting === id) set({ compacting: null });
      }
    },
  };
});
