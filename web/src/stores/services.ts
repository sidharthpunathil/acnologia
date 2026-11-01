import { create } from "zustand";
import { api } from "../lib/api";
import type { ProviderTemplate, Service, ServiceInput } from "../lib/types";

interface ServicesStore {
  services: Service[];
  loaded: boolean;
  loading: boolean;
  providers: ProviderTemplate[];
  fetch: () => Promise<void>;
  fetchOne: (id: number) => Promise<Service>;
  fetchProviders: () => Promise<ProviderTemplate[]>;
  create: (input: ServiceInput) => Promise<Service>;
  update: (id: number, patch: Partial<ServiceInput>) => Promise<Service>;
  remove: (id: number) => Promise<void>;
}

function upsert(list: Service[], s: Service): Service[] {
  const i = list.findIndex((x) => x.id === s.id);
  if (i === -1) return [...list, s];
  const next = list.slice();
  next[i] = s;
  return next;
}

export const useServices = create<ServicesStore>((set, get) => ({
  services: [],
  loaded: false,
  loading: false,
  providers: [],

  fetch: async () => {
    set({ loading: true });
    try {
      const res = await api.listServices();
      set({ services: res.services ?? [], loaded: true });
    } finally {
      set({ loading: false });
    }
  },

  fetchOne: async (id) => {
    const s = await api.getService(id);
    set({ services: upsert(get().services, s) });
    return s;
  },

  fetchProviders: async () => {
    if (get().providers.length) return get().providers;
    const res = await api.listProviders();
    set({ providers: res.providers ?? [] });
    return res.providers ?? [];
  },

  create: async (input) => {
    const s = await api.createService(input);
    set({ services: upsert(get().services, s) });
    return s;
  },

  update: async (id, patch) => {
    const s = await api.updateService(id, patch);
    set({ services: upsert(get().services, s) });
    return s;
  },

  remove: async (id) => {
    await api.deleteService(id);
    set({ services: get().services.filter((s) => s.id !== id) });
  },
}));
