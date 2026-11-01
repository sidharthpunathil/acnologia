import { create } from "zustand";
import { api } from "../lib/api";
import type { App, AppInput, AppWithToken } from "../lib/types";

interface AppsStore {
  apps: App[];
  loaded: boolean;
  error: string | null;
  fetch: () => Promise<void>;
  create: (input: AppInput) => Promise<AppWithToken>;
  update: (id: number, patch: Partial<AppInput & { active: boolean }>) => Promise<App>;
  remove: (id: number) => Promise<void>;
  rotate: (id: number) => Promise<AppWithToken>;
}

export const useApps = create<AppsStore>((set, get) => {
  const put = (a: App) => {
    const list = get().apps;
    const exists = list.some((x) => x.id === a.id);
    set({ apps: exists ? list.map((x) => (x.id === a.id ? a : x)) : [...list, a] });
  };

  return {
    apps: [],
    loaded: false,
    error: null,

    fetch: async () => {
      try {
        const res = await api.listApps();
        set({ apps: res.apps ?? [], loaded: true, error: null });
      } catch (e) {
        set({ loaded: true, error: e instanceof Error ? e.message : "Failed to load apps" });
        throw e;
      }
    },

    create: async (input) => {
      const res = await api.createApp(input);
      put(res.app);
      return res;
    },

    update: async (id, patch) => {
      const a = await api.updateApp(id, patch);
      put(a);
      return a;
    },

    remove: async (id) => {
      await api.deleteApp(id);
      set({ apps: get().apps.filter((a) => a.id !== id) });
    },

    rotate: async (id) => {
      const res = await api.rotateAppToken(id);
      put(res.app);
      return res;
    },
  };
});
