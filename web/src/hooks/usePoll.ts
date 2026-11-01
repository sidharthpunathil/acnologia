import { useEffect, useRef } from "react";

/**
 * Calls `fn` every `ms` while the tab is visible, pauses when it is hidden,
 * and runs once right away when the tab comes back.
 */
export function usePoll(fn: () => unknown, ms: number, enabled = true) {
  const ref = useRef(fn);
  ref.current = fn;
  useEffect(() => {
    if (!enabled) return;
    let t: number | undefined;
    const start = () => {
      window.clearInterval(t);
      t = window.setInterval(() => {
        if (!document.hidden) void ref.current();
      }, ms);
    };
    const onVis = () => {
      if (document.hidden) window.clearInterval(t);
      else {
        void ref.current();
        start();
      }
    };
    if (!document.hidden) start();
    document.addEventListener("visibilitychange", onVis);
    return () => {
      window.clearInterval(t);
      document.removeEventListener("visibilitychange", onVis);
    };
  }, [ms, enabled]);
}
