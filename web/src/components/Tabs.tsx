import { useRef, type KeyboardEvent } from "react";
import { cx } from "./ui";

export interface TabItem<T extends string> {
  id: T;
  label: string;
  count?: number;
}

/** treg style category tabs: label, small count, underline on the active one. Arrow keys move between tabs. */
export function Tabs<T extends string>({
  items,
  value,
  onChange,
  className,
  label,
}: {
  items: TabItem<T>[];
  value: T;
  onChange: (v: T) => void;
  className?: string;
  label?: string;
}) {
  const refs = useRef<Array<HTMLButtonElement | null>>([]);
  const onKey = (e: KeyboardEvent, i: number) => {
    let j = -1;
    if (e.key === "ArrowRight") j = (i + 1) % items.length;
    else if (e.key === "ArrowLeft") j = (i - 1 + items.length) % items.length;
    else if (e.key === "Home") j = 0;
    else if (e.key === "End") j = items.length - 1;
    if (j < 0) return;
    e.preventDefault();
    onChange(items[j].id);
    refs.current[j]?.focus();
  };
  return (
    <div className={cx("no-scrollbar -mx-4 overflow-x-auto px-4 sm:mx-0 sm:px-0", className)}>
      <div role="tablist" aria-label={label} className="flex min-w-max gap-1 border-b border-line">
        {items.map((it, i) => {
          const active = it.id === value;
          return (
            <button
              key={it.id}
              ref={(el) => (refs.current[i] = el)}
              role="tab"
              type="button"
              aria-selected={active}
              tabIndex={active ? 0 : -1}
              onClick={() => onChange(it.id)}
              onKeyDown={(e) => onKey(e, i)}
              className={cx(
                "tap relative -mb-px whitespace-nowrap px-2.5 pb-3 pt-1 text-[13px] transition-colors",
                active ? "font-medium text-ink" : "text-muted hover:text-ink",
              )}
            >
              {it.label}
              {it.count !== undefined && <span className="ml-1.5 font-mono text-[11px] text-faint">{it.count}</span>}
              {active && <span className="absolute inset-x-0 bottom-0 h-[2px] rounded-full bg-ink" />}
            </button>
          );
        })}
      </div>
    </div>
  );
}
