import type { ReactNode } from "react";
import { cx } from "@/utils/cx";
import type { Illustration } from "@/components/foundations/illustrations/frame";
import { IconSlot, type IconLike } from "./icon";

export interface EmptyStateProps {
  /** A Remix icon ref (preferred) or an element. */
  icon?: IconLike;
  /** A spot illustration ref from foundations/illustrations. Shown above the title in place of the icon. */
  art?: Illustration;
  title: ReactNode;
  body?: ReactNode;
  action?: ReactNode;
  /** Dashed outline, for an empty region inside a card or list. */
  dashed?: boolean;
  /** Smaller padding, icon and type for narrow panels. */
  compact?: boolean;
  /** Heading level for the title. Defaults to a plain block (no heading). */
  as?: "h1" | "h2" | "h3" | "div";
  className?: string;
}

/** Illustration or icon tile, title, a line of help and an action slot. */
export function EmptyState({ icon, art: Art, title, body, action, dashed = true, compact, as: Title = "div", className }: EmptyStateProps) {
  return (
    <div
      className={cx(
        "flex flex-col items-center text-center",
        // The faint icon grey: 3:1 on the light canvas (line-strong was 1.2:1), 2.5:1 on the dark one.
        dashed ? "border border-dashed border-foreground-icon-tertiary" : "border border-border-button-default bg-background-primary-default",
        compact ? "rounded-2xl px-3 py-5" : "rounded-3xl px-6 py-12",
        className,
      )}
    >
      {Art ? (
        <Art className={compact ? "mb-2 h-[60px] w-[75px]" : "mb-4 h-24 w-[120px]"} />
      ) : (
        icon != null &&
        icon !== false && (
          <div
            className={cx(
              "flex items-center justify-center border border-border-button-default bg-background-primary-default text-foreground-icon-secondary shadow-xs",
              compact ? "mb-2 size-8 rounded-lg" : "mb-4 size-11 rounded-xl",
            )}
          >
            <IconSlot icon={icon} className={compact ? "size-4" : "size-5"} />
          </div>
        )
      )}
      <Title className={cx("text-text-primary", compact ? "text-body-2-semibold" : "text-headline-semibold")}>{title}</Title>
      {body && (
        <div className={cx("mt-1 max-w-md text-text-secondary", compact ? "text-caption-1-regular" : "text-body-regular")}>{body}</div>
      )}
      {action && <div className={cx("flex flex-wrap justify-center gap-2", compact ? "mt-3" : "mt-5")}>{action}</div>}
    </div>
  );
}
