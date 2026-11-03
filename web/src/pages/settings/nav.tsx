import { useCallback, useEffect, useRef, useState, type MouseEvent } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import type { IconComponent } from "@/components/acn/types";
import { cx } from "@/utils/cx";

export interface SettingsNavItem {
  id: string;
  label: string;
  icon: IconComponent;
  /** The section holds unsaved edits. */
  dirty?: boolean;
}

const FOCUS = "outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring";
/** A section is current once its heading passes this line (top bar plus the phone section bar). */
const SPY_LINE = 160;

const reducedMotion = () => window.matchMedia("(prefers-reduced-motion: reduce)").matches;

/** The section whose heading last crossed the spy line; at the very end, the last one on screen. */
export function useActiveSection(ids: string[]) {
  const [active, setActive] = useState<string | null>(ids[0] ?? null);
  const key = ids.join(",");
  useEffect(() => {
    const list = key ? key.split(",") : [];
    let frame = 0;
    const update = () => {
      frame = 0;
      let current = list[0] ?? null;
      for (const id of list) {
        const el = document.getElementById(id);
        if (!el) continue;
        if (el.getBoundingClientRect().top <= SPY_LINE) current = id;
        else break;
      }
      const atEnd = window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 4;
      if (atEnd && window.scrollY > 0) {
        const last = [...list].reverse().find((id) => {
          const el = document.getElementById(id);
          return el && el.getBoundingClientRect().top < window.innerHeight;
        });
        if (last) current = last;
      }
      setActive(current);
    };
    const onScroll = () => {
      if (!frame) frame = requestAnimationFrame(update);
    };
    update();
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
  }, [key]);
  return active;
}

/**
 * Deep links such as /settings#ai land on their section. Sections fill in as
 * their data arrives, so the target is held in place for a moment, until the
 * reader scrolls or types themselves.
 */
export function useHashLanding(ids: string[]) {
  const { hash } = useLocation();
  const landed = useRef(false);
  const key = ids.join(",");
  useEffect(() => {
    if (landed.current || !hash) return;
    const id = decodeURIComponent(hash.slice(1));
    if (!key.split(",").includes(id)) return;
    const until = performance.now() + 2000;
    let frame = 0;
    let stopped = false;
    // Landing ends when time is up or the reader takes over. An effect cleanup
    // (React's development double run, or leaving) does not count as landed.
    const stop = () => {
      stopped = true;
      landed.current = true;
    };
    const tick = () => {
      if (stopped) return;
      if (performance.now() > until) {
        // Screen readers start at the section once it has settled.
        document.getElementById(id)?.focus({ preventScroll: true });
        return stop();
      }
      const el = document.getElementById(id);
      if (el) {
        const offset = parseFloat(getComputedStyle(el).scrollMarginTop) || 0;
        if (Math.abs(el.getBoundingClientRect().top - offset) > 4) el.scrollIntoView({ block: "start" });
      }
      frame = requestAnimationFrame(tick);
    };
    tick();
    const opts = { passive: true } as const;
    window.addEventListener("wheel", stop, opts);
    window.addEventListener("touchstart", stop, opts);
    window.addEventListener("keydown", stop);
    window.addEventListener("pointerdown", stop);
    return () => {
      stopped = true;
      cancelAnimationFrame(frame);
      window.removeEventListener("wheel", stop);
      window.removeEventListener("touchstart", stop);
      window.removeEventListener("keydown", stop);
      window.removeEventListener("pointerdown", stop);
    };
  }, [hash, key]);
}

/** Jump to a section: keep the hash shareable, scroll (smoothly unless reduced motion is on) and move focus there. */
function useJump() {
  const navigate = useNavigate();
  return useCallback(
    (id: string, e: MouseEvent<HTMLAnchorElement>) => {
      if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.button !== 0) return;
      e.preventDefault();
      navigate({ hash: id }, { replace: true });
      const el = document.getElementById(id);
      if (!el) return;
      el.scrollIntoView({ block: "start", behavior: reducedMotion() ? "auto" : "smooth" });
      el.focus({ preventScroll: true });
    },
    [navigate],
  );
}

function DirtyDot({ on }: { on?: boolean }) {
  if (!on) return null;
  return (
    <>
      <span className="size-1.5 shrink-0 rounded-full bg-accent-500" aria-hidden />
      <span className="sr-only">, unsaved changes</span>
    </>
  );
}

/**
 * The in page section list. Wide screens get a sticky column beside the
 * sections; phones get a sticky row of chips under the top bar that scrolls
 * sideways and keeps the current section in view.
 */
export function SettingsNav({ items, active }: { items: SettingsNavItem[]; active: string | null }) {
  const jump = useJump();
  const strip = useRef<HTMLUListElement>(null);

  // Keep the current chip visible in the phone strip without moving the page.
  useEffect(() => {
    const ul = strip.current;
    const chip = active ? ul?.querySelector<HTMLElement>(`[data-id="${active}"]`) : null;
    if (!ul || !chip || ul.scrollWidth <= ul.clientWidth) return;
    const start = chip.offsetLeft - 16;
    const end = chip.offsetLeft + chip.offsetWidth + 16 - ul.clientWidth;
    if (ul.scrollLeft > start) ul.scrollTo({ left: start, behavior: reducedMotion() ? "auto" : "smooth" });
    else if (ul.scrollLeft < end) ul.scrollTo({ left: end, behavior: reducedMotion() ? "auto" : "smooth" });
  }, [active]);

  return (
    <>
      {/* Phones and tablets: a sticky chip row. */}
      <nav
        aria-label="Settings sections"
        className="sticky top-[calc(3.5rem+env(safe-area-inset-top))] z-20 -mx-4 mb-6 border-b border-separator-border bg-background-full/90 backdrop-blur-sm sm:-mx-6 lg:hidden"
      >
        <ul ref={strip} className="no-scrollbar flex gap-1.5 overflow-x-auto overscroll-x-contain px-4 py-2 sm:px-6">
          {items.map((it) => {
            const on = it.id === active;
            return (
              <li key={it.id} data-id={it.id} className="shrink-0">
                <a
                  href={`#${it.id}`}
                  onClick={(e) => jump(it.id, e)}
                  aria-current={on ? "location" : undefined}
                  className={cx(
                    "flex h-9 items-center gap-1.5 rounded-full px-3 text-body-2-medium whitespace-nowrap transition-colors duration-150 pointer-coarse:h-11",
                    FOCUS,
                    on
                      ? "bg-background-primary-default text-text-primary shadow-xs ring-1 ring-border-button-default"
                      : "text-text-secondary hover:bg-background-secondary-hover hover:text-text-primary",
                  )}
                >
                  <it.icon className={cx("size-4 shrink-0", on ? "text-accent-fg" : "text-foreground-icon-tertiary")} aria-hidden />
                  {it.label}
                  <DirtyDot on={it.dirty} />
                </a>
              </li>
            );
          })}
        </ul>
      </nav>

      {/* Wide screens: a sticky column. */}
      <nav aria-label="Settings sections" className="hidden lg:block">
        <ul className="sticky top-[calc(3.5rem+env(safe-area-inset-top)+2rem)] flex flex-col gap-0.5">
          {items.map((it) => {
            const on = it.id === active;
            return (
              <li key={it.id}>
                <a
                  href={`#${it.id}`}
                  onClick={(e) => jump(it.id, e)}
                  aria-current={on ? "location" : undefined}
                  className={cx(
                    "flex min-h-9 items-center gap-2.5 rounded-2lg px-2.5 py-1.5 transition-colors duration-150",
                    FOCUS,
                    on
                      ? "bg-background-primary-default text-body-medium text-text-primary shadow-xs ring-1 ring-border-button-default"
                      : "text-body-regular text-text-secondary hover:bg-background-secondary-hover hover:text-text-primary",
                  )}
                >
                  <it.icon className={cx("size-4 shrink-0", on ? "text-accent-fg" : "text-foreground-icon-tertiary")} aria-hidden />
                  <span className="min-w-0 flex-1 truncate">{it.label}</span>
                  <DirtyDot on={it.dirty} />
                </a>
              </li>
            );
          })}
        </ul>
      </nav>
    </>
  );
}
