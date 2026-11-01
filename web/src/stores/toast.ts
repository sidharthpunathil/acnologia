import { create } from "zustand";

export type ToastKind = "success" | "error" | "info";

export interface Toast {
  id: number;
  kind: ToastKind;
  message: string;
  leaving?: boolean;
}

interface ToastState {
  toasts: Toast[];
  push: (kind: ToastKind, message: string) => void;
  dismiss: (id: number) => void;
}

let nextId = 1;

export const useToasts = create<ToastState>((set, get) => ({
  toasts: [],
  push: (kind, message) => {
    const id = nextId++;
    set({ toasts: [...get().toasts.slice(-3), { id, kind, message }] });
    window.setTimeout(() => get().dismiss(id), kind === "error" ? 6000 : 3500);
  },
  // Mark as leaving first so the exit transition can play, then remove.
  dismiss: (id) => {
    const t = get().toasts.find((x) => x.id === id);
    if (!t || t.leaving) return;
    set({ toasts: get().toasts.map((x) => (x.id === id ? { ...x, leaving: true } : x)) });
    window.setTimeout(() => set({ toasts: get().toasts.filter((x) => x.id !== id) }), 150);
  },
}));

export const toast = {
  success: (m: string) => useToasts.getState().push("success", m),
  error: (m: string) => useToasts.getState().push("error", m),
  info: (m: string) => useToasts.getState().push("info", m),
};
