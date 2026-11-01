import { create } from "zustand";
import { api } from "../lib/api";
import type { Key, KeyPatch, NewKeyInput } from "../lib/types";

interface KeysStore {
  byService: Record<number, Key[]>;
  loading: Record<number, boolean>;
  fetch: (serviceId: number) => Promise<void>;
  add: (serviceId: number, keys: NewKeyInput[]) => Promise<number>;
  update: (key: Key, patch: KeyPatch) => Promise<Key>;
  remove: (key: Key) => Promise<void>;
  check: (key: Key) => Promise<Key>;
  reset: (key: Key) => Promise<Key>;
  reveal: (key: Key) => Promise<string>;
}

export const useKeys = create<KeysStore>((set, get) => {
  const replace = (k: Key) => {
    const list = get().byService[k.service_id] ?? [];
    set({
      byService: { ...get().byService, [k.service_id]: list.map((x) => (x.id === k.id ? k : x)) },
    });
  };

  return {
    byService: {},
    loading: {},

    fetch: async (serviceId) => {
      set({ loading: { ...get().loading, [serviceId]: true } });
      try {
        const res = await api.listKeys(serviceId);
        set({ byService: { ...get().byService, [serviceId]: res.keys ?? [] } });
      } finally {
        set({ loading: { ...get().loading, [serviceId]: false } });
      }
    },

    add: async (serviceId, keys) => {
      const res = await api.addKeys(serviceId, keys);
      await get().fetch(serviceId);
      return res?.keys?.length ?? keys.length;
    },

    update: async (key, patch) => {
      const k = await api.updateKey(key.id, patch);
      replace(k);
      return k;
    },

    remove: async (key) => {
      await api.deleteKey(key.id);
      const list = get().byService[key.service_id] ?? [];
      set({ byService: { ...get().byService, [key.service_id]: list.filter((x) => x.id !== key.id) } });
    },

    check: async (key) => {
      const k = await api.checkKey(key.id);
      replace(k);
      return k;
    },

    reset: async (key) => {
      const k = await api.resetKey(key.id);
      if (k && typeof k === "object" && "id" in k) replace(k);
      else await get().fetch(key.service_id);
      return k;
    },

    reveal: async (key) => {
      const res = await api.revealKey(key.id);
      return res.secret;
    },
  };
});
