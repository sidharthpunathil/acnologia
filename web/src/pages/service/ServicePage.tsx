import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { ArrowLeft, KeyRound, Plus, RefreshCw, ScrollText } from "lucide-react";
import { useServices } from "../../stores/services";
import { useKeys } from "../../stores/keys";
import { useApps } from "../../stores/apps";
import { useEvents } from "../../stores/events";
import { useAuth } from "../../stores/auth";
import { toast } from "../../stores/toast";
import { ApiError, errorMessage } from "../../lib/api";
import { can } from "../../lib/perms";
import { STRATEGY_LABEL, compact, num } from "../../lib/format";
import type { Key } from "../../lib/types";
import { useNow } from "../../hooks/useNow";
import { ServiceIcon } from "../../components/ServiceIcon";
import { Tabs } from "../../components/Tabs";
import { EventsList } from "../../components/EventsList";
import { Button, Empty, IconButton, PageLoading, Spinner, Stat } from "../../components/ui";
import { KeysTable } from "./KeysTable";
import { KeyEditModal } from "./KeyEditModal";
import { AddKeysModal } from "./AddKeysModal";
import { RevealModal } from "./RevealModal";
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

  const [tab, setTab] = useState<Tab>("keys");
  const [notFound, setNotFound] = useState(false);
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<Key | null>(null);
  const [revealing, setRevealing] = useState<Key | null>(null);
  const [refreshing, setRefreshing] = useState(false);

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
  }, [perms.manage, fetchApps]);

  // Keep the editing modal pointed at the freshest copy of the key.
  const editingKey = useMemo(() => (editing ? keys.find((k) => k.id === editing.id) ?? editing : null), [editing, keys]);

  // Live counts so a finished cooldown shows as available without waiting for a refetch.
  const counts = useMemo(() => {
    if (!service) return null;
    if (!keysLoaded) return service.key_counts;
    const c = { total: keys.length, available: 0, cooling: 0, exhausted: 0, invalid: 0, disabled: 0, backup: 0 };
    for (const k of keys) {
      const s = liveStatus(k, now);
      if (s === "active") c.available++;
      else c[s]++;
      if (k.tier === "backup") c.backup++;
    }
    return c;
  }, [service, keys, keysLoaded, now]);

  if (notFound)
    return (
      <Empty
        title="Service not found"
        body="It may have been deleted."
        action={
          <Link to="/" className="inline-flex h-10 items-center rounded-full bg-ink px-4 text-sm font-medium text-white">
            Back to catalog
          </Link>
        }
      />
    );
  if (!service || !counts) return <PageLoading />;

  const tabs = [
    { id: "keys" as Tab, label: "Keys", count: counts.total },
    { id: "activity" as Tab, label: "Activity", count: events?.length },
    { id: "integrate" as Tab, label: "Integrate" },
    { id: "settings" as Tab, label: "Settings" },
  ];

  return (
    <div>
      <Link to="/" className="mb-6 inline-flex items-center gap-1.5 font-mono text-xs text-muted hover:text-ink">
        <ArrowLeft className="h-3.5 w-3.5" /> catalog
      </Link>

      <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex min-w-0 items-center gap-4">
          <ServiceIcon icon={service.icon} provider={service.provider} name={service.name} size="lg" />
          <div className="min-w-0">
            <h1 className="truncate font-mono text-[26px] leading-tight tracking-tight text-ink sm:text-[30px]">{service.name}</h1>
            <div className="mt-1 truncate font-mono text-xs text-muted">
              {service.slug}{service.provider !== service.slug ? ` · ${service.provider}` : ""} · {STRATEGY_LABEL[service.strategy]} · {service.base_url}
            </div>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <IconButton label="Refresh" onClick={() => void refresh()} disabled={refreshing}>
            {refreshing ? <Spinner /> : <RefreshCw className="h-4 w-4" />}
          </IconButton>
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
        <Stat label="Exhausted" value={num(counts.exhausted)} tone={counts.exhausted ? "amber" : undefined} />
        <Stat label="Invalid" value={num(counts.invalid)} tone={counts.invalid ? "red" : undefined} />
        <Stat label="Backup" value={num(counts.backup)} />
        <Stat label="Requests 24h" value={compact(service.requests_24h)} />
      </div>

      <Tabs className="mt-10" items={tabs} value={tab} onChange={setTab} />

      <div className="mt-6">
        {tab === "keys" &&
          (!keysLoaded ? (
            <PageLoading />
          ) : keys.length === 0 ? (
            <Empty
              icon={<KeyRound className="h-5 w-5" />}
              title="No keys in this pool"
              body={
                perms.manage
                  ? "Paste the API keys for each account you own. Cogito rotates through them and cools down any key that hits a limit."
                  : "An admin has not added keys to this service yet."
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
            <KeysTable keys={keys} apps={apps} now={now} perms={perms} onEdit={setEditing} onReveal={setRevealing} />
          ))}

        {tab === "activity" &&
          (events === undefined ? (
            <PageLoading />
          ) : events.length === 0 ? (
            <Empty
              icon={<ScrollText className="h-5 w-5" />}
              title="No activity yet"
              body="Leases, proxy calls, rate limits and health checks for this service will show up here. Try a request from the Integrate tab."
              action={<Button onClick={() => setTab("integrate")}>Open Integrate</Button>}
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
      <KeyEditModal k={editingKey} apps={apps} canHidden={perms.hidden} onClose={() => setEditing(null)} />
      <RevealModal k={revealing} onClose={() => setRevealing(null)} />
    </div>
  );
}
