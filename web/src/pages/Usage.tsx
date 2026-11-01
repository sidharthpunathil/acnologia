import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { RiBarChartBoxLine, RiRefreshLine, RiSparkling2Line } from "@remixicon/react";
import { AcnProxy } from "@/components/foundations/icons/acn";
import type { Key } from "react-aria-components";
import { Button } from "@/components/base/buttons/button";
import { SegmentedControl, SegmentedControlItem } from "@/components/base/segmented-control/segmented-control";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { Spinner } from "@/components/base/spinner/spinner";
import { Table, TableBody, TableCell, TableColumn, TableHeader, TableRow } from "@/components/base/table/table";
import { cx } from "@/utils/cx";
import { useTitle } from "../hooks/useTitle";
import { errorMessage } from "../lib/api";
import { compact, num, plural } from "../lib/format";
import { fetchUsage, usd, type AiUsageRow, type ProviderUsageRow, type UsageReport } from "../lib/usage";
import { Callout, EmptyState, PageHeader, Section, StatTile } from "@/components/acn";
import { SpendChartCard, dayLabel } from "./usage/SpendChartCard";
import { UnpluggedArt } from "@/components/foundations/illustrations";

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

/** A single select segmented control over string ids. */
function Segments<T extends string>({
  items,
  value,
  onChange,
  label,
  className,
}: {
  items: { id: T; label: string }[];
  value: T;
  onChange: (v: T) => void;
  label: string;
  className?: string;
}) {
  return (
    <SegmentedControl
      aria-label={label}
      selectedKeys={new Set<Key>([value])}
      onSelectionChange={(keys) => {
        const next = [...keys][0];
        if (next !== undefined) onChange(String(next) as T);
      }}
      className={className}
    >
      {items.map((it) => (
        <SegmentedControlItem key={it.id} id={it.id} className="min-h-11 flex-1 sm:min-h-0 sm:flex-none">
          {it.label}
        </SegmentedControlItem>
      ))}
    </SegmentedControl>
  );
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

  return (
    <div>
      <PageHeader
        title="Usage"
        meta={
          data ? (
            <span className="inline-flex items-center gap-2">
              {`${dayLabel(data.since)} to ${dayLabel(data.until)}, UTC`}
              {loading && <Spinner size="xs" aria-label="Refreshing usage" />}
            </span>
          ) : (
            "What the Claude API and your providers have cost"
          )
        }
        actions={<Segments items={RANGES} value={range} onChange={setRange} label="Date range" className="w-full sm:w-auto" />}
      />

      {!data && error ? (
        <EmptyState
          icon={RiBarChartBoxLine}
          title="Usage could not load"
          body={error}
          action={
            <Button variant="secondary" leadingIcon={RiRefreshLine} onClick={() => void load(Number(range))}>
              Try again
            </Button>
          }
        />
      ) : !data ? (
        <UsageSkeleton />
      ) : (
        // Refetching keeps the previous numbers on screen, dimmed, so nothing jumps.
        <div className={cx("flex flex-col gap-5 transition-opacity duration-150 ease-out", loading && "opacity-60")} aria-busy={loading}>
          <Tiles data={data} />
          {data.event_retention_days > 0 && data.days > data.event_retention_days && (
            <Callout tone="info">
              Provider spend only goes back {plural(data.event_retention_days, "day")}, the event retention set by COGITO_EVENT_RETENTION_DAYS. Claude
              spend is kept for the whole range.
            </Callout>
          )}
          <SpendChartCard data={data} />
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
    <div role="status" aria-live="polite" aria-busy="true" className="flex flex-col gap-5">
      <span className="sr-only">Loading usage</span>
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        {Array.from({ length: 4 }).map((_, i) => (
          <Skeleton key={i} className="h-[100px] rounded-2xl" />
        ))}
      </div>
      <Skeleton className="h-[372px] rounded-3xl" />
      <Skeleton className="h-[220px] rounded-3xl" />
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
    <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
      <StatTile label="Claude spend" value={usd(r.ai_cost_usd)} sub={`today ${usd(today.ai_cost_usd)}`} />
      <StatTile label="Provider spend" value={usd(r.provider_cost_usd)} sub={`today ${usd(today.provider_cost_usd)}`} />
      <StatTile label="Requests" value={compact(r.requests)} sub={`today ${num(today.requests)}`} />
      <StatTile
        label="Cache hits saved"
        value={compact(r.cache_hits)}
        sub={served > 0 ? `${hitShare}% of requests` : "no traffic yet"}
      />
    </div>
  );
}

// ---- tables ----

/** Right aligned numeric cell content. */
function Num({ children, strong }: { children: ReactNode; strong?: boolean }) {
  return <span className={cx("block text-end font-mono tabular-nums", strong ? "text-text-primary" : "text-text-secondary")}>{children}</span>;
}

/** Header label aligned with the numbers under it. */
function End({ children }: { children: ReactNode }) {
  return <span className="block text-end">{children}</span>;
}

/** Columns hidden on phones; their cells use the same class. */
const WIDE = "hidden md:table-cell";

/** The table inside a card: hairline frame, rounded to sit inside rounded-3xl. */
function TableFrame({ children }: { children: ReactNode }) {
  return <div className="overflow-hidden rounded-2xl border border-border-table">{children}</div>;
}

function Windows({ data }: { data: UsageReport }) {
  const rows: { label: string; key: "today" | "7d" | "30d" }[] = [
    { label: "Today", key: "today" },
    { label: "Last 7 days", key: "7d" },
    { label: "Last 30 days", key: "30d" },
  ];
  return (
    <Section title="Totals" description="Fixed windows, whatever the range above. Days are UTC.">
      <TableFrame>
        <Table size="sm" aria-label="Spend totals by window">
          <TableHeader>
            <TableColumn isRowHeader>Window</TableColumn>
            <TableColumn>
              <End>Claude</End>
            </TableColumn>
            <TableColumn>
              <End>Provider</End>
            </TableColumn>
            <TableColumn className={WIDE}>
              <End>Requests</End>
            </TableColumn>
            <TableColumn className={WIDE}>
              <End>Cache hits</End>
            </TableColumn>
          </TableHeader>
          <TableBody>
            {rows.map((w) => {
              const t = data.totals[w.key];
              return (
                <TableRow key={w.key} id={w.key}>
                  <TableCell>{w.label}</TableCell>
                  <TableCell>
                    <Num strong>{usd(t.ai_cost_usd)}</Num>
                  </TableCell>
                  <TableCell>
                    <Num strong>{usd(t.provider_cost_usd)}</Num>
                  </TableCell>
                  <TableCell className={WIDE}>
                    <Num>{num(t.requests)}</Num>
                  </TableCell>
                  <TableCell className={WIDE}>
                    <Num>{num(t.cache_hits)}</Num>
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </TableFrame>
    </Section>
  );
}

/** Thin share of cost bar under a row's name, in the series colour. */
function Share({ part, whole, tone }: { part: number; whole: number; tone: "bg-chart-1" | "bg-chart-2" }) {
  const pct = whole > 0 ? Math.max(part > 0 ? 2 : 0, (part / whole) * 100) : 0;
  return (
    <div className="mt-1.5 h-1 w-full max-w-[160px] overflow-hidden rounded-full bg-chart-track" aria-hidden="true">
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
          <Segments
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
          <EmptyState
            compact
            icon={RiSparkling2Line}
            title="No Claude calls in this range"
            body="The provider builder, the Assistant and Fill with AI record each call here with its tokens and cost."
          />
        ) : (
          <EmptyState
            compact
            icon={RiSparkling2Line}
            title="No Claude calls yet. AI features are off"
            body="Set ANTHROPIC_API_KEY and restart Acnologia to turn on the provider builder, the Assistant and Fill with AI. What they cost shows up here."
          />
        )
      ) : (
        <div className="flex flex-col gap-4">
          {!data.ai_enabled && <Callout tone="info">AI features are off now. These calls were made while ANTHROPIC_API_KEY was set.</Callout>}
          <TableFrame>
            <Table size="sm" aria-label={`Claude spend by ${head.toLowerCase()}`}>
              <TableHeader>
                <TableColumn isRowHeader>{head}</TableColumn>
                <TableColumn>
                  <End>Calls</End>
                </TableColumn>
                <TableColumn className={WIDE}>
                  <End>Input</End>
                </TableColumn>
                <TableColumn className={WIDE}>
                  <End>Cache write</End>
                </TableColumn>
                <TableColumn className={WIDE}>
                  <End>Cache read</End>
                </TableColumn>
                <TableColumn className={WIDE}>
                  <End>Output</End>
                </TableColumn>
                <TableColumn className={WIDE}>
                  <End>Searches</End>
                </TableColumn>
                <TableColumn>
                  <End>Cost</End>
                </TableColumn>
              </TableHeader>
              <TableBody>
                {rows.map((r) => (
                  <TableRow key={r.key || "unknown"} id={r.key || "unknown"}>
                    <TableCell className="w-full max-w-0">
                      <div className={cx("truncate", view !== "feature" && "font-mono text-body-2-regular")} title={label(r)}>
                        {label(r)}
                      </div>
                      <Share part={r.cost_usd} whole={total} tone="bg-chart-1" />
                    </TableCell>
                    <TableCell>
                      <Num>{num(r.calls)}</Num>
                    </TableCell>
                    <TableCell className={WIDE}>
                      <Num>{compact(r.input_tokens)}</Num>
                    </TableCell>
                    <TableCell className={WIDE}>
                      <Num>{compact(r.cache_write_tokens)}</Num>
                    </TableCell>
                    <TableCell className={WIDE}>
                      <Num>{compact(r.cache_read_tokens)}</Num>
                    </TableCell>
                    <TableCell className={WIDE}>
                      <Num>{compact(r.output_tokens)}</Num>
                    </TableCell>
                    <TableCell className={WIDE}>
                      <Num>{num(r.web_searches)}</Num>
                    </TableCell>
                    <TableCell>
                      <Num strong>{usd(r.cost_usd)}</Num>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TableFrame>
        </div>
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
          <Segments
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
      <div className="flex flex-col gap-4">
        {unpriced && (
          <Callout tone="warning">
            Provider spend needs cost_per_request on providers. Set quota.cost_per_request in the provider spec, or a cost per request on the service,
            and each successful call is priced from then on.
          </Callout>
        )}
        {rows.length === 0 ? (
          <EmptyState
            art={UnpluggedArt}
            compact
            icon={AcnProxy}
            title="No provider requests in this range"
            body="Call a service through /proxy or lease a key from an app. Spend shows up here once providers have cost_per_request set."
          />
        ) : (
          <TableFrame>
            <Table size="sm" aria-label={`Provider spend by ${view}`}>
              <TableHeader>
                <TableColumn isRowHeader>{view === "service" ? "Service" : "App"}</TableColumn>
                <TableColumn>
                  <End>Requests</End>
                </TableColumn>
                <TableColumn className={WIDE}>
                  <End>Cache hits</End>
                </TableColumn>
                <TableColumn>
                  <End>Cost</End>
                </TableColumn>
              </TableHeader>
              <TableBody>
                {rows.map((r) => (
                  <TableRow key={r.key} id={r.key}>
                    <TableCell className="w-full max-w-0">
                      <div className="truncate" title={r.name || r.key}>
                        {r.name || r.key}
                        {view === "service" && r.name && r.name !== r.key && (
                          <span className="ms-1.5 hidden font-mono text-caption-1-regular text-text-tertiary sm:inline">{r.key}</span>
                        )}
                      </div>
                      <Share part={r.cost_usd} whole={t.provider_cost_usd} tone="bg-chart-2" />
                    </TableCell>
                    <TableCell>
                      <Num>{num(r.requests)}</Num>
                    </TableCell>
                    <TableCell className={WIDE}>
                      <Num>{num(r.cache_hits)}</Num>
                    </TableCell>
                    <TableCell>
                      <Num strong>{usd(r.cost_usd)}</Num>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TableFrame>
        )}
      </div>
    </Section>
  );
}
