import { create } from "zustand";
import { api } from "../lib/api";
import type { ChatAi, ChatItem, ChatReply, ChatThread } from "../lib/types";

interface AssistantStore {
  threads: ChatThread[];
  ai: ChatAi | null;
  loaded: boolean;
  error: string | null;
  /** The open thread. Null shows the empty state, and the first message creates a thread. */
  currentId: number | null;
  items: ChatItem[];
  loadingThread: boolean;
  /** Thread id with a message in flight. */
  sending: number | null;
  /** Pending id of a confirm in flight. */
  confirming: string | null;
  /**
   * One time app tokens by item id. The server sends each one once and never
   * again, so they are kept here for the rest of the session.
   */
  secrets: Record<string, string>;
  fetch: () => Promise<void>;
  open: (id: number | null) => Promise<void>;
  create: () => Promise<ChatThread>;
  remove: (id: number) => Promise<void>;
  send: (text: string) => Promise<void>;
  confirm: (pendingId: string, approve: boolean) => Promise<void>;
}

let localSeq = 0;

export const useAssistant = create<AssistantStore>((set, get) => {
  /** Puts the thread first, since the list is sorted by last update. */
  const putThread = (t: ChatThread) => {
    set({ threads: [t, ...get().threads.filter((x) => x.id !== t.id)] });
  };

  const withSecrets = (items: ChatItem[]) => {
    const secrets = get().secrets;
    return items.map((it) => (!it.secret && secrets[it.id] ? { ...it, secret: secrets[it.id] } : it));
  };

  /** Merges a reply into local state by id: changed items replace old ones, secrets are kept. */
  const apply = (threadId: number, res: ChatReply) => {
    const fresh = Object.fromEntries(res.items.filter((it) => it.secret).map((it) => [it.id, it.secret as string]));
    if (Object.keys(fresh).length) set({ secrets: { ...get().secrets, ...fresh } });
    putThread(res.thread);
    if (get().currentId !== threadId) return;
    const base = res.all ?? get().items.filter((it) => !it.id.startsWith("local-"));
    const changed = new Map(res.items.map((it) => [it.id, it]));
    const merged = base.map((it) => changed.get(it.id) ?? it);
    for (const it of res.items) if (!merged.some((m) => m.id === it.id)) merged.push(it);
    set({ items: withSecrets(merged) });
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
    secrets: {},

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
      // A thread made by create() is already current and has nothing to load yet.
      if (id === get().currentId) return;
      set({ currentId: id, items: [], loadingThread: id !== null });
      if (id === null) return;
      try {
        const res = await api.getChatThread(id);
        if (get().currentId !== id) return;
        set({ items: withSecrets(res.items ?? []), loadingThread: false });
      } catch (e) {
        if (get().currentId === id) set({ currentId: null, loadingThread: false });
        throw e;
      }
    },

    create: async () => {
      const res = await api.createChatThread();
      putThread(res.thread);
      set({ currentId: res.thread.id, items: res.items ?? [], loadingThread: false });
      return res.thread;
    },

    remove: async (id) => {
      await api.deleteChatThread(id);
      set({ threads: get().threads.filter((t) => t.id !== id) });
      if (get().currentId === id) set({ currentId: null, items: [] });
    },

    send: async (text) => {
      let id = get().currentId;
      if (id === null) id = (await get().create()).id;
      const local: ChatItem = { id: `local-${++localSeq}`, kind: "user", text, ok: true, ts: new Date().toISOString() };
      set({ sending: id, items: [...get().items, local] });
      try {
        const res = await api.sendChatMessage(id, text);
        apply(id, res);
      } catch (e) {
        if (get().currentId === id) set({ items: get().items.filter((it) => it.id !== local.id) });
        throw e;
      } finally {
        if (get().sending === id) set({ sending: null });
      }
    },

    confirm: async (pendingId, approve) => {
      const id = get().currentId;
      if (id === null) return;
      set({ confirming: pendingId, sending: id });
      try {
        const res = await api.confirmChat(id, pendingId, approve);
        apply(id, res);
      } finally {
        set({ confirming: null });
        if (get().sending === id) set({ sending: null });
      }
    },
  };
});
