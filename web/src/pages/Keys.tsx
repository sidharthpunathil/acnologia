import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Activity, ChevronRight, KeyRound, RefreshCw, Search, X } from "lucide-react";
import type { KeyRow, KeySummary, Validity } from "../lib/types";
import { errorMessage } from "../lib/api";
import { can } from "../lib/perms";
import { num, plural } from "../lib/format";
import { useAuth } from "../stores/auth";
import { useInsights } from "../stores/insights";
import { useServices } from "../stores/services";
import { useProviders } from "../stores/providers";
import { toast } from "../stores/toast";
import { useNow } from "../hooks/useNow";
import { useTitle } from "../hooks/useTitle";
import { EntityLogo, ServiceLogo } from "../components/EntityLogo";
import { StatusPill } from "../components/StatusPill";
import { QuotaMeter, TimeAgo, ValidityBadge } from "../components/KeyBits";
import { Button, Callout, Empty, IconButton, PageHeader, PageSkeleton, Select, Spinner, cx } from "../components/ui";
import { InUse } from "./service/KeysTable";
import { KeyDrawer } from "./service/KeyDrawer";
import { withLiveStatus } from "./service/keyUtil";

type Sort = "last_used" | "quota" | "validity" | "label";
type Only = "" | "low_quota" | "unused_7d";

const SORTS: { id: Sort; label: string }[] = [
  { id: "last_used", label: "Last used" },
  { id: "quota", label: "Least quota left" },
  { id: "validity", label: "Problems first" },
  { id: "label", label: "Name" },
];

const WEEK = 7 * 24 * 3600 * 1000;
const t = (iso: string | null) => (iso ? Date.parse(iso) || 0 : 0);
const frac = (k: KeyRow) => (k.quota_remaining === null || k.quota_limit === null || k.quota_limit === 0 ? 2 : k.quota_remaining / k.quota_limit);
const isLow = (k: KeyRow) => k.quota_remaining !== null && k.quota_limit !== null && k.quota_limit > 0 && k.quota_remaining < 0.1 * k.quota_limit;
const isUnused = (k: KeyRow, now: number) => !k.last_used_at || t(k.last_used_at) < now - WEEK;
const RANK: Record<string, number> = { invalid: 0, unknown: 1, valid: 2 };

/** Same rules as the server summary, computed from the live rows so tiles never lag behind a check. */
function summarize(rows: KeyRow[], now: number): KeySummary {
  const s: KeySummary = { total: rows.length, valid: 0, invalid: 0, unknown: 0, low_quota: 0, unused_7d: 0 };
  for (const k of rows) {
    if (k.validity === "valid" || k.validity === "invalid" || k.validity === "unknown") s[k.validity]++;
    if (isLow(k)) s.low_quota++;
    if (isUnused(k, now)) s.unused_7d++;
  }
  return s;
}

function Tile({
  label,
  value,
  hint,
  active,
  tone,
  onClick,
}: {
  label: string;
  value: number;
  hint: string;
  active: boolean;
  tone?: "green" | "red" | "amber";
  onClick: () => void;
}) {
  const color = value > 0 && tone ? { green: "text-emerald-700", red: "text-red-700", amber: "text-amber-700" }[tone] : "text-ink";
  return (
    <button
      type="button"
      aria-pressed={active}
      title={hint}
      onClick={onClick}
      className={cx(
        "min-w-0 rounded-card px-4 py-3 text-left shadow-card transition-[background-color,box-shadow,transform] duration-150 ease-out active:scale-[0.98]",
        active ? "bg-surface ring-2 ring-ink" : "bg-surface hover:shadow-lift",
      )}
    >
      <div className="truncate text-[12px] font-medium text-muted">{label}</div>
      <div className={cx("mt-1 font-mono text-xl tabular-nums", color)}>{num(value)}</div>
    </button>
  );
}

function RowLogo({ k }: { k: KeyRow }) {
  const service = useServices((s) => s.services.find((x) => x.id === k.service_id));
  return service ? <ServiceLogo service={service} size="md" /> : <EntityLogo src={k.service_logo_url} name={k.service_name} size="md" />;
}

export function KeysPage() {
  useTitle("Keys");
  const role = useAuth((s) => s.user?.role);
  const manage = can.manage(role);
  const { rows, loaded, error, fetch, checkAll } = useInsights();
  const services = useServices((s) => s.services);
  const servicesLoaded = useServices((s) => s.loaded);
  const fetchServices = useServices((s) => s.fetch);
  const ensureProviders = useProviders((s) => s.ensure);
  const now = useNow(1000);
  const [params, setParams] = useSearchParams();
  const [refreshing, setRefreshing] = useState(false);
  const [checking, setChecking] = useState<number | null>(null);
  const [lastCheck, setLastCheck] = useState<{ message: string; clean: boolean } | null>(null);
  const [openKey, setOpenKey] = useState<KeyRow | null>(null);

  const q = params.get("q") ?? "";
  const serviceId = params.get("service") ?? "";
  const validity = (params.get("validity") ?? "") as Validity | "";
  const only = (params.get("only") ?? "") as Only;
  const sort = (SORTS.some((s) => s.id === params.get("sort")) ? params.get("sort") : "last_used") as Sort;

  const setParam = useCallback(
    (patch: Record<string, string>) => {
      const next = new URLSearchParams(params);
      for (const [k, v] of Object.entries(patch)) {
        if (v) next.set(k, v);
        else next.delete(k);
      }
      setParams(next, { replace: true });
    },
    [params, setParams],
  );

  const load = useCallback(
    async (quiet: boolean) => {
      if (!quiet) setRefreshing(true);
      try {
        await fetch();
      } catch (e) {
        if (!quiet) toast.error(errorMessage(e));
      } finally {
        if (!quiet) setRefreshing(false);
      }
    },
    [fetch],
  );

  useEffect(() => {
    void load(true);
    const id = window.setInterval(() => document.visibilityState === "visible" && void load(true), 30000);
    return () => window.clearInterval(id);
  }, [load]);

  useEffect(() => {
    // Service limits and logos back the in use counts and tiles.
    if (!servicesLoaded) fetchServices().catch(() => undefined);
    ensureProviders().catch(() => undefined);
  }, [servicesLoaded, fetchServices, ensureProviders]);

  const live = useMemo(() => rows.map((k) => withLiveStatus(k, now) as KeyRow), [rows, now]);
  const summary = useMemo(() => summarize(rows, now), [rows, now]);
  const serviceOptions = useMemo(() => {
    const m = new Map<number, string>();
    for (const k of rows) m.set(k.service_id, k.service_name);
    return [...m.entries()].sort((a, b) => a[1].localeCompare(b[1]));
  }, [rows]);
  const maxBy = useMemo(() => Object.fromEntries(services.map((s) => [s.id, s.max_concurrency ?? 0])), [services]);

  const shown = useMemo(() => {
    const n = q.trim().toLowerCase();
    let list = live.filter((k) => {
      if (serviceId && String(k.service_id) !== serviceId) return false;
      if (validity && k.validity !== validity) return false;
      if (only === "low_quota" && !isLow(k)) return false;
      if (only === "unused_7d" && !isUnused(k, now)) return false;
      if (n && ![k.label, k.hint, k.service_name, k.service_slug, k.notes].some((f) => f?.toLowerCase().includes(n))) return false;
      return true;
    });
    list = [...list];
    if (sort === "quota") list.sort((a, b) => frac(a) - frac(b));
    else if (sort === "validity") list.sort((a, b) => (RANK[a.validity] ?? 1) - (RANK[b.validity] ?? 1) || t(b.last_used_at) - t(a.last_used_at));
    else if (sort === "label") list.sort((a, b) => (a.label || a.hint).localeCompare(b.label || b.hint));
    else list.sort((a, b) => t(b.last_used_at) - t(a.last_used_at));
    return list;
  }, [live, q, serviceId, validity, only, sort, now]);

  const filtered = !!(q || serviceId || validity || only);
  const clear = () => setParams(sort !== "last_used" ? new URLSearchParams({ sort }) : new URLSearchParams(), { replace: true });

  const runCheckAll = async () => {
    setChecking(Date.now());
    setLastCheck(null);
    try {
      const res = await checkAll();
      const clean = res.invalid === 0 && res.failed === 0;
      setLastCheck({ message: res.message, clean });
      if (clean) toast.success(res.message);
      else toast.info(res.message);
      await fetch().catch(() => undefined);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setChecking(null);
    }
  };

  if (!loaded) return <PageSkeleton variant="list" tiles={6} />;

  const tiles: Array<{ key: string; label: string; value: number; hint: string; tone?: "green" | "red" | "amber"; active: boolean; apply: () => void }> = [
    { key: "all", label: "All keys", value: summary.total, hint: "Show every key", active: !validity && !only, apply: () => setParam({ validity: "", only: "" }) },
    { key: "valid", label: "Valid", value: summary.valid, hint: "Worked recently or passed a check", tone: "green", active: validity === "valid", apply: () => setParam({ validity: validity === "valid" ? "" : "valid", only: "" }) },
    { key: "invalid", label: "Invalid", value: summary.invalid, hint: "Rejected by the provider", tone: "red", active: validity === "invalid", apply: () => setParam({ validity: validity === "invalid" ? "" : "invalid", only: "" }) },
    { key: "unknown", label: "Unknown", value: summary.unknown, hint: "Not checked or used yet", active: validity === "unknown", apply: () => setParam({ validity: validity === "unknown" ? "" : "unknown", only: "" }) },
    { key: "low", label: "Low quota", value: summary.low_quota, hint: "Less than 10% of the quota left", tone: "amber", active: only === "low_quota", apply: () => setParam({ only: only === "low_quota" ? "" : "low_quota", validity: "" }) },
    { key: "unused", label: "Unused 7 days", value: summary.unused_7d, hint: "Not used in the last 7 days", active: only === "unused_7d", apply: () => setParam({ only: only === "unused_7d" ? "" : "unused_7d", validity: "" }) },
  ];

  return (
    <div>
      <PageHeader
        title="Keys"
        meta={`${plural(summary.total, "key")} across ${plural(serviceOptions.length, "service")} · ${summary.invalid ? `${summary.invalid} need attention` : "none rejected"}`}
        actions={
          <>
            <IconButton label="Refresh" onClick={() => void load(false)} disabled={refreshing}>
              {refreshing ? <Spinner /> : <RefreshCw className="h-4 w-4" />}
            </IconButton>
            {manage && rows.length > 0 && (
              <Button variant="primary" icon={<Activity className="h-4 w-4" />} loading={checking !== null} onClick={() => void runCheckAll()}>
                {checking !== null ? "Checking keys" : "Check all keys"}
              </Button>
            )}
          </>
        }
      />

      {checking !== null && (
        <div className="mb-6 rounded-card bg-surface p-4 shadow-card" role="status" aria-live="polite">
          <div className="flex items-center justify-between gap-3 text-[13px]">
            <span className="text-ink">Checking {plural(summary.total, "key")}, four at a time.</span>
            <span className="font-mono text-xs tabular-nums text-muted">{Math.max(0, Math.round((now - checking) / 1000))}s</span>
          </div>
          <div className="mt-3 h-1.5 overflow-hidden rounded-full bg-well" aria-hidden="true">
            <div className="progress-indeterminate h-full w-2/5 rounded-full bg-ink" />
          </div>
          <p className="mt-2 text-xs text-muted">Large pools can take up to 3 minutes. You can leave this page; the check keeps running.</p>
        </div>
      )}
      {lastCheck && checking === null && (
        <Callout tone={lastCheck.clean ? "green" : "amber"} className="mb-6">
          <div className="flex items-start justify-between gap-3">
            <span>{lastCheck.message}</span>
            <button type="button" onClick={() => setLastCheck(null)} aria-label="Dismiss" className="icon-btn -my-1 -mr-1 inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-lg opacity-70 hover:opacity-100">
              <X className="h-3.5 w-3.5" />
            </button>
          </div>
        </Callout>
      )}

      {error && rows.length === 0 ? (
        <Callout tone="red">
          Could not load keys: {error}.{" "}
          <button type="button" className="font-medium underline underline-offset-2" onClick={() => void load(false)}>
            Try again
          </button>
        </Callout>
      ) : rows.length === 0 ? (
        <Empty
          icon={<KeyRound className="h-5 w-5" />}
          title="No keys yet"
          body={manage ? "Add a service from the catalog, then paste in its keys. They all show up here with their health and quota." : "No keys have been added yet. Ask an admin to add some."}
          action={
            <Link to="/" className="tap inline-flex h-10 items-center rounded-full bg-ink px-4 text-sm font-medium text-on-ink hover:bg-ink-strong">
              Go to catalog
            </Link>
          }
        />
      ) : (
        <>
          <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-3 lg:grid-cols-6" role="group" aria-label="Filter by health">
            {tiles.map((tl) => (
              <Tile key={tl.key} label={tl.label} value={tl.value} hint={tl.hint} tone={tl.tone} active={tl.active} onClick={tl.apply} />
            ))}
          </div>

          <div className="mt-6 flex flex-col gap-2 lg:flex-row lg:items-center">
            <div className="relative min-w-0 lg:flex-1">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted" aria-hidden="true" />
              <input
                type="search"
                value={q}
                onChange={(e) => setParam({ q: e.target.value })}
                placeholder="Search by label, hint, service or note"
                aria-label="Search keys"
                enterKeyHint="search"
                className="field-input h-10 w-full rounded-xl border border-line bg-surface pl-9 pr-3 text-sm text-ink placeholder:text-faint focus:border-ink/30 focus:outline-none focus:ring-2 focus:ring-ink/10"
              />
            </div>
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:flex">
              <Select value={serviceId} onChange={(e) => setParam({ service: e.target.value })} aria-label="Service" className="col-span-2 sm:col-span-1 lg:w-48">
                <option value="">All services</option>
                {serviceOptions.map(([id, name]) => (
                  <option key={id} value={id}>
                    {name}
                  </option>
                ))}
              </Select>
              <Select value={validity} onChange={(e) => setParam({ validity: e.target.value, only: "" })} aria-label="Validity" className="lg:w-40">
                <option value="">Any validity</option>
                <option value="valid">Valid</option>
                <option value="invalid">Invalid</option>
                <option value="unknown">Unknown</option>
              </Select>
              <Select value={sort} onChange={(e) => setParam({ sort: e.target.value === "last_used" ? "" : e.target.value })} aria-label="Sort by" className="lg:w-44">
                {SORTS.map((s) => (
                  <option key={s.id} value={s.id}>
                    Sort: {s.label}
                  </option>
                ))}
              </Select>
            </div>
          </div>

          <div className="mt-3 flex min-h-[28px] items-center justify-between gap-3 font-mono text-xs text-muted">
            <span aria-live="polite">
              {filtered ? `${plural(shown.length, "key")} match` : `${plural(shown.length, "key")}`}
            </span>
            {filtered && (
              <button type="button" onClick={clear} className="tap inline-flex items-center gap-1 rounded-full px-2 font-sans text-[13px] font-medium text-ink hover:bg-ink/[0.06]">
                <X className="h-3.5 w-3.5" aria-hidden="true" /> Clear filters
              </button>
            )}
          </div>

          {shown.length === 0 ? (
            <Empty
              className="mt-3"
              title="No keys match these filters"
              body="Try another search, or clear the filters to see every key."
              action={<Button onClick={clear}>Clear filters</Button>}
            />
          ) : (
            <>
              {/* Large screens: a table. Each row opens the key details. */}
              <div className="mt-3 hidden overflow-hidden rounded-card bg-surface shadow-card lg:block">
                <table className="w-full table-fixed text-left text-[13px]">
                  <colgroup>
                    <col className="w-[30%]" />
                    <col className="w-[17%]" />
                    <col className="w-[16%]" />
                    <col className="w-[14%]" />
                    <col className="w-[19%]" />
                    <col className="w-[4%]" />
                  </colgroup>
                  <thead>
                    <tr className="border-b border-line font-mono text-[11px] uppercase tracking-wide text-faint">
                      <th className="px-4 py-3 font-normal">Key</th>
                      <th className="px-3 py-3 font-normal">Works?</th>
                      <th className="px-3 py-3 font-normal">Status</th>
                      <th className="px-3 py-3 font-normal">Last used</th>
                      <th className="px-3 py-3 font-normal">Quota</th>
                      <th className="py-3">
                        <span className="sr-only">Open</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-line">
                    {shown.map((k) => (
                      <tr
                        key={k.id}
                        tabIndex={0}
                        aria-label={`Open details for ${k.label || k.hint}, ${k.service_name}`}
                        onClick={() => !window.getSelection()?.toString() && setOpenKey(k)}
                        onKeyDown={(e) => {
                          if (e.target === e.currentTarget && (e.key === "Enter" || e.key === " ")) {
                            e.preventDefault();
                            setOpenKey(k);
                          }
                        }}
                        className="cursor-pointer align-middle transition-colors hover:bg-soft focus-visible:bg-soft focus-visible:outline-none"
                      >
                        <td className="px-4 py-3">
                          <div className="flex min-w-0 items-center gap-3">
                            <RowLogo k={k} />
                            <div className="min-w-0">
                              <div className="truncate font-medium text-ink">{k.label || "Untitled"}</div>
                              <div className="truncate font-mono text-[11px] text-muted">
                                {k.hint} · {k.service_name}
                              </div>
                            </div>
                          </div>
                        </td>
                        <td className="px-3 py-3">
                          <ValidityBadge validity={k.validity} reason={k.validity_reason} />
                          <div className="mt-1 truncate text-[11px] text-muted" title={k.validity_reason}>
                            {k.validity_reason}
                          </div>
                        </td>
                        <td className="px-3 py-3">
                          <div className="flex flex-col items-start gap-1">
                            <StatusPill k={k} now={now} />
                            <InUse k={k} serviceMax={maxBy[k.service_id] ?? 0} />
                          </div>
                        </td>
                        <td className="px-3 py-3 font-mono text-xs">
                          <TimeAgo iso={k.last_used_at} now={now} className="text-ink" />
                          <div className="mt-0.5 truncate text-[11px] text-muted">
                            worked <TimeAgo iso={k.last_ok_at} now={now} />
                          </div>
                        </td>
                        <td className="px-3 py-3">
                          <QuotaMeter k={k} unit={k.quota_unit} now={now} compact />
                        </td>
                        <td className="py-3 pr-3">
                          <ChevronRight className="h-4 w-4 text-faint" aria-hidden="true" />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              {/* Phones and tablets: cards. */}
              <ul className="mt-3 grid grid-cols-1 gap-2.5 md:grid-cols-2 lg:hidden">
                {shown.map((k) => (
                  <li key={k.id}>
                    <button
                      type="button"
                      onClick={() => setOpenKey(k)}
                      className="flex h-full w-full flex-col gap-3 rounded-card bg-surface p-4 text-left shadow-card transition-transform duration-150 ease-out active:scale-[0.99]"
                    >
                      <div className="flex w-full items-start gap-3">
                        <RowLogo k={k} />
                        <div className="min-w-0 flex-1">
                          <div className="break-words font-medium text-ink">{k.label || "Untitled"}</div>
                          <div className="wrap-anywhere font-mono text-[11px] text-muted">
                            {k.hint} · {k.service_name}
                          </div>
                        </div>
                        <ChevronRight className="mt-0.5 h-4 w-4 shrink-0 text-faint" aria-hidden="true" />
                      </div>
                      <div className="flex flex-wrap items-center gap-1.5">
                        <ValidityBadge validity={k.validity} />
                        <StatusPill k={k} now={now} />
                        <InUse k={k} serviceMax={maxBy[k.service_id] ?? 0} />
                      </div>
                      {k.validity_reason && <div className="-mt-1 text-[12px] text-muted">{k.validity_reason}</div>}
                      <QuotaMeter k={k} unit={k.quota_unit} now={now} compact />
                      <div className="font-mono text-[11px] text-muted">
                        used <TimeAgo iso={k.last_used_at} now={now} /> · worked <TimeAgo iso={k.last_ok_at} now={now} />
                      </div>
                    </button>
                  </li>
                ))}
              </ul>
            </>
          )}
        </>
      )}

      <KeyDrawer keyId={openKey?.id ?? null} seed={openKey} onClose={() => setOpenKey(null)} />
    </div>
  );
}
