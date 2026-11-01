import { useCallback, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { BarChart3, Info, Plug, RotateCw, Sparkles } from "lucide-react";
import { useTitle } from "../hooks/useTitle";
import { errorMessage } from "../lib/api";
import { compact, num, plural } from "../lib/format";
import { fetchUsage, usd, usdAxis, type AiUsageRow, type ProviderUsageRow, type UsageReport } from "../lib/usage";
import { Button, Callout, Empty, PageHeader, Section, Segmented, Skeleton, Spinner, Stat, cx } from "../components/ui";

type Range = "7" | "30" | "90";
const RANGES: { id: Range; label: string }[] = [
  { id: "7", label: "7 days" },
  { id: "30", label: "30 days" },
  { id: "90", label: "90 days" },
];

const FEATURE_LABEL: Record<string, string> = {
  builder: "Provider builder",
  assistant: "Assistant",
  fill: "Fill with AI",
};

/** "Oct 4" for a UTC YYYY-MM-DD day. */
function dayLabel(day: string, withWeekday = false): string {
  const d = new Date(day + "T00:00:00Z");
  return d.toLocaleDateString("en-US", {
    timeZone: "UTC",
    month: "short",
    day: "numeric",
    ...(withWeekday ? { weekday: "short" } : {}),
  });
}

export function UsagePage() {
  useTitle("Usage");
  const [range, setRange] = useState<Range>("30");
  const [data, setData] = useState<UsageReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const seq = useRef(0);

  const load = useCallback(async (days: number) => {
    const mine = ++seq.current;
    setLoading(true);
    setError(null);
    try {
      const r = await fetchUsage(days);
      if (mine === seq.current) setData(r);
    } catch (e) {
      if (mine === seq.current) setError(errorMessage(e));
    } finally {
      if (mine === seq.current) setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load(Number(range));
  }, [load, range]);

  const switcher = <Segmented items={RANGES} value={range} onChange={setRange} label="Date range" block className="sm:inline-flex sm:w-auto" />;

  return (
    <div>
      <PageHeader
        title="Usage"
        meta={
          data ? (
            <span className="inline-flex items-center gap-2">
              {`${dayLabel(data.since)} to ${dayLabel(data.until)}, UTC`}
              {loading && <Spinner className="h-3 w-3" />}
            </span>
          ) : (
            "What the Claude API and your providers have cost"
          )
        }
        actions={switcher}
      />

      {!data && error ? (
        <Empty
          icon={<BarChart3 className="h-5 w-5" />}
          title="Usage could not load"
          body={error}
          action={
            <Button icon={<RotateCw className="h-4 w-4" />} onClick={() => void load(Number(range))}>
              Try again
            </Button>
          }
        />
      ) : !data ? (
        <UsageSkeleton />
      ) : (
        // Refetching keeps the previous numbers on screen, dimmed, so nothing jumps.
        <div className={cx("space-y-5 transition-opacity duration-150", loading && "opacity-60")} aria-busy={loading}>
          <Tiles data={data} />
          {data.event_retention_days > 0 && data.days > data.event_retention_days && (
            <Callout icon={<Info className="h-4 w-4" />}>
              Provider spend only goes back {plural(data.event_retention_days, "day")}, the event retention set by COGITO_EVENT_RETENTION_DAYS. Claude
              spend is kept for the whole range.
            </Callout>
          )}
          <Section title="Daily spend" description="Claude API and provider spend per UTC day, stacked.">
            <SpendChart data={data} />
          </Section>
          <Windows data={data} />
          <AiSection data={data} />
          <ProviderSection data={data} />
        </div>
      )}
    </div>
  );
}

function UsageSkeleton() {
  return (
    <div role="status" aria-live="polite" aria-busy="true" className="space-y-5">
      <span className="sr-only">Loading usage</span>
      <div className="grid grid-cols-2 gap-2.5 lg:grid-cols-4">
        {Array.from({ length: 4 }).map((_, i) => (
          <Skeleton key={i} className="h-[78px] rounded-card" />
        ))}
      </div>
      <Skeleton className="h-[300px] rounded-card" />
      <Skeleton className="h-[220px] rounded-card" />
    </div>
  );
}

// ---- headline tiles ----

function Tiles({ data }: { data: UsageReport }) {
  const r = data.totals.range;
  const today = data.totals.today;
  const served = r.requests + r.cache_hits;
  const hitShare = served > 0 ? Math.round((r.cache_hits / served) * 100) : 0;
  return (
    <div className="grid grid-cols-2 gap-2.5 lg:grid-cols-4">
      <Stat label="Claude spend" value={usd(r.ai_cost_usd)} sub={`today ${usd(today.ai_cost_usd)}`} />
      <Stat label="Provider spend" value={usd(r.provider_cost_usd)} sub={`today ${usd(today.provider_cost_usd)}`} />
      <Stat label="Requests" value={compact(r.requests)} sub={`today ${num(today.requests)}`} />
      <Stat label="Cache hits saved" value={compact(r.cache_hits)} sub={served > 0 ? `${hitShare}% of requests` : "no traffic yet"} />
    </div>
  );
}

// ---- fixed windows ----

function Windows({ data }: { data: UsageReport }) {
  const rows: { label: string; key: "today" | "7d" | "30d" }[] = [
    { label: "Today", key: "today" },
    { label: "Last 7 days", key: "7d" },
    { label: "Last 30 days", key: "30d" },
  ];
  return (
    <Section title="Totals" description="Fixed windows, whatever the range above. Days are UTC.">
      <div className="-mx-1 overflow-x-auto">
        <table className="w-full min-w-[320px] text-left text-[13px]">
          <thead>
            <tr className="border-b border-line font-mono text-[11px] uppercase tracking-wide text-faint">
              <th scope="col" className="px-1 py-2 font-normal">
                Window
              </th>
              <th scope="col" className="px-1 py-2 text-right font-normal">
                Claude
              </th>
              <th scope="col" className="px-1 py-2 text-right font-normal">
                Provider
              </th>
              <th scope="col" className="hidden px-1 py-2 text-right font-normal sm:table-cell">
                Requests
              </th>
              <th scope="col" className="hidden px-1 py-2 text-right font-normal sm:table-cell">
                Cache hits
              </th>
            </tr>
          </thead>
          <tbody>
            {rows.map((w) => {
              const t = data.totals[w.key];
              return (
                <tr key={w.key} className="border-b border-line last:border-0">
                  <th scope="row" className="px-1 py-2.5 font-medium text-ink">
                    {w.label}
                  </th>
                  <td className="px-1 py-2.5 text-right font-mono tabular-nums text-ink">{usd(t.ai_cost_usd)}</td>
                  <td className="px-1 py-2.5 text-right font-mono tabular-nums text-ink">{usd(t.provider_cost_usd)}</td>
                  <td className="hidden px-1 py-2.5 text-right font-mono tabular-nums text-muted sm:table-cell">{num(t.requests)}</td>
                  <td className="hidden px-1 py-2.5 text-right font-mono tabular-nums text-muted sm:table-cell">{num(t.cache_hits)}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </Section>
  );
}

// ---- chart ----

interface DayPoint {
  day: string;
  ai: number;
  prov: number;
}

/** Rounds up to 1, 2 or 5 times a power of ten so ticks land on clean numbers. */
function niceMax(v: number): number {
  if (v <= 0) return 1;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 2, 5, 10]) if (m * p >= v) return m * p;
  return 10 * p;
}

/** A rect with 4px rounded corners at the top only: the data end, square at the baseline. */
function topRounded(x: number, y: number, w: number, h: number, r: number): string {
  const rr = Math.max(0, Math.min(r, w / 2, h));
  return `M${x},${y + h}V${y + rr}Q${x},${y} ${x + rr},${y}H${x + w - rr}Q${x + w},${y} ${x + w},${y + rr}V${y + h}Z`;
}

function useWidth<T extends HTMLElement>() {
  const ref = useRef<T>(null);
  const [w, setW] = useState(0);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver((entries) => setW(Math.floor(entries[0].contentRect.width)));
    ro.observe(el);
    setW(Math.floor(el.getBoundingClientRect().width));
    return () => ro.disconnect();
  }, []);
  return [ref, w] as const;
}

const H = 200;
const PAD = { l: 48, r: 4, t: 10, b: 24 };
const GAP = 2; // surface gap between stacked segments

function SpendChart({ data }: { data: UsageReport }) {
  const points: DayPoint[] = useMemo(
    () => data.ai.by_day.map((a, i) => ({ day: a.key, ai: a.cost_usd, prov: data.provider.by_day[i]?.cost_usd ?? 0 })),
    [data],
  );
  const [boxRef, width] = useWidth<HTMLDivElement>();
  const [active, setActive] = useState<number | null>(null);
  const descId = useId();

  const totalAi = points.reduce((s, p) => s + p.ai, 0);
  const totalProv = points.reduce((s, p) => s + p.prov, 0);
  const peak = points.reduce<DayPoint | null>((b, p) => (!b || p.ai + p.prov > b.ai + b.prov ? p : b), null);
  const empty = totalAi + totalProv === 0;

  const summary = empty
    ? `No spend in the last ${plural(points.length, "day")}.`
    : `Over the last ${plural(points.length, "day")}, Claude API spend was ${usd(totalAi)} and provider spend was ${usd(totalProv)}. ` +
      (peak ? `The highest day was ${dayLabel(peak.day, true)} at ${usd(peak.ai + peak.prov)}.` : "");

  if (empty) {
    return (
      <Empty
        compact
        icon={<BarChart3 className="h-5 w-5" />}
        title="No spend in this range"
        body="Claude calls and priced provider requests will draw a bar per day here."
      />
    );
  }

  const n = points.length;
  const plotW = Math.max(0, width - PAD.l - PAD.r);
  const plotH = H - PAD.t - PAD.b;
  const top = niceMax(Math.max(...points.map((p) => p.ai + p.prov)));
  const ticks = [0, top / 2, top];
  const slot = plotW / n;
  const barW = Math.max(1, Math.min(24, slot - GAP));
  const y = (v: number) => (v / top) * plotH;
  const base = PAD.t + plotH;
  const xLabels = n > 1 ? [0, Math.floor((n - 1) / 2), n - 1] : [0];

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

  const a = active !== null ? points[active] : null;
  const tipX = active !== null ? PAD.l + slot * active + slot / 2 : 0;
  // Keep the 168px tooltip inside the chart.
  const tipLeft = Math.min(Math.max(tipX, 84), Math.max(84, width - 84));

  return (
    <div>
      <div className="mb-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-[12px] text-muted" aria-hidden="true">
        <span className="inline-flex items-center gap-1.5">
          <span className="h-2.5 w-2.5 rounded-[3px] bg-brand" />
          Claude API
        </span>
        <span className="inline-flex items-center gap-1.5">
          <span className="h-2.5 w-2.5 rounded-[3px] bg-spark" />
          Provider APIs
        </span>
      </div>
      <p id={descId} className="sr-only">
        {summary} Use the left and right arrow keys to read each day.
      </p>
      <div
        ref={boxRef}
        tabIndex={0}
        role="group"
        aria-label="Daily spend chart"
        aria-describedby={descId}
        onKeyDown={onKey}
        onFocus={() => setActive((v) => v ?? n - 1)}
        onBlur={() => setActive(null)}
        className="relative rounded-lg outline-hidden focus-visible:ring-2 focus-visible:ring-ink/30 focus-visible:ring-offset-2 focus-visible:ring-offset-surface"
      >
        {width > 0 && (
          <svg width={width} height={H} className="block select-none" aria-hidden="true" onPointerLeave={() => setActive(null)}>
            {ticks.map((t) => (
              <g key={t}>
                <line x1={PAD.l} x2={width - PAD.r} y1={base - y(t)} y2={base - y(t)} className="stroke-line" strokeWidth={1} shapeRendering="crispEdges" />
                <text x={PAD.l - 8} y={base - y(t)} dy="0.32em" textAnchor="end" className="fill-faint font-mono text-[10px] tabular-nums">
                  {usdAxis(t)}
                </text>
              </g>
            ))}
            {points.map((p, i) => {
              const x = PAD.l + slot * i + (slot - barW) / 2;
              const ha = p.ai > 0 ? Math.max(1, y(p.ai)) : 0;
              const hp = p.prov > 0 ? Math.max(1, y(p.prov)) : 0;
              const gap = ha > 0 && hp > 0 ? GAP : 0;
              const dim = active !== null && active !== i;
              return (
                <g key={p.day} className="transition-opacity duration-150" style={{ opacity: dim ? 0.4 : 1 }}>
                  {ha > 0 && <path d={topRounded(x, base - ha, barW, ha, hp > 0 ? 0 : 4)} className="fill-brand" />}
                  {hp > 0 && <path d={topRounded(x, base - ha - gap - hp, barW, hp, 4)} className="fill-spark" />}
                </g>
              );
            })}
            {xLabels.map((i, k) => (
              <text
                key={i}
                x={PAD.l + slot * i + slot / 2}
                y={H - 6}
                textAnchor={xLabels.length === 1 ? "middle" : k === 0 ? "start" : k === xLabels.length - 1 ? "end" : "middle"}
                className="fill-faint font-mono text-[10px]"
              >
                {dayLabel(points[i].day)}
              </text>
            ))}
            {/* Hit targets: the whole column, wider than the bar. */}
            {points.map((p, i) => (
              <rect
                key={p.day}
                x={PAD.l + slot * i}
                y={PAD.t}
                width={slot}
                height={plotH}
                fill="transparent"
                onPointerEnter={() => setActive(i)}
                onPointerMove={() => setActive(i)}
              />
            ))}
          </svg>
        )}
        {a && (
          <div
            className="pointer-events-none absolute top-0 z-10 w-[168px] -translate-x-1/2 rounded-xl bg-surface px-3 py-2.5 text-[12px] shadow-card ring-1 ring-line"
            style={{ left: tipLeft }}
          >
            <div className="text-[11px] text-muted">{dayLabel(a.day, true)}</div>
            <TipRow swatch="bg-brand" label="Claude" value={usd(a.ai)} />
            <TipRow swatch="bg-spark" label="Provider" value={usd(a.prov)} />
            <div className="mt-1.5 flex items-baseline justify-between border-t border-line pt-1.5">
              <span className="text-muted">Total</span>
              <span className="font-mono font-medium tabular-nums text-ink">{usd(a.ai + a.prov)}</span>
            </div>
          </div>
        )}
      </div>
      <div className="sr-only" aria-live="polite">
        {a ? `${dayLabel(a.day, true)}: Claude ${usd(a.ai)}, provider ${usd(a.prov)}, total ${usd(a.ai + a.prov)}.` : ""}
      </div>
      <DailyTable points={points} />
    </div>
  );
}

function TipRow({ swatch, label, value }: { swatch: string; label: string; value: string }) {
  return (
    <div className="mt-1 flex items-baseline justify-between gap-3">
      <span className="inline-flex items-center gap-1.5 text-muted">
        <span className={cx("h-0.5 w-2.5 rounded-full", swatch)} aria-hidden="true" />
        {label}
      </span>
      <span className="font-mono font-medium tabular-nums text-ink">{value}</span>
    </div>
  );
}

/** The chart's values as a table, so nothing is reachable only by hovering. */
function DailyTable({ points }: { points: DayPoint[] }) {
  const rows = points.filter((p) => p.ai + p.prov > 0).reverse();
  return (
    <details className="group mt-4">
      <summary className="tap inline-flex cursor-pointer select-none items-center gap-1 rounded-md text-[12px] text-muted transition-colors duration-150 hover:text-ink">
        <span className="inline-block transition-transform duration-150 group-open:rotate-90" aria-hidden="true">
          ›
        </span>
        Daily values ({plural(rows.length, "day")} with spend)
      </summary>
      <table className="mt-2 w-full text-left text-[13px]">
        <thead>
          <tr className="border-b border-line font-mono text-[11px] uppercase tracking-wide text-faint">
            <th scope="col" className="py-2 font-normal">
              Day
            </th>
            <th scope="col" className="py-2 text-right font-normal">
              Claude
            </th>
            <th scope="col" className="py-2 text-right font-normal">
              Provider
            </th>
            <th scope="col" className="py-2 text-right font-normal">
              Total
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((p) => (
            <tr key={p.day} className="border-b border-line last:border-0">
              <th scope="row" className="py-2 font-normal text-ink">
                {dayLabel(p.day, true)}
              </th>
              <td className="py-2 text-right font-mono tabular-nums text-muted">{usd(p.ai)}</td>
              <td className="py-2 text-right font-mono tabular-nums text-muted">{usd(p.prov)}</td>
              <td className="py-2 text-right font-mono tabular-nums text-ink">{usd(p.ai + p.prov)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </details>
  );
}

// ---- tables ----

function Th({ children, right, wide }: { children: ReactNode; right?: boolean; wide?: boolean }) {
  return (
    <th scope="col" className={cx("px-1 py-2 font-normal", right && "text-right", wide && "hidden md:table-cell")}>
      {children}
    </th>
  );
}

function Td({ children, wide, strong }: { children: ReactNode; wide?: boolean; strong?: boolean }) {
  return (
    <td className={cx("px-1 py-2.5 text-right font-mono tabular-nums", strong ? "text-ink" : "text-muted", wide && "hidden md:table-cell")}>{children}</td>
  );
}

/** Thin share-of-cost bar under a row's name. */
function Share({ part, whole, tone }: { part: number; whole: number; tone: "bg-brand" | "bg-spark" }) {
  const pct = whole > 0 ? Math.max(part > 0 ? 2 : 0, (part / whole) * 100) : 0;
  return (
    <div className="mt-1.5 h-1 w-full max-w-[160px] overflow-hidden rounded-full bg-well" aria-hidden="true">
      <div className={cx("h-full rounded-full", tone)} style={{ width: `${pct}%` }} />
    </div>
  );
}

type AiView = "feature" | "model" | "user";

function AiSection({ data }: { data: UsageReport }) {
  const [view, setView] = useState<AiView>("feature");
  const rows: AiUsageRow[] = view === "feature" ? data.ai.by_feature : view === "model" ? data.ai.by_model : data.ai.by_user;
  const total = data.totals.range.ai_cost_usd;
  const p = data.ai_price;
  const $ = (n: number) => (Number.isInteger(n) ? `$${n}` : `$${n.toFixed(2)}`);
  const label = (r: AiUsageRow) => (view === "feature" ? (FEATURE_LABEL[r.key] ?? r.key) : r.key || "Unknown");
  const head = view === "feature" ? "Feature" : view === "model" ? "Model" : "User";

  return (
    <Section
      title="Claude API"
      description={`Every call the provider builder, the Assistant and Fill with AI made with your ANTHROPIC_API_KEY. Priced at ${$(p.input)} input, ${$(p.cache_write)} cache write, ${$(p.cache_read)} cache read and ${$(p.output)} output per million tokens, ${$(p.web_search)} per 1,000 web searches.`}
      actions={
        data.ai.by_feature.length > 0 ? (
          <Segmented
            size="sm"
            label="Group Claude spend by"
            value={view}
            onChange={setView}
            items={[
              { id: "feature", label: "Feature" },
              { id: "model", label: "Model" },
              { id: "user", label: "User" },
            ]}
          />
        ) : undefined
      }
    >
      {rows.length === 0 ? (
        data.ai_enabled ? (
          <Empty
            compact
            icon={<Sparkles className="h-5 w-5" />}
            title="No Claude calls in this range"
            body="The provider builder, the Assistant and Fill with AI record each call here with its tokens and cost."
          />
        ) : (
          <Empty
            compact
            icon={<Sparkles className="h-5 w-5" />}
            title="No Claude calls yet. AI features are off"
            body="Set ANTHROPIC_API_KEY and restart Acnologia to turn on the provider builder, the Assistant and Fill with AI. What they cost shows up here."
          />
        )
      ) : (
        <>
          {!data.ai_enabled && (
            <Callout className="mb-4" icon={<Info className="h-4 w-4" />}>
              AI features are off now. These calls were made while ANTHROPIC_API_KEY was set.
            </Callout>
          )}
          <div className="-mx-1 overflow-x-auto">
            <table className="w-full min-w-[300px] text-left text-[13px]">
              <thead>
                <tr className="border-b border-line font-mono text-[11px] uppercase tracking-wide text-faint">
                  <Th>{head}</Th>
                  <Th right>Calls</Th>
                  <Th right wide>
                    Input
                  </Th>
                  <Th right wide>
                    Cache write
                  </Th>
                  <Th right wide>
                    Cache read
                  </Th>
                  <Th right wide>
                    Output
                  </Th>
                  <Th right wide>
                    Searches
                  </Th>
                  <Th right>Cost</Th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => (
                  <tr key={r.key} className="border-b border-line last:border-0">
                    <th scope="row" className="max-w-0 px-1 py-2.5 font-normal sm:max-w-none">
                      <div className={cx("truncate text-ink", view !== "feature" && "font-mono text-[12px]")} title={label(r)}>
                        {label(r)}
                      </div>
                      <Share part={r.cost_usd} whole={total} tone="bg-brand" />
                    </th>
                    <Td>{num(r.calls)}</Td>
                    <Td wide>{compact(r.input_tokens)}</Td>
                    <Td wide>{compact(r.cache_write_tokens)}</Td>
                    <Td wide>{compact(r.cache_read_tokens)}</Td>
                    <Td wide>{compact(r.output_tokens)}</Td>
                    <Td wide>{num(r.web_searches)}</Td>
                    <Td strong>{usd(r.cost_usd)}</Td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </Section>
  );
}

type ProvView = "service" | "app";

function ProviderSection({ data }: { data: UsageReport }) {
  const [view, setView] = useState<ProvView>("service");
  const rows: ProviderUsageRow[] = view === "service" ? data.provider.by_service : data.provider.by_app;
  const t = data.totals.range;
  const unpriced = t.requests > 0 && t.provider_cost_usd === 0;

  return (
    <Section
      title="Provider APIs"
      description="Requests through /proxy and key leases, priced per successful call. Cache hits never reach the provider, so they cost nothing."
      actions={
        rows.length > 0 || data.provider.by_app.length > 0 ? (
          <Segmented
            size="sm"
            label="Group provider spend by"
            value={view}
            onChange={setView}
            items={[
              { id: "service", label: "Service" },
              { id: "app", label: "App" },
            ]}
          />
        ) : undefined
      }
    >
      {unpriced && (
        <Callout className="mb-4" tone="amber" icon={<Info className="h-4 w-4" />}>
          Provider spend needs cost_per_request on providers. Set quota.cost_per_request in the provider spec, or a cost per request on the service,
          and each successful call is priced from then on.
        </Callout>
      )}
      {rows.length === 0 ? (
        <Empty
          compact
          icon={<Plug className="h-5 w-5" />}
          title="No provider requests in this range"
          body="Call a service through /proxy or lease a key from an app. Spend shows up here once providers have cost_per_request set."
        />
      ) : (
        <div className="-mx-1 overflow-x-auto">
          <table className="w-full min-w-[300px] text-left text-[13px]">
            <thead>
              <tr className="border-b border-line font-mono text-[11px] uppercase tracking-wide text-faint">
                <Th>{view === "service" ? "Service" : "App"}</Th>
                <Th right>Requests</Th>
                <Th right wide>
                  Cache hits
                </Th>
                <Th right>Cost</Th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.key} className="border-b border-line last:border-0">
                  <th scope="row" className="max-w-0 px-1 py-2.5 font-normal sm:max-w-none">
                    <div className="truncate text-ink" title={r.name || r.key}>
                      {r.name || r.key}
                      {view === "service" && r.name && r.name !== r.key && (
                        <span className="ml-1.5 hidden font-mono text-[11px] text-faint sm:inline">{r.key}</span>
                      )}
                    </div>
                    <Share part={r.cost_usd} whole={t.provider_cost_usd} tone="bg-spark" />
                  </th>
                  <Td>{num(r.requests)}</Td>
                  <Td wide>{num(r.cache_hits)}</Td>
                  <Td strong>{usd(r.cost_usd)}</Td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Section>
  );
}
