import { useEffect, useMemo, useState, type MouseEvent } from "react";
import { useTitle } from "../../hooks/useTitle";
import { Link, useNavigate } from "react-router-dom";
import { RiAddLine, RiSearchLine } from "@remixicon/react";
import { AcnAiBuilder, AcnProviderSpec, categoryIcon } from "@/components/foundations/icons/acn";
import { useProviders } from "../../stores/providers";
import { useAuth } from "../../stores/auth";
import { can } from "../../lib/perms";
import { dateTime, plural, relTime } from "../../lib/format";
import type { Provider } from "../../lib/types";
import { ProviderLogo } from "../../components/EntityLogo";
import { ButtonLink, type ButtonLinkProps } from "@/components/base/buttons/button";
import { Chip } from "@/components/base/badges/chip";
import { SearchField } from "@/components/base/search-field/search-field";
import { Tab, TabList, TabPanel, Tabs } from "@/components/base/tabs/tabs";
import { cx } from "@/utils/cx";
import { Callout } from "@/components/acn/callout";
import { EmptyState } from "@/components/acn/empty-state";
import { PageHeader } from "@/components/acn/page-header";
import { PageSkeleton } from "@/components/acn/page-skeleton";
import { SearchArt, SpecSheetArt } from "@/components/foundations/illustrations";
import { FreeOfferChip } from "../../components/OfferBits";

/**
 * ButtonLink that routes on the client. A plain click goes through the
 * router; modified clicks (new tab, new window) keep the browser default.
 */
export function RouteButton({ to, ...props }: Omit<ButtonLinkProps, "href"> & { to: string }) {
  const navigate = useNavigate();
  return (
    <ButtonLink
      {...props}
      href={to}
      onClick={(e: MouseEvent<HTMLAnchorElement>) => {
        props.onClick?.(e);
        if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
        e.preventDefault();
        navigate(to);
      }}
    />
  );
}

export function SourceBadge({ source }: { source: Provider["source"] }) {
  if (source === "ai")
    return (
      <Chip variant="caption" color="purple" title="Written by the AI builder" className="gap-1 font-mono">
        <AcnAiBuilder className="size-3" aria-hidden />
        ai
      </Chip>
    );
  if (source === "builtin")
    return (
      <Chip variant="caption" color="neutral" title="Ships with Acnologia" className="font-mono">
        builtin
      </Chip>
    );
  return (
    <Chip variant="caption" color="blue" className="font-mono">
      manual
    </Chip>
  );
}

function ProviderCard({ p }: { p: Provider }) {
  const disabled = p.status === "disabled";
  // No keys yet: keep the card quiet until it is hovered or focused.
  const idle = (p.keys_count ?? 0) === 0;
  const CategoryMark = categoryIcon(p.category);
  return (
    <Link
      to={`/providers/${encodeURIComponent(p.slug)}`}
      className={cx(
        "group flex flex-col gap-3 rounded-3xl border p-5 outline-none",
        "transition-[background-color,border-color] duration-150 ease",
        "focus-visible:ring-2 focus-visible:ring-border-focus-ring",
        idle
          ? "border-border-button-default bg-background-primary-default/50 hover:border-border-button-hover hover:bg-background-primary-default"
          : "border-border-button-default bg-background-primary-default hover:border-border-button-hover active:bg-background-primary-active",
      )}
    >
      <div className="flex items-start gap-3.5">
        <span
          className={cx(
            "shrink-0",
            idle &&
              "opacity-50 grayscale transition-[filter,opacity] duration-200 ease-out group-hover:opacity-100 group-hover:grayscale-0 group-focus-visible:opacity-100 group-focus-visible:grayscale-0",
          )}
        >
          <ProviderLogo provider={p} />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className={cx("truncate text-body-medium", disabled || idle ? "text-text-secondary" : "text-text-primary")}>{p.name}</span>
            {disabled && <Chip variant="caption" color="yellow">disabled</Chip>}
            <FreeOfferChip spec={p.spec} className="ms-auto" />
          </div>
          <div className="mt-0.5 flex min-w-0 items-center gap-1 font-mono text-caption-1-regular text-text-secondary">
            <CategoryMark className="size-3.5 shrink-0" aria-hidden />
            <span className="truncate">
              {p.slug} · {p.category || "Other"} · v{p.version}
            </span>
          </div>
        </div>
      </div>
      {p.description && <p className="line-clamp-2 text-body-2-regular text-text-secondary">{p.description}</p>}
      <div className="mt-auto flex items-center gap-2 border-t border-separator-border pt-3">
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
          <SourceBadge source={p.source} />
          <Chip variant="caption" color={p.services_count > 0 ? "lime" : "neutral"} className="font-mono">
            {plural(p.services_count, "service")}
          </Chip>
          {idle ? (
            <Chip variant="caption" color="neutral" className="font-mono">
              No keys
            </Chip>
          ) : (
            <Chip variant="caption" color="lime" className="font-mono">
              {plural(p.keys_count, "key")}
            </Chip>
          )}
        </div>
        {/* Short, so it stays on the chip row instead of wrapping to a line of its own. */}
        {p.updated_at && (
          <span className="shrink-0 whitespace-nowrap font-mono text-caption-2-regular text-text-tertiary" title={`Updated ${dateTime(p.updated_at)}`}>
            {relTime(p.updated_at)}
          </span>
        )}
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

  if (!loaded) return <PageSkeleton variant="grid" />;

  const actions = manage ? (
    <>
      <RouteButton to="/providers/new" variant="secondary" leadingIcon={RiAddLine}>
        New provider
      </RouteButton>
      <RouteButton to="/providers/ai" variant="primary" leadingIcon={AcnAiBuilder}>
        Build with AI
      </RouteButton>
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
        <Callout tone="error" className="mb-6">
          Could not load providers: {error}
        </Callout>
      )}

      <SearchField
        aria-label="Search providers"
        placeholder="Search providers"
        icon={RiSearchLine}
        value={q}
        onChange={setQ}
        enterKeyHint="search"
        className="max-w-[760px]"
        // On the bare page the field needs the card surface to read as a field.
        fieldClassName="h-11 border border-border-button-default bg-background-primary-default shadow-xs"
      />

      <Tabs selectedKey={tab} onSelectionChange={(k) => setTab(String(k))} className="mt-8 gap-0">
        <div className="-mx-4 overflow-x-auto px-4 [scrollbar-width:none] [mask-image:linear-gradient(to_right,black_calc(100%-2rem),transparent)] sm:mx-0 sm:px-0">
          <TabList aria-label="Provider source" className="w-max min-w-full">
            {tabs.map((t) => (
              <Tab key={t.id} id={t.id} count={t.count}>
                {t.label}
              </Tab>
            ))}
          </TabList>
        </div>
        <TabPanel id={tab}>
        {providers.length === 0 ? (
          <EmptyState
            art={SpecSheetArt}
            className="mt-8"
            icon={AcnProviderSpec}
            title="No providers yet"
            body="A provider tells Acnologia how an API signals rate limits and quota. Build one with AI from a name or docs URL, or write the rules yourself."
            action={actions}
          />
        ) : shown.length === 0 ? (
          <EmptyState
            art={SearchArt}
            className="mt-8"
            title={q ? `Nothing matches "${q}"` : "None in this group yet"}
            body={manage ? "Try another search, or build the provider with AI." : "Try another search."}
            action={
              manage ? (
                <RouteButton to="/providers/ai" variant="primary" leadingIcon={AcnAiBuilder}>
                  Build with AI
                </RouteButton>
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
        </TabPanel>
      </Tabs>
    </div>
  );
}
