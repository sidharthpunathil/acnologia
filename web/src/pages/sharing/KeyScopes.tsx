import { useId, useMemo, useState } from "react";
import type { Key as AriaKey } from "react-aria-components";
import type { Key, Service } from "../../lib/types";
import type { KeyScope, KeyScopeMode } from "../../lib/sharing";
import { plural } from "../../lib/format";
import { StatusPill } from "../../components/StatusPill";
import { matchesQuery } from "../../components/MultiSelect";
import { SegmentedControl, SegmentedControlItem } from "@/components/base/segmented-control/segmented-control";
import { NumberField } from "@/components/base/number-field/number-field";
import { SearchField } from "@/components/base/search-field/search-field";
import { Checkbox } from "@/components/base/checkbox/checkbox";
import { Button } from "@/components/base/buttons/button";
import { Chip } from "@/components/base/badges/chip";
import { Skeleton } from "@/components/base/skeleton/skeleton";
import { ScrollFade, useScrollFade } from "@/components/foundations/scroll-fade/scroll-fade";
import { cx } from "@/utils/cx";

/** Keys loaded per service id: the list, or an error message. */
export type KeyLists = Record<number, Key[] | string>;

const MODES: { id: KeyScopeMode; label: string }[] = [
  { id: "all", label: "All keys" },
  { id: "count", label: "Up to" },
  { id: "pick", label: "Pick keys" },
];

/** Keys a share can see in the picker: hidden ones only with include_hidden, for a superadmin. */
export function visibleKeys(keys: Key[], includeHidden: boolean, isSuper: boolean): Key[] {
  return keys.filter((k) => !k.hidden || (includeHidden && isSuper));
}

/** Not invalid, banned or disabled: a key cooling down or out of quota still counts. */
function inRotation(k: Key): boolean {
  return k.status !== "invalid" && k.status !== "banned" && k.status !== "disabled";
}

/** How many of the keys the scope resolves to, as the server would count it. */
export function scopedCount(scope: KeyScope | undefined, keys: Key[]): number {
  if (!scope || scope.mode === "all") return keys.length;
  if (scope.mode === "count") return Math.min(scope.count ?? 1, keys.filter(inRotation).length);
  const ids = new Set(scope.key_ids ?? []);
  return keys.filter((k) => ids.has(k.id)).length;
}

export function scopeSummary(scope: KeyScope | undefined, keys: Key[]): string {
  if (!scope || scope.mode === "all") return `All ${plural(keys.length, "key")}`;
  return `${scopedCount(scope, keys)} of ${plural(keys.length, "key")}`;
}

function KeyChecklist({ service, keys, picked, onChange, now }: { service: Service; keys: Key[]; picked: number[]; onChange: (ids: number[]) => void; now: number }) {
  const [query, setQuery] = useState("");
  const fade = useScrollFade();
  const statusId = useId();
  const shown = useMemo(() => keys.filter((k) => matchesQuery({ label: k.label, meta: k.hint }, query)), [keys, query]);
  const set = new Set(picked);
  const shownIds = shown.map((k) => k.id);
  const allShownOn = shown.length > 0 && shown.every((k) => set.has(k.id));

  if (!keys.length) return <p className="text-caption-1-regular text-text-secondary">This service has no keys the share can use.</p>;
  return (
    <div className="flex flex-col gap-2">
      {keys.length > 6 && (
        <SearchField
          aria-label={`Search keys of ${service.name}`}
          aria-describedby={statusId}
          placeholder="Search keys"
          value={query}
          onChange={setQuery}
          size="small"
          shape="rounded"
        />
      )}
      <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1">
        <p id={statusId} aria-live="polite" className="text-caption-1-regular text-text-secondary">
          {query.trim() ? `${shown.length} of ${plural(keys.length, "key")} match` : plural(keys.length, "key")}
        </p>
        <div className="flex items-center gap-1">
          <Button size="xs" variant="ghost" disabled={allShownOn} onClick={() => onChange([...new Set([...picked, ...shownIds])])}>
            {query.trim() ? "Select shown" : "Select all"}
          </Button>
          <Button size="xs" variant="ghost" disabled={picked.length === 0} onClick={() => onChange(query.trim() ? picked.filter((id) => !shownIds.includes(id)) : [])}>
            {query.trim() ? "Clear shown" : "Clear"}
          </Button>
        </div>
      </div>
      <div className="relative">
        <ul
          onScroll={fade.onScroll}
          aria-label={`Keys of ${service.name}`}
          className="flex max-h-64 flex-col gap-0.5 overflow-y-auto overscroll-contain rounded-xl bg-background-tertiary-default p-1"
        >
          {shown.length === 0 && <li className="px-2 py-1.5 text-caption-1-regular text-text-secondary">No key matches "{query.trim()}".</li>}
          {shown.map((k) => (
            <li key={k.id} className="rounded-lg px-2 py-1.5 hover:bg-background-tertiary-hover">
              <Checkbox
                size="sm"
                isSelected={set.has(k.id)}
                onChange={(on) => onChange(on ? [...picked, k.id] : picked.filter((id) => id !== k.id))}
                className="w-full items-start"
              >
                <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                  <span className="min-w-0 wrap-anywhere">{k.label || `Key ${k.id}`}</span>
                  <code dir="ltr" className="font-mono text-caption-1-regular text-text-tertiary">
                    {k.hint}
                  </code>
                  <StatusPill k={k} now={now} serviceMax={service.max_concurrency} />
                  {k.tier === "backup" && (
                    <Chip variant="caption" color="neutral">
                      backup
                    </Chip>
                  )}
                  {k.hidden && (
                    <Chip variant="caption" color="yellow">
                      hidden
                    </Chip>
                  )}
                </span>
              </Checkbox>
            </li>
          ))}
        </ul>
        <ScrollFade progress={fade.progress} surfaceClassName="from-background-tertiary-default" className="rounded-t-xl" />
      </div>
    </div>
  );
}

/** One shared service's key scope: all keys, up to N, or a picked list. */
function KeyScopeRow({
  service,
  scope,
  keys,
  onChange,
  now,
}: {
  service: Service;
  scope: KeyScope | undefined;
  keys: Key[] | string | undefined;
  onChange: (s: KeyScope) => void;
  now: number;
}) {
  const mode = scope?.mode ?? "all";
  const list = Array.isArray(keys) ? keys : null;
  const total = list?.length ?? service.key_counts.total;
  const setMode = (m: KeyScopeMode) => {
    if (m === "all") onChange({ mode: "all" });
    else if (m === "count") onChange({ mode: "count", count: Math.max(1, Math.min(scope?.count ?? 1, total || 1)) });
    else onChange({ mode: "pick", key_ids: scope?.key_ids ?? [] });
  };
  return (
    <li className="flex flex-col gap-3 rounded-2xl border border-border-button-default p-3">
      <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-2">
        <div className="min-w-0">
          <div className="wrap-anywhere text-body-medium text-text-primary">{service.name}</div>
          <div className="text-caption-1-regular text-text-secondary">
            <span className="font-mono">{service.slug}</span> ·{" "}
            <span aria-live="polite">{list ? scopeSummary(scope, list) : typeof keys === "string" ? "keys did not load" : "loading keys"}</span>
          </div>
        </div>
        <SegmentedControl
          aria-label={`Keys of ${service.name} to share`}
          selectedKeys={new Set<AriaKey>([mode])}
          onSelectionChange={(sel) => {
            const next = [...sel][0];
            if (next !== undefined) setMode(String(next) as KeyScopeMode);
          }}
        >
          {MODES.map((m) => (
            <SegmentedControlItem key={m.id} id={m.id} className="min-h-9 sm:min-h-0">
              {m.label}
            </SegmentedControlItem>
          ))}
        </SegmentedControl>
      </div>
      {mode === "count" && (
        <NumberField
          label="Most keys to use"
          minValue={1}
          maxValue={Math.max(1, total)}
          value={scope?.count ?? 1}
          onChange={(n) => !Number.isNaN(n) && onChange({ mode: "count", count: Math.max(1, Math.round(n)) })}
          hint="The first keys still in rotation, by priority. A key that stops working is replaced by the next one."
          className="sm:max-w-60"
        />
      )}
      {mode === "pick" &&
        (list ? (
          <KeyChecklist service={service} keys={list} picked={scope?.key_ids ?? []} onChange={(ids) => onChange({ mode: "pick", key_ids: ids })} now={now} />
        ) : typeof keys === "string" ? (
          <p className="text-caption-1-regular text-text-error-primary">{keys}</p>
        ) : (
          <Skeleton className="h-24 w-full rounded-xl" />
        ))}
    </li>
  );
}

/** The key scope of each selected service, under the Services picker. */
export function KeyScopes({
  services,
  selected,
  scopes,
  keys,
  includeHidden,
  isSuper,
  now,
  onChange,
}: {
  services: Service[];
  selected: string[];
  scopes: Record<string, KeyScope>;
  keys: KeyLists;
  includeHidden: boolean;
  isSuper: boolean;
  now: number;
  onChange: (slug: string, s: KeyScope) => void;
}) {
  const rows = selected.map((slug) => services.find((s) => s.slug === slug)).filter((s): s is Service => !!s);
  if (!rows.length) return null;
  return (
    <ul className={cx("flex flex-col gap-2")} aria-label="Keys to share per service">
      {rows.map((sv) => {
        const k = keys[sv.id];
        return (
          <KeyScopeRow
            key={sv.slug}
            service={sv}
            scope={scopes[sv.slug]}
            keys={Array.isArray(k) ? visibleKeys(k, includeHidden, isSuper) : k}
            onChange={(s) => onChange(sv.slug, s)}
            now={now}
          />
        );
      })}
    </ul>
  );
}
