import type { Key } from "../lib/types";
import { countdown } from "../lib/format";
import { cx } from "./ui";

const styles: Record<string, { dot: string; pill: string; label: string }> = {
  active: { dot: "bg-emerald-500", pill: "bg-emerald-50 text-emerald-700", label: "available" },
  busy: { dot: "bg-violet-500", pill: "bg-violet-50 text-violet-700", label: "busy" },
  cooling: { dot: "bg-amber-500", pill: "bg-amber-50 text-amber-700", label: "cooling" },
  exhausted: { dot: "bg-orange-500", pill: "bg-orange-50 text-orange-700", label: "out of quota" },
  invalid: { dot: "bg-red-500", pill: "bg-red-50 text-red-700", label: "invalid" },
  banned: { dot: "bg-red-500", pill: "bg-red-50 text-red-700", label: "banned" },
  disabled: { dot: "bg-stone-400", pill: "bg-well text-muted", label: "disabled" },
};

/** True when the key is serving as many requests as its limit allows. */
export function isBusy(k: Pick<Key, "in_flight" | "max_concurrency">, serviceMax: number): boolean {
  const limit = k.max_concurrency ?? serviceMax;
  return limit > 0 && (k.in_flight ?? 0) >= limit;
}

/**
 * The key's live state. An available key at its concurrency limit reads
 * "busy", so the pill never says available and busy at once.
 */
export function StatusPill({ k, now, serviceMax = 0 }: { k: Key; now: number; serviceMax?: number }) {
  const state = k.effective_status === "active" && isBusy(k, serviceMax) ? "busy" : k.effective_status;
  const s = styles[state] ?? styles.disabled;
  let extra = "";
  if (k.effective_status === "cooling") extra = countdown(k.cooldown_until, now);
  if (k.effective_status === "exhausted" && k.quota_reset_at) extra = countdown(k.quota_reset_at, now);
  return (
    <span className={cx("inline-flex max-w-full flex-wrap items-center gap-x-1.5 rounded-full px-2 py-0.5 font-mono text-[11px]", s.pill)}>
      <span className={cx("h-1.5 w-1.5 shrink-0 rounded-full", s.dot)} aria-hidden="true" />
      <span className="whitespace-nowrap">{s.label}</span>
      {extra && <span className="whitespace-nowrap tabular-nums">{extra}</span>}
    </span>
  );
}
