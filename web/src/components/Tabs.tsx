import { useCallback, useEffect, useRef, useState, type KeyboardEvent } from "react";
import { cx } from "./ui";

export interface TabItem<T extends string> {
  id: T;
  label: string;
  count?: number;
}

/**
 * Category tabs: label, small count, underline on the active one. Arrow keys
 * move between tabs. When the row is wider than the screen it scrolls
 * sideways, with a fade on the edge that has more tabs behind it.
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
  const refs = useRef<Array<HTMLButtonElement | null>>([]);
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
    // Watch the row too: late web fonts or count changes widen it without resizing the scroller.
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    if (el.firstElementChild) ro.observe(el.firstElementChild);
    return () => ro.disconnect();
  }, [measure, items.length]);

  // Keep the active tab in view, for deep links and keyboard moves. The first
  // pass jumps (nothing to follow on load); later changes glide.
  const mounted = useRef(false);
  useEffect(() => {
    const i = items.findIndex((it) => it.id === value);
    const btn = refs.current[i];
    const el = scroller.current;
    const first = !mounted.current;
    mounted.current = true;
    if (!btn || !el || el.scrollWidth <= el.clientWidth) return;
    // Skip when the tab is already clear of the 32px edge fades, so clicking a visible tab does not shove the row.
    const fade = 32;
    const start = el.scrollLeft > 2 ? fade : 0;
    const clear = btn.offsetLeft >= el.scrollLeft + start && btn.offsetLeft + btn.offsetWidth <= el.scrollLeft + el.clientWidth - fade;
    if (clear) return;
    const left = btn.offsetLeft - el.clientWidth / 2 + btn.offsetWidth / 2;
    const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    el.scrollTo({ left: Math.max(0, left), behavior: first || reduce ? "auto" : "smooth" });
    // Only when the selection changes; parents re-render often with a fresh items array.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value, items.length]);

  const onKey = (e: KeyboardEvent, i: number) => {
    let j = -1;
    if (e.key === "ArrowRight") j = (i + 1) % items.length;
    else if (e.key === "ArrowLeft") j = (i - 1 + items.length) % items.length;
    else if (e.key === "Home") j = 0;
    else if (e.key === "End") j = items.length - 1;
    if (j < 0) return;
    e.preventDefault();
    onChange(items[j].id);
    refs.current[j]?.focus();
  };

  const fade = edges.start && edges.end ? "fade-x-both" : edges.end ? "fade-x-end" : edges.start ? "fade-x-start" : "";

  return (
    <div
      ref={scroller}
      onScroll={measure}
      className={cx("no-scrollbar relative -mx-4 overflow-x-auto overscroll-x-contain px-4 sm:mx-0 sm:px-0", fade, className)}
    >
      <div role="tablist" aria-label={label} className="flex min-w-max gap-1 border-b border-line">
        {items.map((it, i) => {
          const active = it.id === value;
          return (
            <button
              key={it.id}
              ref={(el) => (refs.current[i] = el)}
              role="tab"
              type="button"
              aria-selected={active}
              tabIndex={active ? 0 : -1}
              onClick={() => onChange(it.id)}
              onKeyDown={(e) => onKey(e, i)}
              className={cx(
                "tap relative -mb-px whitespace-nowrap px-2.5 pb-3 pt-1 text-[13px] transition-colors md:px-2",
                active ? "font-medium text-ink" : "text-muted hover:text-ink",
              )}
            >
              {it.label}
              {it.count !== undefined && <span className="ml-1.5 font-mono text-[11px] text-faint">{it.count}</span>}
              {active && <span className="absolute inset-x-0 bottom-0 h-[2px] rounded-full bg-ink" />}
            </button>
          );
        })}
      </div>
    </div>
  );
}
