import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { X } from "lucide-react";
import { cx } from "./ui";

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

/**
 * Accessible dialog: Escape closes, focus is trapped inside while open and
 * returned to the element that opened it on close.
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
}) {
  const panel = useRef<HTMLDivElement>(null);
  // Stay mounted briefly after closing so the exit animation can play, and
  // keep showing the last content so the panel does not blank while leaving.
  const [mounted, setMounted] = useState(open);
  const last = useRef({ title, description, children, footer, headerAction });
  if (open) last.current = { title, description, children, footer, headerAction };
  useEffect(() => {
    if (open) {
      setMounted(true);
      return;
    }
    const t = window.setTimeout(() => setMounted(false), 150);
    return () => window.clearTimeout(t);
  }, [open]);
  const closing = !open && mounted;
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  const titleId = useId();
  const descId = useId();

  useEffect(() => {
    if (!open) return;
    const opener = document.activeElement as HTMLElement | null;
    const onKey = (e: KeyboardEvent) => {
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
      const last = items[items.length - 1];
      if (e.shiftKey && (document.activeElement === first || document.activeElement === panel.current)) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKey);
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    // Focus the first field, else the panel itself so screen readers announce the dialog.
    const t = window.setTimeout(() => {
      const field = panel.current?.querySelector<HTMLElement>("[data-autofocus], input:not([type=hidden]):not([disabled]), textarea, select");
      (field ?? panel.current)?.focus();
    }, 0);
    return () => {
      window.clearTimeout(t);
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = prev;
      if (opener && document.contains(opener)) opener.focus();
    };
  }, [open]);

  if (!open && !mounted) return null;
  ({ title, description, children, footer, headerAction } = last.current);

  return createPortal(
    <div className="fixed inset-0 z-50 flex items-end justify-center sm:items-center sm:p-6">
      <div className={cx(closing ? "scrim-out" : "scrim-in", "absolute inset-0 bg-[#18181a]/40 backdrop-blur-[2px]")} onClick={closing ? undefined : onClose} aria-hidden="true" />
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={description ? descId : undefined}
        tabIndex={-1}
        className={cx(
          closing ? "modal-out pointer-events-none" : "modal-in",
          "relative flex max-h-[92dvh] w-full flex-col rounded-t-2xl bg-white shadow-pop focus:outline-none sm:rounded-2xl",
          wide ? "sm:max-w-2xl" : "sm:max-w-lg",
        )}
      >
        <div className="flex items-start justify-between gap-4 border-b border-line px-5 py-4">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <h2 id={titleId} className="text-[15px] font-semibold text-ink">
                {title}
              </h2>
              {headerAction}
            </div>
            {description && (
              <p id={descId} className="mt-0.5 text-[13px] text-muted">
                {description}
              </p>
            )}
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label="Close dialog"
            className="icon-btn -mr-1.5 -mt-0.5 inline-flex h-8 w-8 items-center justify-center rounded-lg text-muted hover:bg-black/[0.05] hover:text-ink"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="overflow-y-auto overscroll-contain px-5 py-4">{children}</div>
        {footer && (
          <div className="flex flex-wrap justify-end gap-2 border-t border-line px-5 py-3 pb-[max(0.75rem,env(safe-area-inset-bottom))]">
            {footer}
          </div>
        )}
      </div>
    </div>,
    document.body,
  );
}
