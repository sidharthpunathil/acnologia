import { useMemo, useState } from "react";
import { RiExternalLinkLine } from "@remixicon/react";
import { SearchField } from "@/components/base/search-field/search-field";
import { Chip } from "@/components/base/badges/chip";
import { cx } from "@/utils/cx";
import type { ProviderSpec, SpecHost } from "../lib/types";
import { safeHref } from "../lib/url";

/** Above this many APIs the list starts with only the first few of each group shown. */
const FOLD_AT = 24;
const PER_GROUP = 6;

/**
 * "APIs" on the service page, for a provider that serves many products on
 * hosts of their own (Omkar Cloud's scrapers, say): every API grouped by
 * category, with the path prefix that picks it.
 */
export function ApisBlock({
  spec,
  service,
  className,
}: {
  spec: ProviderSpec | null | undefined;
  /** The service slug, for the example path. */
  service?: string;
  className?: string;
}) {
  const hosts = spec?.hosts ?? [];
  const [q, setQ] = useState("");
  const [open, setOpen] = useState<Record<string, boolean>>({});
  const groups = useMemo(() => groupHosts(hosts, q), [hosts, q]);
  if (hosts.length === 0) return null;
  const shown = groups.reduce((n, g) => n + g.hosts.length, 0);
  const folds = hosts.length > FOLD_AT && !q.trim();
  const first = hosts[0];

  return (
    <section aria-labelledby="apis-heading" className={cx("rounded-3xl border border-border-button-default bg-background-primary-default p-4 sm:p-5", className)}>
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <h2 id="apis-heading" className="text-body-semibold text-text-primary">
          APIs <span className="text-text-tertiary tabular-nums">{hosts.length}</span>
        </h2>
        <p className="text-caption-1-regular text-text-tertiary">
          One key works on every API. Start the path with its prefix, for example{" "}
          <code className="font-mono text-text-secondary">
            /proxy/{service ?? "{service}"}/~{first.name}/...
          </code>
        </p>
      </div>
      <SearchField
        value={q}
        onChange={setQ}
        placeholder={`Search ${hosts.length} APIs`}
        aria-label="Search APIs"
        enterKeyHint="search"
        className="mt-3"
        fieldClassName="border border-border-button-default bg-background-primary-default"
      />
      {shown === 0 ? (
        <p className="mt-4 text-body-2-regular text-text-tertiary">No API matches “{q}”.</p>
      ) : (
        <div className="mt-4 flex flex-col gap-5">
          {groups.map((g) => {
            const expanded = !folds || open[g.name] || g.hosts.length <= PER_GROUP;
            const list = expanded ? g.hosts : g.hosts.slice(0, PER_GROUP);
            return (
              <div key={g.name || "other"}>
                <h3 className="flex items-center gap-2 text-caption-1-semibold uppercase tracking-wide text-text-tertiary">
                  {g.name || "Other"}
                  <span className="tabular-nums">{g.hosts.length}</span>
                </h3>
                <ul className="mt-2 grid grid-cols-1 gap-x-4 sm:grid-cols-2 lg:grid-cols-3">
                  {list.map((h) => (
                    <HostRow key={h.name} host={h} />
                  ))}
                </ul>
                {!expanded && (
                  <button
                    type="button"
                    onClick={() => setOpen((o) => ({ ...o, [g.name]: true }))}
                    className="mt-1 cursor-pointer rounded-md text-caption-1-medium text-text-secondary underline-offset-2 outline-none hover:text-text-primary hover:underline focus-visible:ring-2 focus-visible:ring-border-focus-ring"
                  >
                    Show all {g.hosts.length}
                  </button>
                )}
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}

function HostRow({ host }: { host: SpecHost }) {
  const docs = host.docs_url ? safeHref(host.docs_url) : null;
  return (
    <li className="flex min-w-0 flex-col gap-0.5 border-b border-separator-border py-2">
      <div className="flex min-w-0 items-center gap-2">
        <span className="truncate text-body-2-medium text-text-primary" title={host.description || undefined}>
          {host.title || host.name}
        </span>
        {docs && (
          <a
            href={docs}
            target="_blank"
            rel="noreferrer"
            aria-label={`${host.title || host.name} docs`}
            className="shrink-0 rounded text-foreground-icon-tertiary outline-none hover:text-foreground-icon-primary focus-visible:ring-2 focus-visible:ring-border-focus-ring"
          >
            <RiExternalLinkLine className="size-3.5" aria-hidden />
          </a>
        )}
      </div>
      <Chip variant="caption" color="gray" className="w-fit max-w-full truncate font-mono">
        ~{host.name}
      </Chip>
    </li>
  );
}

type HostGroup = { name: string; hosts: SpecHost[] };

/** Groups hosts by category in first seen order, keeping those that match q. */
function groupHosts(hosts: SpecHost[], q: string): HostGroup[] {
  const needle = q.trim().toLowerCase();
  const out: HostGroup[] = [];
  const idx = new Map<string, number>();
  for (const h of hosts) {
    if (needle && ![h.name, h.title, h.group, h.description].some((v) => v?.toLowerCase().includes(needle))) continue;
    const g = h.group?.trim() ?? "";
    let i = idx.get(g);
    if (i === undefined) {
      i = out.length;
      idx.set(g, i);
      out.push({ name: g, hosts: [] });
    }
    out[i].hosts.push(h);
  }
  return out;
}
