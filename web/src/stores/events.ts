import { create } from "zustand";
import { api } from "../lib/api";
import type { CogitoEvent } from "../lib/types";

interface EventsStore {
  /** Global feed for the Activity page. */
  feed: CogitoEvent[];
  feedLoaded: boolean;
  /** Per service recent events for the service page. */
  byService: Record<number, CogitoEvent[]>;
  fetchFeed: (params?: { service_id?: number; limit?: number }) => Promise<void>;
  fetchForService: (serviceId: number, limit?: number) => Promise<void>;
}

export const useEvents = create<EventsStore>((set, get) => ({
  feed: [],
  feedLoaded: false,
  byService: {},

  fetchFeed: async (params = {}) => {
    const res = await api.listEvents({ limit: 200, ...params });
    set({ feed: res.events ?? [], feedLoaded: true });
  },

  fetchForService: async (serviceId, limit = 30) => {
    const res = await api.listEvents({ service_id: serviceId, limit });
    set({ byService: { ...get().byService, [serviceId]: res.events ?? [] } });
  },
}));
