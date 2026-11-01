import { useEffect, useMemo, useState } from "react";
import { useTitle } from "../../hooks/useTitle";
import { Link } from "react-router-dom";
import { Blocks, Plus, Search, Sparkles } from "lucide-react";
import { useProviders } from "../../stores/providers";
import { useAuth } from "../../stores/auth";
import { can } from "../../lib/perms";
import { plural, relTime } from "../../lib/format";
import type { Provider } from "../../lib/types";
import { ProviderLogo } from "../../components/EntityLogo";
import { Tabs } from "../../components/Tabs";
import { Badge, Callout, Empty, PageHeader, PageSkeleton, cx } from "../../components/ui";

export function SourceBadge({ source }: { source: Provider["source"] }) {
  if (source === "ai")
    return (
      <Badge tone="violet" title="Written by the AI builder">
        <Sparkles className="h-3 w-3" aria-hidden="true" />
        ai
      </Badge>
    );
  if (source === "builtin") return <Badge title="Ships with Cogito">builtin</Badge>;
  return <Badge tone="blue">manual</Badge>;
}

function ProviderCard({ p }: { p: Provider }) {
  const disabled = p.status === "disabled";
  // No keys yet: keep the card quiet until it is hovered or focused.
  const idle = (p.keys_count ?? 0) === 0;
  return (
    <Link
      to={`/providers/${encodeURIComponent(p.slug)}`}
      className={cx(
        "group flex flex-col gap-3 rounded-card p-4 transition-[box-shadow,transform,background-color] duration-150 ease-out hover:-translate-y-px active:translate-y-0 active:scale-[0.99] hover:shadow-lift",
        idle ? "bg-surface/55 shadow-none ring-1 ring-inset ring-line hover:bg-surface" : "bg-surface shadow-card",
      )}
    >
      <div className="flex items-start gap-3.5">
        <span className={cx("shrink-0", idle && "grayscale opacity-50 transition-[filter,opacity] duration-200 ease-out group-hover:grayscale-0 group-hover:opacity-100 group-focus-visible:grayscale-0 group-focus-visible:opacity-100")}>
          <ProviderLogo provider={p} />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className={disabled || idle ? "truncate text-[15px] font-medium text-muted" : "truncate text-[15px] font-medium text-ink"}>{p.name}</span>
            {disabled && <Badge>disabled</Badge>}
          </div>
          <div className="mt-1 truncate font-mono text-xs text-muted">
            {p.slug} · {p.category || "Other"} · v{p.version}
          </div>
        </div>
      </div>
      {p.description && <p className="line-clamp-2 text-[13px] leading-relaxed text-muted">{p.description}</p>}
      <div className="mt-auto flex flex-wrap items-center gap-1.5 border-t border-line pt-3">
        <SourceBadge source={p.source} />
        <Badge tone={p.services_count > 0 ? "green" : "neutral"}>{plural(p.services_count, "service")}</Badge>
        {idle ? <Badge>No keys</Badge> : <Badge tone="green">{plural(p.keys_count, "key")}</Badge>}
        <span className="ml-auto font-mono text-[11px] text-faint">{p.updated_at ? `updated ${relTime(p.updated_at)}` : ""}</span>
      </div>
    </Link>
  );
}

const ALL = "__all";

export function ProvidersPage() {
  useTitle("Providers");
  const { providers, loaded, error, fetch } = useProviders();
  const role = useAuth((s) => s.user?.role);
  const manage = can.manage(role);
  const [q, setQ] = useState("");
  const [tab, setTab] = useState<string>(ALL);

  useEffect(() => {
    fetch().catch(() => undefined);
  }, [fetch]);

  const filtered = useMemo(() => {
    const n = q.trim().toLowerCase();
    const list = n ? providers.filter((p) => [p.name, p.slug, p.category, p.description].some((f) => f?.toLowerCase().includes(n))) : providers;
    return [...list].sort((a, b) => a.name.localeCompare(b.name));
  }, [providers, q]);

  const counts = useMemo(() => {
    const c = { builtin: 0, manual: 0, ai: 0 };
    for (const p of filtered) c[p.source] = (c[p.source] ?? 0) + 1;
    return c;
  }, [filtered]);

  const tabs = [
    { id: ALL, label: "All", count: filtered.length },
    { id: "builtin", label: "Built in", count: counts.builtin },
    { id: "ai", label: "Built with AI", count: counts.ai },
    { id: "manual", label: "Manual", count: counts.manual },
  ];
  const shown = tab === ALL ? filtered : filtered.filter((p) => p.source === tab);

  if (!loaded) return <PageSkeleton />;

  const actions = manage ? (
    <>
      <Link
        to="/providers/new"
        className="tap inline-flex h-10 items-center gap-2 rounded-full bg-surface px-4 text-sm font-medium text-ink shadow-card transition-colors hover:bg-soft"
      >
        <Plus className="h-4 w-4" /> New provider
      </Link>
      <Link
        to="/providers/ai"
        className="tap inline-flex h-10 items-center gap-2 rounded-full bg-ink px-4 text-sm font-medium text-on-ink transition-colors hover:bg-ink-strong"
      >
        <Sparkles className="h-4 w-4 text-spark" /> Build with AI
      </Link>
    </>
  ) : undefined;

  return (
    <div>
      <PageHeader
        title="Providers"
        meta={`${plural(providers.length, "provider")} · how each API signals limits, plus its health check and tests`}
        actions={actions}
      />

      {error && providers.length === 0 && (
        <Callout tone="red" className="mb-6">
          Could not load providers: {error}
        </Callout>
      )}

      <div className="relative max-w-[760px]">
        <Search className="pointer-events-none absolute left-4 top-1/2 h-4 w-4 -translate-y-1/2 text-muted" aria-hidden="true" />
        <input
          type="search"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search providers"
          enterKeyHint="search"
          aria-label="Search providers"
          className="field-input h-[52px] w-full rounded-card bg-surface pl-11 pr-4 text-[15px] text-ink shadow-card placeholder:text-muted focus:outline-none focus:ring-2 focus:ring-ink/20"
        />
      </div>

      <Tabs className="mt-8" items={tabs} value={tab} onChange={setTab} />

      {providers.length === 0 ? (
        <Empty
          className="mt-8"
          icon={<Blocks className="h-5 w-5" />}
          title="No providers yet"
          body="A provider tells Cogito how an API signals rate limits and quota. Build one with AI from a name or docs URL, or write the rules yourself."
          action={actions}
        />
      ) : shown.length === 0 ? (
        <Empty
          className="mt-8"
          title={q ? `Nothing matches "${q}"` : "None in this group yet"}
          body={manage ? "Try another search, or build the provider with AI." : "Try another search."}
          action={
            manage ? (
              <Link to="/providers/ai" className="tap inline-flex h-10 items-center gap-2 rounded-full bg-ink px-4 text-sm font-medium text-on-ink hover:bg-ink-strong">
                <Sparkles className="h-4 w-4" /> Build with AI
              </Link>
            ) : undefined
          }
        />
      ) : (
        <div className="mt-6 grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {shown.map((p) => (
            <ProviderCard key={p.slug} p={p} />
          ))}
        </div>
      )}
    </div>
  );
}
