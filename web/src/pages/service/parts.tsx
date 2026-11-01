import type { ComponentProps, ComponentType, ReactNode } from "react";
import { Focusable } from "react-aria-components";
import { IconButton } from "@/components/base/buttons/icon-button";
import { Tooltip, TooltipTrigger } from "@/components/base/tooltip/tooltip";
import { ButtonSpinner } from "@/components/base/spinner/spinner";

/*
 * Small area B helpers built from BoardUI parts. Page headers, callouts,
 * empty states, stat tiles and confirms come from the shared acn/ folder.
 */

type IconComponent = ComponentType<{ className?: string; "aria-hidden"?: boolean | "true" | "false" }>;

/** The card surface every panel on these pages shares. */
export const CARD = "rounded-3xl border border-border-button-default bg-background-primary-default";

/**
 * A tooltip on a BoardUI control. Plain button components go through
 * Focusable; React Aria controls (`aria`) already speak to the trigger.
 */
export function Tip({
  label,
  children,
  placement = "top",
  aria,
}: {
  label: ReactNode;
  children: ComponentProps<typeof Focusable>["children"];
  placement?: "top" | "bottom";
  aria?: boolean;
}) {
  return (
    <TooltipTrigger delay={300}>
      {aria ? children : <Focusable>{children}</Focusable>}
      <Tooltip placement={placement}>{label}</Tooltip>
    </TooltipTrigger>
  );
}

/** An icon only action with its name as a tooltip and as the accessible name. */
export function ActionIcon({
  icon,
  label,
  onClick,
  disabled,
  busy,
  size = "small",
  className,
}: {
  icon: IconComponent;
  label: string;
  onClick: () => void;
  disabled?: boolean;
  busy?: boolean;
  size?: "small" | "medium";
  className?: string;
}) {
  return (
    <Tip label={label}>
      <IconButton
        icon={busy ? ButtonSpinner : icon}
        aria-label={label}
        aria-busy={busy || undefined}
        size={size}
        disabled={disabled}
        onClick={onClick}
        className={className}
      />
    </Tip>
  );
}
