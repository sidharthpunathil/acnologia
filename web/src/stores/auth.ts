import { create } from "zustand";
import { api, setUnauthorizedHandler } from "../lib/api";
import type { User } from "../lib/types";

type Status = "loading" | "setup" | "anonymous" | "authenticated" | "error";

interface AuthStore {
  status: Status;
  user: User | null;
  error: string | null;
  load: () => Promise<void>;
  login: (email: string, password: string) => Promise<void>;
  setup: (email: string, name: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  updateMe: (patch: { name?: string; current_password: string; password?: string }) => Promise<void>;
  expire: () => void;
}

export const useAuth = create<AuthStore>((set, get) => ({
  status: "loading",
  user: null,
  error: null,

  load: async () => {
    try {
      const s = await api.authState();
      if (s.setup_required) set({ status: "setup", user: null, error: null });
      else if (s.user) set({ status: "authenticated", user: s.user, error: null });
      else set({ status: "anonymous", user: null, error: null });
    } catch (e) {
      set({ status: "error", error: e instanceof Error ? e.message : "Cannot reach server" });
    }
  },

  login: async (email, password) => {
    await api.login({ email, password });
    await get().load();
  },

  setup: async (email, name, password) => {
    await api.setup({ email, name, password });
    // Some servers log the new superadmin in directly; others need a login.
    await get().load();
    if (get().status !== "authenticated") {
      await api.login({ email, password });
      await get().load();
    }
  },

  logout: async () => {
    try {
      await api.logout();
    } finally {
      set({ status: "anonymous", user: null });
    }
  },

  updateMe: async (patch) => {
    const user = await api.updateMe(patch);
    if (user && typeof user === "object" && "id" in user) set({ user });
    else await get().load();
  },

  expire: () => {
    if (get().status === "authenticated") set({ status: "anonymous", user: null });
  },
}));

setUnauthorizedHandler(() => useAuth.getState().expire());
