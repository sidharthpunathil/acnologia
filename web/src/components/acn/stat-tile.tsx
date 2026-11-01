import type { ReactNode } from "react";
import { ToggleButton } from "react-aria-components";
import { cx } from "@/utils/cx";

export type StatTone = "neutral" | "success" | "warning" | "error" | "accent";

const VALUE_TONE: Record<StatTone, string> = {
  neutral: "text-text-primary",
  success: "text-status-lime-text",
  warning: "text-status-yellow-text",
  error: "text-status-rose-text",
  accent: "text-accent-fg",
};

export interface StatTileProps {
  label: ReactNode;
  value: ReactNode;
  /** Small line under the value. */
  sub?: ReactNode;
  tone?: StatTone;
  className?: string;
  /** Makes the tile a toggle (filters). Pair with `isSelected` and `onChange`. */
  isSelected?: boolean;
  onChange?: (selected: boolean) => void;
  /** Id when used inside a RAC ToggleButtonGroup. */
  id?: string;
  isDisabled?: boolean;
}

function Body({ label, value, sub, tone = "neutral" }: Pick<StatTileProps, "label" | "value" | "sub" | "tone">) {
  return (
    <>
      <div className="truncate font-mono text-caption-2-semibold uppercase text-text-tertiary">{label}</div>
      <div className={cx("mt-1 font-mono text-title-2-medium tabular-nums", VALUE_TONE[tone])}>{value}</div>
      {sub && (
        // Wraps to two lines on narrow tiles rather than cutting the hint off.
        <div className="mt-0.5 line-clamp-2 font-mono text-caption-1-regular break-words text-text-tertiary" title={typeof sub === "string" ? sub : undefined}>
          {sub}
        </div>
      )}
    </>
  );
}

const TILE = "min-w-0 rounded-3xl border border-border-button-default bg-background-primary-default px-4 py-3 text-start";

/** One number with a caption. Pass `onChange` to make it a pressable filter. */
export function StatTile({ className, isSelected, onChange, id, isDisabled, ...body }: StatTileProps) {
  if (!onChange && isSelected === undefined) {
    return (
      <div className={cx(TILE, className)}>
        <Body {...body} />
      </div>
    );
  }
  return (
    <ToggleButton
      id={id}
      isSelected={isSelected}
      onChange={onChange}
      isDisabled={isDisabled}
      className={({ isSelected: on }) =>
        cx(
          TILE,
          "cursor-pointer outline-none transition-[background-color,border-color] duration-150 ease-out",
          "hover:border-border-button-hover focus-visible:ring-2 focus-visible:ring-border-focus-ring",
          on && "border-accent-line bg-accent-subtle",
          className,
        )
      }
    >
      <Body {...body} />
    </ToggleButton>
  );
}
