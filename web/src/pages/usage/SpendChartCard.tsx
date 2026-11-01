import { useId, useMemo, useState, type KeyboardEvent } from "react";
import { RiArrowRightSLine, RiBarChartBoxLine } from "@remixicon/react";
import { useReducedMotion } from "motion/react";
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { EmptyState } from "@/components/acn";
import { Table, TableBody, TableCell, TableColumn, TableHeader, TableRow } from "@/components/base/table/table";
import { useCountUp } from "@/hooks/use-count-up";
import { plural } from "@/lib/format";
import { usd, usdAxis, type UsageReport } from "@/lib/usage";
import { cx } from "@/utils/cx";

/**
 * Spend Chart Card: BoardUI's orders chart card, changed to one stacked bar
 * per UTC day. Claude API spend (chart-1) sits on the baseline and provider
 * spend (chart-2) on top, with a 2px surface gap between them and only the
 * top segment rounded. The headline is the range total as a count up figure;
 * hovering or arrowing to a day swaps it for that day and its split.
 */

export interface DayPoint {
  day: string;
  ai: number;
  prov: number;
}

/** "Oct 4", or "Sat, Oct 4" with the weekday, for a UTC YYYY-MM-DD day. */
export function dayLabel(day: string, withWeekday = false): string {
  const d = new Date(day + "T00:00:00Z");
  return d.toLocaleDateString("en-US", {
    timeZone: "UTC",
    month: "short",
    day: "numeric",
    ...(withWeekday ? { weekday: "short" } : {}),
  });
}

/** Rounds up to 1, 2 or 5 times a power of ten so ticks land on clean numbers. */
function niceMax(v: number): number {
  if (v <= 0) return 1;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 2, 5, 10]) if (m * p >= v) return m * p;
  return 10 * p;
}

/** A rect with rounded top corners only: the data end, square at the baseline. */
function topRounded(x: number, y: number, w: number, h: number, r: number): string {
  const rr = Math.max(0, Math.min(r, w / 2, h));
  return `M${x},${y + h}V${y + rr}Q${x},${y} ${x + rr},${y}H${x + w - rr}Q${x + w},${y} ${x + w},${y + rr}V${y + h}Z`;
}

/** Surface gap between the two stacked segments. */
const GAP = 2;
const RADIUS = 4;

interface SegmentProps {
  x?: number;
  y?: number;
  width?: number;
  height?: number;
  index?: number;
  payload?: DayPoint;
}

/** Money that rolls to its new value; sub dollar amounts are shown exactly. */
function CountUpUsd({ value }: { value: number }) {
  const reduce = useReducedMotion();
  const cents = useCountUp(Math.round(value * 100), reduce ? 1 : 320);
  return <>{Math.abs(value) >= 1 ? usd(cents / 100) : usd(value)}</>;
}

const TICK = { fontSize: 12, fill: "var(--color-text-tertiary)" };

export function SpendChartCard({ data, className }: { data: UsageReport; className?: string }) {
  const points: DayPoint[] = useMemo(
    () => data.ai.by_day.map((a, i) => ({ day: a.key, ai: a.cost_usd, prov: data.provider.by_day[i]?.cost_usd ?? 0 })),
    [data],
  );
  const reduce = useReducedMotion();
  const [active, setActive] = useState<number | null>(null);
  const descId = useId();

  const totalAi = points.reduce((s, p) => s + p.ai, 0);
  const totalProv = points.reduce((s, p) => s + p.prov, 0);
  const peak = points.reduce<DayPoint | null>((b, p) => (!b || p.ai + p.prov > b.ai + b.prov ? p : b), null);
  const empty = totalAi + totalProv === 0;
  const n = points.length;

  const point = active !== null ? (points[active] ?? null) : null;
  const headAi = point ? point.ai : totalAi;
  const headProv = point ? point.prov : totalProv;
  const label = point ? dayLabel(point.day, true) : `Spend, last ${plural(n, "day")}`;

  const summary = empty
    ? `No spend in the last ${plural(n, "day")}.`
    : `Over the last ${plural(n, "day")}, Claude API spend was ${usd(totalAi)} and provider spend was ${usd(totalProv)}. ` +
      (peak ? `The highest day was ${dayLabel(peak.day, true)} at ${usd(peak.ai + peak.prov)}.` : "");

  const top = niceMax(Math.max(0, ...points.map((p) => p.ai + p.prov)));
  const xTicks = n > 1 ? [points[0].day, points[Math.floor((n - 1) / 2)].day, points[n - 1].day] : points.map((p) => p.day);

  const onKey = (e: KeyboardEvent<HTMLDivElement>) => {
    const cur = active ?? n - 1;
    let next = -1;
    if (e.key === "ArrowRight") next = Math.min(n - 1, cur + 1);
    else if (e.key === "ArrowLeft") next = Math.max(0, cur - 1);
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = n - 1;
    else if (e.key === "Escape") {
      setActive(null);
      return;
    }
    if (next < 0) return;
    e.preventDefault();
    setActive(next);
  };

  const dim = (i: number | undefined) => (active !== null && i !== active ? 0.45 : 1);

  // Claude sits on the baseline; its top is rounded only when no provider segment sits on it.
  const AiShape = ({ x = 0, y = 0, width = 0, height = 0, index, payload }: SegmentProps) => {
    if (height <= 0) return <g />;
    const h = Math.max(1, height);
    return (
      <path
        d={topRounded(x, y + height - h, width, h, payload && payload.prov > 0 ? 0 : RADIUS)}
        className="fill-chart-1 transition-opacity duration-150 ease-out"
        style={{ opacity: dim(index) }}
      />
    );
  };
  // Provider sits on top with a surface gap below it when Claude is under it.
  const ProvShape = ({ x = 0, y = 0, width = 0, height = 0, index, payload }: SegmentProps) => {
    if (height <= 0) return <g />;
    const gap = payload && payload.ai > 0 ? GAP : 0;
    const h = Math.max(1, height - gap);
    return (
      <path
        d={topRounded(x, y, width, h, RADIUS)}
        className="fill-chart-2 transition-opacity duration-150 ease-out"
        style={{ opacity: dim(index) }}
      />
    );
  };

  return (
    <section
      aria-labelledby={`${descId}-title`}
      className={cx("flex min-w-0 flex-col gap-6 rounded-3xl border border-border-button-default bg-background-primary-default p-5 sm:p-6", className)}
    >
      <h2 id={`${descId}-title`} className="sr-only">
        Daily spend
      </h2>
      {/* Header: label over the count up figure and its split; legend on the right */}
      <div className="flex w-full flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex min-w-0 flex-col gap-0.5">
          <p className="text-body-medium text-text-secondary">{label}</p>
          <p
            key={active ?? "total"}
            className="animate-number-fade whitespace-nowrap font-mono text-title-1-medium tabular-nums text-text-primary"
          >
            <CountUpUsd value={headAi + headProv} />
          </p>
          <p className="font-mono text-body-2-medium tabular-nums text-text-tertiary">
            Claude {usd(headAi)}, provider {usd(headProv)}
          </p>
        </div>
        {!empty && (
          <dl className="flex shrink-0 items-center gap-4 text-body-2-medium text-text-secondary">
            <div className="flex items-center gap-1.5">
              <span className="size-2 rounded-full bg-chart-1" aria-hidden />
              <dt>Claude API</dt>
            </div>
            <div className="flex items-center gap-1.5">
              <span className="size-2 rounded-full bg-chart-2" aria-hidden />
              <dt>Provider APIs</dt>
            </div>
          </dl>
        )}
      </div>

      {empty ? (
        <EmptyState
          compact
          icon={RiBarChartBoxLine}
          title="No spend in this range"
          body="Claude calls and priced provider requests will draw a bar per day here."
        />
      ) : (
        <>
          <p id={descId} className="sr-only">
            {summary} Use the left and right arrow keys to read each day.
          </p>
          <div
            tabIndex={0}
            role="group"
            aria-label="Daily spend chart"
            aria-describedby={descId}
            onKeyDown={onKey}
            onFocus={() => setActive((v) => v ?? n - 1)}
            onBlur={() => setActive(null)}
            className="h-[216px] w-full rounded-xl outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring focus-visible:ring-offset-4 focus-visible:ring-offset-background-primary-default"
          >
            <div aria-hidden="true" className="size-full select-none">
              <ResponsiveContainer width="100%" height="100%">
                <BarChart
                  data={points}
                  margin={{ top: 4, right: 2, bottom: 0, left: 0 }}
                  barCategoryGap="28%"
                  accessibilityLayer={false}
                  onMouseMove={(state) => {
                    const index = Number(state?.activeTooltipIndex);
                    if (state?.isTooltipActive && Number.isFinite(index)) setActive(index);
                  }}
                  onMouseLeave={() => setActive(null)}
                >
                  <CartesianGrid vertical={false} strokeDasharray="2 4" stroke="var(--color-separator-border)" />
                  <YAxis
                    width={44}
                    domain={[0, top]}
                    ticks={[0, top / 2, top]}
                    tickFormatter={usdAxis}
                    tickLine={false}
                    axisLine={false}
                    tick={{ ...TICK, fontFamily: "var(--font-mono)" }}
                  />
                  <XAxis
                    dataKey="day"
                    ticks={xTicks}
                    interval={0}
                    tickFormatter={(d: string) => dayLabel(d)}
                    tickLine={false}
                    axisLine={{ stroke: "var(--color-separator-border)" }}
                    tickMargin={10}
                    tick={TICK}
                  />
                  <Tooltip content={() => null} cursor={{ fill: "var(--color-chart-track)", opacity: 0.5, radius: 6 }} isAnimationActive={false} />
                  <Bar dataKey="ai" stackId="spend" shape={AiShape} isAnimationActive={!reduce} animationDuration={450} animationEasing="ease-out" />
                  <Bar dataKey="prov" stackId="spend" shape={ProvShape} isAnimationActive={!reduce} animationDuration={450} animationEasing="ease-out" />
                </BarChart>
              </ResponsiveContainer>
            </div>
          </div>
          <div className="sr-only" aria-live="polite">
            {point ? `${dayLabel(point.day, true)}: Claude ${usd(point.ai)}, provider ${usd(point.prov)}, total ${usd(point.ai + point.prov)}.` : ""}
          </div>
          <DailyTable points={points} />
        </>
      )}
    </section>
  );
}

/** The chart's values as a table, so nothing is reachable only by hovering. */
function DailyTable({ points }: { points: DayPoint[] }) {
  const rows = points.filter((p) => p.ai + p.prov > 0).reverse();
  return (
    <details className="group -mt-2">
      <summary className="inline-flex cursor-pointer select-none list-none items-center gap-1 rounded-md text-body-2-medium text-text-secondary outline-none transition-colors duration-150 ease hover:text-text-primary focus-visible:ring-2 focus-visible:ring-border-focus-ring [&::-webkit-details-marker]:hidden">
        <RiArrowRightSLine className="size-4 transition-transform duration-150 ease-out group-open:rotate-90 motion-reduce:transition-none" aria-hidden />
        Daily values ({plural(rows.length, "day")} with spend)
      </summary>
      <div className="mt-3 overflow-hidden rounded-2xl border border-border-table">
        <Table size="sm" aria-label="Daily spend values">
          <TableHeader>
            <TableColumn isRowHeader>Day</TableColumn>
            <TableColumn>
              <span className="block text-end">Claude</span>
            </TableColumn>
            <TableColumn>
              <span className="block text-end">Provider</span>
            </TableColumn>
            <TableColumn>
              <span className="block text-end">Total</span>
            </TableColumn>
          </TableHeader>
          <TableBody>
            {rows.map((p) => (
              <TableRow key={p.day} id={p.day}>
                <TableCell>{dayLabel(p.day, true)}</TableCell>
                <TableCell>
                  <span className="block text-end font-mono tabular-nums text-text-secondary">{usd(p.ai)}</span>
                </TableCell>
                <TableCell>
                  <span className="block text-end font-mono tabular-nums text-text-secondary">{usd(p.prov)}</span>
                </TableCell>
                <TableCell>
                  <span className="block text-end font-mono tabular-nums">{usd(p.ai + p.prov)}</span>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </details>
  );
}
