import { useId, useMemo, useState } from "react";
import { RiCheckLine } from "@remixicon/react";
import { ToggleButton } from "react-aria-components";
import { SearchField } from "@/components/base/search-field/search-field";
import { ScrollFade, useScrollFade } from "@/components/foundations/scroll-fade/scroll-fade";
import { cx } from "@/utils/cx";

export interface Option<T extends string | number> {
  value: T;
  label: string;
  meta?: string;
}

/** Past this many options a search field appears above the chips. */
export const SEARCH_THRESHOLD = 8;

/** Case insensitive match on an option's label and meta (its slug). */
export function matchesQuery(o: { label: string; meta?: string }, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return o.label.toLowerCase().includes(q) || (o.meta ?? "").toLowerCase().includes(q);
}

/**
 * Chip style multi select: each option is a toggle (Space or Enter flips it,
 * aria-pressed tells assistive tech). Past SEARCH_THRESHOLD options a search
 * field filters the chips by label and meta. A selection hidden by the
 * search stays selected, and the summary under the field says so.
 */
export function MultiSelect<T extends string | number>({
  options,
  value,
  onChange,
  empty = "Nothing to pick from yet.",
  disabled,
  searchLabel = "options",
  searchable,
}: {
  options: Option<T>[];
  value: T[];
  onChange: (v: T[]) => void;
  empty?: string;
  disabled?: boolean;
  /** What the options are, for the search field's name: "Search services". */
  searchLabel?: string;
  /** Force the search field on or off; by default it shows past SEARCH_THRESHOLD options. */
  searchable?: boolean;
}) {
  const [query, setQuery] = useState("");
  const fade = useScrollFade();
  const statusId = useId();
  const showSearch = searchable ?? options.length > SEARCH_THRESHOLD;
  const q = showSearch ? query : "";
  const shown = useMemo(() => options.filter((o) => matchesQuery(o, q)), [options, q]);

  if (!options.length)
    return (
      <div className="rounded-2lg border border-dashed border-border-button-hover px-3 py-3 text-caption-1-regular text-text-secondary">
        {empty}
      </div>
    );
  const set = new Set(value);
  const shownSet = new Set(shown.map((o) => o.value));
  const hiddenSelected = value.filter((v) => !shownSet.has(v)).length;

  return (
    <div className="flex flex-col gap-2">
      {showSearch && (
        <div className="flex flex-col gap-1">
          <SearchField
            aria-label={`Search ${searchLabel}`}
            aria-describedby={statusId}
            placeholder={`Search ${searchLabel}`}
            value={query}
            onChange={setQuery}
            size="small"
            shape="rounded"
            isDisabled={disabled}
          />
          <p id={statusId} aria-live="polite" className="text-caption-1-regular text-text-secondary">
            {q.trim() ? `${shown.length} of ${options.length} match` : `${options.length} ${searchLabel}`}
            {value.length > 0 && ` · ${value.length} selected`}
            {hiddenSelected > 0 && q.trim() ? `, ${hiddenSelected} not shown` : ""}
          </p>
        </div>
      )}
      <div className="relative">
        <div
          onScroll={fade.onScroll}
          className="flex flex-wrap gap-1.5 rounded-2lg bg-background-tertiary-default p-2 sm:max-h-44 sm:overflow-y-auto"
        >
          {shown.length === 0 && (
            <p className="px-1 py-0.5 text-caption-1-regular text-text-secondary">Nothing matches "{q.trim()}".</p>
          )}
          {shown.map((o) => {
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
                  // Disabled, the selection is still the information: chosen chips
                  // stay at full strength and only the others fade.
                  "disabled:cursor-not-allowed",
                  !on && "disabled:opacity-60",
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
        <ScrollFade progress={fade.progress} surfaceClassName="from-background-tertiary-default" className="rounded-t-2lg" />
      </div>
    </div>
  );
}
