import { useEffect, useRef, useSyncExternalStore, type ReactNode } from "react";
import { Modal as BoardModal, ModalBody, ModalFooter, ModalHeader } from "@/components/base/modal/modal";
import { Drawer } from "@/components/base/drawer/drawer";
import { cx } from "@/utils/cx";

/** Phone layout, portrait or landscape: dialogs and drawers become bottom sheets. Mirrors the `dlg` variant. */
export const SHEET_QUERY = "(max-width: 639px), (pointer: coarse) and (max-height: 500px)";

function subscribeSheet(cb: () => void) {
  const mq = window.matchMedia(SHEET_QUERY);
  mq.addEventListener("change", cb);
  return () => mq.removeEventListener("change", cb);
}

/** True on phones, where overlays open as bottom sheets. */
export function useSheetLayout() {
  return useSyncExternalStore(
    subscribeSheet,
    () => window.matchMedia(SHEET_QUERY).matches,
    () => false,
  );
}

/** Moves focus to `[data-autofocus]` or, on wide screens, the first field, once the panel is open. */
function AutoFocus({ phone }: { phone: boolean }) {
  const probe = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    const t = window.setTimeout(() => {
      const dialog = probe.current?.closest<HTMLElement>("[role=dialog],[role=alertdialog]");
      if (!dialog) return;
      const field = phone
        ? dialog.querySelector<HTMLElement>("[data-autofocus]")
        : dialog.querySelector<HTMLElement>("[data-autofocus], input:not([type=hidden]):not([disabled]), textarea:not([disabled]), select:not([disabled])");
      if (field) field.focus({ preventScroll: true });
      // Nothing to type into: still move focus into the dialog, so Escape and Tab work at once.
      else if (!dialog.contains(document.activeElement)) {
        if (!dialog.hasAttribute("tabindex")) dialog.setAttribute("tabindex", "-1");
        dialog.focus({ preventScroll: true });
      }
    }, 30);
    return () => window.clearTimeout(t);
  }, [phone]);
  return <span ref={probe} hidden />;
}

/**
 * Legacy dialog API over BoardUI Modal and Drawer. Escape, a press on the
 * backdrop and the close button dismiss it; focus is trapped and returns to
 * the opener. `variant="drawer"` is a side panel on wide screens. On phones
 * both variants open as bottom sheets.
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
  /** Small control under the title, such as "Fill with AI". */
  headerAction?: ReactNode;
  variant?: "dialog" | "drawer";
  /** Replaces the default title block; `title` is still the accessible name. */
  header?: ReactNode;
}) {
  const phone = useSheetLayout();
  // Keep showing the last content while the exit animation plays, so the panel does not blank.
  const last = useRef({ title, description, children, footer, headerAction, header });
  if (open) last.current = { title, description, children, footer, headerAction, header };
  const c = last.current;

  const onOpenChange = (o: boolean) => {
    if (!o) onClose();
  };

  const content = (
    <>
      {open && <AutoFocus phone={phone} />}
      {c.header ? (
        <ModalHeader title={<span className="sr-only">{c.title}</span>}>{c.header}</ModalHeader>
      ) : (
        <ModalHeader title={<span className="break-words">{c.title}</span>} description={c.description}>
          {c.headerAction ? <div className="mt-2 flex flex-wrap gap-2">{c.headerAction}</div> : null}
        </ModalHeader>
      )}
      <ModalBody className={cx("text-text-primary", !c.footer && "pb-2")}>{c.children}</ModalBody>
      {c.footer ? <ModalFooter className="flex-wrap">{c.footer}</ModalFooter> : null}
    </>
  );

  if (phone) {
    return (
      <Drawer placement="bottom" isOpen={open} onOpenChange={onOpenChange} className="pb-[max(1.5rem,env(safe-area-inset-bottom))]">
        {content}
      </Drawer>
    );
  }
  if (variant === "drawer") {
    return (
      <Drawer placement="end" size="md" isOpen={open} onOpenChange={onOpenChange}>
        {content}
      </Drawer>
    );
  }
  return (
    <BoardModal size={wide ? "lg" : "md"} isOpen={open} onOpenChange={onOpenChange}>
      {content}
    </BoardModal>
  );
}
