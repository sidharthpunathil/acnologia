import { useCallback, useEffect, useMemo, useState } from "react";
import { Activity as ActivityIcon, Pause, Play } from "lucide-react";
import { useEvents } from "../stores/events";
import { useServices } from "../stores/services";
import { toast } from "../stores/toast";
import { errorMessage } from "../lib/api";
import { EVENT_KINDS, type EventKind } from "../lib/types";
import { useNow } from "../hooks/useNow";
import { EventsList } from "../components/EventsList";
import { Button, Empty, PageHeader, PageLoading, Select, Spinner } from "../components/ui";

const REFRESH_MS = 10000;

export function ActivityPage() {
  const { feed, feedLoaded, fetchFeed } = useEvents();
  const { services, loaded: servicesLoaded, fetch: fetchServices } = useServices();
  const [serviceId, setServiceId] = useState<string>("");
  const [kind, setKind] = useState<EventKind | "">("");
  const [live, setLive] = useState(true);
  const [loading, setLoading] = useState(false);
  const now = useNow(5000);

  const load = useCallback(
    async (quiet: boolean) => {
      if (!quiet) setLoading(true);
      try {
        await fetchFeed({ service_id: serviceId ? Number(serviceId) : undefined, limit: 200 });
      } catch (e) {
        if (!quiet) toast.error(errorMessage(e));
      } finally {
        if (!quiet) setLoading(false);
      }
    },
    [fetchFeed, serviceId],
  );

  useEffect(() => {
    void load(false);
  }, [load]);

  useEffect(() => {
    if (!live) return;
    const t = window.setInterval(() => {
      if (document.visibilityState === "visible") void load(true);
    }, REFRESH_MS);
    return () => window.clearInterval(t);
  }, [live, load]);

  useEffect(() => {
    if (!servicesLoaded) fetchServices().catch(() => undefined);
  }, [servicesLoaded, fetchServices]);

  // The events endpoint filters by service only, so kind is filtered here.
  const shown = useMemo(() => (kind ? feed.filter((e) => e.kind === kind) : feed), [feed, kind]);
  const idBySlug = useMemo(() => Object.fromEntries(services.map((s) => [s.slug, s.id])), [services]);
  const limited = feed.filter((e) => e.kind === "rate_limited" || e.kind === "quota_exhausted").length;

  return (
    <div>
      <PageHeader
        title="Activity"
        meta={`${shown.length} events · ${limited} limits hit · ${live ? "refreshing every 10s" : "paused"}`}
        actions={
          <Button icon={live ? <Pause className="h-4 w-4" /> : <Play className="h-4 w-4" />} onClick={() => setLive(!live)}>
            {live ? "Pause" : "Resume"}
          </Button>
        }
      />

      <div className="mb-5 flex flex-col gap-2 sm:flex-row sm:items-center">
        <Select value={serviceId} onChange={(e) => setServiceId(e.target.value)} className="sm:w-56">
          <option value="">All services</option>
          {services.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </Select>
        <Select value={kind} onChange={(e) => setKind(e.target.value as EventKind | "")} className="sm:w-48">
          <option value="">All kinds</option>
          {EVENT_KINDS.map((k) => (
            <option key={k} value={k}>
              {k.replace("_", " ")}
            </option>
          ))}
        </Select>
        {loading && <Spinner className="ml-1" />}
      </div>

      {!feedLoaded ? (
        <PageLoading />
      ) : shown.length === 0 ? (
        <Empty
          icon={<ActivityIcon className="h-5 w-5" />}
          title={feed.length ? "No events match these filters" : "No activity yet"}
          body={
            feed.length
              ? "Clear a filter to see more."
              : "Create an app, then call a service through /proxy or lease a key. Every call, rate limit and health check is logged here."
          }
          action={
            feed.length && (kind || serviceId) ? (
              <Button
                onClick={() => {
                  setKind("");
                  setServiceId("");
                }}
              >
                Clear filters
              </Button>
            ) : undefined
          }
        />
      ) : (
        <EventsList events={shown} now={now} serviceIdBySlug={idBySlug} />
      )}
    </div>
  );
}
