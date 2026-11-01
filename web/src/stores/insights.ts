import { create } from "zustand";
import { api } from "../lib/api";
import type { CheckSummary, Key, KeyInsights, KeyRow, KeySummary } from "../lib/types";

interface InsightsStore {
  /** Every key across services, for the Keys page. */
  rows: KeyRow[];
  summary: KeySummary | null;
  loaded: boolean;
  error: string | null;
  /** Detail for the key drawer, by key id. */
  byId: Record<number, KeyInsights>;
  fetch: () => Promise<void>;
  fetchInsight: (id: number) => Promise<KeyInsights>;
  checkAll: () => Promise<CheckSummary>;
  checkService: (serviceId: number) => Promise<CheckSummary>;
  /** Folds a fresh key from any endpoint into the rows and the drawer. */
  patchKey: (k: Key) => void;
}

export const useInsights = create<InsightsStore>((set, get) => ({
  rows: [],
  summary: null,
  loaded: false,
  error: null,
  byId: {},

  fetch: async () => {
    try {
      const res = await api.listAllKeys();
      set({ rows: res.keys ?? [], summary: res.summary ?? null, loaded: true, error: null });
    } catch (e) {
      set({ loaded: true, error: e instanceof Error ? e.message : "Could not load keys" });
      throw e;
    }
  },

  fetchInsight: async (id) => {
    const res = await api.keyInsights(id);
    set({ byId: { ...get().byId, [id]: res } });
    return res;
  },

  checkAll: async () => {
    const res = await api.checkAllKeys();
    for (const k of res.keys ?? []) get().patchKey(k);
    return res;
  },

  checkService: async (serviceId) => {
    const res = await api.checkServiceKeys(serviceId);
    for (const k of res.keys ?? []) get().patchKey(k);
    return res;
  },

  patchKey: (k) => {
    const { rows, byId } = get();
    const i = rows.findIndex((r) => r.id === k.id);
    const nextRows = i === -1 ? rows : rows.map((r) => (r.id === k.id ? { ...r, ...k } : r));
    const ins = byId[k.id];
    set({ rows: nextRows, byId: ins ? { ...byId, [k.id]: { ...ins, key: { ...ins.key, ...k } } } : byId });
  },
}));
