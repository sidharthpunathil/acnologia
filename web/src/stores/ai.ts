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
  /** Deletes one finished run. */
  remove: (id: number) => Promise<void>;
  /** Deletes every failed, cancelled and published run; returns how many. */
  clearFinished: () => Promise<number>;
  publish: (id: number) => Promise<void>;
}

let statusInflight: Promise<unknown> | null = null;

/** Monotonic poll tickets, and the newest ticket applied per run id. */
let pollTicket = 0;
const appliedTicket: Record<number, number> = {};

export const ACTIVE_STATUSES = new Set(["queued", "running", "testing"]);
/** Runs "Clear finished" removes. Runs waiting for review keep their draft. */
export const CLEARABLE_STATUSES = new Set(["failed", "cancelled", "published"]);

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

    remove: async (id) => {
      await api.deleteRun(id);
      const { [id]: _gone, ...rest } = get().byId;
      set({ runs: get().runs.filter((r) => r.id !== id), byId: rest });
    },

    clearFinished: async () => {
      const res = await api.clearRuns();
      await get().fetchRuns();
      return res.deleted;
    },

    start: async (input) => {
      const run = await api.createRun(input);
      putRun(run);
      set({ byId: { ...get().byId, [run.id]: { run, steps: [], lastSeq: 0 } } });
      return run;
    },

    poll: async (id) => {
      // Polls can overlap (interval tick plus cancel/publish). Each one takes a
      // ticket; a response from an older ticket than the last applied one must
      // not overwrite the newer run status.
      const ticket = ++pollTicket;
      const after = get().byId[id]?.lastSeq ?? 0;
      const res = await api.getRun(id, after);

      // Merge against the state as it is now, not as it was before the await.
      const cur = get().byId[id];
      const curSeq = cur?.lastSeq ?? 0;
      const fresh = (res.steps ?? []).filter((s) => s.seq > curSeq);
      const steps = cur ? [...cur.steps, ...fresh] : fresh;
      const lastSeq = fresh.length ? Math.max(curSeq, ...fresh.map((s) => s.seq)) : curSeq;

      const stale =
        !!cur &&
        ((appliedTicket[id] ?? 0) > ticket ||
          // A finished run never goes back to an active status.
          (!ACTIVE_STATUSES.has(cur.run.status) && ACTIVE_STATUSES.has(res.run.status)));
      const run = stale ? cur.run : res.run;
      if (!stale) appliedTicket[id] = ticket;

      const next: RunState = { run, steps, lastSeq };
      set({ byId: { ...get().byId, [id]: next } });
      if (!stale) putRun(run);
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
