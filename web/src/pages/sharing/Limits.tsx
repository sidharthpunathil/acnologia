import type { AppLimits } from "../../lib/ops";
import type { Share } from "../../lib/sharing";
import { num } from "../../lib/format";
import { Meter, type MeterTone } from "@/components/base/meter/meter";

const usd = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", minimumFractionDigits: 2, maximumFractionDigits: 2 });

export function money(n: number): string {
  return usd.format(n);
}

interface LimitLine {
  key: keyof AppLimits;
  name: string;
  used: number;
  cap: number;
  period: "today" | "this month";
  format: (n: number) => string;
}

type Usage = Pick<NonNullable<Share["usage"]>, "today_requests" | "month_requests" | "today_cost" | "month_cost">;

function lines(limits: AppLimits | null | undefined, usage: Usage | undefined): LimitLine[] {
  const out: LimitLine[] = [];
  if (!limits) return out;
  const u = usage ?? { today_requests: 0, month_requests: 0, today_cost: 0, month_cost: 0 };
  if (limits.daily_requests != null)
    out.push({ key: "daily_requests", name: "Daily requests", used: u.today_requests, cap: limits.daily_requests, period: "today", format: num });
  if (limits.monthly_requests != null)
    out.push({ key: "monthly_requests", name: "Monthly requests", used: u.month_requests, cap: limits.monthly_requests, period: "this month", format: num });
  if (limits.daily_budget != null)
    out.push({ key: "daily_budget", name: "Daily budget", used: u.today_cost, cap: limits.daily_budget, period: "today", format: money });
  if (limits.monthly_budget != null)
    out.push({ key: "monthly_budget", name: "Monthly budget", used: u.month_cost ?? 0, cap: limits.monthly_budget, period: "this month", format: money });
  return out;
}

function toneOf(used: number, cap: number): MeterTone {
  const ratio = cap > 0 ? used / cap : 1;
  if (ratio >= 1) return "error";
  if (ratio >= 0.8) return "warning";
  return "accent";
}

/** A share's limits as compact meters: used of cap per set limit, or a quiet "No limits". */
export function LimitMeters({ share }: { share: Share }) {
  return <UsageMeters limits={share.limits} usage={share.usage} />;
}

/** Limits and usage from anywhere (a share, or a person's own access page). */
export function UsageMeters({ limits, usage }: { limits: AppLimits; usage: Usage | undefined }) {
  const items = lines(limits, usage);
  if (!items.length) return <p className="text-caption-1-regular text-text-tertiary">No limits</p>;
  return (
    <ul className="grid max-w-xl grid-cols-1 gap-x-6 gap-y-2.5 sm:grid-cols-2" aria-label="Limits">
      {items.map((l) => {
        const tone = toneOf(l.used, l.cap);
        const text = `${l.format(l.used)} / ${l.format(l.cap)} ${l.period}`;
        return (
          <li key={l.key} className="flex min-w-0 flex-col gap-1">
            <span className="truncate font-mono text-caption-1-regular text-text-secondary tabular-nums" title={l.name}>
              <span className={tone === "error" ? "text-text-error-primary" : "text-text-primary"}>{l.format(l.used)}</span> / {l.format(l.cap)}{" "}
              {l.period}
            </span>
            <Meter
              aria-label={l.name}
              value={l.cap > 0 ? Math.min(l.used, l.cap) : 1}
              maxValue={l.cap > 0 ? l.cap : 1}
              valueLabel={text}
              size="sm"
              tone={tone}
            />
          </li>
        );
      })}
    </ul>
  );
}

/** One line naming the chosen limits, for the drawer: "Up to 1,000 requests a day and $5.00 a month." */
export function limitSummary(limits: AppLimits): string {
  const parts: string[] = [];
  if (limits.daily_requests != null) parts.push(`${num(limits.daily_requests)} requests a day`);
  if (limits.monthly_requests != null) parts.push(`${num(limits.monthly_requests)} requests a month`);
  if (limits.daily_budget != null) parts.push(`${money(limits.daily_budget)} a day`);
  if (limits.monthly_budget != null) parts.push(`${money(limits.monthly_budget)} a month`);
  if (!parts.length) return "";
  const list = parts.length === 1 ? parts[0] : `${parts.slice(0, -1).join(", ")} and ${parts[parts.length - 1]}`;
  return `Up to ${list}.`;
}
