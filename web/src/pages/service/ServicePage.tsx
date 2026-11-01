import { useCallback, useEffect, useMemo, useState } from "react";
import { useTitle } from "../../hooks/useTitle";
import { Link, useParams } from "react-router-dom";
import { Activity, ArrowLeft, KeyRound, Plus, RefreshCw, ScrollText } from "lucide-react";
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
import { ServiceLogo } from "../../components/EntityLogo";
import { Tabs } from "../../components/Tabs";
import { EventsList } from "../../components/EventsList";
import { Button, Empty, IconButton, PageSkeleton, Skeleton, Spinner, Stat } from "../../components/ui";
import { KeysTable } from "./KeysTable";
import { KeyEditModal } from "./KeyEditModal";
import { AddKeysModal } from "./AddKeysModal";
import { RevealModal } from "./RevealModal";
import { KeyDrawer } from "./KeyDrawer";
import { IntegratePanel } from "./IntegratePanel";
import { ServiceSettings } from "./ServiceSettings";
import { liveStatus } from "./keyUtil";

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
    const t = window.setInterval(() => void refresh(true), 30000);
    return () => window.clearInterval(t);
  }, [refresh]);

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
    if (!keysLoaded) return { ...service.key_counts, inUse: service.in_flight ?? 0 };
    const c = { total: keys.length, available: 0, cooling: 0, exhausted: 0, invalid: 0, disabled: 0, backup: 0, inUse: 0 };
    for (const k of keys) {
      c.inUse += k.in_flight ?? 0;
      const s = liveStatus(k, now);
      if (s === "active") c.available++;
      else c[s]++;
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
      <Empty
        title="Service not found"
        body="It may have been deleted, or the link is wrong."
        action={
          <Link to="/" className="tap inline-flex h-10 items-center rounded-full bg-ink px-4 text-sm font-medium text-on-ink">
            Back to catalog
          </Link>
        }
      />
    );
  if (!service || !counts) return <PageSkeleton tiles={6} variant="list" />;

  const tabs = [
    { id: "keys" as Tab, label: "Keys", count: counts.total },
    { id: "activity" as Tab, label: "Activity", count: events?.length },
    { id: "integrate" as Tab, label: "Integrate" },
    { id: "settings" as Tab, label: "Settings" },
  ];

  return (
    <div>
      <Link to="/" className="tap mb-5 inline-flex items-center gap-1.5 font-mono text-xs text-muted hover:text-ink">
        <ArrowLeft className="h-3.5 w-3.5" /> Catalog
      </Link>

      <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex min-w-0 items-center gap-4">
          <ServiceLogo service={service} size="lg" />
          <div className="min-w-0">
            <h1 className="break-words font-mono text-[26px] leading-tight tracking-tight text-ink sm:text-[30px]">{service.name}</h1>
            <div className="mt-1 flex flex-wrap items-center gap-x-1.5 gap-y-0.5 font-mono text-xs text-muted">
              <span>{service.slug}</span>
              <span aria-hidden="true">·</span>
              <Link to={`/providers/${service.provider}`} className="underline-offset-2 hover:text-ink hover:underline">
                {provider?.name ?? service.provider}
              </Link>
              <span aria-hidden="true">·</span>
              <span>{STRATEGY_LABEL[service.strategy]}</span>
              {service.max_concurrency > 0 && (
                <>
                  <span aria-hidden="true">·</span>
                  <span>{service.max_concurrency} per key</span>
                </>
              )}
              <span aria-hidden="true">·</span>
              <span className="wrap-anywhere min-w-0 max-w-full">{service.base_url}</span>
            </div>
          </div>
        </div>
        <div className="flex shrink-0 flex-wrap items-center gap-2">
          <IconButton label="Refresh" onClick={() => void refresh()} disabled={refreshing}>
            {refreshing ? <Spinner /> : <RefreshCw className="h-4 w-4" />}
          </IconButton>
          {perms.manage && keys.length > 0 && (
            <Button icon={<Activity className="h-4 w-4" />} loading={checking} onClick={() => void checkAll()}>
              {checking ? "Checking keys" : "Check all"}
            </Button>
          )}
          {perms.manage && (
            <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={() => setAdding(true)}>
              Add keys
            </Button>
          )}
        </div>
      </div>
      {service.description && <p className="mt-4 max-w-2xl text-[13px] text-muted">{service.description}</p>}

      <div className="mt-8 grid grid-cols-2 gap-2.5 sm:grid-cols-3 lg:grid-cols-6">
        <Stat label="Available" value={num(counts.available)} tone={counts.available > 0 ? "green" : "red"} />
        <Stat label="Cooling" value={num(counts.cooling)} tone={counts.cooling ? "amber" : undefined} />
        <Stat label="Out of quota" value={num(counts.exhausted)} tone={counts.exhausted ? "amber" : undefined} />
        <Stat label="Invalid" value={num(counts.invalid)} tone={counts.invalid ? "red" : undefined} />
        <Stat
          label="In use"
          value={num(counts.inUse)}
          tone={counts.inUse > 0 ? "violet" : undefined}
          sub={service.max_concurrency > 0 ? `up to ${service.max_concurrency} per key` : "no limit per key"}
        />
        <Stat label="Requests, 24h" value={compact(service.requests_24h)} sub={counts.backup ? `${counts.backup} backup` : undefined} />
      </div>

      <Tabs className="mt-10" items={tabs} value={tab} onChange={setTab} />

      <div className="mt-6">
        {tab === "keys" &&
          (!keysLoaded ? (
            <div className="space-y-2" role="status" aria-label="Loading keys">
              {[0, 1, 2].map((i) => (
                <Skeleton key={i} className="h-[64px] rounded-card" />
              ))}
            </div>
          ) : keys.length === 0 ? (
            <Empty
              icon={<KeyRound className="h-5 w-5" />}
              title="No keys in this pool yet"
              body={
                perms.manage
                  ? "Paste a key from each account you own. Cogito rotates through them and rests any key that hits a limit."
                  : "No keys here yet. Ask an admin to add some."
              }
              action={
                perms.manage ? (
                  <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={() => setAdding(true)}>
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
              onOpen={setOpenKey}
              onEdit={setEditing}
              onReveal={setRevealing}
            />
          ))}

        {tab === "activity" &&
          (events === undefined ? (
            <div className="space-y-2" role="status" aria-label="Loading activity">
              {[0, 1, 2].map((i) => (
                <Skeleton key={i} className="h-[52px] rounded-card" />
              ))}
            </div>
          ) : events.length === 0 ? (
            <Empty
              icon={<ScrollText className="h-5 w-5" />}
              title="No activity yet"
              body="Requests, rate limits and health checks for this service show up here. Send a test request from the Integrate tab."
              action={<Button onClick={() => setTab("integrate")}>Show integration code</Button>}
            />
          ) : (
            <EventsList events={events} now={now} showService={false} />
          ))}

        {tab === "integrate" && <IntegratePanel service={service} />}

        {tab === "settings" && <ServiceSettings service={service} canManage={perms.manage} />}
      </div>

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
