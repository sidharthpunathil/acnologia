import { RiCheckLine } from "@remixicon/react";
import { ToggleButton } from "react-aria-components";
import { cx } from "@/utils/cx";

export interface Option<T extends string | number> {
  value: T;
  label: string;
  meta?: string;
}

/**
 * Chip style multi select: each option is a toggle (Space or Enter flips it,
 * aria-pressed tells assistive tech). Good enough for tens of options, scrolls past that.
 */
export function MultiSelect<T extends string | number>({
  options,
  value,
  onChange,
  empty = "Nothing to pick from yet.",
  disabled,
}: {
  options: Option<T>[];
  value: T[];
  onChange: (v: T[]) => void;
  empty?: string;
  disabled?: boolean;
}) {
  if (!options.length)
    return (
      <div className="rounded-2lg border border-dashed border-border-button-hover px-3 py-3 text-caption-1-regular text-text-secondary">
        {empty}
      </div>
    );
  const set = new Set(value);
  return (
    <div className="flex flex-wrap gap-1.5 rounded-2lg bg-background-tertiary-default p-2 sm:max-h-44 sm:overflow-y-auto">
      {options.map((o) => {
        const on = set.has(o.value);
        return (
          <ToggleButton
            key={String(o.value)}
            isSelected={on}
            isDisabled={disabled}
            onChange={(next) => onChange(next ? [...value, o.value] : value.filter((v) => v !== o.value))}
            className={cx(
              "inline-flex min-h-7 cursor-pointer items-center gap-1 rounded-full border px-2.5 py-1 text-caption-1-medium outline-none",
              "transition-colors duration-150 ease-out focus-visible:ring-2 focus-visible:ring-border-focus-ring",
              "disabled:cursor-not-allowed disabled:opacity-60",
              on
                ? "border-accent-line bg-accent-subtle text-accent-fg"
                : "border-border-button-default bg-background-primary-default text-text-primary hover:border-border-button-hover",
            )}
          >
            {on && <RiCheckLine className="size-3.5" aria-hidden />}
            {o.label}
            {o.meta && <span className={cx("font-mono text-caption-2-regular", on ? "text-accent-fg" : "text-text-tertiary")}>{o.meta}</span>}
          </ToggleButton>
        );
      })}
    </div>
  );
}
