import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link as RouterLink, useSearchParams } from "react-router-dom";
import { ToggleButton } from "react-aria-components";
import { RiArrowRightSLine, RiCloseLine, RiPulseLine, RiRefreshLine } from "@remixicon/react";
import { AcnApiKey } from "@/components/foundations/icons/acn";
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
import { AccountLine, QuotaMeter, TimeAgo, providerGivesReset, quotaInfo } from "../components/KeyBits";
import { usePoll } from "../hooks/usePoll";
import { Button } from "@/components/base/buttons/button";
import { Pagination } from "@/components/base/pagination/pagination";
import { ProgressTrack } from "@/components/base/progress/progress";
import { SearchField } from "@/components/base/search-field/search-field";
import { Select, SelectItem } from "@/components/base/select/select";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Table, TableBody, TableCell, TableColumn, TableHeader, TableRow } from "@/components/base/table/table";
import { cx } from "@/utils/cx";
import { KeyHealth } from "./service/KeysTable";
import { KeyDrawer } from "./service/KeyDrawer";
import { withLiveStatus } from "./service/keyUtil";
import { HandoverChip, useLoadHandovers } from "../components/HandoverChip";
import { Callout, EmptyState, PageHeader, StatTile, type StatTone } from "@/components/acn";
import { ActionIcon, CARD, Tip } from "./service/parts";
import { KeyRingArt, SearchArt } from "@/components/foundations/illustrations";

type Sort = "last_used" | "quota" | "validity" | "label";
type Only = "" | "low_quota" | "unused_7d";

const SORTS: { id: Sort; label: string }[] = [
  { id: "last_used", label: "Last used" },
  { id: "quota", label: "Least quota left" },
  { id: "validity", label: "Problems first" },
  { id: "label", label: "Name" },
];

/** Rows per page. Every row re-renders each second for its live times, so long pools page. */
const PAGE_SIZE = 50;

const WEEK = 7 * 24 * 3600 * 1000;
const t = (iso: string | null) => (iso ? Date.parse(iso) || 0 : 0);
const frac = (k: KeyRow) => (k.quota_remaining === null || k.quota_limit === null || k.quota_limit === 0 ? 2 : k.quota_remaining / k.quota_limit);
const isLow = (k: KeyRow) => k.quota_remaining !== null && k.quota_limit !== null && k.quota_limit > 0 && k.quota_remaining < 0.1 * k.quota_limit;
const isUnused = (k: KeyRow, now: number) => !k.last_used_at || t(k.last_used_at) < now - WEEK;
const CHIP_TONE: Record<StatTone, string> = {
  neutral: "text-text-primary",
  success: "text-status-lime-text",
  warning: "text-status-yellow-text",
  error: "text-status-rose-text",
  accent: "text-accent-fg",
};
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
  useLoadHandovers();
  const [params, setParams] = useSearchParams();
  const [refreshing, setRefreshing] = useState(false);
  const [checking, setChecking] = useState<number | null>(null);
  const [lastCheck, setLastCheck] = useState<{ message: string; clean: boolean } | null>(null);
  const [openKey, setOpenKey] = useState<KeyRow | null>(null);
  const [page, setPage] = useState(1);
  const listTop = useRef<HTMLDivElement>(null);

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
  }, [load]);
  // Every 4s while the tab is visible, so in use, busy and cooldowns stay live.
  usePoll(() => load(true), 4000);

  useEffect(() => {
    // Service limits and logos back the in use counts and tiles.
    if (!servicesLoaded) fetchServices().catch(() => undefined);
    ensureProviders().catch(() => undefined);
  }, [servicesLoaded, fetchServices, ensureProviders]);

  const live = useMemo(() => rows.map((k) => withLiveStatus(k, now) as KeyRow), [rows, now]);
  const needle = q.trim().toLowerCase();
  // Service and search narrow everything, tiles included; the tile and validity filters apply on top.
  const scoped = useMemo(
    () =>
      live.filter((k) => {
        if (serviceId && String(k.service_id) !== serviceId) return false;
        if (needle && ![k.label, k.hint, k.service_name, k.service_slug, k.notes, k.account_name, k.account_email, k.account_phone, k.account_ref].some((f) => f?.toLowerCase().includes(needle))) return false;
        return true;
      }),
    [live, serviceId, needle],
  );
  const summary = useMemo(() => summarize(scoped, now), [scoped, now]);
  const allInvalid = rows.filter((k) => k.validity === "invalid").length;
  const providers = useProviders((s) => s.providers);
  const resetBy = useMemo(() => {
    const out: Record<number, boolean | undefined> = {};
    for (const s of services) out[s.id] = providerGivesReset(providers.find((p) => p.slug === s.provider));
    return out;
  }, [services, providers]);
  const serviceOptions = useMemo(() => {
    const m = new Map<number, string>();
    for (const k of rows) m.set(k.service_id, k.service_name);
    return [...m.entries()].sort((a, b) => a[1].localeCompare(b[1]));
  }, [rows]);
  const maxBy = useMemo(() => Object.fromEntries(services.map((s) => [s.id, s.max_concurrency ?? 0])), [services]);

  const shown = useMemo(() => {
    let list = scoped.filter((k) => {
      if (validity && k.validity !== validity) return false;
      if (only === "low_quota" && !isLow(k)) return false;
      if (only === "unused_7d" && !isUnused(k, now)) return false;
      return true;
    });
    list = [...list];
    // Last used to the second, then the key id, so keys used in the same second keep a stable order.
    const recent = (a: KeyRow, b: KeyRow) =>
      Math.floor(t(b.last_used_at) / 1000) - Math.floor(t(a.last_used_at) / 1000) || a.id - b.id;
    if (sort === "quota") list.sort((a, b) => frac(a) - frac(b) || a.id - b.id);
    else if (sort === "validity") list.sort((a, b) => (RANK[a.validity] ?? 1) - (RANK[b.validity] ?? 1) || recent(a, b));
    else if (sort === "label") list.sort((a, b) => (a.label || a.hint).localeCompare(b.label || b.hint) || a.id - b.id);
    else list.sort(recent);
    return list;
  }, [scoped, validity, only, sort, now]);

  // A new filter or sort starts from the first page.
  useEffect(() => setPage(1), [q, serviceId, validity, only, sort]);
  const pageCount = Math.max(1, Math.ceil(shown.length / PAGE_SIZE));
  const curPage = Math.min(page, pageCount);
  const paged = pageCount > 1 ? shown.slice((curPage - 1) * PAGE_SIZE, curPage * PAGE_SIZE) : shown;
  const goTo = (p: number) => {
    setPage(p);
    // Bring the top of the list back into view when the pager sits below the fold.
    const el = listTop.current;
    if (el && el.getBoundingClientRect().top < 0) el.scrollIntoView({ block: "start" });
  };

  const filtered = !!(q || serviceId || validity || only);
  const clear = () => setParams(sort !== "last_used" ? new URLSearchParams({ sort }) : new URLSearchParams(), { replace: true });

  const runCheckAll = async () => {
    setChecking(Date.now());
    setLastCheck(null);
    try {
      const res = await checkAll();
      const clean = res.invalid === 0 && res.failed === 0 && (res.exhausted ?? 0) === 0;
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

  if (!loaded) return <KeysSkeleton />;

  const tiles: Array<{ key: string; label: string; value: number; hint: string; tone?: StatTone; active: boolean; apply: () => void }> = [
    { key: "all", label: "All keys", value: summary.total, hint: "Show every key", active: !validity && !only, apply: () => setParam({ validity: "", only: "" }) },
    { key: "valid", label: "Valid", value: summary.valid, hint: "Worked recently or passed a check", tone: "success", active: validity === "valid", apply: () => setParam({ validity: validity === "valid" ? "" : "valid", only: "" }) },
    { key: "invalid", label: "Invalid", value: summary.invalid, hint: "Rejected by the provider", tone: "error", active: validity === "invalid", apply: () => setParam({ validity: validity === "invalid" ? "" : "invalid", only: "" }) },
    { key: "unknown", label: "Unknown", value: summary.unknown, hint: "Not checked or used yet", active: validity === "unknown", apply: () => setParam({ validity: validity === "unknown" ? "" : "unknown", only: "" }) },
    { key: "low", label: "Low quota", value: summary.low_quota, hint: "Less than 10% of the quota left", tone: "warning", active: only === "low_quota", apply: () => setParam({ only: only === "low_quota" ? "" : "low_quota", validity: "" }) },
    { key: "unused", label: "Unused 7 days", value: summary.unused_7d, hint: "Not used in the last 7 days", active: only === "unused_7d", apply: () => setParam({ only: only === "unused_7d" ? "" : "unused_7d", validity: "" }) },
  ];

  return (
    <div>
      <PageHeader
        title="Keys"
        meta={
          // A failed load has nothing true to count; the callout below says what went wrong.
          error && rows.length === 0
            ? undefined
            : `${plural(rows.length, "key")} across ${plural(serviceOptions.length, "service")} · ${allInvalid ? `${num(allInvalid)} invalid` : "all valid"}`
        }
        actions={
          <>
            <ActionIcon icon={RiRefreshLine} label="Refresh" size="medium" busy={refreshing} disabled={refreshing} onClick={() => void load(false)} />
            {manage && rows.length > 0 && (
              <Button
                variant="primary"
                leadingIcon={checking !== null ? ButtonSpinner : RiPulseLine}
                disabled={checking !== null}
                onClick={() => void runCheckAll()}
                className="flex-1 sm:flex-none"
              >
                {checking !== null ? "Checking keys" : "Check all keys"}
              </Button>
            )}
          </>
        }
      />

      {checking !== null && (
        <div className={cx(CARD, "mb-6 p-4 sm:px-5")} role="status" aria-live="polite">
          <div className="flex items-center justify-between gap-3 text-body-2-regular">
            <span className="text-text-primary">Checking {plural(rows.length, "key")}, four at a time.</span>
            <span className="font-mono text-caption-1-regular tabular-nums text-text-secondary">{Math.max(0, Math.round((now - checking) / 1000))}s</span>
          </div>
          <ProgressTrack percentage={0} isIndeterminate size="sm" className="mt-3" />
          <p className="mt-2 text-caption-1-regular text-text-secondary">Large pools can take up to 3 minutes. You can leave this page; the check keeps running.</p>
        </div>
      )}
      {lastCheck && checking === null && (
        <Callout
          tone={lastCheck.clean ? "success" : "warning"}
          className="mb-6"
          action={<ActionIcon icon={RiCloseLine} label="Dismiss" onClick={() => setLastCheck(null)} className="border-transparent bg-transparent shadow-none" />}
        >
          {lastCheck.message}
        </Callout>
      )}

      {error && rows.length === 0 ? (
        <Callout
          tone="error"
          action={
            <Button variant="secondary" size="small" onClick={() => void load(false)}>
              Try again
            </Button>
          }
        >
          Could not load your keys. The server said: {error.replace(/\.$/, "")}.
        </Callout>
      ) : rows.length === 0 ? (
        <EmptyState
          art={KeyRingArt}
          as="h2"
          icon={AcnApiKey}
          title="No keys yet"
          body={manage ? "Add a service from the catalog, then paste in its keys. They all show up here with their health and quota." : "No keys have been added yet. Ask an admin to add some."}
          action={
            <RouterLink
              to="/"
              className="inline-flex h-9 items-center rounded-2lg bg-button-primary px-3 text-body-medium text-text-white shadow-xs outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring focus-visible:ring-offset-2"
            >
              Go to catalog
            </RouterLink>
          }
        />
      ) : (
        <>
          {/* Phones: one compact row of chips, so the list starts on the first screen. */}
          <div
            className="-mx-4 flex gap-1.5 overflow-x-auto px-4 pb-1 [scrollbar-width:none] sm:hidden [&::-webkit-scrollbar]:hidden"
            role="group"
            aria-label="Filter by health"
          >
            {tiles.map((tl) => (
              <ToggleButton
                key={tl.key}
                isSelected={tl.active}
                onChange={tl.apply}
                aria-label={`${tl.label}: ${num(tl.value)}. ${tl.hint}`}
                className={({ isSelected, isFocusVisible }) =>
                  cx(
                    "inline-flex h-9 shrink-0 cursor-pointer items-center gap-1.5 rounded-full border px-3 text-body-2-medium outline-none transition-colors duration-150 ease-out",
                    isSelected
                      ? "border-accent-line bg-accent-subtle text-text-primary"
                      : "border-border-button-default bg-background-primary-default text-text-secondary active:bg-background-primary-active",
                    isFocusVisible && "ring-2 ring-border-focus-ring",
                  )
                }
              >
                {tl.label}
                <span className={cx("font-mono tabular-nums", tl.value > 0 && tl.tone ? CHIP_TONE[tl.tone] : "text-text-primary")}>{num(tl.value)}</span>
              </ToggleButton>
            ))}
          </div>

          <div className="hidden grid-cols-2 gap-2.5 sm:grid sm:grid-cols-3 lg:grid-cols-6" role="group" aria-label="Filter by health">
            {tiles.map((tl) => (
              <Tip key={tl.key} label={tl.hint} aria>
                <StatTile
                  label={tl.label}
                  value={num(tl.value)}
                  tone={tl.value > 0 ? tl.tone : undefined}
                  isSelected={tl.active}
                  onChange={tl.apply}
                />
              </Tip>
            ))}
          </div>

          <div className="mt-4 flex flex-col gap-2 sm:mt-6 lg:flex-row lg:items-center">
            <SearchField
              value={q}
              onChange={(v) => setParam({ q: v })}
              placeholder="Search keys"
              aria-label="Search keys"
              enterKeyHint="search"
              className="min-w-0 lg:flex-1"
              fieldClassName="border border-border-button-default bg-background-primary-default"
            />
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:flex">
              <Select
                aria-label="Service"
                selectedKey={serviceId || "all"}
                onSelectionChange={(k) => setParam({ service: k === "all" ? "" : String(k) })}
                className="col-span-2 sm:col-span-1 lg:w-48"
              >
                <SelectItem id="all">All services</SelectItem>
                {serviceOptions.map(([id, name]) => (
                  <SelectItem key={id} id={String(id)} textValue={name}>
                    {name}
                  </SelectItem>
                ))}
              </Select>
              <Select
                aria-label="Validity"
                selectedKey={validity || "any"}
                onSelectionChange={(k) => setParam({ validity: k === "any" ? "" : String(k), only: "" })}
                className="lg:w-40"
              >
                <SelectItem id="any">Any validity</SelectItem>
                <SelectItem id="valid">Valid</SelectItem>
                <SelectItem id="invalid">Invalid</SelectItem>
                <SelectItem id="unknown">Unknown</SelectItem>
              </Select>
              <Select
                aria-label="Sort by"
                selectedKey={sort}
                onSelectionChange={(k) => setParam({ sort: k === "last_used" ? "" : String(k) })}
                className="lg:w-48"
              >
                {SORTS.map((s) => (
                  <SelectItem key={s.id} id={s.id} textValue={s.label}>
                    Sort: {s.label}
                  </SelectItem>
                ))}
              </Select>
            </div>
          </div>

          <div ref={listTop} className="mt-3 flex min-h-8 scroll-mt-4 items-center justify-between gap-3 font-mono text-caption-1-regular text-text-secondary">
            <span aria-live="polite">
              {filtered ? `${plural(shown.length, "key")} match` : `${plural(shown.length, "key")}`}
              {pageCount > 1 && ` · ${num((curPage - 1) * PAGE_SIZE + 1)}–${num((curPage - 1) * PAGE_SIZE + paged.length)} shown`}
            </span>
            {filtered && (
              <Button variant="ghost" size="small" leadingIcon={RiCloseLine} onClick={clear}>
                Clear filters
              </Button>
            )}
          </div>

          {shown.length === 0 ? (
            <EmptyState
              art={SearchArt}
              className="mt-3"
              as="h2"
              title="No keys match these filters"
              body="Try another search, or clear the filters to see every key."
              action={
                <Button variant="secondary" onClick={clear}>
                  Clear filters
                </Button>
              }
            />
          ) : (
            <>
              {/* Large screens: a table. Each row opens the key details. */}
              <div className={cx(CARD, "mt-3 hidden overflow-hidden lg:block")}>
                <Table
                  aria-label="Keys"
                  size="sm"
                  containerClassName="-mt-px"
                  className="table-fixed"
                  onRowAction={(id) => {
                    if (window.getSelection()?.toString()) return;
                    const k = shown.find((x) => x.id === Number(id));
                    if (k) setOpenKey(k);
                  }}
                >
                  <TableHeader>
                    <TableColumn isRowHeader className="w-[31%] ps-5">
                      Key
                    </TableColumn>
                    <TableColumn className="w-[31%]">Health</TableColumn>
                    <TableColumn className="w-[15%]">Last used</TableColumn>
                    <TableColumn className="w-[19%]">Quota</TableColumn>
                    <TableColumn className="w-[4%]">
                      <span className="sr-only">Open</span>
                    </TableColumn>
                  </TableHeader>
                  <TableBody items={paged}>
                    {(k) => (
                      <TableRow
                        id={k.id}
                        textValue={`${k.label || k.hint}, ${k.service_name}`}
                        className="group cursor-pointer align-top hover:bg-background-secondary-default"
                      >
                        <TableCell className="py-3 ps-5 align-top">
                          <div className="flex min-w-0 items-center gap-3">
                            <RowLogo k={k} />
                            <div className="min-w-0">
                              <div className="truncate text-body-2-medium text-text-primary" title={k.label || undefined}>
                                {k.label || "Untitled"}
                              </div>
                              <HandoverChip k={k} className="my-0.5" />
                              <AccountLine k={k} />
                              <div className="truncate font-mono text-caption-1-regular text-text-secondary">
                                {k.hint} · {k.service_name}
                              </div>
                            </div>
                          </div>
                        </TableCell>
                        <TableCell className="py-3 align-top">
                          <KeyHealth k={k} now={now} serviceMax={maxBy[k.service_id] ?? 0} />
                        </TableCell>
                        <TableCell className="py-3 align-top font-mono">
                          <TimeAgo iso={k.last_used_at} now={now} className="block truncate text-caption-1-regular text-text-primary" />
                          <div className="mt-0.5 truncate text-caption-1-regular text-text-secondary">
                            worked <TimeAgo iso={k.last_ok_at} now={now} />
                          </div>
                        </TableCell>
                        <TableCell className="py-3 align-top">
                          <QuotaMeter k={k} unit={k.quota_unit} now={now} compact resetExact={resetBy[k.service_id]} />
                        </TableCell>
                        <TableCell className="py-3 pe-3 align-top">
                          <RiArrowRightSLine
                            className="mt-0.5 size-4 text-foreground-icon-tertiary transition-transform duration-150 ease-out group-hover:translate-x-0.5 rtl:rotate-180"
                            aria-hidden
                          />
                        </TableCell>
                      </TableRow>
                    )}
                  </TableBody>
                </Table>
              </div>

              {/* Phones and tablets: cards. */}
              <ul className="mt-3 grid grid-cols-1 gap-2.5 md:grid-cols-2 lg:hidden">
                {paged.map((k) => (
                  <li key={k.id}>
                    <button
                      type="button"
                      onClick={() => setOpenKey(k)}
                      aria-label={`Open details for ${k.label || k.hint}, ${k.service_name}`}
                      className={cx(
                        CARD,
                        "flex h-full w-full min-w-0 cursor-pointer flex-col gap-3 p-4 text-start outline-none transition-colors duration-150 ease",
                        "hover:bg-background-primary-hover active:bg-background-primary-active focus-visible:ring-2 focus-visible:ring-border-focus-ring",
                      )}
                    >
                      <div className="flex w-full items-start gap-3">
                        <RowLogo k={k} />
                        <div className="min-w-0 flex-1">
                          <div className="wrap-anywhere text-body-medium text-text-primary">{k.label || "Untitled"}</div>
                          <HandoverChip k={k} className="my-0.5" />
                          <AccountLine k={k} wrap />
                          <div className="wrap-anywhere line-clamp-2 font-mono text-caption-1-regular text-text-secondary">
                            {k.hint} · {k.service_name}
                          </div>
                        </div>
                        <RiArrowRightSLine className="mt-0.5 size-4 shrink-0 text-foreground-icon-tertiary rtl:rotate-180" aria-hidden />
                      </div>
                      <KeyHealth k={k} now={now} serviceMax={maxBy[k.service_id] ?? 0} />
                      {quotaInfo(k, k.quota_unit) && <QuotaMeter k={k} unit={k.quota_unit} now={now} compact resetExact={resetBy[k.service_id]} />}
                      <div className="font-mono text-caption-1-regular text-text-secondary">
                        used <TimeAgo iso={k.last_used_at} now={now} /> · worked <TimeAgo iso={k.last_ok_at} now={now} />
                      </div>
                    </button>
                  </li>
                ))}
              </ul>
              <Pagination page={curPage} totalPages={pageCount} onChange={goTo} aria-label="Key pages" className="mt-4" />
            </>
          )}
        </>
      )}

      <KeyDrawer keyId={openKey?.id ?? null} seed={openKey} onClose={() => setOpenKey(null)} />
    </div>
  );
}

function KeysSkeleton() {
  return (
    <div role="status" aria-label="Loading keys">
      <Skeleton shape="text" className="h-7 w-24" />
      <Skeleton shape="text" className="mt-2 h-3.5 w-64" />
      <div className="mt-8 flex gap-1.5 overflow-hidden sm:hidden">
        {[0, 1, 2, 3].map((i) => (
          <Skeleton key={i} className="h-9 w-24 shrink-0 rounded-full" />
        ))}
      </div>
      <div className="mt-8 hidden grid-cols-2 gap-2.5 sm:grid sm:grid-cols-3 lg:grid-cols-6">
        {[0, 1, 2, 3, 4, 5].map((i) => (
          <Skeleton key={i} className="h-[74px] rounded-3xl" />
        ))}
      </div>
      <Skeleton className="mt-6 h-9 w-full rounded-full" />
      <div className={cx(CARD, "mt-6")}>
        {[0, 1, 2, 3].map((i) => (
          <div key={i} className="flex items-center gap-3 border-b border-separator-border px-5 py-4 last:border-b-0">
            <Skeleton className="size-10" />
            <Skeleton shape="text" className="h-4 w-40" />
          </div>
        ))}
      </div>
    </div>
  );
}
