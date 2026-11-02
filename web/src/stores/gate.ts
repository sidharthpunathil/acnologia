import { create } from "zustand";
import { api, GATE_LOCKED_EVENT } from "../lib/api";

/**
 * The access gate: a site-wide password the server asks for before setup,
 * sign in or any data, when ACNOLOGIA_ACCESS_PASSWORD is set. The lock screen
 * is a convenience, not the boundary: the server refuses every locked request
 * whatever this renders.
 */
type GateStatus = "loading" | "open" | "locked" | "error";

interface GateStore {
  status: GateStatus;
  /** The server has a password set, so "Lock this browser" means something. */
  required: boolean;
  load: () => Promise<void>;
  unlock: (password: string) => Promise<void>;
  lock: () => Promise<void>;
}

export const useGate = create<GateStore>((set) => ({
  status: "loading",
  required: false,

  load: async () => {
    try {
      const s = await api.gateState();
      set({ status: s.locked ? "locked" : "open", required: s.required });
    } catch {
      // A server that cannot say whether there is a lock must not be read as
      // "no lock": show that it is unreachable, with a retry.
      set({ status: "error" });
    }
  },

  unlock: async (password) => {
    await api.gateUnlock(password);
    // A full load rather than a state flip: nothing behind the gate has
    // fetched yet, and every store then starts clean.
    window.location.reload();
  },

  lock: async () => {
    try {
      await api.gateLock();
    } finally {
      // A full page load drops every store, so nothing stays on screen.
      window.location.assign("/");
    }
  },
}));

// The cookie can expire mid use: any locked answer brings the screen back.
if (typeof window !== "undefined") {
  window.addEventListener(GATE_LOCKED_EVENT, () => {
    if (useGate.getState().status === "open") useGate.setState({ status: "locked", required: true });
  });
}
