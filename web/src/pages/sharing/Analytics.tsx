// Sharing analytics: how long each share has been live, how much it was
// used (requests, LLM tokens), what that cost, and who used it.
import { useCallback, useEffect, useId, useMemo, useState, type KeyboardEvent, type ReactNode } from "react";
import type { Key as AriaKey } from "react-aria-components";
import { RiArrowRightSLine, RiBarChartBoxLine, RiRefreshLine } from "@remixicon/react";
import { useReducedMotion } from "motion/react";
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { useTitle } from "../../hooks/useTitle";
import { errorMessage } from "../../lib/api";
import { compact, dateTime, num, plural, relTime } from "../../lib/format";
import { sharing, type AnalyticsDay, type AnalyticsShare, type SharingAnalytics } from "../../lib/sharing";
import { usd, usdAxis } from "../../lib/usage";
import { dayLabel } from "../usage/SpendChartCard";
import { Callout, EmptyState, PageHeader, PageSkeleton, StatTile } from "@/components/acn";
import { Button } from "@/components/base/buttons/button";
import { Chip, type ChipColor } from "@/components/base/badges/chip";
import { SegmentedControl, SegmentedControlItem } from "@/components/base/segmented-control/segmented-control";
import { Table, TableBody, TableCell, TableColumn, TableHeader, TableRow } from "@/components/base/table/table";
import { cx } from "@/utils/cx";

const RANGES = [7, 30, 90] as const;
type Metric = "cost" | "requests" | "tokens";
const METRICS: { id: Metric; label: string }[] = [
  { id: "cost", label: "Spend" },
  { id: "requests", label: "Requests" },
  { id: "tokens", label: "Tokens" },
];

/** "45s", "12m", "3h 20m", "4d 6h". */
export function duration(sec: number): string {
  if (sec <= 0) return "-";
  if (sec < 60) return `${sec}s`;
  const m = Math.floor(sec / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return m % 60 ? `${h}h ${m % 60}m` : `${h}h`;
  const d = Math.floor(h / 24);
  return h % 24 ? `${d}d ${h % 24}h` : `${d}d`;
}

/** A duration as a <time> element, so it carries a machine value too. */
function Duration({ sec }: { sec: number }) {
  if (sec <= 0) return <>-</>;
  return <time dateTime={`PT${sec}S`}>{duration(sec)}</time>;
}

const tokensOf = (x: { input_tokens: number; output_tokens: number }) => x.input_tokens + x.output_tokens;

function metricValue(d: AnalyticsDay, m: Metric): number {
  if (m === "cost") return d.cost;
  if (m === "requests") return d.requests;
  return tokensOf(d);
}

function metricText(v: number, m: Metric): string {
  if (m === "cost") return usd(v);
  if (m === "requests") return plural(v, "request");
  return `${compact(v)} tokens`;
}

function niceMax(v: number): number {
  if (v <= 0) return 1;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const k of [1, 2, 5, 10]) if (k * p >= v) return k * p;
  return 10 * p;
}

const TICK = { fontSize: 12, fill: "var(--color-text-tertiary)" };

/** A rect rounded at the top only: the data end, square at the baseline. */
function topRounded(x: number, y: number, w: number, h: number, r: number): string {
  const rr = Math.max(0, Math.min(r, w / 2, h));
  return `M${x},${y + h}V${y + rr}Q${x},${y} ${x + rr},${y}H${x + w - rr}Q${x + w},${y} ${x + w},${y + rr}V${y + h}Z`;
}

// ---------------------------------------------------------------- chart

function DailyChart({ series }: { series: AnalyticsDay[] }) {
  const [metric, setMetric] = useState<Metric>("cost");
  const [active, setActive] = useState<number | null>(null);
  const reduce = useReducedMotion();
  const descId = useId();
  const points = useMemo(() => series.map((d) => ({ day: d.day, v: metricValue(d, metric), d })), [series, metric]);
  const n = points.length;
  const total = points.reduce((s, p) => s + p.v, 0);
  const point = active !== null ? (points[active] ?? null) : null;
  const top = niceMax(Math.max(0, ...points.map((p) => p.v)));
  const xTicks = n > 1 ? [points[0].day, points[Math.floor((n - 1) / 2)].day, points[n - 1].day] : points.map((p) => p.day);
  const axis = (v: number) => (metric === "cost" ? usdAxis(v) : compact(v));
  const label = point ? dayLabel(point.day, true) : `${METRICS.find((m) => m.id === metric)?.label}, last ${plural(n, "day")}`;
  const sub = point
    ? `${plural(point.d.requests, "request")}, ${compact(tokensOf(point.d))} tokens, ${usd(point.d.cost)}${point.d.errors ? `, ${plural(point.d.errors, "error")}` : ""}`
    : null;

  const onKey = (e: KeyboardEvent<HTMLDivElement>) => {
    const cur = active ?? n - 1;
    let next = -1;
    if (e.key === "ArrowRight") next = Math.min(n - 1, cur + 1);
    else if (e.key === "ArrowLeft") next = Math.max(0, cur - 1);
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = n - 1;
    else if (e.key === "Escape") return setActive(null);
    if (next < 0) return;
    e.preventDefault();
    setActive(next);
  };

  const Shape = ({ x = 0, y = 0, width = 0, height = 0, index }: { x?: number; y?: number; width?: number; height?: number; index?: number }) => {
    if (height <= 0) return <g />;
    const h = Math.max(1, height);
    return (
      <path
        d={topRounded(x, y + height - h, width, h, 4)}
        className="fill-chart-1 transition-opacity duration-150 ease-out"
        style={{ opacity: active !== null && index !== active ? 0.45 : 1 }}
      />
    );
  };

  return (
    <section aria-labelledby={`${descId}-t`} className="flex min-w-0 flex-col gap-6 rounded-3xl border border-border-button-default bg-background-primary-default p-5 sm:p-6">
      <h2 id={`${descId}-t`} className="sr-only">
        Daily use of your shares
      </h2>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex min-w-0 flex-col gap-0.5">
          <p className="text-body-medium text-text-secondary">{label}</p>
          <p className="whitespace-nowrap font-mono text-title-1-medium tabular-nums text-text-primary">{metricText(point ? point.v : total, metric)}</p>
          <p className="min-h-5 font-mono text-body-2-medium tabular-nums text-text-tertiary">{sub ?? "Hover or arrow through the days"}</p>
        </div>
        <SegmentedControl
          aria-label="Chart metric"
          selectedKeys={new Set<AriaKey>([metric])}
          onSelectionChange={(sel) => {
            const next = [...sel][0];
            if (next !== undefined) setMetric(String(next) as Metric);
          }}
          className="w-full sm:w-auto"
        >
          {METRICS.map((m) => (
            <SegmentedControlItem key={m.id} id={m.id} className="min-h-9 flex-1 sm:min-h-0 sm:flex-none">
              {m.label}
            </SegmentedControlItem>
          ))}
        </SegmentedControl>
      </div>
      {total === 0 ? (
        <EmptyState
          compact
          icon={RiBarChartBoxLine}
          title={metric === "tokens" ? "No tokens in this range" : "No use in this range"}
          body={
            metric === "tokens"
              ? "Tokens are counted for LLM services shared through this server, from what the provider reports in each answer."
              : "Calls made through your proxy shares, and what handover shares report, draw a bar per day here."
          }
        />
      ) : (
        <>
          <p id={descId} className="sr-only">
            {`${metricText(total, metric)} over the last ${plural(n, "day")}.`} Use the left and right arrow keys to read each day.
          </p>
          <div
            tabIndex={0}
            role="group"
            aria-label="Daily use chart"
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
                  onMouseMove={(st) => {
                    const i = Number(st?.activeTooltipIndex);
                    if (st?.isTooltipActive && Number.isFinite(i)) setActive(i);
                  }}
                  onMouseLeave={() => setActive(null)}
                >
                  <CartesianGrid vertical={false} strokeDasharray="2 4" stroke="var(--color-separator-border)" />
                  <YAxis
                    width={48}
                    domain={[0, top]}
                    ticks={[0, top / 2, top]}
                    tickFormatter={axis}
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
                  <Bar dataKey="v" shape={Shape} isAnimationActive={!reduce} animationDuration={450} animationEasing="ease-out" />
                </BarChart>
              </ResponsiveContainer>
            </div>
          </div>
          <div className="sr-only" aria-live="polite">
            {point && sub ? `${dayLabel(point.day, true)}: ${sub}.` : ""}
          </div>
          <DailyTable series={series} />
        </>
      )}
    </section>
  );
}

function Num({ children, strong }: { children: string; strong?: boolean }) {
  return <span className={cx("block text-end font-mono tabular-nums", strong ? "text-text-primary" : "text-text-secondary")}>{children}</span>;
}

function DailyTable({ series }: { series: AnalyticsDay[] }) {
  const rows = series.filter((d) => d.requests + tokensOf(d) > 0 || d.cost > 0).reverse();
  return (
    <details className="group -mt-2">
      <summary className="inline-flex cursor-pointer select-none list-none items-center gap-1 rounded-md text-body-2-medium text-text-secondary outline-none transition-colors duration-150 ease hover:text-text-primary focus-visible:ring-2 focus-visible:ring-border-focus-ring [&::-webkit-details-marker]:hidden">
        <RiArrowRightSLine className="size-4 transition-transform duration-150 ease-out group-open:rotate-90 motion-reduce:transition-none" aria-hidden />
        Daily values ({plural(rows.length, "day")} with use)
      </summary>
      <div className="mt-3 overflow-x-auto rounded-2xl border border-border-table">
        <Table size="sm" aria-label="Daily use values">
          <TableHeader>
            <TableColumn isRowHeader>Day</TableColumn>
            <TableColumn><span className="block text-end">Requests</span></TableColumn>
            <TableColumn><span className="block text-end">Errors</span></TableColumn>
            <TableColumn><span className="block text-end">Tokens in</span></TableColumn>
            <TableColumn><span className="block text-end">Tokens out</span></TableColumn>
            <TableColumn><span className="block text-end">Spend</span></TableColumn>
          </TableHeader>
          <TableBody>
            {rows.map((d) => (
              <TableRow key={d.day} id={d.day}>
                <TableCell>{dayLabel(d.day, true)}</TableCell>
                <TableCell><Num>{num(d.requests)}</Num></TableCell>
                <TableCell><Num>{num(d.errors)}</Num></TableCell>
                <TableCell><Num>{num(d.input_tokens)}</Num></TableCell>
                <TableCell><Num>{num(d.output_tokens)}</Num></TableCell>
                <TableCell><Num strong>{usd(d.cost)}</Num></TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </details>
  );
}

// ---------------------------------------------------------------- per share

const RECIPIENT: Record<AnalyticsShare["recipient"], { label: string; color: ChipColor }> = {
  server: { label: "Server", color: "blue" },
  person: { label: "Person", color: "purple" },
  link: { label: "Link", color: "cyan" },
};

const STATUS_COLOR: Record<string, ChipColor> = { active: "lime", pending: "yellow", paused: "orange", revoked: "rose", expired: "gray" };

function costText(a: AnalyticsShare): string {
  if (a.tracking === "none") return "-";
  return (a.cost_estimate ? "~" : "") + usd(a.cost);
}

function Fact({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="text-caption-1-medium text-text-tertiary">{label}</dt>
      <dd className="mt-0.5 truncate font-mono text-body-2-medium tabular-nums text-text-primary" title={typeof value === "string" ? value : undefined}>
        {value}
      </dd>
    </div>
  );
}

function trackingNote(a: AnalyticsShare): string | null {
  if (a.tracking === "none")
    return `The keys were shown to ${a.who || "the person"} (${plural(a.keys_revealed, "key")}), so their use happens at the provider and is not visible here. Check the provider's dashboard for those keys.`;
  if (a.tracking === "reported")
    return `${a.who || "The other server"} holds the keys and reports request counts when it syncs. Requests are its lifetime count, and spend is an estimate at each provider's price per request. Tokens are not reported.`;
  if (!a.started_at) return "Not claimed yet, so nothing has been used.";
  return null;
}

function ShareCard({ a, maxCost, maxReq }: { a: AnalyticsShare; maxCost: number; maxReq: number }) {
  const r = RECIPIENT[a.recipient] ?? RECIPIENT.server;
  const note = trackingNote(a);
  const share = maxCost > 0 ? a.cost / maxCost : maxReq > 0 ? a.requests / maxReq : 0;
  const tokens = tokensOf(a);
  return (
    <li className="rounded-3xl border border-border-button-default bg-background-primary-default">
      <details className="group">
        <summary className="flex cursor-pointer list-none flex-col gap-3 rounded-3xl p-4 outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring sm:p-5 [&::-webkit-details-marker]:hidden">
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <RiArrowRightSLine className="size-4 shrink-0 text-foreground-icon-tertiary transition-transform duration-150 ease-out group-open:rotate-90 motion-reduce:transition-none rtl:-scale-x-100" aria-hidden />
            <span className="min-w-0 wrap-anywhere text-body-medium text-text-primary">
              <bdi>{a.who || a.name}</bdi>
            </span>
            {a.who && a.name && a.who !== a.name && <span className="min-w-0 wrap-anywhere text-body-2-regular text-text-secondary">{a.name}</span>}
            <Chip variant="caption" color={r.color}>{r.label}</Chip>
            <Chip variant="caption" color="soft">{a.mode}</Chip>
            <Chip variant="caption" color={STATUS_COLOR[a.status] ?? "gray"}>{a.status}</Chip>
          </div>
          <dl className="grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-5">
            <Fact label="Live for" value={<Duration sec={a.live_seconds} />} />
            <Fact label="Requests" value={a.tracking === "none" ? "-" : compact(a.requests)} />
            <Fact label="Tokens" value={a.tracking === "measured" ? compact(tokens) : "-"} />
            <Fact label="Spend" value={costText(a)} />
            <Fact label="Last used" value={a.tracking === "none" ? "-" : relTime(a.last_used_at)} />
          </dl>
          {share > 0 && (
            <div className="h-1.5 w-full overflow-hidden rounded-full bg-background-tertiary-default" aria-hidden>
              <div className="h-full rounded-full bg-chart-1" style={{ width: `${Math.max(2, share * 100)}%` }} />
            </div>
          )}
        </summary>
        <div className="flex flex-col gap-5 border-t border-separator-border p-4 sm:p-5">
          {note && <Callout tone="neutral">{note}</Callout>}
          <dl className="grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-4">
            <Fact label="Shared on" value={dateTime(a.created_at)} />
            <Fact label="Access started" value={a.started_at ? dateTime(a.started_at) : "not yet"} />
            <Fact label={a.ended_at ? "Ended" : "Ends"} value={a.ended_at ? dateTime(a.ended_at) : a.ends_at ? dateTime(a.ends_at) : "no end date"} />
            <Fact label="First use in range" value={a.first_used_at ? dateTime(a.first_used_at) : "-"} />
            {a.tracking === "measured" && (
              <>
                <Fact label="Active days" value={num(a.active_days)} />
                <Fact label="Errors" value={a.requests ? `${num(a.errors)} (${Math.round((a.errors / Math.max(1, a.requests)) * 100)}%)` : num(a.errors)} />
                <Fact label="Avg latency" value={a.requests > a.cache_hits ? `${num(a.avg_latency_ms)} ms` : "-"} />
                <Fact label="Cache hits" value={num(a.cache_hits)} />
                <Fact label="Tokens in" value={num(a.input_tokens)} />
                <Fact label="Tokens out" value={num(a.output_tokens)} />
                <Fact label="Answers with tokens" value={num(a.token_calls)} />
                {a.recipient === "link" && <Fact label="Claims" value={num(a.claim_count)} />}
              </>
            )}
          </dl>
          {a.services.length > 0 && (
            <div className="overflow-x-auto rounded-2xl border border-border-table">
              <Table size="sm" aria-label={`Use by service for ${a.who || a.name}`}>
                <TableHeader>
                  <TableColumn isRowHeader>Service</TableColumn>
                  <TableColumn><span className="block text-end">Requests</span></TableColumn>
                  <TableColumn><span className="block text-end">Tokens in</span></TableColumn>
                  <TableColumn><span className="block text-end">Tokens out</span></TableColumn>
                  <TableColumn><span className="block text-end">Spend</span></TableColumn>
                </TableHeader>
                <TableBody>
                  {a.services.map((s) => (
                    <TableRow key={s.slug} id={s.slug}>
                      <TableCell><code dir="ltr" className="font-mono text-body-2-regular">{s.slug}</code></TableCell>
                      <TableCell><Num>{num(s.requests)}</Num></TableCell>
                      <TableCell><Num>{a.tracking === "measured" ? num(s.input_tokens) : "-"}</Num></TableCell>
                      <TableCell><Num>{a.tracking === "measured" ? num(s.output_tokens) : "-"}</Num></TableCell>
                      <TableCell><Num strong>{(a.cost_estimate ? "~" : "") + usd(s.cost)}</Num></TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
          {a.claimants.length > 1 || (a.recipient === "link" && a.claimants.length > 0) ? (
            <div className="flex flex-col gap-2">
              <h3 className="text-body-medium text-text-primary">Who used it</h3>
              <p className="text-caption-1-regular text-text-secondary">One row per token. On a link anyone can claim, each claim gets its own token, so this is roughly one row per person.</p>
              <div className="overflow-x-auto rounded-2xl border border-border-table">
                <Table size="sm" aria-label={`Use by token for ${a.who || a.name}`}>
                  <TableHeader>
                    <TableColumn isRowHeader>Token</TableColumn>
                    <TableColumn><span className="block text-end">Requests</span></TableColumn>
                    <TableColumn><span className="block text-end">Spend</span></TableColumn>
                    <TableColumn><span className="block text-end">Last used</span></TableColumn>
                  </TableHeader>
                  <TableBody>
                    {a.claimants.map((c) => (
                      <TableRow key={c.token_id} id={String(c.token_id)}>
                        <TableCell>
                          <span className={cx("wrap-anywhere", c.revoked && "text-text-tertiary line-through")}>{c.name}</span>
                        </TableCell>
                        <TableCell><Num>{num(c.requests)}</Num></TableCell>
                        <TableCell><Num strong>{usd(c.cost)}</Num></TableCell>
                        <TableCell><Num>{relTime(c.last_used_at)}</Num></TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </div>
          ) : null}
        </div>
      </details>
    </li>
  );
}

// ---------------------------------------------------------------- page

type Filter = "all" | "server" | "person" | "link";
const FILTER_LABEL: Record<Filter, string> = { all: "All", server: "Servers", person: "People", link: "Links" };

export function SharingAnalyticsPage() {
  useTitle("Sharing analytics");
  const [days, setDays] = useState<number>(30);
  const [data, setData] = useState<SharingAnalytics | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [filter, setFilter] = useState<Filter>("all");

  const load = useCallback(async (d: number) => {
    setBusy(true);
    try {
      setData(await sharing.analytics(d));
      setError(null);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }, []);

  useEffect(() => {
    void load(days);
  }, [days, load]);

  if (!data && !error) return <PageSkeleton variant="list" />;

  const t = data?.totals;
  const shares = (data?.shares ?? []).filter((s) => filter === "all" || s.recipient === filter);
  const maxCost = Math.max(0, ...shares.map((s) => s.cost));
  const maxReq = Math.max(0, ...shares.map((s) => s.requests));
  const counts = (r: Filter) => (data?.shares ?? []).filter((s) => r === "all" || s.recipient === r).length;
  const estimated = (data?.shares ?? []).some((s) => s.cost_estimate);
  const capped = data && data.retention_days > 0 && data.days > data.retention_days;

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Sharing analytics"
        back={{ href: "/sharing", label: "Sharing" }}
        meta={data ? `Since ${dayLabel(data.since.slice(0, 10))} · ${plural(data.totals.shares, "share")}` : undefined}
        className="mb-0"
        actions={
          <div className="flex items-center gap-2">
            <SegmentedControl
              aria-label="Range"
              selectedKeys={new Set<AriaKey>([String(days)])}
              onSelectionChange={(sel) => {
                const next = [...sel][0];
                if (next !== undefined) setDays(Number(next));
              }}
            >
              {RANGES.map((d) => (
                <SegmentedControlItem key={d} id={String(d)}>
                  {d}d
                </SegmentedControlItem>
              ))}
            </SegmentedControl>
            <Button variant="secondary" size="small" iconOnly aria-label="Refresh" leadingIcon={RiRefreshLine} disabled={busy} onClick={() => void load(days)} />
          </div>
        }
      />

      {error && (
        <Callout tone="error" title="Could not load the analytics">
          {error}
        </Callout>
      )}

      {data && t && (
        <>
          <div className="grid grid-cols-2 gap-3 lg:grid-cols-5">
            <StatTile label="Spend" value={(estimated ? "~" : "") + usd(t.cost)} sub="priced per request" />
            <StatTile label="Requests" value={compact(t.requests)} sub={t.errors ? plural(t.errors, "error") : "no errors"} />
            <StatTile label="Tokens" value={compact(t.input_tokens + t.output_tokens)} sub={`${compact(t.input_tokens)} in, ${compact(t.output_tokens)} out`} />
            <StatTile label="Recipients" value={num(t.recipients)} sub={`${t.live_shares} live ${t.live_shares === 1 ? "share" : "shares"}`} />
            <StatTile label="Access time" value={<Duration sec={t.live_seconds} />} sub="summed over shares" className="col-span-2 lg:col-span-1" />
          </div>

          {capped && (
            <Callout tone="info">
              Requests, errors and spend reach back {plural(data.retention_days, "day")}, the event retention (ACNOLOGIA_EVENT_RETENTION_DAYS). Tokens are kept for the whole range.
            </Callout>
          )}

          <DailyChart series={data.series} />

          <section aria-labelledby="by-recipient" className="flex flex-col gap-3">
            <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
              <div>
                <h2 id="by-recipient" className="text-title-3-semibold text-text-primary">
                  By recipient
                </h2>
                <p className="mt-1 text-body-2-regular text-text-secondary">Most spend first. Open a share for its services, tokens and who used it.</p>
              </div>
              <SegmentedControl
                aria-label="Recipient"
                selectedKeys={new Set<AriaKey>([filter])}
                onSelectionChange={(sel) => {
                  const next = [...sel][0];
                  if (next !== undefined) setFilter(String(next) as Filter);
                }}
                className="w-full overflow-x-auto sm:w-auto"
              >
                {(["all", "server", "person", "link"] as Filter[]).map((f) => (
                  <SegmentedControlItem key={f} id={f} className="min-h-9 flex-1 sm:min-h-0 sm:flex-none">
                    {FILTER_LABEL[f]} {counts(f)}
                  </SegmentedControlItem>
                ))}
              </SegmentedControl>
            </div>
            {shares.length === 0 ? (
              <EmptyState compact icon={RiBarChartBoxLine} title="No shares here" body="Shares you give to servers, people and links show their use here." />
            ) : (
              <ul className="flex flex-col gap-3">
                {shares.map((a) => (
                  <ShareCard key={a.id} a={a} maxCost={maxCost} maxReq={maxReq} />
                ))}
              </ul>
            )}
          </section>
        </>
      )}
    </div>
  );
}
