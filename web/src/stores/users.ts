import { create } from "zustand";
import { api } from "../lib/api";
import type { AuditEntry, Role, User } from "../lib/types";

interface UsersStore {
  users: User[];
  loaded: boolean;
  audit: AuditEntry[];
  auditLoaded: boolean;
  fetch: () => Promise<void>;
  create: (input: { email: string; name: string; password: string; role: Role }) => Promise<User>;
  update: (id: number, patch: Partial<{ name: string; role: Role; disabled: boolean }>) => Promise<User>;
  remove: (id: number) => Promise<void>;
  fetchAudit: () => Promise<void>;
}

export const useUsers = create<UsersStore>((set, get) => {
  const put = (u: User) => {
    const list = get().users;
    const exists = list.some((x) => x.id === u.id);
    set({ users: exists ? list.map((x) => (x.id === u.id ? u : x)) : [...list, u] });
  };

  return {
    users: [],
    loaded: false,
    audit: [],
    auditLoaded: false,

    fetch: async () => {
      const res = await api.listUsers();
      set({ users: res.users ?? [], loaded: true });
    },

    create: async (input) => {
      const u = await api.createUser(input);
      put(u);
      return u;
    },

    update: async (id, patch) => {
      const u = await api.updateUser(id, patch);
      put(u);
      return u;
    },

    remove: async (id) => {
      await api.deleteUser(id);
      set({ users: get().users.filter((u) => u.id !== id) });
    },

    fetchAudit: async () => {
      const res = await api.listAudit();
      set({ audit: res.audit ?? [], auditLoaded: true });
    },
  };
});
