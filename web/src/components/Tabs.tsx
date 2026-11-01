import { useCallback, useEffect, useRef, useState } from "react";
import type { Key } from "react-aria-components";
import { Tab, TabList, Tabs as BoardTabs } from "@/components/base/tabs/tabs";
import { cx } from "@/utils/cx";

export interface TabItem<T extends string> {
  id: T;
  label: string;
  count?: number;
}

/**
 * Legacy tab strip over BoardUI Tabs: label, count, underline on the active
 * one, arrow keys between tabs. When the row is wider than the screen it
 * scrolls sideways, with a fade on the edge that has more tabs behind it.
 * Panels are the caller's: this only drives the selection.
 */
export function Tabs<T extends string>({
  items,
  value,
  onChange,
  className,
  label,
}: {
  items: TabItem<T>[];
  value: T;
  onChange: (v: T) => void;
  className?: string;
  label?: string;
}) {
  const scroller = useRef<HTMLDivElement>(null);
  const [edges, setEdges] = useState({ start: false, end: false });

  const measure = useCallback(() => {
    const el = scroller.current;
    if (!el) return;
    const start = el.scrollLeft > 2;
    const end = el.scrollLeft + el.clientWidth < el.scrollWidth - 2;
    setEdges((p) => (p.start === start && p.end === end ? p : { start, end }));
  }, []);

  useEffect(() => {
    measure();
    const el = scroller.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    if (el.firstElementChild) ro.observe(el.firstElementChild);
    return () => ro.disconnect();
  }, [measure, items.length]);

  // Keep the active tab in view, for deep links and keyboard moves. The first pass jumps; later changes glide.
  const mounted = useRef(false);
  useEffect(() => {
    const el = scroller.current;
    const tab = el?.querySelector<HTMLElement>("[role=tab][data-selected]");
    const first = !mounted.current;
    mounted.current = true;
    if (!tab || !el || el.scrollWidth <= el.clientWidth) return;
    const fade = 32;
    const start = el.scrollLeft > 2 ? fade : 0;
    const clear = tab.offsetLeft >= el.scrollLeft + start && tab.offsetLeft + tab.offsetWidth <= el.scrollLeft + el.clientWidth - fade;
    if (clear) return;
    const left = tab.offsetLeft - el.clientWidth / 2 + tab.offsetWidth / 2;
    const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    el.scrollTo({ left: Math.max(0, left), behavior: first || reduce ? "auto" : "smooth" });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value, items.length]);

  const fade = edges.start && edges.end ? "fade-x-both" : edges.end ? "fade-x-end" : edges.start ? "fade-x-start" : "";

  return (
    <BoardTabs selectedKey={value} onSelectionChange={(k: Key) => onChange(String(k) as T)} className={className}>
      <div
        ref={scroller}
        onScroll={measure}
        className={cx("no-scrollbar relative -mx-4 overflow-x-auto overscroll-x-contain px-4 sm:mx-0 sm:px-0", fade)}
      >
        <TabList aria-label={label} items={items} className="min-w-max">
          {(it) => (
            <Tab id={it.id} count={it.count}>
              {it.label}
            </Tab>
          )}
        </TabList>
      </div>
    </BoardTabs>
  );
}
