import { useCallback, useEffect, useMemo, useState } from "react";
import { Link as RouterLink, useParams } from "react-router-dom";
import {
  RiAddLine,
  RiFileList3Line,
  RiInformationLine,
  RiPulseLine,
  RiRefreshLine,
} from "@remixicon/react";
import { AcnKeyPool } from "@/components/foundations/icons/acn";
import { useTitle } from "../../hooks/useTitle";
import { useServices } from "../../stores/services";
import { useProviders } from "../../stores/providers";
import { useKeys } from "../../stores/keys";
import { useApps } from "../../stores/apps";
import { useEvents } from "../../stores/events";
import { useInsights } from "../../stores/insights";
import { useAuth } from "../../stores/auth";
import { toast } from "../../stores/toast";
import { ApiError, errorMessage } from "../../lib/api";
import { can } from "../../lib/perms";
import { STRATEGY_LABEL, compact, num } from "../../lib/format";
import type { Key } from "../../lib/types";
import { useNow } from "../../hooks/useNow";
import { usePoll } from "../../hooks/usePoll";
import { isBusy } from "../../components/StatusPill";
import { providerGivesReset } from "../../components/KeyBits";
import { ServiceLogo } from "../../components/EntityLogo";
import { EventsList } from "../../components/EventsList";
import { Breadcrumb, BreadcrumbItem } from "@/components/base/breadcrumb/breadcrumb";
import { Button } from "@/components/base/buttons/button";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Tab as TabItem, TabList, TabPanel, Tabs } from "@/components/base/tabs/tabs";
import { cx } from "@/utils/cx";
import { KeysTable } from "./KeysTable";
import { KeyEditModal } from "./KeyEditModal";
import { AddKeysModal } from "./AddKeysModal";
import { RevealModal } from "./RevealModal";
import { KeyDrawer } from "./KeyDrawer";
import { IntegratePanel } from "./IntegratePanel";
import { LlmBriefButton } from "./LlmBrief";
import { ServiceSettings } from "./ServiceSettings";
import { liveStatus } from "./keyUtil";
import { EmptyState, PageHeader, StatTile } from "@/components/acn";
import { ActionIcon, CARD } from "./parts";
import { OffersBlock } from "../../components/OfferBits";
import { KeyRingArt, LostPathArt, PulseArt } from "@/components/foundations/illustrations";

type Tab = "keys" | "activity" | "integrate" | "settings";

const EMPTY_KEYS: Key[] = [];

export function ServicePage() {
  const id = Number(useParams().id);
  const role = useAuth((s) => s.user?.role);
  const service = useServices((s) => s.services.find((x) => x.id === id));
  const fetchOne = useServices((s) => s.fetchOne);
  const keys = useKeys((s) => s.byService[id]) ?? EMPTY_KEYS;
  const keysLoaded = useKeys((s) => s.byService[id] !== undefined);
  const fetchKeys = useKeys((s) => s.fetch);
  const { apps, fetch: fetchApps } = useApps();
  const events = useEvents((s) => s.byService[id]);
  const fetchEvents = useEvents((s) => s.fetchForService);
  const now = useNow(1000);
  useTitle(service?.name ?? "Service");
  const ensureProviders = useProviders((s) => s.ensure);
  const provider = useProviders((s) => (service ? s.providers.find((p) => p.slug === service.provider) : undefined));

  const [tab, setTab] = useState<Tab>("keys");
  const [notFound, setNotFound] = useState(false);
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<Key | null>(null);
  const [revealing, setRevealing] = useState<Key | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [openKey, setOpenKey] = useState<Key | null>(null);
  const [checking, setChecking] = useState(false);
  const checkService = useInsights((s) => s.checkService);

  const perms = { manage: can.manage(role), hidden: can.hidden(role), reveal: can.reveal(role) };

  const refresh = useCallback(
    async (quiet = false) => {
      if (!quiet) setRefreshing(true);
      try {
        await Promise.all([fetchOne(id), fetchKeys(id), fetchEvents(id)]);
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) setNotFound(true);
        else if (!quiet) toast.error(errorMessage(e));
      } finally {
        if (!quiet) setRefreshing(false);
      }
    },
    [id, fetchOne, fetchKeys, fetchEvents],
  );

  useEffect(() => {
    setNotFound(false);
    void refresh(true);
  }, [refresh]);
  // Every 4s while the tab is visible, so in use and busy counts stay live.
  usePoll(() => refresh(true), 4000);

  useEffect(() => {
    // App names power the restricted allow list. Members may not be allowed to list apps.
    if (perms.manage) fetchApps().catch(() => undefined);
    ensureProviders().catch(() => undefined);
  }, [perms.manage, fetchApps, ensureProviders]);

  // Keep the editing modal pointed at the freshest copy of the key.
  const editingKey = useMemo(() => (editing ? keys.find((k) => k.id === editing.id) ?? editing : null), [editing, keys]);

  // Live counts so a finished cooldown shows as available without waiting for a refetch.
  const counts = useMemo(() => {
    if (!service) return null;
    // The server counts busy keys inside available; the Available tile shows only free ones.
    if (!keysLoaded) {
      const kc = service.key_counts;
      return {
        ...kc,
        busy: kc.busy ?? 0,
        banned: kc.banned ?? 0,
        available: Math.max(0, kc.available - (kc.busy ?? 0)),
        inUse: service.in_flight ?? 0,
      };
    }
    const max = service.max_concurrency ?? 0;
    const c = { total: keys.length, available: 0, busy: 0, cooling: 0, exhausted: 0, invalid: 0, banned: 0, disabled: 0, backup: 0, inUse: 0 };
    for (const k of keys) {
      c.inUse += k.in_flight ?? 0;
      const s = liveStatus(k, now);
      if (s === "active") {
        if (isBusy(k, max)) c.busy++;
        else c.available++;
      } else c[s]++;
      if (k.tier === "backup") c.backup++;
    }
    return c;
  }, [service, keys, keysLoaded, now]);

  const checkAll = async () => {
    setChecking(true);
    try {
      const res = await checkService(id);
      const clean = res.invalid === 0 && res.failed === 0 && res.checked > 0;
      if (clean) toast.success(res.message);
      else toast.info(res.message || "Check finished.");
      await fetchKeys(id).catch(() => undefined);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setChecking(false);
    }
  };

  if (notFound)
    return (
      <EmptyState
        art={LostPathArt}
        as="h1"
        title="Service not found"
        body="It may have been deleted, or the link is wrong."
        action={
          <RouterLink
            to="/"
            className="inline-flex h-9 items-center rounded-2lg bg-button-primary px-3 text-body-medium text-text-white shadow-xs outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring focus-visible:ring-offset-2"
          >
            Back to catalog
          </RouterLink>
        }
      />
    );
  if (!service || !counts) return <ServiceSkeleton />;

  const tabs = [
    { id: "keys" as Tab, label: "Keys", count: counts.total },
    { id: "activity" as Tab, label: "Activity", count: events?.length },
    { id: "integrate" as Tab, label: "Integrate" },
    { id: "settings" as Tab, label: "Settings" },
  ];

  const rowSkeleton = (label: string) => (
    <div className={CARD} role="status" aria-label={label}>
      {[0, 1, 2].map((i) => (
        <div key={i} className="flex items-center gap-4 border-b border-separator-border px-5 py-4 last:border-b-0">
          <Skeleton shape="text" className="h-4 w-32" />
          <Skeleton shape="text" className="h-3.5 flex-1" />
        </div>
      ))}
    </div>
  );

  return (
    <div>
      <Breadcrumb className="mb-3">
        <BreadcrumbItem href="/">Catalog</BreadcrumbItem>
        <BreadcrumbItem current>{service.name}</BreadcrumbItem>
      </Breadcrumb>
      <PageHeader
        leading={
          <>
            <ServiceLogo service={service} size="md" className="mt-0.5 sm:hidden" />
            <ServiceLogo service={service} size="lg" className="hidden sm:flex" />
          </>
        }
        title={service.name}
        meta={
          <div className="flex flex-wrap items-center gap-x-1.5 gap-y-0.5">
            <span>{service.slug}</span>
            <span aria-hidden="true">·</span>
            <RouterLink
              to={`/providers/${service.provider}`}
              className="rounded-sm underline-offset-2 outline-none hover:text-text-primary hover:underline focus-visible:ring-2 focus-visible:ring-border-focus-ring"
            >
              {provider?.name ?? service.provider}
            </RouterLink>
            {service.peer_link_id && (
              <>
                <span aria-hidden="true">·</span>
                <RouterLink
                  to="/sharing"
                  className="rounded-sm underline-offset-2 outline-none hover:text-text-primary hover:underline focus-visible:ring-2 focus-visible:ring-border-focus-ring"
                >
                  from {service.peer_name || "another server"}
                </RouterLink>
              </>
            )}
            <span aria-hidden="true">·</span>
            <span>{STRATEGY_LABEL[service.strategy]}</span>
            {service.max_concurrency > 0 && (
              <>
                <span aria-hidden="true">·</span>
                <span>{service.max_concurrency} per key</span>
              </>
            )}
            <span aria-hidden="true">·</span>
            <span className="wrap-anywhere min-w-0 max-w-full" dir="ltr">
              {service.base_url}
            </span>
          </div>
        }
        actions={
          <>
            <ActionIcon icon={RiRefreshLine} label="Refresh" size="medium" busy={refreshing} disabled={refreshing} onClick={() => void refresh()} />
            <LlmBriefButton service={service} />
            {perms.manage && keys.length > 0 && provider?.spec?.health && (
              <Button
                variant="secondary"
                leadingIcon={checking ? ButtonSpinner : RiPulseLine}
                disabled={checking}
                onClick={() => void checkAll()}
                className="flex-1 sm:flex-none"
              >
                {checking ? "Checking" : "Check all"}
              </Button>
            )}
            {perms.manage && (
              <Button variant="primary" leadingIcon={RiAddLine} onClick={() => setAdding(true)} className="flex-1 sm:flex-none">
                Add keys
              </Button>
            )}
          </>
        }
        className="mb-0"
      />
      {service.description && <p className="mt-4 max-w-2xl text-body-2-regular text-text-secondary">{service.description}</p>}
      {perms.manage && keys.length > 0 && provider && !provider.spec?.health && (
        <p className="mt-3 flex items-start gap-1.5 text-body-2-regular text-text-secondary">
          <RiInformationLine className="mt-0.5 size-3.5 shrink-0" aria-hidden />
          This provider has no health check. Validity comes from real requests.
        </p>
      )}

      {/* Phones: two columns, and the tiles that read 0 for momentary states drop out to keep the keys close. */}
      <div className="mt-6 grid grid-cols-2 gap-2.5 sm:mt-8 sm:grid-cols-4 lg:grid-cols-7" role="group" aria-label="Key pool summary">
        <StatTile
          label="Available"
          value={num(counts.available)}
          tone={counts.available > 0 ? "success" : counts.busy > 0 ? undefined : "error"}
          sub={counts.backup ? `${counts.backup} backup` : undefined}
        />
        <StatTile
          className={cx(!counts.busy && "max-sm:hidden")}
          label="Busy"
          value={num(counts.busy)}
          tone={counts.busy ? "accent" : undefined}
          sub="at their concurrency limit"
        />
        <StatTile
          className={cx(!counts.cooling && "max-sm:hidden")}
          label="Cooling"
          value={num(counts.cooling)}
          tone={counts.cooling ? "warning" : undefined}
        />
        <StatTile label="Out of quota" value={num(counts.exhausted)} tone={counts.exhausted ? "warning" : undefined} />
        {/* Banned keys count as invalid here; the sub line splits them out. */}
        <StatTile
          label="Invalid"
          value={num(counts.invalid + counts.banned)}
          tone={counts.invalid + counts.banned ? "error" : undefined}
          sub={counts.banned ? `${counts.banned} banned` : undefined}
        />
        <StatTile
          className={cx(!counts.inUse && "max-sm:hidden")}
          label="In use"
          value={num(counts.inUse)}
          tone={counts.inUse > 0 ? "accent" : undefined}
          sub="requests running now"
        />
        <StatTile label="Requests, 24h" value={compact(service.requests_24h)} />
      </div>

      <OffersBlock spec={provider?.spec} className="mt-4" />

      <Tabs selectedKey={tab} onSelectionChange={(k) => setTab(k as Tab)} className="mt-10 gap-6">
        <div className="-mx-4 overflow-x-auto px-4 [scrollbar-width:none] sm:mx-0 sm:px-0 [&::-webkit-scrollbar]:hidden">
          <TabList aria-label="Service sections" className="min-w-max">
            {tabs.map((t) => (
              <TabItem key={t.id} id={t.id} count={t.count}>
                {t.label}
              </TabItem>
            ))}
          </TabList>
        </div>

        {/* Only the selected panel mounts, so each switch settles in with fade-up instead of swapping in place. */}
        <TabPanel id="keys" className="fade-up">
          {!keysLoaded ? (
            rowSkeleton("Loading keys")
          ) : keys.length === 0 ? (
            <EmptyState
              art={KeyRingArt}
              as="h2"
              icon={AcnKeyPool}
              title="No keys in this pool yet"
              body={
                perms.manage
                  ? "Paste a key from each account you own. Acnologia rotates through them and rests any key that hits a limit."
                  : "No keys here yet. Ask an admin to add some."
              }
              action={
                perms.manage ? (
                  <Button variant="primary" leadingIcon={RiAddLine} onClick={() => setAdding(true)}>
                    Add keys
                  </Button>
                ) : undefined
              }
            />
          ) : (
            <KeysTable
              keys={keys}
              apps={apps}
              now={now}
              perms={perms}
              serviceMax={service.max_concurrency ?? 0}
              quotaUnit={provider?.spec?.quota?.unit}
              resetExact={providerGivesReset(provider)}
              strategy={service.strategy}
              onOpen={setOpenKey}
              onEdit={setEditing}
              onReveal={setRevealing}
            />
          )}
        </TabPanel>

        <TabPanel id="activity" className="fade-up">
          {events === undefined ? (
            rowSkeleton("Loading activity")
          ) : events.length === 0 ? (
            <EmptyState
              art={PulseArt}
              as="h2"
              icon={RiFileList3Line}
              title="No activity yet"
              body="Requests, rate limits and health checks for this service show up here. Send a test request from the Integrate tab."
              action={
                <Button variant="secondary" onClick={() => setTab("integrate")}>
                  Show integration code
                </Button>
              }
            />
          ) : (
            <EventsList events={events} now={now} showService={false} />
          )}
        </TabPanel>

        <TabPanel id="integrate" className="fade-up">
          <IntegratePanel service={service} />
        </TabPanel>

        <TabPanel id="settings" className="fade-up">
          <ServiceSettings service={service} canManage={perms.manage} />
        </TabPanel>
      </Tabs>

      {perms.manage && (
        <AddKeysModal
          open={adding}
          onClose={() => setAdding(false)}
          service={service}
          apps={apps}
          existingCount={keys.length}
          canHidden={perms.hidden}
        />
      )}
      <KeyEditModal
        k={editingKey}
        apps={apps}
        canHidden={perms.hidden}
        serviceMax={service.max_concurrency ?? 0}
        onClose={() => setEditing(null)}
      />
      <RevealModal k={revealing} onClose={() => setRevealing(null)} />
      <KeyDrawer keyId={openKey?.id ?? null} seed={openKey} onClose={() => setOpenKey(null)} />
    </div>
  );
}

function ServiceSkeleton() {
  return (
    <div role="status" aria-label="Loading service">
      <Skeleton shape="text" className="h-3.5 w-20" />
      <div className="mt-5 flex items-center gap-4">
        <Skeleton className="size-14 rounded-2xl" />
        <div>
          <Skeleton shape="text" className="h-7 w-48" />
          <Skeleton shape="text" className="mt-2 h-3.5 w-72 max-w-full" />
        </div>
      </div>
      <div className="mt-8 grid grid-cols-2 gap-2.5 sm:grid-cols-4 lg:grid-cols-7">
        {[0, 1, 2, 3, 4, 5, 6].map((i) => (
          <Skeleton key={i} className={cx("h-[74px] rounded-3xl", i > 3 && "hidden sm:block")} />
        ))}
      </div>
    </div>
  );
}
