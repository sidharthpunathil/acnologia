import { useRef } from "react";
import { Link } from "react-router-dom";
import type { CogitoEvent, EventKind } from "../lib/types";
import { dateTime, relTime } from "../lib/format";
import { Chip, type ChipColor } from "@/components/base/badges/chip";
import { cx } from "@/utils/cx";

const kindColor: Record<EventKind, ChipColor> = {
  lease: "blue",
  proxy: "purple",
  ok: "lime",
  rate_limited: "yellow",
  quota_exhausted: "orange",
  invalid: "rose",
  error: "rose",
  health: "neutral",
  busy: "yellow",
  banned: "rose",
  cache_hit: "cyan",
  service_down: "rose",
  service_up: "lime",
};

/** The event kind as a tinted chip; the word always carries the meaning. */
export function KindBadge({ kind, className }: { kind: EventKind; className?: string }) {
  return (
    <Chip variant="caption" color={kindColor[kind] ?? "neutral"} className={cx("rounded-full px-2 py-0.5 font-mono", className)}>
      {kind.replace(/_/g, " ")}
    </Chip>
  );
}

export function EventsList({
  events,
  now,
  showService = true,
  serviceIdBySlug,
}: {
  events: CogitoEvent[];
  now: number;
  showService?: boolean;
  serviceIdBySlug?: Record<string, number>;
}) {
  // Events that arrive after the first render settle in, so a live feed does
  // not jump. The first batch renders still: nothing is new on page load.
  // Each id remembers when it first showed up; the class stays for the length
  // of the entrance, so a clock tick re-render cannot cut it short.
  const seen = useRef<Map<number, number> | null>(null);
  const t = Date.now();
  if (seen.current === null) seen.current = new Map(events.map((e) => [e.id, 0]));
  for (const e of events) if (!seen.current.has(e.id)) seen.current.set(e.id, t);
  const isFresh = (id: number) => {
    const at = seen.current?.get(id) ?? 0;
    return at > 0 && t - at < 400;
  };
  return (
    <ul className="overflow-hidden rounded-3xl border border-border-button-default bg-background-primary-default">
      {events.map((e) => {
        const sid = e.service_id ?? serviceIdBySlug?.[e.service_slug];
        return (
          <li
            key={e.id}
            className={cx(
              "flex flex-col gap-1.5 border-b border-separator-border px-4 py-3 last:border-b-0 sm:flex-row sm:items-center sm:gap-4 sm:px-5",
              isFresh(e.id) && "fade-up",
            )}
          >
            <div className="flex min-w-0 items-center gap-2 sm:w-[156px] sm:shrink-0">
              <KindBadge kind={e.kind} />
              {e.status_code > 0 && (
                <span
                  className={cx(
                    "font-mono text-caption-1-regular tabular-nums",
                    e.status_code >= 500 ? "text-text-error-primary" : e.status_code >= 400 ? "text-status-yellow-text" : "text-text-secondary",
                  )}
                >
                  {e.status_code}
                </span>
              )}
            </div>
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-body-2-regular text-text-primary">
                {showService &&
                  e.service_slug &&
                  (sid ? (
                    <Link
                      to={`/services/${sid}`}
                      className="rounded-sm text-body-2-medium outline-none underline-offset-2 hover:underline focus-visible:ring-2 focus-visible:ring-border-focus-ring"
                    >
                      {e.service_slug}
                    </Link>
                  ) : (
                    <span className="text-body-2-medium">{e.service_slug}</span>
                  ))}
                {e.key_label && <span className="wrap-anywhere min-w-0 font-mono text-caption-1-regular text-text-secondary">key {e.key_label}</span>}
                {e.app_name && <span className="wrap-anywhere min-w-0 font-mono text-caption-1-regular text-text-secondary">app {e.app_name}</span>}
              </div>
              {e.detail && (
                <div className="wrap-anywhere mt-0.5 line-clamp-2 text-caption-1-regular text-text-secondary" title={e.detail}>
                  {e.detail}
                </div>
              )}
            </div>
            <div className="flex shrink-0 items-center gap-3 font-mono text-caption-1-regular tabular-nums text-text-tertiary">
              {e.latency_ms > 0 && <span>{e.latency_ms}ms</span>}
              <time dateTime={e.ts} title={dateTime(e.ts)}>
                {relTime(e.ts, now)}
              </time>
            </div>
          </li>
        );
      })}
    </ul>
  );
}
