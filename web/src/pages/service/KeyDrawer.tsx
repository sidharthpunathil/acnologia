import { useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { Activity, Eye, Info, Pencil, RotateCcw } from "lucide-react";
import type { Key } from "../../lib/types";
import { ApiError, errorMessage } from "../../lib/api";
import { can } from "../../lib/perms";
import { countdown, dateTime, num, relTime } from "../../lib/format";
import { useAuth } from "../../stores/auth";
import { useApps } from "../../stores/apps";
import { useKeys } from "../../stores/keys";
import { useInsights } from "../../stores/insights";
import { toast } from "../../stores/toast";
import { useNow } from "../../hooks/useNow";
import { Modal } from "../../components/Modal";
import { EntityLogo, ServiceLogo } from "../../components/EntityLogo";
import { StatusPill } from "../../components/StatusPill";
import { ActivityChart, QuotaMeter, ValidityBadge } from "../../components/KeyBits";
import { KindBadge } from "../../components/EventsList";
import { Button, Callout, Skeleton, cx } from "../../components/ui";
import { useServices } from "../../stores/services";
import { InUse } from "./KeysTable";
import { KeyEditModal } from "./KeyEditModal";
import { RevealModal } from "./RevealModal";
import { withLiveStatus } from "./keyUtil";

function Fact({ label, children, sub, wide }: { label: string; children: ReactNode; sub?: ReactNode; wide?: boolean }) {
  return (
    <div className={cx("min-w-0 rounded-xl bg-soft px-3 py-2.5", wide && "col-span-2")}>
      <dt className="text-[11px] font-medium text-muted">{label}</dt>
      <dd className="mt-0.5 min-w-0 text-[13px] text-ink">{children}</dd>
      {sub && <dd className="mt-0.5 font-mono text-[11px] text-muted">{sub}</dd>}
    </div>
  );
}

/** Relative time as the value, exact time underneath so it is readable on touch screens too. */
function When({ iso, now, never = "Never" }: { iso: string | null | undefined; now: number; never?: string }) {
  if (!iso) return <span className="text-muted">{never}</span>;
  return <time dateTime={iso}>{relTime(iso, now)}</time>;
}

const NO_HEALTH = "This provider has no health check, so Cogito learns whether the key works from real requests.";

/**
 * Everything about one key: validity, timing, quota, 14 days of activity and
 * recent events. A side sheet on desktop and a bottom sheet on phones.
 */
export function KeyDrawer({ keyId, seed, onClose }: { keyId: number | null; seed?: Key | null; onClose: () => void }) {
  const role = useAuth((s) => s.user?.role);
  const perms = { manage: can.manage(role), hidden: can.hidden(role), reveal: can.reveal(role) };
  const insight = useInsights((s) => (keyId ? s.byId[keyId] : undefined));
  const fetchInsight = useInsights((s) => s.fetchInsight);
  const { check, reset } = useKeys();
  const { apps, loaded: appsLoaded, fetch: fetchApps } = useApps();
  const services = useServices((s) => s.services);
  const now = useNow(1000);
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const [busy, setBusy] = useState<"check" | "reset" | null>(null);
  const [noHealth, setNoHealth] = useState(false);
  const [editing, setEditing] = useState(false);
  const [revealing, setRevealing] = useState<Key | null>(null);
  const [showAll, setShowAll] = useState(false);

  useEffect(() => {
    setLoadErr(null);
    setNoHealth(false);
    setShowAll(false);
    if (!keyId) return;
    let live = true;
    const load = () =>
      fetchInsight(keyId).catch((e) => {
        if (live) setLoadErr(errorMessage(e));
      });
    void load();
    const t = window.setInterval(() => document.visibilityState === "visible" && void load(), 30000);
    return () => {
      live = false;
      window.clearInterval(t);
    };
  }, [keyId, fetchInsight]);

  useEffect(() => {
    if (keyId && perms.manage && !appsLoaded) fetchApps().catch(() => undefined);
  }, [keyId, perms.manage, appsLoaded, fetchApps]);

  const raw = insight?.key ?? (seed && seed.id === keyId ? seed : null);
  const k = raw ? withLiveStatus(raw, now) : null;
  const svc = insight?.service;
  const service = services.find((s) => s.id === (svc?.id ?? k?.service_id));
  const serviceMax = svc?.max_concurrency ?? service?.max_concurrency ?? 0;
  const unit = insight?.quota_unit ?? k?.quota_unit ?? "";
  const hasHealth = insight?.has_health_check ?? true;
  const canTouch = !!k && perms.manage && (!k.hidden || perms.hidden);

  const run = async (kind: "check" | "reset") => {
    if (!k) return;
    setBusy(kind);
    try {
      if (kind === "check") {
        const nk = await check(k);
        setNoHealth(false);
        if (nk.validity === "invalid") toast.error(`${nk.label || nk.hint} failed the check: ${nk.last_error || "the provider rejected it"}.`);
        else toast.success(`${nk.label || nk.hint} passed the check.`);
      } else {
        await reset(k);
        toast.success(`${k.label || k.hint} is reset and back in rotation.`);
      }
      void fetchInsight(k.id).catch(() => undefined);
    } catch (e) {
      if (e instanceof ApiError && e.code === "no_health_check") setNoHealth(true);
      else {
        toast.error(errorMessage(e));
        void fetchInsight(k.id).catch(() => undefined);
      }
    } finally {
      setBusy(null);
    }
  };

  const title = k ? `Key ${k.label || k.hint}` : "Key";
  const needsReset = !!k && (k.effective_status !== "active" || k.consecutive_fails > 0);
  const limit = k ? (k.max_concurrency ?? serviceMax) : 0;
  const events = insight?.events ?? [];
  const shownEvents = showAll ? events : events.slice(0, 8);

  const header = k ? (
    <div className="flex min-w-0 items-start gap-3">
      {service ? (
        <ServiceLogo service={service} size="md" />
      ) : (
        <EntityLogo src={svc?.logo_url} name={svc?.name ?? "?"} size="md" />
      )}
      <div className="min-w-0 flex-1">
        <div className="break-words text-[16px] font-semibold leading-snug text-ink">{k.label || "Untitled key"}</div>
        <div className="mt-0.5 flex flex-wrap items-center gap-x-1.5 font-mono text-[11px] text-muted">
          <span>{k.hint}</span>
          {svc && (
            <>
              <span aria-hidden="true">·</span>
              <Link to={`/services/${svc.id}`} onClick={onClose} className="underline-offset-2 hover:text-ink hover:underline">
                {svc.name}
              </Link>
            </>
          )}
        </div>
        <div className="mt-2 flex flex-wrap items-center gap-1.5">
          <ValidityBadge validity={k.validity} />
          <StatusPill k={k} now={now} />
          <InUse k={k} serviceMax={serviceMax} />
        </div>
      </div>
    </div>
  ) : (
    <div className="flex items-center gap-3">
      <Skeleton className="h-10 w-10 rounded-[11px]" />
      <div className="flex-1">
        <Skeleton className="h-4 w-32" />
        <Skeleton className="mt-2 h-3 w-48" />
      </div>
    </div>
  );

  const footer =
    k && (canTouch || perms.reveal) ? (
      <div className="grid w-full grid-cols-2 gap-2 sm:flex sm:justify-end">
        {canTouch && (
          <>
            <Button variant="primary" icon={<Activity className="h-4 w-4" />} loading={busy === "check"} disabled={busy !== null} onClick={() => void run("check")}>
              Check now
            </Button>
            <Button
              icon={<RotateCcw className="h-4 w-4" />}
              loading={busy === "reset"}
              disabled={busy !== null || !needsReset}
              title={needsReset ? "Clear the cooldown and errors, and put the key back in rotation" : "Nothing to reset: the key is available"}
              onClick={() => void run("reset")}
            >
              Reset
            </Button>
            <Button icon={<Pencil className="h-4 w-4" />} onClick={() => setEditing(true)}>
              Edit
            </Button>
          </>
        )}
        {perms.reveal && (
          <Button icon={<Eye className="h-4 w-4" />} onClick={() => raw && setRevealing(raw)}>
            Reveal
          </Button>
        )}
      </div>
    ) : undefined;

  return (
    <>
      <Modal open={keyId !== null} onClose={onClose} variant="drawer" title={title} header={header} footer={footer}>
        {!k && loadErr ? (
          <Callout tone="red">Could not load this key: {loadErr}</Callout>
        ) : !k ? (
          <div className="space-y-3">
            <Skeleton className="h-24 rounded-xl" />
            <Skeleton className="h-32 rounded-xl" />
          </div>
        ) : (
          <div className="space-y-6">
            {(noHealth || (insight && !hasHealth)) && (
              <Callout tone={noHealth ? "amber" : "neutral"} icon={<Info className="h-4 w-4" />}>
                {NO_HEALTH}
              </Callout>
            )}
            {k.validity_reason && (
              <p className="text-[13px] text-muted">
                <span className="font-medium text-ink">
                  {k.validity === "valid" ? "Works. " : k.validity === "invalid" ? "Not working. " : "Not known yet. "}
                </span>
                <span className="wrap-anywhere">{k.validity_reason.charAt(0).toUpperCase() + k.validity_reason.slice(1)}.</span>
              </p>
            )}

            <section aria-labelledby="key-facts">
              <h3 id="key-facts" className="sr-only">
                Details
              </h3>
              <dl className="grid grid-cols-2 gap-2">
                <Fact label="Last used" sub={k.last_used_at ? dateTime(k.last_used_at) : undefined}>
                  <When iso={k.last_used_at} now={now} />
                </Fact>
                <Fact label="Last worked" sub={k.last_ok_at ? dateTime(k.last_ok_at) : undefined}>
                  <When iso={k.last_ok_at} now={now} />
                </Fact>
                <Fact label="Last checked" sub={k.last_checked_at ? dateTime(k.last_checked_at) : undefined}>
                  {hasHealth ? <When iso={k.last_checked_at} now={now} /> : <span className="text-muted">No health check</span>}
                </Fact>
                <Fact label="Added" sub={dateTime(k.created_at)}>
                  <When iso={k.created_at} now={now} />
                </Fact>
                <Fact label="Requests today" sub={`${num(k.usage_total)} in total`}>
                  <span className="font-mono tabular-nums">{num(k.usage_today)}</span>
                </Fact>
                <Fact label="In use" sub={limit > 0 ? (k.max_concurrency !== null ? "Key limit" : "Service limit") : "No limit"}>
                  <span className="font-mono tabular-nums">{limit > 0 ? `${num(k.in_flight ?? 0)} of ${num(limit)}` : num(k.in_flight ?? 0)}</span>
                </Fact>
                {k.effective_status === "cooling" && k.cooldown_until && (
                  <Fact label="Cooling down" sub={dateTime(k.cooldown_until)} wide>
                    <span className="font-mono tabular-nums">Back in {countdown(k.cooldown_until, now)}</span>
                  </Fact>
                )}
                <Fact label="Quota" wide>
                  <QuotaMeter k={k} unit={unit} now={now} />
                </Fact>
                {k.last_error && (
                  <Fact label="Last error" wide>
                    <span className="wrap-anywhere text-red-700">{k.last_error}</span>
                  </Fact>
                )}
              </dl>
            </section>

            <section aria-labelledby="key-activity">
              <h3 id="key-activity" className="mb-2 text-[13px] font-semibold text-ink">
                Last 14 days
              </h3>
              {insight ? <ActivityChart daily={insight.daily ?? []} /> : <Skeleton className="h-24 rounded-xl" />}
            </section>

            <section aria-labelledby="key-events">
              <h3 id="key-events" className="mb-2 text-[13px] font-semibold text-ink">
                Recent events
              </h3>
              {!insight ? (
                <Skeleton className="h-20 rounded-xl" />
              ) : events.length === 0 ? (
                <p className="rounded-xl border border-dashed border-line-strong px-3 py-4 text-center text-[13px] text-muted">
                  No events yet. Requests, rate limits and checks for this key show up here.
                </p>
              ) : (
                <>
                  <ul className="divide-y divide-line rounded-xl border border-line">
                    {shownEvents.map((e) => (
                      <li key={e.id} className="flex items-start gap-2.5 px-3 py-2.5">
                        <div className="flex shrink-0 flex-col items-start gap-1 pt-0.5">
                          <KindBadge kind={e.kind} />
                        </div>
                        <div className="min-w-0 flex-1">
                          {e.detail ? (
                            <div className="wrap-anywhere text-[12px] leading-relaxed text-ink">{e.detail}</div>
                          ) : (
                            <div className="text-[12px] text-faint">No detail</div>
                          )}
                          <div className="mt-0.5 flex flex-wrap gap-x-2 font-mono text-[11px] text-muted">
                            {e.status_code > 0 && <span>HTTP {e.status_code}</span>}
                            {e.latency_ms > 0 && <span>{e.latency_ms}ms</span>}
                            {e.app_name && <span>app {e.app_name}</span>}
                          </div>
                        </div>
                        <time dateTime={e.ts} title={dateTime(e.ts)} className="shrink-0 font-mono text-[11px] text-faint">
                          {relTime(e.ts, now)}
                        </time>
                      </li>
                    ))}
                  </ul>
                  {events.length > shownEvents.length && (
                    <Button size="sm" variant="ghost" className="mt-2" onClick={() => setShowAll(true)}>
                      Show all {events.length}
                    </Button>
                  )}
                </>
              )}
            </section>
          </div>
        )}
      </Modal>
      {k && editing && (
        <KeyEditModal
          k={k}
          apps={apps}
          canHidden={perms.hidden}
          serviceMax={serviceMax}
          onClose={() => {
            setEditing(false);
            void fetchInsight(k.id).catch(() => undefined);
          }}
        />
      )}
      {/* A snapshot, so background refreshes never trigger a second (audited) reveal. */}
      <RevealModal k={revealing} onClose={() => setRevealing(null)} />
    </>
  );
}
