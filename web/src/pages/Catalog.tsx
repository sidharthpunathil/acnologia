import { useEffect, useMemo, useState } from "react";
import { Link as RouterLink } from "react-router-dom";
import { Link } from "react-aria-components";
import { RiAddLine, RiLayoutGridLine, RiSparkling2Line } from "@remixicon/react";
import { useTitle } from "../hooks/useTitle";
import { useServices } from "../stores/services";
import { useProviders } from "../stores/providers";
import { useAuth } from "../stores/auth";
import { toast } from "../stores/toast";
import { errorMessage } from "../lib/api";
import { can } from "../lib/perms";
import { STRATEGY_LABEL, compact, plural } from "../lib/format";
import type { Service } from "../lib/types";
import { ServiceLogo } from "../components/EntityLogo";
import { Button } from "@/components/base/buttons/button";
import { SearchField } from "@/components/base/search-field/search-field";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { Tab, TabList, TabPanel, Tabs } from "@/components/base/tabs/tabs";
import { cx } from "@/utils/cx";
import { AddServiceModal } from "./AddServiceModal";
import { Callout, EmptyState, PageHeader } from "@/components/acn";
import { CARD } from "./service/parts";

const ALL = "__all";

type MetaPart = { text: string; tone?: "red" | "amber" | "violet" };

function metaParts(s: Service): MetaPart[] {
  const c = s.key_counts;
  // Problems first and in color, so a narrow card never hides them behind the totals.
  const parts: MetaPart[] = [];
  if (c.invalid) parts.push({ text: `${c.invalid} invalid`, tone: "red" });
  if (c.banned) parts.push({ text: `${c.banned} banned`, tone: "red" });
  if (c.exhausted) parts.push({ text: `${c.exhausted} out of quota`, tone: "amber" });
  if (c.cooling) parts.push({ text: `${c.cooling} cooling`, tone: "amber" });
  if (c.busy) parts.push({ text: `${c.busy} busy`, tone: "violet" });
  if (c.total > 0) parts.push({ text: `${Math.max(0, c.available - (c.busy ?? 0))} of ${plural(c.total, "key")} available` });
  else parts.push({ text: "No keys yet" });
  return parts;
}

/** The card line plus the rotation strategy, for the tooltip and anywhere text is enough. */
export function serviceMeta(s: Service): string {
  return [...metaParts(s).map((p) => p.text), STRATEGY_LABEL[s.strategy] ?? s.strategy].join(" · ");
}

const TONE = { red: "text-text-error-primary", amber: "text-status-yellow-text", violet: "text-status-purple-text" } as const;

/** Green when a key is free, red when every key is broken, amber while keys only rest, gray with no keys. */
function healthDot(s: Service): { cls: string; label: string } {
  const c = s.key_counts;
  if (c.total === 0) return { cls: "bg-border-button-hover", label: "No keys yet" };
  if (c.available > 0) return { cls: "bg-notification-success-foreground", label: "Keys available" };
  if (c.cooling || c.exhausted || c.busy) return { cls: "bg-chart-8-active", label: "Keys resting, none free right now" };
  return { cls: "bg-notification-error-foreground", label: "No working keys" };
}

function categoryOf(s: Service): string {
  return s.category?.trim() || "Other";
}

/** The provider's free tier note, such as "1,000 searches a month", when its spec has one. */
function FreeTierLine({ provider }: { provider: string }) {
  const freeTier = useProviders((st) => st.providers.find((p) => p.slug === provider)?.spec?.quota?.free_tier);
  if (!freeTier) return null;
  return (
    <div className="mt-1 truncate text-caption-1-regular text-text-tertiary" title={freeTier}>
      Free tier: {freeTier}
    </div>
  );
}

function ServiceCard({ s }: { s: Service }) {
  const dot = healthDot(s);
  const parts = metaParts(s);
  // No keys yet: keep the card quiet until it is hovered or focused.
  const idle = s.key_counts.total === 0;
  return (
    <Link
      href={`/services/${s.id}`}
      className={({ isFocusVisible }) =>
        cx(
          "group flex min-h-[76px] items-center gap-3.5 rounded-3xl border px-4 py-4 outline-none",
          "transition-[background-color,border-color] duration-150 ease",
          idle
            ? "border-border-button-default bg-background-secondary-default hover:bg-background-primary-default"
            : "border-border-button-default bg-background-primary-default hover:border-border-button-hover hover:bg-background-primary-hover",
          "data-[pressed]:bg-background-primary-active",
          isFocusVisible && "ring-2 ring-border-focus-ring",
        )
      }
    >
      <span
        className={cx(
          "shrink-0",
          idle &&
            "opacity-50 grayscale transition-[filter,opacity] duration-200 ease-out group-hover:opacity-100 group-hover:grayscale-0 group-data-[focus-visible]:opacity-100 group-data-[focus-visible]:grayscale-0",
        )}
      >
        <ServiceLogo service={s} />
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className={cx("min-w-0 truncate text-body-medium", idle ? "text-text-secondary" : "text-text-primary")}>{s.name}</span>
          <span className={cx("size-1.5 shrink-0 rounded-full", dot.cls)} title={dot.label} aria-hidden="true" />
          <span className="sr-only">{dot.label}</span>
        </div>
        <div className="mt-1 line-clamp-2 font-mono text-caption-1-regular text-text-secondary" title={serviceMeta(s)}>
          {parts.map((p, i) => (
            <span key={p.text}>
              {i > 0 && <span className="text-text-tertiary"> · </span>}
              <span className={cx("whitespace-nowrap", p.tone && TONE[p.tone])}>{p.text}</span>
            </span>
          ))}
        </div>
        <FreeTierLine provider={s.provider} />
      </div>
    </Link>
  );
}

function CatalogSkeleton() {
  return (
    <div role="status" aria-label="Loading services">
      <Skeleton shape="text" className="h-8 w-80 max-w-full" />
      <Skeleton shape="text" className="mt-3 h-3.5 w-56" />
      <Skeleton className="mt-8 h-12 w-full max-w-[760px] rounded-full" />
      <div className="mt-12 grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {[0, 1, 2, 3, 4, 5].map((i) => (
          <div key={i} className={cx(CARD, "flex items-center gap-3.5 px-4 py-4")}>
            <Skeleton className="size-10" />
            <div className="flex-1">
              <Skeleton shape="text" className="h-4 w-28" />
              <Skeleton shape="text" className="mt-2 h-3 w-40" />
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

export function CatalogPage() {
  useTitle("Catalog");
  const { services, loaded, error, fetch } = useServices();
  const ensureProviders = useProviders((s) => s.ensure);
  const role = useAuth((s) => s.user?.role);
  const [q, setQ] = useState("");
  const [tab, setTab] = useState<string>(ALL);
  const [adding, setAdding] = useState(false);

  useEffect(() => {
    fetch().catch((e) => toast.error(errorMessage(e)));
    // Provider colors and logos back the service tiles.
    ensureProviders().catch(() => undefined);
  }, [fetch, ensureProviders]);

  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase();
    if (!needle) return services;
    return services.filter((s) =>
      [s.name, s.slug, s.category, s.description, s.provider].some((f) => f?.toLowerCase().includes(needle)),
    );
  }, [services, q]);

  const groups = useMemo(() => {
    const m = new Map<string, Service[]>();
    for (const s of filtered) {
      const c = categoryOf(s);
      if (!m.has(c)) m.set(c, []);
      m.get(c)!.push(s);
    }
    for (const list of m.values()) list.sort((a, b) => a.name.localeCompare(b.name));
    return [...m.entries()].sort((a, b) => a[0].localeCompare(b[0]));
  }, [filtered]);

  const totalKeys = services.reduce((n, s) => n + (s.key_counts?.total ?? 0), 0);
  const available = services.reduce((n, s) => n + (s.key_counts?.available ?? 0), 0);
  const req24 = services.reduce((n, s) => n + (s.requests_24h ?? 0), 0);

  const tabs = [{ id: ALL, label: "All", count: filtered.length }, ...groups.map(([c, list]) => ({ id: c, label: c, count: list.length }))];
  const activeTab = tabs.some((t) => t.id === tab) ? tab : ALL;
  const shown = activeTab === ALL ? groups : groups.filter(([c]) => c === activeTab);

  if (!loaded) return <CatalogSkeleton />;

  return (
    <div>
      <PageHeader
        title={`${compact(totalKeys)} ${totalKeys === 1 ? "key" : "keys"} across ${plural(services.length, "service")}`}
        meta={`${available} available now · ${compact(req24)} requests in 24h`}
        actions={
          can.manage(role) && (
            <Button variant="primary" leadingIcon={RiAddLine} onClick={() => setAdding(true)}>
              Add service
            </Button>
          )
        }
        className="mb-0"
      />
      {error && services.length === 0 && (
        <Callout
          tone="error"
          className="mt-6"
          action={
            <Button variant="secondary" size="small" onClick={() => fetch().catch(() => undefined)}>
              Try again
            </Button>
          }
        >
          Could not load services: {error}.
        </Callout>
      )}

      <SearchField
        value={q}
        onChange={setQ}
        placeholder="Search by name, slug or category"
        aria-label="Search services"
        enterKeyHint="search"
        className="mt-8 max-w-[760px]"
        fieldClassName="h-12 bg-background-primary-default border border-border-button-default"
      />

      {services.length === 0 ? (
        <EmptyState
          className="mt-12"
          as="h2"
          icon={RiLayoutGridLine}
          title="No services yet"
          body={
            can.manage(role)
              ? "Pick a provider, then paste in a key from each account you own. Provider not listed? Build it with AI in a minute."
              : "No services yet. Ask an admin to add one with its keys."
          }
          action={
            can.manage(role) ? (
              <>
                <Button variant="primary" leadingIcon={RiAddLine} onClick={() => setAdding(true)}>
                  Add your first service
                </Button>
                <RouterLink
                  to="/providers/ai"
                  className="inline-flex h-9 items-center gap-1.5 rounded-2lg border border-border-button-default bg-background-primary-default px-3 text-body-medium text-text-primary shadow-xs outline-none transition-colors duration-150 hover:bg-background-primary-hover focus-visible:ring-2 focus-visible:ring-border-focus-ring"
                >
                  <RiSparkling2Line className="size-5 text-accent-fg" aria-hidden /> Build a provider with AI
                </RouterLink>
              </>
            ) : undefined
          }
        />
      ) : (
        <Tabs selectedKey={activeTab} onSelectionChange={(k) => setTab(String(k))} className="mt-10 gap-0">
          <div className="-mx-4 overflow-x-auto px-4 [scrollbar-width:none] sm:mx-0 sm:px-0 [&::-webkit-scrollbar]:hidden">
            <TabList aria-label="Categories" className="min-w-max">
              {tabs.map((t) => (
                <Tab key={t.id} id={t.id} count={t.count}>
                  {t.label}
                </Tab>
              ))}
            </TabList>
          </div>
          <TabPanel id={activeTab} className="focus-visible:ring-offset-4">
            {shown.length === 0 ? (
              <EmptyState
                className="mt-10"
                as="h2"
                title={`Nothing matches "${q}"`}
                body="Try another name, or clear the search."
                action={
                  <Button variant="secondary" onClick={() => setQ("")}>
                    Clear search
                  </Button>
                }
              />
            ) : (
              shown.map(([cat, list]) => {
                const keys = list.reduce((n, s) => n + s.key_counts.total, 0);
                return (
                  <section key={cat} className="mt-10" aria-labelledby={`cat-${cat}`}>
                    <div className="flex items-baseline gap-2.5">
                      <h2 id={`cat-${cat}`} className="text-headline-semibold text-text-primary">
                        {cat}
                      </h2>
                      <span className="font-mono text-caption-1-regular text-text-tertiary">{list.length}</span>
                    </div>
                    <p className="mt-1 text-body-2-regular text-text-secondary">
                      {plural(keys, "key")} pooled across {plural(list.length, "service")}
                    </p>
                    <div className="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
                      {list.map((s) => (
                        <ServiceCard key={s.id} s={s} />
                      ))}
                    </div>
                  </section>
                );
              })
            )}
          </TabPanel>
        </Tabs>
      )}

      <AddServiceModal open={adding} onClose={() => setAdding(false)} />
    </div>
  );
}
