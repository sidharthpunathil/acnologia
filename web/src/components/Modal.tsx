import { useEffect, useId, useRef, useState, type PointerEvent as ReactPointerEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { X } from "lucide-react";
import { cx } from "./ui";

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

/** Open dialogs, newest last. Only the top one answers Escape and traps Tab. */
const stack: symbol[] = [];

/** Phone layout, portrait or landscape: dialogs and drawers become bottom sheets. Mirrors the `dlg` screen. */
const SHEET_QUERY = "(max-width: 639px), (pointer: coarse) and (max-height: 500px)";
const isPhone = () => typeof window !== "undefined" && window.matchMedia(SHEET_QUERY).matches;

/**
 * Accessible dialog: Escape closes, focus is trapped inside while open and
 * returned to the element that opened it on close.
 *
 * `variant="drawer"` is a side sheet on desktop. On phones both variants are
 * bottom sheets that can be dragged down to close.
 */
export function Modal({
  open,
  onClose,
  title,
  description,
  children,
  footer,
  wide,
  headerAction,
  variant = "dialog",
  header,
}: {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  description?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  wide?: boolean;
  /** Small control next to the title, such as "Fill with AI". */
  headerAction?: ReactNode;
  variant?: "dialog" | "drawer";
  /** Replaces the default title block; `title` is still used as the accessible name. */
  header?: ReactNode;
}) {
  const panel = useRef<HTMLDivElement>(null);
  // Stay mounted briefly after closing so the exit animation can play, and
  // keep showing the last content so the panel does not blank while leaving.
  const [mounted, setMounted] = useState(open);
  const last = useRef({ title, description, children, footer, headerAction, header });
  if (open) last.current = { title, description, children, footer, headerAction, header };
  useEffect(() => {
    if (open) {
      setMounted(true);
      return;
    }
    const t = window.setTimeout(() => setMounted(false), 180);
    return () => window.clearTimeout(t);
  }, [open]);
  const closing = !open && mounted;
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  const titleId = useId();
  const descId = useId();
  const drawer = variant === "drawer";

  useEffect(() => {
    if (!open) return;
    const me = Symbol("modal");
    stack.push(me);
    const opener = document.activeElement as HTMLElement | null;
    const onKey = (e: KeyboardEvent) => {
      if (stack[stack.length - 1] !== me) return;
      if (e.key === "Escape") {
        e.stopPropagation();
        closeRef.current();
        return;
      }
      if (e.key !== "Tab" || !panel.current) return;
      const items = [...panel.current.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((el) => el.offsetParent !== null);
      if (!items.length) {
        e.preventDefault();
        panel.current.focus();
        return;
      }
      const first = items[0];
      const lastEl = items[items.length - 1];
      if (e.shiftKey && (document.activeElement === first || document.activeElement === panel.current)) {
        e.preventDefault();
        lastEl.focus();
      } else if (!e.shiftKey && document.activeElement === lastEl) {
        e.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKey);
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    // Focus the first field, else the panel itself so screen readers announce the dialog.
    // On phones skip auto focusing inputs: the keyboard would cover half the sheet.
    const t = window.setTimeout(() => {
      const field = isPhone()
        ? panel.current?.querySelector<HTMLElement>("[data-autofocus]")
        : panel.current?.querySelector<HTMLElement>("[data-autofocus], input:not([type=hidden]):not([disabled]), textarea, select");
      (field ?? panel.current)?.focus({ preventScroll: true });
    }, 0);
    return () => {
      window.clearTimeout(t);
      document.removeEventListener("keydown", onKey);
      const i = stack.indexOf(me);
      if (i >= 0) stack.splice(i, 1);
      document.body.style.overflow = prev;
      if (opener && document.contains(opener)) opener.focus({ preventScroll: true });
    };
  }, [open]);

  // Drag down to dismiss on phones. Velocity counts, so a quick flick is enough.
  const drag = useRef<{ y: number; t: number; id: number } | null>(null);
  const onDragStart = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (!isPhone() || drag.current || e.button !== 0) return;
    if ((e.target as HTMLElement).closest("button, a, input, select, textarea, label")) return;
    drag.current = { y: e.clientY, t: performance.now(), id: e.pointerId };
    e.currentTarget.setPointerCapture(e.pointerId);
    if (panel.current) {
      // The entrance animation holds transform; drop it so the panel follows the finger.
      panel.current.style.animation = "none";
      panel.current.style.transition = "none";
    }
  };
  const onDragMove = (e: ReactPointerEvent<HTMLDivElement>) => {
    const d = drag.current;
    if (!d || e.pointerId !== d.id || !panel.current) return;
    const dy = e.clientY - d.y;
    // Friction when pulling up past the resting point.
    const y = dy < 0 ? -Math.sqrt(-dy) * 2 : dy;
    panel.current.style.transform = `translateY(${y}px)`;
  };
  const onDragEnd = (e: ReactPointerEvent<HTMLDivElement>) => {
    const d = drag.current;
    if (!d || e.pointerId !== d.id || !panel.current) return;
    drag.current = null;
    const dy = e.clientY - d.y;
    const v = dy / Math.max(1, performance.now() - d.t);
    const el = panel.current;
    if (dy > 120 || (dy > 24 && v > 0.5)) {
      el.style.transition = "transform 180ms ease-out";
      el.style.transform = "translateY(100%)";
      window.setTimeout(() => closeRef.current(), 120);
    } else {
      el.style.transition = "transform 220ms var(--ease-drawer)";
      el.style.transform = "";
    }
  };
  // Clear inline drag styles when the panel opens again.
  useEffect(() => {
    if (open && panel.current) {
      panel.current.style.transform = "";
      panel.current.style.transition = "";
      panel.current.style.animation = "";
    }
  }, [open]);

  if (!open && !mounted) return null;
  ({ title, description, children, footer, headerAction, header } = last.current);

  return createPortal(
    <div className={cx("fixed inset-0 z-50 flex items-end justify-center", drawer ? "dlg:items-stretch dlg:justify-end" : "dlg:items-center dlg:p-6")}>
      <div className={cx(closing ? "scrim-out" : "scrim-in", "scrim absolute inset-0 backdrop-blur-[2px]")} onClick={closing ? undefined : onClose} aria-hidden="true" />
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={description ? descId : undefined}
        tabIndex={-1}
        className={cx(
          drawer ? (closing ? "drawer-out pointer-events-none" : "drawer-in") : closing ? "modal-out pointer-events-none" : "modal-in",
          "relative flex max-h-[92dvh] w-full flex-col overscroll-contain rounded-t-2xl bg-surface shadow-pop focus:outline-none",
          // On phones a drawer scrolls as one piece, header included, so short screens keep room for details.
          drawer && "overflow-y-auto dlg:overflow-visible",
          drawer ? "dlg:h-dvh dlg:max-h-none dlg:max-w-[480px] dlg:rounded-none dlg:rounded-l-2xl" : cx("dlg:rounded-2xl", wide ? "dlg:max-w-2xl" : "dlg:max-w-lg"),
        )}
      >
        {/* Grab handle: drag down to close on phones. */}
        <div
          className="flex shrink-0 cursor-grab touch-none justify-center pb-1 pt-2 dlg:hidden"
          onPointerDown={onDragStart}
          onPointerMove={onDragMove}
          onPointerUp={onDragEnd}
          onPointerCancel={onDragEnd}
          aria-hidden="true"
        >
          <span className="h-1 w-10 rounded-full bg-line-strong" />
        </div>
        <div
          className="flex shrink-0 items-start justify-between gap-4 border-b border-line px-5 pb-4 pt-2 dlg:pt-4"
          onPointerDown={onDragStart}
          onPointerMove={onDragMove}
          onPointerUp={onDragEnd}
          onPointerCancel={onDragEnd}
        >
          <div className="min-w-0 flex-1">
            {header ? (
              <>
                <h2 id={titleId} className="sr-only">
                  {title}
                </h2>
                {header}
              </>
            ) : (
              <>
                <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                  <h2 id={titleId} className="min-w-0 break-words text-[15px] font-semibold text-ink">
                    {title}
                  </h2>
                  {headerAction}
                </div>
                {description && (
                  <p id={descId} className="mt-0.5 break-words text-[13px] text-muted">
                    {description}
                  </p>
                )}
              </>
            )}
          </div>
          <button
            type="button"
            onClick={onClose}
            onPointerDown={(e) => e.stopPropagation()}
            aria-label="Close"
            className="icon-btn -mr-1.5 -mt-0.5 inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-muted hover:bg-ink/[0.06] hover:text-ink"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        <div
          className={cx(
            "px-5 py-4",
            drawer ? "dlg:min-h-0 dlg:flex-1 dlg:overflow-y-auto dlg:overscroll-contain" : "min-h-0 flex-1 overflow-y-auto overscroll-contain",
            !footer && "pb-[max(1rem,env(safe-area-inset-bottom))]",
          )}
        >
          {children}
        </div>
        {footer && (
          <div className={cx("flex shrink-0 flex-wrap justify-end gap-2 border-t border-line bg-surface px-5 py-3 pb-[max(0.75rem,env(safe-area-inset-bottom))]", drawer && "sticky bottom-0 dlg:static")}>
            {footer}
          </div>
        )}
      </div>
    </div>,
    document.body,
  );
}
