import { create } from "zustand";
import { api } from "../lib/api";
import type { LogoUpload, Service, ServiceFamily, ServiceInput } from "../lib/types";
import { bumpLogos } from "./providers";

interface ServicesStore {
  services: Service[];
  /** Services one account's key serves, grouped for the catalog. */
  families: ServiceFamily[];
  loaded: boolean;
  loading: boolean;
  error: string | null;
  fetch: () => Promise<void>;
  fetchOne: (id: number) => Promise<Service>;
  create: (input: ServiceInput) => Promise<Service>;
  update: (id: number, patch: Partial<ServiceInput>) => Promise<Service>;
  remove: (id: number) => Promise<void>;
  setLogo: (id: number, logo: LogoUpload) => Promise<void>;
  removeLogo: (id: number) => Promise<void>;
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
  families: [],
  loaded: false,
  loading: false,
  error: null,

  fetch: async () => {
    set({ loading: true });
    try {
      const res = await api.listServices();
      set({ services: res.services ?? [], families: res.families ?? [], loaded: true, error: null });
    } catch (e) {
      set({ loaded: true, error: e instanceof Error ? e.message : "Failed to load services" });
      throw e;
    } finally {
      set({ loading: false });
    }
  },

  fetchOne: async (id) => {
    const s = await api.getService(id);
    set({ services: upsert(get().services, s) });
    return s;
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

  setLogo: async (id, logo) => {
    await api.setServiceLogo(id, logo);
    bumpLogos();
    await get().fetchOne(id);
  },

  removeLogo: async (id) => {
    await api.deleteServiceLogo(id);
    bumpLogos();
    await get().fetchOne(id);
  },
}));
