import { cx } from "./ui";

export interface TabItem<T extends string> {
  id: T;
  label: string;
  count?: number;
}

/** treg style category tabs: label, small count, underline on the active one. */
export function Tabs<T extends string>({
  items,
  value,
  onChange,
  className,
}: {
  items: TabItem<T>[];
  value: T;
  onChange: (v: T) => void;
  className?: string;
}) {
  return (
    <div className={cx("no-scrollbar -mx-4 overflow-x-auto px-4 sm:mx-0 sm:px-0", className)}>
      <div role="tablist" className="flex min-w-max gap-1 border-b border-line">
        {items.map((it) => {
          const active = it.id === value;
          return (
            <button
              key={it.id}
              role="tab"
              type="button"
              aria-selected={active}
              onClick={() => onChange(it.id)}
              className={cx(
                "relative -mb-px whitespace-nowrap px-2.5 pb-3 pt-1 text-[13px] transition-colors",
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
