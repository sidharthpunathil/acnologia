import { useRef, useState, type ReactNode } from "react";
import { RiCheckboxCircleLine, RiCloseCircleLine, RiQuestionLine } from "@remixicon/react";
import type { DayActivity, Key, Provider, Validity } from "../lib/types";
import { compact as short, dateTime, num, plural, relTime } from "../lib/format";
import { Chip, type ChipColor } from "@/components/base/badges/chip";
import { Meter, type MeterTone } from "@/components/base/meter/meter";
import { cx } from "@/utils/cx";

const VALIDITY: Record<Validity, { label: string; icon: typeof RiCheckboxCircleLine; color: ChipColor; iconCls: string }> = {
  valid: { label: "valid", icon: RiCheckboxCircleLine, color: "lime", iconCls: "text-notification-success-foreground" },
  invalid: { label: "invalid", icon: RiCloseCircleLine, color: "rose", iconCls: "text-notification-error-foreground" },
  unknown: { label: "unknown", icon: RiQuestionLine, color: "neutral", iconCls: "text-foreground-icon-tertiary" },
};

/** Valid, invalid or unknown, always with an icon and a word, never color alone. */
export function ValidityBadge({ validity, reason, className }: { validity: Validity | undefined; reason?: string; className?: string }) {
  const v = VALIDITY[validity ?? "unknown"] ?? VALIDITY.unknown;
  const I = v.icon;
  return (
    <Chip variant="caption" color={v.color} title={reason || undefined} className={cx("gap-1 rounded-full px-2 py-0.5 font-mono", className)}>
      <I className="size-3 shrink-0" aria-hidden />
      {v.label}
      {reason && <span className="sr-only">: {reason}</span>}
    </Chip>
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
 * True when the validity badge would only repeat the status pill, as with an
 * invalid key that is also out of rotation for being invalid.
 */
export function validityRepeatsStatus(k: Pick<Key, "validity" | "effective_status">): boolean {
  return k.validity === "invalid" && (k.effective_status === "invalid" || k.effective_status === "banned");
}

/**
 * Whether the key works, as one quiet line: an icon for the verdict and the
 * reason as text. Lets a table carry validity without a second pill per row.
 */
export function ValidityLine({
  k,
  now,
  clamp = true,
  className,
}: {
  k: Pick<Key, "validity" | "validity_reason" | "last_ok_at" | "last_error">;
  now: number;
  /** Two lines at most, with the full text on hover. Off for cards that can grow. */
  clamp?: boolean;
  className?: string;
}) {
  const v = VALIDITY[k.validity ?? "unknown"] ?? VALIDITY.unknown;
  const I = v.icon;
  const text = validityText(k, now);
  return (
    <div className={cx("flex min-w-0 items-start gap-1.5 text-caption-1-regular text-text-secondary", className)} title={clamp ? text : undefined}>
      <I className={cx("mt-px size-3.5 shrink-0", v.iconCls)} aria-hidden />
      <span className={cx("min-w-0", clamp ? "line-clamp-2 break-words" : "wrap-anywhere")}>
        <span className="sr-only">{v.label}: </span>
        {text}
      </span>
    </div>
  );
}

/**
 * Replays a short entrance when `id` changes after the first render, so a pill
 * that flips from available to cooling catches the eye without the whole list
 * animating on load. Reduced motion turns it into a fade (see .fade-up).
 */
export function OnChange({ id, children, className }: { id: string; children: ReactNode; className?: string }) {
  const last = useRef(id);
  const changed = useRef(false);
  if (last.current !== id) {
    last.current = id;
    changed.current = true;
  }
  return (
    <span key={id} className={cx("inline-flex max-w-full", changed.current && "fade-up", className)}>
      {children}
    </span>
  );
}

/** The one account field worth showing in a list: email, else name, else phone, else the other identifier. */
export function accountLabel(k: Pick<Key, "account_name" | "account_email" | "account_phone" | "account_ref">): string {
  return (k.account_email || k.account_name || k.account_phone || k.account_ref || "").trim();
}

/** A small muted line under a key label naming the account it belongs to. Renders nothing when unknown. */
export function AccountLine({
  k,
  wrap,
  className,
}: {
  k: Pick<Key, "account_name" | "account_email" | "account_phone" | "account_ref">;
  /** Wrap long values instead of cutting them off, for cards on narrow screens. */
  wrap?: boolean;
  className?: string;
}) {
  const text = accountLabel(k);
  if (!text) return null;
  return (
    <div className={cx("text-caption-1-regular text-text-secondary", wrap ? "wrap-anywhere" : "truncate", className)} title={text}>
      {text}
    </div>
  );
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
  /** The same, with large numbers shortened (12.4M) for table cells. */
  short: string;
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
    return { text: `${num(rem)} / ${num(limit)}${u}`, short: `${short(rem)} / ${short(limit)}${u}`, frac, low: rem < 0.1 * limit };
  }
  if (rem !== null) return { text: `${num(rem)}${u} left`, short: `${short(rem)}${u} left`, frac: null, low: rem <= 0 };
  return { text: `${num(limit)}${u} limit, remaining unknown`, short: `${short(limit ?? 0)}${u} limit`, frac: null, low: false };
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
  /** False when the provider does not report reset times, so an exhausted key's reset is Acnologia's estimate. */
  resetExact?: boolean;
}) {
  const q = quotaInfo(k, unit);
  if (!q) return <span className={cx("font-mono text-caption-1-regular text-text-tertiary", className)}>{compact ? "-" : "No quota data yet"}</span>;
  const pct = q.frac === null ? null : q.frac * 100;
  const tone: MeterTone = pct === null ? "accent" : pct > 40 ? "success" : pct > 10 ? "warning" : "error";
  const reset = k.quota_reset_at ? Date.parse(k.quota_reset_at) : NaN;
  const exhausted = k.effective_status === "exhausted" || k.status === "exhausted";
  return (
    <div className={cx("min-w-0", className)}>
      <div className="flex flex-wrap items-baseline gap-x-1.5 font-mono text-caption-1-regular tabular-nums text-text-primary" title={compact && q.short !== q.text ? q.text : undefined}>
        <span className="wrap-anywhere">{compact ? q.short : q.text}</span>
        {q.low && <span className="text-text-error-primary">low</span>}
      </div>
      {pct !== null && (
        <Meter
          aria-label="Quota left"
          value={Math.round(pct)}
          valueLabel={q.text}
          tone={tone}
          size="sm"
          className="mt-1.5 max-w-[160px]"
        />
      )}
      {!Number.isNaN(reset) && (
        <div className="mt-1 font-mono text-caption-1-regular text-text-secondary" title={dateTime(k.quota_reset_at)}>
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
      <figcaption className="text-body-2-regular text-text-secondary">{summary}</figcaption>
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
                      "flex h-full w-full cursor-pointer flex-col-reverse justify-start overflow-hidden rounded-t-[4px] outline-none transition-colors duration-150 ease",
                      "focus-visible:ring-2 focus-visible:ring-border-focus-ring",
                      sel === i ? "bg-background-tertiary-hover" : "hover:bg-background-tertiary-default",
                    )}
                  >
                    {total === 0 ? (
                      <span className="block h-[2px] w-full bg-border-button-hover" />
                    ) : (
                      <span className="animate-bar-rise flex h-full w-full flex-col-reverse" style={{ animationDelay: `${i * 18}ms` }}>
                        <span className="block w-full bg-chart-1" style={{ height: h(d.requests) }} />
                        <span className="block w-full bg-chart-8-active" style={{ height: h(d.rate_limited) }} />
                        <span className="block w-full bg-notification-error-foreground" style={{ height: h(d.errors) }} />
                      </span>
                    )}
                  </button>
                </li>
              );
            })}
          </ol>
          <div className="mt-1.5 flex justify-between font-mono text-caption-2-regular text-text-tertiary" aria-hidden="true">
            <span>{daily[0] && dayLabel(daily[0].day)}</span>
            <span>{daily.length > 0 && dayLabel(daily[daily.length - 1].day)}</span>
          </div>
          <p className="mt-2 min-h-5 font-mono text-caption-1-regular text-text-primary" aria-live="polite">
            {picked ? daySentence(picked) : <span className="text-text-tertiary">Tap or hover a bar for that day.</span>}
          </p>
          <ul className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-caption-1-regular text-text-secondary" aria-label="Legend">
            <li className="inline-flex items-center gap-1.5">
              <span className="size-2.5 rounded-xs bg-chart-1" aria-hidden="true" /> Requests
            </li>
            <li className="inline-flex items-center gap-1.5">
              <span className="size-2.5 rounded-xs bg-chart-8-active" aria-hidden="true" /> Rate limited
            </li>
            <li className="inline-flex items-center gap-1.5">
              <span className="size-2.5 rounded-xs bg-notification-error-foreground" aria-hidden="true" /> Errors
            </li>
          </ul>
        </>
      )}
    </figure>
  );
}
