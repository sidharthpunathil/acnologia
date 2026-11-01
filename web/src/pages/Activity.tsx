import { useCallback, useEffect, useMemo, useState } from "react";
import { RiPauseLine, RiPlayLine, RiPulseLine } from "@remixicon/react";
import { useTitle } from "../hooks/useTitle";
import { useEvents } from "../stores/events";
import { useServices } from "../stores/services";
import { toast } from "../stores/toast";
import { errorMessage } from "../lib/api";
import { EVENT_KINDS, type EventKind } from "../lib/types";
import { useNow } from "../hooks/useNow";
import { EventsList } from "../components/EventsList";
import { Button } from "@/components/base/buttons/button";
import { Pagination } from "@/components/base/pagination/pagination";
import { Select, SelectItem } from "@/components/base/select/select";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { Spinner } from "@/components/base/spinner/spinner";
import { EmptyState, PageHeader } from "@/components/acn";
import { CARD } from "./service/parts";
import { PulseArt, SearchArt } from "@/components/foundations/illustrations";

const REFRESH_MS = 10000;
const ALL = "all";
/** Events per page. The feed holds the newest 200, so this is at most four pages. */
const PAGE_SIZE = 50;

export function ActivityPage() {
  useTitle("Activity");
  const { feed, feedLoaded, fetchFeed } = useEvents();
  const { services, loaded: servicesLoaded, fetch: fetchServices } = useServices();
  const [serviceId, setServiceId] = useState<string>("");
  const [kind, setKind] = useState<EventKind | "">("");
  const [live, setLive] = useState(true);
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(1);
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
  // A live refresh or a narrower filter can shrink the list under the current page.
  const pageCount = Math.max(1, Math.ceil(shown.length / PAGE_SIZE));
  const current = Math.min(page, pageCount);
  const pageEvents = useMemo(() => shown.slice((current - 1) * PAGE_SIZE, current * PAGE_SIZE), [shown, current]);
  const goTo = (p: number) => {
    setPage(p);
    window.scrollTo({ top: 0 });
  };
  const limited = feed.filter((e) => e.kind === "rate_limited" || e.kind === "quota_exhausted").length;

  return (
    <div>
      <PageHeader
        title="Activity"
        meta={`${shown.length} events · ${limited} limits hit · ${live ? "updates every 10s" : "paused"}`}
        actions={
          <Button variant="secondary" leadingIcon={live ? RiPauseLine : RiPlayLine} onClick={() => setLive(!live)} aria-pressed={!live}>
            {live ? "Pause" : "Resume"}
          </Button>
        }
      />

      <div className="mb-5 flex flex-col gap-2 sm:flex-row sm:items-center">
        <Select
          aria-label="Service"
          selectedKey={serviceId || ALL}
          onSelectionChange={(k) => {
            setServiceId(k === ALL ? "" : String(k));
            setPage(1);
          }}
          className="sm:w-56"
        >
          <SelectItem id={ALL}>All services</SelectItem>
          {services.map((s) => (
            <SelectItem key={s.id} id={String(s.id)}>
              {s.name}
            </SelectItem>
          ))}
        </Select>
        <Select
          aria-label="Event type"
          selectedKey={kind || ALL}
          onSelectionChange={(k) => {
            setKind(k === ALL ? "" : (String(k) as EventKind));
            setPage(1);
          }}
          className="sm:w-52"
        >
          <SelectItem id={ALL}>All event types</SelectItem>
          {EVENT_KINDS.map((k) => (
            <SelectItem key={k} id={k}>
              {k.replace(/_/g, " ")}
            </SelectItem>
          ))}
        </Select>
        {loading && <Spinner size="sm" className="ms-1" aria-label="Loading events" />}
      </div>

      {!feedLoaded ? (
        <div className={CARD} role="status" aria-label="Loading activity">
          {[0, 1, 2, 3, 4].map((i) => (
            <div key={i} className="flex items-center gap-4 border-b border-separator-border px-5 py-3.5 last:border-b-0">
              <Skeleton className="h-5 w-20 rounded-full" />
              <Skeleton shape="text" className="h-3.5 flex-1" />
              <Skeleton shape="text" className="h-3 w-14" />
            </div>
          ))}
        </div>
      ) : shown.length === 0 ? (
        <EmptyState
          art={feed.length ? SearchArt : PulseArt}
          as="h2"
          icon={RiPulseLine}
          title={feed.length ? "No events match these filters" : "No activity yet"}
          body={
            feed.length
              ? "Clear a filter to see more."
              : "Create an app, then call a service through /proxy or lease a key. Every request, rate limit and health check shows up here."
          }
          action={
            feed.length && (kind || serviceId) ? (
              <Button
                variant="secondary"
                onClick={() => {
                  setKind("");
                  setServiceId("");
                  setPage(1);
                }}
              >
                Clear filters
              </Button>
            ) : undefined
          }
        />
      ) : (
        <div className="flex flex-col gap-4">
          {/* Keyed by page so a new page renders still instead of every row entering as a fresh event. */}
          <EventsList key={current} events={pageEvents} now={now} serviceIdBySlug={idBySlug} />
          <Pagination page={current} totalPages={pageCount} onChange={goTo} aria-label="Activity pages" />
        </div>
      )}
    </div>
  );
}
