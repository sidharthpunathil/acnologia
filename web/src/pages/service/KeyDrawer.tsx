import { useEffect, useState, type ComponentType, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { RiEditLine, RiEyeLine, RiEyeOffLine, RiPulseLine, RiRestartLine } from "@remixicon/react";
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
import { useSheetLayout } from "../../components/Modal";
import { EntityLogo, ServiceLogo } from "../../components/EntityLogo";
import { StatusPill } from "../../components/StatusPill";
import { ActivityChart, OnChange, QuotaMeter, ValidityBadge, accountLabel, providerGivesReset, validityRepeatsStatus, validityText } from "../../components/KeyBits";
import { usePoll } from "../../hooks/usePoll";
import { useProviders } from "../../stores/providers";
import { KindBadge } from "../../components/EventsList";
import { useServices } from "../../stores/services";
import { Chip } from "@/components/base/badges/chip";
import { Button } from "@/components/base/buttons/button";
import { Drawer, DrawerBody, DrawerFooter, DrawerHeader } from "@/components/base/drawer/drawer";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { cx } from "@/utils/cx";
import { InUse, pillState } from "./KeysTable";
import { KeyEditModal } from "./KeyEditModal";
import { RevealModal } from "./RevealModal";
import { withLiveStatus } from "./keyUtil";
import { ExposureChips, useLoadHandovers } from "../../components/HandoverChip";
import { Callout } from "@/components/acn";

type IconComponent = ComponentType<{ className?: string; "aria-hidden"?: boolean | "true" | "false" }>;

function Fact({ label, children, sub, wide }: { label: string; children: ReactNode; sub?: ReactNode; wide?: boolean }) {
  return (
    <div className={cx("min-w-0 rounded-2xl bg-background-secondary-default px-3 py-2.5", wide && "col-span-2")}>
      <dt className="text-caption-1-medium text-text-secondary">{label}</dt>
      <dd className="mt-0.5 min-w-0 text-body-2-regular text-text-primary">{children}</dd>
      {sub && <dd className="mt-0.5 font-mono text-caption-1-regular text-text-secondary">{sub}</dd>}
    </div>
  );
}

/** Relative time as the value, exact time underneath so it is readable on touch screens too. */
function When({ iso, now, never = "Never" }: { iso: string | null | undefined; now: number; never?: string }) {
  if (!iso) return <span className="text-text-secondary">{never}</span>;
  return <time dateTime={iso}>{relTime(iso, now)}</time>;
}

const NO_HEALTH = "This provider has no health check, so Acnologia learns whether the key works from real requests.";

/**
 * Everything about one key: validity, timing, quota, 14 days of activity and
 * recent events. A side sheet on desktop and a bottom sheet on phones.
 */
export function KeyDrawer({ keyId, seed, onClose }: { keyId: number | null; seed?: Key | null; onClose: () => void }) {
  const sheet = useSheetLayout();
  const role = useAuth((s) => s.user?.role);
  const perms = { manage: can.manage(role), hidden: can.hidden(role), reveal: can.reveal(role) };
  const insight = useInsights((s) => (keyId ? s.byId[keyId] : undefined));
  const fetchInsight = useInsights((s) => s.fetchInsight);
  const { check, reset } = useKeys();
  const { apps, loaded: appsLoaded, fetch: fetchApps } = useApps();
  const services = useServices((s) => s.services);
  const now = useNow(1000);
  useLoadHandovers();
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
    return () => {
      live = false;
    };
  }, [keyId, fetchInsight]);
  // Every 4s while open and visible, so in use, cooldowns and events stay live.
  usePoll(() => keyId && fetchInsight(keyId).catch(() => undefined), 4000, keyId !== null);

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
  const provider = useProviders((s) => (service ? s.providers.find((p) => p.slug === service.provider) : undefined));

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

  const needsReset = !!k && (k.effective_status !== "active" || k.consecutive_fails > 0);
  const limit = k ? (k.max_concurrency ?? serviceMax) : 0;
  const events = insight?.events ?? [];
  const shownEvents = showAll ? events : events.slice(0, 8);

  type Action = { id: string; label: string; aria?: string; title?: string; icon: IconComponent; onClick: () => void; disabled: boolean; busy?: boolean; primary?: boolean };
  const actions: Action[] = k
    ? ([
        canTouch && { id: "check", label: "Check now", aria: "Check now", icon: RiPulseLine, onClick: () => void run("check"), disabled: busy !== null, busy: busy === "check", primary: true },
        canTouch && {
          id: "reset",
          label: "Reset",
          aria: "Reset: clear cooldown and errors",
          title: needsReset ? "Clear the cooldown and errors, and put the key back in rotation" : "Nothing to reset: the key is available",
          icon: RiRestartLine,
          onClick: () => void run("reset"),
          disabled: busy !== null || !needsReset,
          busy: busy === "reset",
        },
        canTouch && { id: "edit", label: "Edit", icon: RiEditLine, onClick: () => setEditing(true), disabled: false },
        perms.reveal && { id: "reveal", label: "Reveal", icon: RiEyeLine, onClick: () => raw && setRevealing(raw), disabled: false },
      ].filter(Boolean) as Action[])
    : [];

  return (
    <>
      <Drawer
        isOpen={keyId !== null}
        onOpenChange={(o) => !o && onClose()}
        placement={sheet ? "bottom" : "end"}
        size="md"
        aria-label={k ? undefined : "Key"}
        className={cx(sheet && "max-h-full")}
      >
        {k ? (
          <DrawerHeader
            title={
              <span className="flex min-w-0 items-center gap-3">
                {service ? <ServiceLogo service={service} size="md" /> : <EntityLogo src={svc?.logo_url} name={svc?.name ?? "?"} size="md" />}
                <span className="line-clamp-2 min-w-0 break-words" title={k.label}>
                  {k.label || "Untitled key"}
                </span>
              </span>
            }
          >
            <div className="mt-1.5 flex min-w-0 items-center gap-x-1.5 font-mono text-caption-1-regular text-text-secondary">
              <span className="shrink-0">{k.hint}</span>
              {svc && (
                <>
                  <span aria-hidden="true">·</span>
                  <Link
                    to={`/services/${svc.id}`}
                    onClick={onClose}
                    className="min-w-0 truncate rounded-sm underline-offset-2 outline-none hover:text-text-primary hover:underline focus-visible:ring-2 focus-visible:ring-border-focus-ring"
                  >
                    {svc.name}
                  </Link>
                </>
              )}
            </div>
            <div className="mt-2.5 flex flex-wrap items-center gap-1.5">
              <OnChange id={pillState(k, serviceMax)}>
                <StatusPill k={k} now={now} serviceMax={serviceMax} />
              </OnChange>
              {!validityRepeatsStatus(k) && <ValidityBadge validity={k.validity} />}
              <InUse k={k} serviceMax={serviceMax} />
              {k.tier === "backup" && (
                <Chip variant="caption" color="blue" className="rounded-full px-2 py-0.5 font-mono">
                  backup
                </Chip>
              )}
              {k.hidden && (
                <Chip variant="caption" color="gray" title="Hidden: only superadmins can see or count this key" className="gap-1 rounded-full px-2 py-0.5 font-mono">
                  <RiEyeOffLine className="size-3" aria-hidden />
                  hidden
                </Chip>
              )}
              <ExposureChips k={k} className="py-0.5" />
            </div>
          </DrawerHeader>
        ) : (
          <DrawerHeader title={<Skeleton shape="text" className="h-5 w-40" />}>
            <Skeleton shape="text" className="mt-2 h-3 w-48" />
          </DrawerHeader>
        )}

        <DrawerBody className="pb-4">
          {!k && loadErr ? (
            <Callout tone="error">Could not load this key: {loadErr}</Callout>
          ) : !k ? (
            <div className="space-y-3" role="status" aria-label="Loading key">
              <Skeleton className="h-24 rounded-2xl" />
              <Skeleton className="h-32 rounded-2xl" />
            </div>
          ) : (
            <div className="space-y-6">
              {(noHealth || (insight && !hasHealth)) && <Callout tone={noHealth ? "warning" : "neutral"}>{NO_HEALTH}</Callout>}
              <p className="text-body-2-regular text-text-secondary">
                <span className="text-body-2-medium text-text-primary">
                  {k.validity === "valid" ? "Works. " : k.validity === "invalid" ? "Not working. " : "Not known yet. "}
                </span>
                <span className="wrap-anywhere">{validityText(k, now).replace(/\.$/, "")}.</span>
              </p>

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
                    {hasHealth ? <When iso={k.last_checked_at} now={now} /> : <span className="text-text-secondary">No health check</span>}
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
                    <QuotaMeter k={k} unit={unit} now={now} resetExact={providerGivesReset(provider)} />
                  </Fact>
                  {accountLabel(k) && (
                    <>
                      <Fact label="Account name">{k.account_name ? <span className="wrap-anywhere">{k.account_name}</span> : <span className="text-text-secondary">Not set</span>}</Fact>
                      <Fact label="Account email">{k.account_email ? <span className="wrap-anywhere">{k.account_email}</span> : <span className="text-text-secondary">Not set</span>}</Fact>
                      <Fact label="Account phone">{k.account_phone ? <span className="wrap-anywhere">{k.account_phone}</span> : <span className="text-text-secondary">Not set</span>}</Fact>
                      <Fact label="Other identifier">{k.account_ref ? <span className="wrap-anywhere">{k.account_ref}</span> : <span className="text-text-secondary">Not set</span>}</Fact>
                    </>
                  )}
                  {/* For an invalid key the line above already quotes this error. */}
                  {k.last_error && k.validity !== "invalid" && (
                    <Fact label="Last error" wide>
                      <span className="wrap-anywhere text-status-rose-text">{k.last_error}</span>
                    </Fact>
                  )}
                </dl>
              </section>

              <section aria-labelledby="key-activity">
                <h3 id="key-activity" className="mb-2 text-body-2-semibold text-text-primary">
                  Last 14 days
                </h3>
                {insight ? <ActivityChart daily={insight.daily ?? []} /> : <Skeleton className="h-24 rounded-2xl" />}
              </section>

              <section aria-labelledby="key-events">
                <h3 id="key-events" className="mb-2 text-body-2-semibold text-text-primary">
                  Recent events
                </h3>
                {!insight ? (
                  <Skeleton className="h-20 rounded-2xl" />
                ) : events.length === 0 ? (
                  <p className="rounded-2xl border border-dashed border-border-button-hover px-3 py-4 text-center text-body-2-regular text-text-secondary">
                    No events yet. Requests, rate limits and checks for this key show up here.
                  </p>
                ) : (
                  <>
                    <ul className="overflow-hidden rounded-2xl border border-border-button-default">
                      {shownEvents.map((e) => (
                        <li key={e.id} className="flex items-start gap-2.5 border-b border-separator-border px-3 py-2.5 last:border-b-0">
                          <div className="shrink-0 pt-0.5">
                            <KindBadge kind={e.kind} detail={e.detail} />
                          </div>
                          <div className="min-w-0 flex-1">
                            {e.detail ? (
                              <div className="wrap-anywhere text-caption-1-regular text-text-primary">{e.detail}</div>
                            ) : (
                              <div className="text-caption-1-regular text-text-tertiary">No detail</div>
                            )}
                            <div className="mt-0.5 flex flex-wrap gap-x-2 font-mono text-caption-1-regular text-text-secondary">
                              {e.status_code > 0 && <span>HTTP {e.status_code}</span>}
                              {e.latency_ms > 0 && <span>{e.latency_ms}ms</span>}
                              {e.app_name && <span>app {e.app_name}</span>}
                            </div>
                          </div>
                          <time dateTime={e.ts} title={dateTime(e.ts)} className="shrink-0 font-mono text-caption-1-regular text-text-tertiary">
                            {relTime(e.ts, now)}
                          </time>
                        </li>
                      ))}
                    </ul>
                    {events.length > shownEvents.length && (
                      <Button size="small" variant="ghost" className="mt-2" onClick={() => setShowAll(true)}>
                        Show all {events.length}
                      </Button>
                    )}
                  </>
                )}
              </section>
            </div>
          )}
        </DrawerBody>

        {k && actions.length > 0 && (
          <DrawerFooter className="flex-row">
            {/* Phones: one row of icon-over-label buttons, so short screens keep room for details. */}
            <div className="grid w-full gap-1.5 dlg:hidden" style={{ gridTemplateColumns: `repeat(${actions.length}, minmax(0, 1fr))` }}>
              {actions.map((a) => {
                const Icon = a.busy ? ButtonSpinner : a.icon;
                return (
                  <button
                    key={a.id}
                    type="button"
                    onClick={a.onClick}
                    disabled={a.disabled}
                    aria-label={a.aria}
                    aria-busy={a.busy || undefined}
                    className={cx(
                      "flex min-h-[52px] cursor-pointer flex-col items-center justify-center gap-1 rounded-2xl px-1 text-caption-1-medium outline-none transition-colors duration-150",
                      "focus-visible:ring-2 focus-visible:ring-border-focus-ring disabled:cursor-not-allowed",
                      a.primary
                        ? "bg-button-primary text-text-white disabled:opacity-60"
                        : "bg-background-tertiary-default text-text-primary active:bg-background-tertiary-hover disabled:text-text-tertiary",
                    )}
                  >
                    <Icon className="size-4" aria-hidden />
                    {a.id === "check" ? "Check" : a.label}
                  </button>
                );
              })}
            </div>
            <div className="hidden w-full justify-end gap-2 dlg:flex">
              {actions.map((a) => (
                <Button
                  key={a.id}
                  variant={a.primary ? "primary" : "secondary"}
                  leadingIcon={a.busy ? ButtonSpinner : a.icon}
                  disabled={a.disabled}
                  aria-busy={a.busy || undefined}
                  title={a.title}
                  onClick={a.onClick}
                >
                  {a.label}
                </Button>
              ))}
            </div>
          </DrawerFooter>
        )}
      </Drawer>
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
