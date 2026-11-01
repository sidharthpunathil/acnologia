import { create } from "zustand";
import { api } from "../lib/api";
import type { AgentRun, AgentRunInput, AgentStep, AiStatus } from "../lib/types";

interface RunState {
  run: AgentRun;
  steps: AgentStep[];
  lastSeq: number;
}

interface AiStore {
  status: AiStatus | null;
  statusError: string | null;
  runs: AgentRun[];
  runsLoaded: boolean;
  byId: Record<number, RunState>;
  fetchStatus: () => Promise<AiStatus | null>;
  /** Fetch the AI status once per session. */
  ensureStatus: () => void;
  fetchRuns: () => Promise<void>;
  start: (input: AgentRunInput) => Promise<AgentRun>;
  /** Fetches the run and only the steps after the last one seen. */
  poll: (id: number) => Promise<RunState>;
  cancel: (id: number) => Promise<void>;
  publish: (id: number) => Promise<void>;
}

let statusInflight: Promise<unknown> | null = null;

export const ACTIVE_STATUSES = new Set(["queued", "running", "testing"]);

export const useAi = create<AiStore>((set, get) => {
  const putRun = (r: AgentRun) => {
    const list = get().runs;
    set({ runs: list.some((x) => x.id === r.id) ? list.map((x) => (x.id === r.id ? r : x)) : [r, ...list] });
  };

  return {
    status: null,
    statusError: null,
    runs: [],
    runsLoaded: false,
    byId: {},

    fetchStatus: async () => {
      try {
        const s = await api.aiStatus();
        set({ status: s, statusError: null });
        return s;
      } catch (e) {
        set({ statusError: e instanceof Error ? e.message : "AI status unavailable" });
        return null;
      }
    },

    ensureStatus: () => {
      if (get().status || statusInflight) return;
      statusInflight = get()
        .fetchStatus()
        .finally(() => (statusInflight = null));
    },

    fetchRuns: async () => {
      const res = await api.listRuns();
      set({ runs: res.runs ?? [], runsLoaded: true });
    },

    start: async (input) => {
      const run = await api.createRun(input);
      putRun(run);
      set({ byId: { ...get().byId, [run.id]: { run, steps: [], lastSeq: 0 } } });
      return run;
    },

    poll: async (id) => {
      const prev = get().byId[id];
      const after = prev?.lastSeq ?? 0;
      const res = await api.getRun(id, after);
      const fresh = (res.steps ?? []).filter((s) => s.seq > after);
      const steps = prev ? [...prev.steps, ...fresh] : fresh;
      const lastSeq = steps.length ? Math.max(after, ...fresh.map((s) => s.seq)) : after;
      const next: RunState = { run: res.run, steps, lastSeq };
      set({ byId: { ...get().byId, [id]: next } });
      putRun(res.run);
      return next;
    },

    cancel: async (id) => {
      await api.cancelRun(id);
      await get().poll(id);
    },

    publish: async (id) => {
      await api.publishRun(id);
      await get().poll(id);
    },
  };
});
