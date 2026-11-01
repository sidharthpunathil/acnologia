import { Link } from "react-router-dom";
import type { CogitoEvent, EventKind } from "../lib/types";
import { dateTime, relTime } from "../lib/format";
import { Badge, cx } from "./ui";

const kindTone: Record<EventKind, "neutral" | "green" | "amber" | "red" | "blue" | "violet"> = {
  lease: "blue",
  proxy: "violet",
  ok: "green",
  rate_limited: "amber",
  quota_exhausted: "amber",
  invalid: "red",
  error: "red",
  health: "neutral",
};

export function KindBadge({ kind }: { kind: EventKind }) {
  return <Badge tone={kindTone[kind] ?? "neutral"}>{kind.replace("_", " ")}</Badge>;
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
  return (
    <ul className="divide-y divide-line overflow-hidden rounded-card bg-surface shadow-card">
      {events.map((e) => {
        const sid = e.service_id ?? serviceIdBySlug?.[e.service_slug];
        return (
          <li key={e.id} className="flex flex-col gap-1.5 px-4 py-3 sm:flex-row sm:items-center sm:gap-4">
            <div className="flex min-w-0 items-center gap-2 sm:w-[150px] sm:shrink-0">
              <KindBadge kind={e.kind} />
              {e.status_code > 0 && (
                <span
                  className={cx(
                    "font-mono text-[11px]",
                    e.status_code >= 500 ? "text-red-700" : e.status_code >= 400 ? "text-amber-700" : "text-muted",
                  )}
                >
                  {e.status_code}
                </span>
              )}
            </div>
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[13px] text-ink">
                {showService && e.service_slug && (
                  sid ? (
                    <Link to={`/services/${sid}`} className="tap-inline font-medium hover:underline">
                      {e.service_slug}
                    </Link>
                  ) : (
                    <span className="font-medium">{e.service_slug}</span>
                  )
                )}
                {e.key_label && <span className="font-mono text-xs text-muted">key {e.key_label}</span>}
                {e.app_name && <span className="font-mono text-xs text-muted">app {e.app_name}</span>}
              </div>
              {e.detail && <div className="wrap-anywhere mt-0.5 line-clamp-2 text-xs text-muted" title={e.detail}>{e.detail}</div>}
            </div>
            <div className="flex shrink-0 items-center gap-3 font-mono text-[11px] text-faint">
              {e.latency_ms > 0 && <span>{e.latency_ms}ms</span>}
              <span title={dateTime(e.ts)}>{relTime(e.ts, now)}</span>
            </div>
          </li>
        );
      })}
    </ul>
  );
}
