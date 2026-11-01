import { useEffect, useMemo, useState } from "react";
import { useTitle } from "../hooks/useTitle";
import { Link } from "react-router-dom";
import { Layers, Plus, Search, Sparkles } from "lucide-react";
import { useServices } from "../stores/services";
import { useProviders } from "../stores/providers";
import { useAuth } from "../stores/auth";
import { toast } from "../stores/toast";
import { errorMessage } from "../lib/api";
import { can } from "../lib/perms";
import { STRATEGY_LABEL, compact, plural } from "../lib/format";
import type { Service } from "../lib/types";
import { Tabs } from "../components/Tabs";
import { ServiceLogo } from "../components/EntityLogo";
import { Button, Callout, Empty, PageSkeleton } from "../components/ui";
import { AddServiceModal } from "./AddServiceModal";

const ALL = "__all";

export function serviceMeta(s: Service): string {
  const c = s.key_counts;
  // Problems first, so a narrow card never hides them behind the totals.
  const parts: string[] = [];
  if (c.invalid) parts.push(`${c.invalid} invalid`);
  if (c.exhausted) parts.push(`${c.exhausted} out of quota`);
  if (c.busy) parts.push(`${c.busy} busy`);
  if (c.cooling) parts.push(`${c.cooling} cooling`);
  parts.push(plural(c.total, "key"));
  if (c.total > 0) parts.push(`${Math.max(0, c.available - (c.busy ?? 0))} available`);
  parts.push(STRATEGY_LABEL[s.strategy] ?? s.strategy);
  return parts.join(" · ");
}

function categoryOf(s: Service): string {
  return s.category?.trim() || "Other";
}

/** The provider's free tier note, such as "1,000 searches a month", when its spec has one. */
function FreeTierLine({ provider }: { provider: string }) {
  const freeTier = useProviders((st) => st.providers.find((p) => p.slug === provider)?.spec?.quota?.free_tier);
  if (!freeTier) return null;
  return (
    <div className="mt-0.5 truncate text-[11px] text-faint" title={freeTier}>
      Free tier: {freeTier}
    </div>
  );
}

function ServiceCard({ s }: { s: Service }) {
  const healthy = s.key_counts.available > 0;
  const meta = serviceMeta(s);
  return (
    <Link
      to={`/services/${s.id}`}
      className="group flex min-h-[72px] items-center gap-3.5 rounded-card bg-surface px-4 py-4 shadow-card transition-[box-shadow,transform] duration-150 ease-out hover:-translate-y-px active:translate-y-0 active:scale-[0.99] hover:shadow-lift"
    >
      <ServiceLogo service={s} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="min-w-0 truncate text-[15px] font-medium text-ink">{s.name}</span>
          <span
            className={healthy ? "h-1.5 w-1.5 shrink-0 rounded-full bg-emerald-500" : "h-1.5 w-1.5 shrink-0 rounded-full bg-line-strong"}
            aria-hidden="true"
          />
          <span className="sr-only">{healthy ? "Keys available" : "No key available"}</span>
        </div>
        <div className="mt-1 line-clamp-2 font-mono text-xs text-muted" title={meta}>
          {meta}
        </div>
        <FreeTierLine provider={s.provider} />
      </div>
    </Link>
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

  if (!loaded) return <PageSkeleton />;

  return (
    <div>
      <div className="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 className="font-mono text-[26px] leading-tight tracking-tight text-ink sm:text-[32px]">
            {compact(totalKeys)} {totalKeys === 1 ? "key" : "keys"} across {plural(services.length, "service")}
          </h1>
          <p className="mt-2 font-mono text-xs text-muted">
            {available} available now · {compact(req24)} requests in 24h
          </p>
        </div>
        {can.manage(role) && (
          <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={() => setAdding(true)}>
            Add service
          </Button>
        )}
      </div>
      {error && services.length === 0 && (
        <Callout tone="red" className="mt-6">
          Could not load services: {error}.{" "}
          <button type="button" className="font-medium underline underline-offset-2" onClick={() => fetch().catch(() => undefined)}>
            Try again
          </button>
        </Callout>
      )}

      <div className="relative mt-8 max-w-[760px]">
        <Search className="pointer-events-none absolute left-4 top-1/2 h-4 w-4 -translate-y-1/2 text-muted" />
        <input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search by name, slug or category"
          enterKeyHint="search"
          aria-label="Search services"
          type="search"
          className="field-input h-[52px] w-full rounded-card bg-surface pl-11 pr-4 text-[15px] text-ink shadow-card placeholder:text-muted focus:outline-none focus:ring-2 focus:ring-ink/20"
        />
      </div>

      {services.length === 0 ? (
        <div className="mt-12">
          <Empty
            icon={<Layers className="h-5 w-5" />}
            title="No services yet"
            body={
              can.manage(role)
                ? "Pick a provider, then paste in a key from each account you own. Provider not listed? Build it with AI in a minute."
                : "No services yet. Ask an admin to add one with its keys."
            }
            action={
              can.manage(role) ? (
                <>
                  <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={() => setAdding(true)}>
                    Add your first service
                  </Button>
                  <Link
                    to="/providers/ai"
                    className="tap inline-flex h-10 items-center gap-2 rounded-full bg-surface px-4 text-sm font-medium text-ink shadow-card hover:bg-soft"
                  >
                    <Sparkles className="h-4 w-4 text-brand" /> Build a provider with AI
                  </Link>
                </>
              ) : undefined
            }
          />
        </div>
      ) : (
        <>
          <Tabs className="mt-10" items={tabs} value={activeTab} onChange={setTab} />
          {shown.length === 0 ? (
            <div className="mt-10">
              <Empty title={`Nothing matches "${q}"`} body="Try another name, or clear the search." action={<Button onClick={() => setQ("")}>Clear search</Button>} />
            </div>
          ) : (
            shown.map(([cat, list]) => {
              const keys = list.reduce((n, s) => n + s.key_counts.total, 0);
              return (
                <section key={cat} className="mt-10">
                  <div className="flex items-baseline gap-3">
                    <h2 className="text-[19px] font-semibold tracking-tight text-ink">{cat}</h2>
                    <span className="font-mono text-xs text-faint">{list.length}</span>
                  </div>
                  <p className="mt-1.5 text-[13px] text-muted">
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
        </>
      )}

      <AddServiceModal open={adding} onClose={() => setAdding(false)} />
    </div>
  );
}
