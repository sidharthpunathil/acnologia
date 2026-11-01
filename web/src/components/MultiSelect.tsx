import { Check } from "lucide-react";
import { cx } from "./ui";

export interface Option<T extends string | number> {
  value: T;
  label: string;
  meta?: string;
}

/** Chip style multi select. Good enough for tens of options, scrolls past that. */
export function MultiSelect<T extends string | number>({
  options,
  value,
  onChange,
  empty = "Nothing to choose from yet.",
  disabled,
}: {
  options: Option<T>[];
  value: T[];
  onChange: (v: T[]) => void;
  empty?: string;
  disabled?: boolean;
}) {
  if (!options.length) return <div className="rounded-xl border border-dashed border-line px-3 py-3 text-xs text-muted">{empty}</div>;
  const set = new Set(value);
  return (
    <div className="flex max-h-44 flex-wrap gap-1.5 overflow-y-auto rounded-xl border border-line bg-white p-2">
      {options.map((o) => {
        const on = set.has(o.value);
        return (
          <button
            key={String(o.value)}
            type="button"
            disabled={disabled}
            onClick={() => onChange(on ? value.filter((v) => v !== o.value) : [...value, o.value])}
            className={cx(
              "inline-flex items-center gap-1 rounded-full border px-2.5 py-1 text-xs transition-colors disabled:cursor-not-allowed disabled:opacity-60",
              on ? "border-ink bg-ink text-white" : "border-line bg-white text-ink hover:border-ink/30",
            )}
          >
            {on && <Check className="h-3 w-3" />}
            {o.label}
            {o.meta && <span className={cx("font-mono text-[10px]", on ? "text-white/60" : "text-faint")}>{o.meta}</span>}
          </button>
        );
      })}
    </div>
  );
}
