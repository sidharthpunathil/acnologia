import { create } from "zustand";
import { mcpApi } from "../lib/api";
import type { MCPSettings, MCPSettingsInput, MCPView } from "../lib/types";

export function inputOf(s: MCPSettings): MCPSettingsInput {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const { updated_at, ...rest } = s;
  return { ...rest, disabled_tools: [...(rest.disabled_tools ?? [])] };
}

export function sameInput(a: MCPSettingsInput, b: MCPSettingsInput): boolean {
  const keys = Object.keys(a) as Array<keyof MCPSettingsInput>;
  for (const k of keys) {
    if (k === "disabled_tools") continue;
    if (a[k] !== b[k]) return false;
  }
  const x = [...a.disabled_tools].sort();
  const y = [...b.disabled_tools].sort();
  return x.length === y.length && x.every((v, i) => v === y[i]);
}

/** Whether a tool is on under the given settings. */
export function toolOn(s: MCPSettingsInput, t: { name: string; set: keyof MCPSettingsInput }): boolean {
  return !!s[t.set] && !s.disabled_tools.includes(t.name);
}

/** Tools the agent sees right now. */
export const enabledCount = (v: MCPView) => v.tools.filter((t) => t.enabled).length;

interface McpStore {
  views: Record<number, MCPView>;
  errors: Record<number, string>;
  /** Unsaved edits per app. Kept when the panel closes so nothing is lost by a stray swipe. */
  drafts: Record<number, MCPSettingsInput>;
  load: (appId: number, force?: boolean) => Promise<MCPView | undefined>;
  setDraft: (appId: number, next: MCPSettingsInput) => void;
  discard: (appId: number) => void;
  save: (appId: number) => Promise<MCPView>;
}

const inflight = new Map<number, Promise<MCPView | undefined>>();

export const useMcp = create<McpStore>((set, get) => {
  const without = <T,>(rec: Record<number, T>, id: number) => {
    const next = { ...rec };
    delete next[id];
    return next;
  };

  return {
    views: {},
    errors: {},
    drafts: {},

    load: (appId, force) => {
      if (!force && get().views[appId]) return Promise.resolve(get().views[appId]);
      const running = inflight.get(appId);
      if (running) return running;
      const p = mcpApi
        .get(appId)
        .then((v) => {
          set({ views: { ...get().views, [appId]: v }, errors: without(get().errors, appId) });
          return v;
        })
        .catch((e: unknown) => {
          set({ errors: { ...get().errors, [appId]: e instanceof Error ? e.message : "Could not load MCP settings." } });
          return undefined;
        })
        .finally(() => inflight.delete(appId));
      inflight.set(appId, p);
      return p;
    },

    setDraft: (appId, next) => {
      const v = get().views[appId];
      if (v && sameInput(next, inputOf(v.settings))) set({ drafts: without(get().drafts, appId) });
      else set({ drafts: { ...get().drafts, [appId]: next } });
    },

    discard: (appId) => set({ drafts: without(get().drafts, appId) }),

    // Optimistic: show the new settings at once, then take the server's view, or roll back on error.
    save: async (appId) => {
      const prev = get().views[appId];
      const draft = get().drafts[appId];
      if (!prev || !draft) throw new Error("Nothing to save.");
      const optimistic: MCPView = {
        ...prev,
        settings: { ...draft, updated_at: prev.settings.updated_at },
        tools: prev.tools.map((t) => ({ ...t, enabled: toolOn(draft, t) })),
      };
      set({ views: { ...get().views, [appId]: optimistic }, drafts: without(get().drafts, appId) });
      try {
        const v = await mcpApi.put(appId, draft);
        set({ views: { ...get().views, [appId]: v } });
        return v;
      } catch (e) {
        set({ views: { ...get().views, [appId]: prev }, drafts: { ...get().drafts, [appId]: draft } });
        throw e;
      }
    },
  };
});
