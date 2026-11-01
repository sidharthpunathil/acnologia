import { useEffect, useState, type MouseEvent } from "react";
import { cx } from "@/utils/cx";
import type { DocHeading } from "./headings";

/** How far below the viewport top a heading counts as "current": the top bar plus some air. */
const SPY_LINE = 120;

/** Which heading the reader is in: the last one scrolled past the top bar, or the last one at the page end. */
export function useActiveHeading(headings: DocHeading[]) {
  const [active, setActive] = useState<string | null>(null);
  useEffect(() => {
    const items = headings.filter((h) => h.depth === 2 || h.depth === 3);
    if (!items.length) {
      setActive(null);
      return;
    }
    let frame = 0;
    const update = () => {
      frame = 0;
      const atEnd = window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 4;
      let current: string | null = null;
      for (const h of items) {
        const el = document.getElementById(h.id);
        if (!el) continue;
        if (el.getBoundingClientRect().top <= SPY_LINE) current = h.id;
        else break;
      }
      if (atEnd && window.scrollY > 0) {
        // The last sections may be too short to reach the line; at the end, the last one visible wins.
        const last = [...items].reverse().find((h) => {
          const el = document.getElementById(h.id);
          return el && el.getBoundingClientRect().top < window.innerHeight;
        });
        if (last) current = last.id;
      }
      setActive(current);
    };
    const onScroll = () => {
      if (!frame) frame = requestAnimationFrame(update);
    };
    update();
    // Diagrams and the lazy renderer change the page height after load.
    const ro = new ResizeObserver(onScroll);
    ro.observe(document.body);
    window.addEventListener("scroll", onScroll, { passive: true });
    window.addEventListener("resize", onScroll);
    return () => {
      ro.disconnect();
      window.removeEventListener("scroll", onScroll);
      window.removeEventListener("resize", onScroll);
      if (frame) cancelAnimationFrame(frame);
    };
  }, [headings]);
  return active;
}

export const tocItems = (headings: DocHeading[]) => headings.filter((h) => h.depth === 2 || h.depth === 3);

/** The page outline: a rail of links with the current section marked. */
export function Toc({
  headings,
  active,
  onPick,
  className,
}: {
  headings: DocHeading[];
  active: string | null;
  onPick?: (id: string, e: MouseEvent<HTMLAnchorElement>) => void;
  className?: string;
}) {
  const items = tocItems(headings);
  if (!items.length) return null;
  return (
    <ul className={cx("flex flex-col border-s border-separator-border", className)}>
      {items.map((h) => {
        const on = h.id === active;
        return (
          <li key={h.id}>
            <a
              href={`#${h.id}`}
              onClick={onPick ? (e) => onPick(h.id, e) : undefined}
              aria-current={on ? "location" : undefined}
              className={cx(
                "-ms-px block rounded-e-md border-s-2 py-1.5 text-body-2-regular outline-none transition-colors duration-150 focus-visible:ring-2 focus-visible:ring-border-focus-ring pointer-coarse:py-2.5",
                h.depth === 3 ? "ps-6" : "ps-3",
                on ? "border-button-primary text-body-2-medium text-text-primary" : "border-transparent text-text-secondary hover:text-text-primary",
              )}
            >
              {h.text}
            </a>
          </li>
        );
      })}
    </ul>
  );
}
