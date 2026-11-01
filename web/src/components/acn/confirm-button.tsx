import { useRef, useState, type ReactNode } from "react";
import { Focusable } from "react-aria-components";
import { RiDeleteBinLine } from "@remixicon/react";
import { Button } from "@/components/base/buttons/button";
import { Popover, PopoverFooter, PopoverHeader } from "@/components/base/popover/popover";
import { Tooltip, TooltipTrigger } from "@/components/base/tooltip/tooltip";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { cx } from "@/utils/cx";
import { IconSlot, isIconComponent, type IconLike } from "./icon";

export interface ConfirmButtonProps {
  onConfirm: () => Promise<void> | void;
  /** Trigger label, and the accessible name of the icon only trigger. */
  label?: string;
  /** The danger button inside the popover. */
  confirmLabel?: string;
  /** Short question or consequence shown in the popover. */
  description?: ReactNode;
  iconOnly?: boolean;
  /** Trigger icon; a Remix component ref. Defaults to a bin. */
  icon?: IconLike;
  /** `xs` for dense table rows; `medium` to match a 36px IconButton. */
  size?: "xs" | "small" | "medium";
  /**
   * `ghost` (default) is a quiet text or icon trigger. `outline` matches
   * IconButton's bordered look, for rows of outlined icon buttons.
   */
  variant?: "ghost" | "outline";
  className?: string;
}

/**
 * A destructive action behind a small confirm popover. Focus lands on
 * Cancel, Escape or a press outside closes it and focus returns to the
 * trigger. For deleting a whole page's subject use AlertDialog instead.
 */
export function ConfirmButton({
  onConfirm,
  label = "Delete",
  confirmLabel = "Confirm delete",
  description,
  iconOnly,
  icon = RiDeleteBinLine,
  size = "small",
  variant = "ghost",
  className,
}: ConfirmButtonProps) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const Lead = isIconComponent(icon) ? icon : undefined;

  const run = async () => {
    setBusy(true);
    try {
      await onConfirm();
    } finally {
      setBusy(false);
      setOpen(false);
    }
  };

  const triggerRef = useRef<HTMLButtonElement>(null);
  const toggle = (o: boolean) => {
    if (!busy) setOpen(o);
  };

  const trigger = (
    <Button
      ref={triggerRef}
      variant={variant === "outline" ? "secondary" : "ghost"}
      size={size}
      iconOnly={iconOnly}
      aria-label={iconOnly ? label : undefined}
      aria-haspopup="dialog"
      aria-expanded={open}
      leadingIcon={Lead}
      onClick={() => toggle(!open)}
      className={cx(
        "hover:text-text-error-primary",
        variant === "outline" && "rounded-2lg text-foreground-icon-primary",
        variant === "outline" && iconOnly && size === "medium" && "size-9 p-0",
        // A 44px target on touch screens.
        iconOnly && "pointer-coarse:size-11",
        !iconOnly && "pointer-coarse:h-11",
        className,
      )}
    >
      {Lead ? label : (
        <span className="inline-flex items-center gap-1.5">
          <IconSlot icon={icon} />
          {!iconOnly && label}
        </span>
      )}
    </Button>
  );

  return (
    <>
      {iconOnly ? (
        <TooltipTrigger delay={400}>
          <Focusable>{trigger}</Focusable>
          <Tooltip>{label}</Tooltip>
        </TooltipTrigger>
      ) : (
        trigger
      )}
      <Popover triggerRef={triggerRef} isOpen={open} onOpenChange={toggle} placement="bottom end" className="w-72" aria-label={label}>
        <PopoverHeader title={`${label}?`} description={description ?? "This cannot be undone."} />
        <PopoverFooter>
          <Button variant="secondary" size="small" autoFocus onClick={() => setOpen(false)} disabled={busy}>
            Cancel
          </Button>
          <Button variant="danger" size="small" onClick={() => void run()} disabled={busy} leadingIcon={busy ? ButtonSpinner : undefined}>
            {confirmLabel}
          </Button>
        </PopoverFooter>
      </Popover>
    </>
  );
}
