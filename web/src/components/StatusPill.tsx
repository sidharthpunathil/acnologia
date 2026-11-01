import type { Key } from "../lib/types";
import { countdown } from "../lib/format";
import { cx } from "./ui";

const styles: Record<string, { dot: string; pill: string; label: string }> = {
  active: { dot: "bg-emerald-500", pill: "bg-emerald-50 text-emerald-700", label: "available" },
  cooling: { dot: "bg-amber-500", pill: "bg-amber-50 text-amber-700", label: "cooling" },
  exhausted: { dot: "bg-orange-500", pill: "bg-orange-50 text-orange-700", label: "out of quota" },
  invalid: { dot: "bg-red-500", pill: "bg-red-50 text-red-700", label: "invalid" },
  disabled: { dot: "bg-stone-400", pill: "bg-well text-muted", label: "disabled" },
};

export function StatusPill({ k, now }: { k: Key; now: number }) {
  const s = styles[k.effective_status] ?? styles.disabled;
  let extra = "";
  if (k.effective_status === "cooling") extra = countdown(k.cooldown_until, now);
  if (k.effective_status === "exhausted" && k.quota_reset_at) extra = countdown(k.quota_reset_at, now);
  return (
    <span className={cx("inline-flex items-center gap-1.5 whitespace-nowrap rounded-full px-2 py-0.5 font-mono text-[11px]", s.pill)}>
      <span className={cx("h-1.5 w-1.5 rounded-full", s.dot)} />
      {s.label}
      {extra && <span className="tabular-nums opacity-80">{extra}</span>}
    </span>
  );
}
