import { useState } from "react";
import { CheckCircle2, CircleHelp, XCircle } from "lucide-react";
import type { DayActivity, Key, Provider, Validity } from "../lib/types";
import { dateTime, num, plural, relTime } from "../lib/format";
import { cx } from "./ui";

const VALIDITY: Record<Validity, { label: string; icon: typeof CheckCircle2; cls: string }> = {
  valid: { label: "valid", icon: CheckCircle2, cls: "bg-emerald-50 text-emerald-700" },
  invalid: { label: "invalid", icon: XCircle, cls: "bg-red-50 text-red-700" },
  unknown: { label: "unknown", icon: CircleHelp, cls: "bg-well text-muted" },
};

/** Valid, invalid or unknown, always with an icon and a word, never color alone. */
export function ValidityBadge({ validity, reason, className }: { validity: Validity | undefined; reason?: string; className?: string }) {
  const v = VALIDITY[validity ?? "unknown"] ?? VALIDITY.unknown;
  const I = v.icon;
  return (
    <span
      title={reason || undefined}
      className={cx("inline-flex items-center gap-1 whitespace-nowrap rounded-full px-2 py-0.5 font-mono text-[11px]", v.cls, className)}
    >
      <I className="h-3 w-3 shrink-0" aria-hidden="true" />
      {v.label}
      {reason && <span className="sr-only">: {reason}</span>}
    </span>
  );
}

/**
 * Why the key is valid, invalid or unknown, built from live fields so it keeps
 * ticking ("Last worked 18s ago"). The server text is used only for invalid keys.
 */
export function validityText(k: Pick<Key, "validity" | "validity_reason" | "last_ok_at" | "last_error">, now: number): string {
  if (k.validity === "invalid") return k.last_error || k.validity_reason || "The provider rejected this key";
  if (k.last_ok_at) {
    const r = relTime(k.last_ok_at, now);
    return r === "just now" ? "Worked just now" : `Last worked ${r}`;
  }
  return "Not checked or used yet";
}

/**
 * Whether a provider reports real quota reset times. Undefined when the provider
 * is not loaded yet, so callers can hedge.
 */
export function providerGivesReset(p: Pick<Provider, "spec"> | undefined): boolean | undefined {
  if (!p) return undefined;
  return !!p.spec?.health?.quota?.reset_at;
}

/** Relative time with the exact time on hover and for screen readers. */
export function TimeAgo({ iso, now, never = "never", className }: { iso: string | null | undefined; now: number; never?: string; className?: string }) {
  if (!iso) return <span className={className}>{never}</span>;
  return (
    <time dateTime={iso} title={dateTime(iso)} className={className}>
      {relTime(iso, now)}
    </time>
  );
}

export interface QuotaInfo {
  text: string;
  /** 0..1 when both numbers are known. */
  frac: number | null;
  low: boolean;
}

/** "1,240 / 5,000 searches", or what can be said when part of it is unknown. */
export function quotaInfo(k: Pick<Key, "quota_limit" | "quota_remaining">, unit?: string): QuotaInfo | null {
  const u = unit?.trim() ? ` ${unit.trim()}` : "";
  const { quota_limit: limit, quota_remaining: rem } = k;
  if (limit === null && rem === null) return null;
  if (limit !== null && rem !== null && limit > 0) {
    const frac = Math.max(0, Math.min(1, rem / limit));
    return { text: `${num(rem)} / ${num(limit)}${u}`, frac, low: rem < 0.1 * limit };
  }
  if (rem !== null) return { text: `${num(rem)}${u} left`, frac: null, low: rem <= 0 };
  return { text: `${num(limit)}${u} limit, remaining unknown`, frac: null, low: false };
}

/** Quota numbers, a bar and the reset time. The words carry the meaning; the bar only adds a glance. */
export function QuotaMeter({
  k,
  unit,
  now,
  compact,
  className,
  resetExact,
}: {
  k: Pick<Key, "quota_limit" | "quota_remaining" | "quota_reset_at"> & Partial<Pick<Key, "effective_status" | "status">>;
  unit?: string;
  now: number;
  compact?: boolean;
  className?: string;
  /** False when the provider does not report reset times, so an exhausted key's reset is Cogito's estimate. */
  resetExact?: boolean;
}) {
  const q = quotaInfo(k, unit);
  if (!q) return <span className={cx("font-mono text-xs text-faint", className)}>{compact ? "-" : "No quota data yet"}</span>;
  const pct = q.frac === null ? null : q.frac * 100;
  const bar = pct === null ? null : pct > 40 ? "bg-emerald-500" : pct > 10 ? "bg-amber-500" : "bg-red-500";
  const reset = k.quota_reset_at ? Date.parse(k.quota_reset_at) : NaN;
  const exhausted = k.effective_status === "exhausted" || k.status === "exhausted";
  return (
    <div className={cx("min-w-0", className)}>
      <div className="flex flex-wrap items-baseline gap-x-1.5 font-mono text-[12px] tabular-nums text-ink">
        <span>{q.text}</span>
        {q.low && <span className="text-[11px] text-red-700">low</span>}
      </div>
      {pct !== null && (
        <div
          className="mt-1 h-1.5 w-full max-w-[160px] overflow-hidden rounded-full bg-well"
          role="meter"
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={Math.round(pct)}
          aria-label="Quota left"
        >
          <div className={cx("h-full rounded-full", bar)} style={{ width: `${pct}%` }} />
        </div>
      )}
      {!Number.isNaN(reset) && (
        <div className="mt-1 font-mono text-[11px] text-muted" title={dateTime(k.quota_reset_at)}>
          {reset <= now
            ? "reset due now"
            : exhausted && resetExact !== true
              ? `resets ${relTime(k.quota_reset_at, now).replace(/^in /, "in about ")}${resetExact === false ? " (estimate)" : ""}`
              : `resets ${relTime(k.quota_reset_at, now)}`}
        </div>
      )}
    </div>
  );
}

const dayLabel = (day: string, long = false) =>
  new Date(`${day}T00:00:00Z`).toLocaleDateString(undefined, { month: "short", day: "numeric", weekday: long ? "short" : undefined, timeZone: "UTC" });

function daySentence(d: DayActivity): string {
  return `${dayLabel(d.day, true)}: ${plural(d.requests, "request")}, ${plural(d.errors, "error")}, ${num(d.rate_limited)} rate limited`;
}

/**
 * 14 days of requests, errors and rate limits as stacked bars. A sentence sums
 * it up for everyone; each bar has its own label, and tapping or focusing a
 * bar shows that day's numbers.
 */
export function ActivityChart({ daily }: { daily: DayActivity[] }) {
  // `pinned` is the day picked by a tap or click; `sel` also follows a mouse hover.
  const [sel, setSel] = useState<number | null>(null);
  const [pinned, setPinned] = useState<number | null>(null);
  const totals = daily.reduce((a, d) => ({ r: a.r + d.requests, e: a.e + d.errors, l: a.l + d.rate_limited }), { r: 0, e: 0, l: 0 });
  const max = Math.max(1, ...daily.map((d) => d.requests + d.errors + d.rate_limited));
  const busiest = daily.reduce<DayActivity | null>((b, d) => (!b || d.requests > b.requests ? d : b), null);
  const empty = totals.r + totals.e + totals.l === 0;
  const summary = empty
    ? `No activity in the last ${daily.length} days.`
    : `${plural(totals.r, "request")} in ${daily.length} days${busiest && busiest.requests > 0 ? `, busiest on ${dayLabel(busiest.day)} with ${num(busiest.requests)}` : ""}. ${plural(totals.e, "error")}, ${num(totals.l)} rate limited.`;
  const picked = sel !== null ? daily[sel] : null;

  return (
    <figure>
      <figcaption className="text-[13px] leading-relaxed text-muted">{summary}</figcaption>
      {!empty && (
        <>
          <ol className="mt-3 flex h-24 items-end gap-[3px]" aria-label="Activity per day" onPointerLeave={(e) => e.pointerType === "mouse" && setSel(pinned)}>
            {daily.map((d, i) => {
              const total = d.requests + d.errors + d.rate_limited;
              const h = (n: number) => `${(n / max) * 100}%`;
              const label = daySentence(d);
              return (
                <li key={d.day} className="flex h-full min-w-0 flex-1">
                  <button
                    type="button"
                    title={label}
                    aria-label={label}
                    aria-pressed={pinned === i}
                    // Hover previews with a mouse only; on touch the tap below decides.
                    onPointerEnter={(e) => e.pointerType === "mouse" && setSel(i)}
                    // Keyboard focus previews too, but not the focus a tap brings with it.
                    onFocus={(e) => e.currentTarget.matches(":focus-visible") && setSel(i)}
                    onClick={() => {
                      // First tap selects the day; a second tap on the same bar clears it.
                      const next = pinned === i ? null : i;
                      setPinned(next);
                      setSel(next);
                    }}
                    className={cx(
                      "flex h-full w-full flex-col-reverse justify-start overflow-hidden rounded-[3px] transition-colors",
                      sel === i ? "bg-ink/[0.08]" : "hover:bg-ink/[0.04]",
                    )}
                  >
                    {total === 0 ? (
                      <span className="block h-[2px] w-full bg-line-strong" />
                    ) : (
                      <>
                        <span className="block w-full bg-chart-ok" style={{ height: h(d.requests) }} />
                        <span className="block w-full bg-amber-500" style={{ height: h(d.rate_limited) }} />
                        <span className="block w-full bg-red-500" style={{ height: h(d.errors) }} />
                      </>
                    )}
                  </button>
                </li>
              );
            })}
          </ol>
          <div className="mt-1.5 flex justify-between font-mono text-[10px] text-faint" aria-hidden="true">
            <span>{daily[0] && dayLabel(daily[0].day)}</span>
            <span>{daily.length > 0 && dayLabel(daily[daily.length - 1].day)}</span>
          </div>
          <p className="mt-2 min-h-[1.25rem] font-mono text-[11px] text-ink" aria-live="polite">
            {picked ? daySentence(picked) : <span className="text-faint">Tap or hover a bar for that day.</span>}
          </p>
          <ul className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[12px] text-muted" aria-label="Legend">
            <li className="inline-flex items-center gap-1.5">
              <span className="h-2.5 w-2.5 rounded-sm bg-chart-ok" aria-hidden="true" /> Requests
            </li>
            <li className="inline-flex items-center gap-1.5">
              <span className="h-2.5 w-2.5 rounded-sm bg-amber-500" aria-hidden="true" /> Rate limited
            </li>
            <li className="inline-flex items-center gap-1.5">
              <span className="h-2.5 w-2.5 rounded-sm bg-red-500" aria-hidden="true" /> Errors
            </li>
          </ul>
        </>
      )}
    </figure>
  );
}
