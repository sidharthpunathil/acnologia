import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { errorMessage } from "@/lib/api";

export const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
/** How the save shortcut reads on this platform. */
export const SAVE_SHORTCUT = isMac ? "⌘S" : "Ctrl S";

/**
 * Cmd or Ctrl+S saves the form in `scope` while it has unsaved edits: when
 * focus is inside it, or when nothing else on the page has focus. It also
 * keeps the browser's "Save page" dialog away while there is something to save.
 */
export function useSaveShortcut(scope: RefObject<HTMLElement | null>, dirty: boolean, save: () => void) {
  const saveRef = useRef(save);
  saveRef.current = save;
  useEffect(() => {
    if (!dirty) return;
    const onKey = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey) || e.altKey || e.shiftKey || e.key.toLowerCase() !== "s") return;
      const active = document.activeElement;
      const inside = !!active && !!scope.current?.contains(active);
      const idle = !active || active === document.body || active.tagName === "MAIN" || active.tagName === "SECTION";
      if (!inside && !idle) return;
      e.preventDefault();
      saveRef.current();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [dirty, scope]);
}

/**
 * Ask before leaving with unsaved edits: closing or reloading the tab (the
 * ProviderEditor pattern), and following a link to another page of the app.
 * The app uses BrowserRouter, which has no navigation blocker, so link clicks
 * are caught in the capture phase before the router sees them.
 */
export function useUnsavedWarning(dirty: boolean, message = "Leave without saving? Your unsaved changes will be lost.") {
  useEffect(() => {
    if (!dirty) return;
    const onUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = "";
    };
    const onClick = (e: MouseEvent) => {
      if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      const a = (e.target as Element | null)?.closest?.("a[href]") as HTMLAnchorElement | null;
      if (!a || a.target === "_blank" || a.hasAttribute("download")) return;
      const to = new URL(a.href, window.location.href);
      if (to.origin === window.location.origin && to.pathname === window.location.pathname) return;
      if (window.confirm(message)) return;
      e.preventDefault();
      e.stopPropagation();
    };
    window.addEventListener("beforeunload", onUnload);
    document.addEventListener("click", onClick, true);
    return () => {
      window.removeEventListener("beforeunload", onUnload);
      document.removeEventListener("click", onClick, true);
    };
  }, [dirty, message]);
}

export interface Loaded<T> {
  data: T | null;
  error: string | null;
  reload: () => void;
  /** Replace the data after a save. */
  set: (data: T) => void;
}

/**
 * Load once on mount (and on reload). Each section owns its loads, so one
 * failing endpoint shows an error in its own section and leaves the rest
 * alone. Pass `enabled: false` to skip a load this role cannot make.
 */
export function useLoad<T>(load: () => Promise<T>, enabled = true): Loaded<T> {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [nonce, setNonce] = useState(0);
  const loadRef = useRef(load);
  loadRef.current = load;
  useEffect(() => {
    if (!enabled) return;
    let live = true;
    setError(null);
    loadRef
      .current()
      .then((d) => {
        if (live) setData(d);
      })
      .catch((e) => {
        if (live) setError(errorMessage(e));
      });
    return () => {
      live = false;
    };
  }, [enabled, nonce]);
  const reload = useCallback(() => {
    setData(null);
    setNonce((n) => n + 1);
  }, []);
  return { data, error, reload, set: setData };
}
