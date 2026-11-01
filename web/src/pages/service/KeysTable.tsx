import { useState, type KeyboardEvent, type MouseEvent } from "react";
import { Activity, ChevronRight, Eye, EyeOff, Lock, Pencil, RotateCcw } from "lucide-react";
import type { App, Key } from "../../lib/types";
import { num } from "../../lib/format";
import { useKeys } from "../../stores/keys";
import { toast } from "../../stores/toast";
import { ApiError, errorMessage } from "../../lib/api";
import { StatusPill } from "../../components/StatusPill";
import { ConfirmButton } from "../../components/ConfirmButton";
import { Badge, IconButton, Spinner, cx } from "../../components/ui";
import { QuotaMeter, TimeAgo, ValidityBadge, validityText } from "../../components/KeyBits";
import { withLiveStatus } from "./keyUtil";

export interface KeyPerms {
  manage: boolean;
  hidden: boolean;
  reveal: boolean;
}

/** "1/2 in use" next to the status. Busy styling when the key is at its limit. */
export function InUse({ k, serviceMax }: { k: Key; serviceMax: number }) {
  const limit = k.max_concurrency ?? serviceMax;
  const n = k.in_flight ?? 0;
  if (n === 0 && limit <= 0) return null;
  const busy = limit > 0 && n >= limit;
  return (
    <span
      title={limit > 0 ? `${n} of ${limit} concurrent requests in use${k.max_concurrency !== null ? " (key override)" : ""}` : `${n} requests in flight`}
      className={cx(
        "inline-flex items-center gap-1 whitespace-nowrap rounded-full px-2 py-0.5 font-mono text-[11px] tabular-nums",
        busy ? "bg-violet-100 text-violet-800" : n > 0 ? "bg-violet-50 text-violet-700" : "text-faint",
      )}
    >
      {busy && <span className="h-1.5 w-1.5 rounded-full bg-violet-500" aria-hidden="true" />}
      {limit > 0 ? `${n}/${limit}` : n} in use
    </span>
  );
}

function KeyBadges({ k, apps }: { k: Key; apps: App[] }) {
  const allowed = k.app_ids.map((id) => apps.find((a) => a.id === id)?.name ?? `#${id}`);
  return (
    <span className="inline-flex flex-wrap items-center gap-1">
      {k.tier === "backup" && <Badge tone="blue">backup</Badge>}
      {k.hidden && (
        <Badge tone="dark" title="Hidden: only superadmins can see or count this key">
          <EyeOff className="h-3 w-3" />
          hidden
        </Badge>
      )}
      {k.restricted && (
        <Badge tone="violet" title={allowed.length ? `Only these apps can use it: ${allowed.join(", ")}` : "Restricted, and no apps are allowed yet"}>
          <Lock className="h-3 w-3" />
          {allowed.length}
        </Badge>
      )}
    </span>
  );
}

function RowActions({
  k,
  perms,
  onEdit,
  onReveal,
}: {
  k: Key;
  perms: KeyPerms;
  onEdit: (k: Key) => void;
  onReveal: (k: Key) => void;
}) {
  const { check, reset, remove } = useKeys();
  const [busy, setBusy] = useState<"check" | "reset" | null>(null);
  const canTouch = perms.manage && (!k.hidden || perms.hidden);
  if (!canTouch && !perms.reveal) return null;

  const run = async (kind: "check" | "reset") => {
    setBusy(kind);
    try {
      if (kind === "check") {
        const nk = await check(k);
        if (nk.validity === "invalid") toast.error(`${k.label || k.hint} failed the check: ${nk.last_error || "the provider rejected it"}.`);
        else toast.success(`${k.label || k.hint} passed the check.`);
      } else {
        await reset(k);
        toast.success(`${k.label || k.hint} is reset and back in rotation.`);
      }
    } catch (e) {
      if (e instanceof ApiError && e.code === "no_health_check")
        toast.info("This provider has no health check. Cogito learns whether the key works from real requests.");
      else toast.error(errorMessage(e));
    } finally {
      setBusy(null);
    }
  };

  const needsReset = k.effective_status !== "active" || k.consecutive_fails > 0;

  return (
    // Clicks here should not also open the key drawer behind the row.
    <div className="flex flex-wrap items-center justify-end gap-0.5" onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>
      {canTouch && (
        <>
          <IconButton label="Check this key now" onClick={() => run("check")} disabled={busy !== null}>
            {busy === "check" ? <Spinner /> : <Activity className="h-4 w-4" />}
          </IconButton>
          <IconButton label="Reset: clear cooldown and errors" onClick={() => run("reset")} disabled={busy !== null || !needsReset}>
            {busy === "reset" ? <Spinner /> : <RotateCcw className="h-4 w-4" />}
          </IconButton>
          <IconButton label="Edit key" onClick={() => onEdit(k)}>
            <Pencil className="h-4 w-4" />
          </IconButton>
        </>
      )}
      {perms.reveal && (
        <IconButton label="Reveal secret" onClick={() => onReveal(k)}>
          <Eye className="h-4 w-4" />
        </IconButton>
      )}
      {canTouch && (
        <ConfirmButton
          iconOnly
          label="Delete key"
          confirmLabel="Delete"
          onConfirm={async () => {
            try {
              await remove(k);
              toast.success(`Deleted ${k.label || k.hint}`);
            } catch (e) {
              toast.error(errorMessage(e));
            }
          }}
        />
      )}
    </div>
  );
}

/** Opens the drawer from a row with Enter or Space, like a button. */
function rowKey(e: KeyboardEvent, fn: () => void) {
  if (e.target !== e.currentTarget) return;
  if (e.key === "Enter" || e.key === " ") {
    e.preventDefault();
    fn();
  }
}

export function KeysTable({
  keys,
  apps,
  now,
  perms,
  serviceMax = 0,
  quotaUnit,
  resetExact,
  onOpen,
  onEdit,
  onReveal,
}: {
  keys: Key[];
  apps: App[];
  now: number;
  perms: KeyPerms;
  serviceMax?: number;
  quotaUnit?: string;
  /** Whether the provider reports quota reset times; see QuotaMeter. */
  resetExact?: boolean;
  onOpen: (k: Key) => void;
  onEdit: (k: Key) => void;
  onReveal: (k: Key) => void;
}) {
  const rows = keys.map((k) => withLiveStatus(k, now));
  const open = (k: Key) => (e?: MouseEvent) => {
    // Let people select text in a row without opening the drawer.
    if (e && window.getSelection()?.toString()) return;
    onOpen(k);
  };
  return (
    <>
      {/* Large screens: a table. */}
      <div className="hidden overflow-hidden rounded-card bg-surface shadow-card lg:block">
        <table className="w-full table-fixed text-left text-[13px]">
          <colgroup>
            <col className="w-[23%]" />
            <col className="w-[15%]" />
            <col className="w-[17%]" />
            <col className="w-[18%]" />
            <col className="w-[11%]" />
            <col className="w-[16%]" />
          </colgroup>
          <thead>
            <tr className="border-b border-line font-mono text-[11px] uppercase tracking-wide text-faint">
              <th className="px-4 py-3 font-normal">Key</th>
              <th className="px-3 py-3 font-normal">Works?</th>
              <th className="px-3 py-3 font-normal">Status</th>
              <th className="px-3 py-3 font-normal">Quota</th>
              <th className="px-3 py-3 font-normal">Last used</th>
              <th className="px-3 py-3">
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {rows.map((k) => (
              <tr
                key={k.id}
                tabIndex={0}
                aria-label={`Open details for ${k.label || k.hint}`}
                onClick={open(k)}
                onKeyDown={(e) => rowKey(e, () => onOpen(k))}
                className="cursor-pointer align-middle transition-colors hover:bg-soft focus-visible:bg-soft focus-visible:outline focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ink"
              >
                <td className="px-4 py-3">
                  <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                    <span className="min-w-0 truncate font-medium text-ink">{k.label || "Untitled"}</span>
                    <KeyBadges k={k} apps={apps} />
                  </div>
                  <div className="mt-0.5 truncate font-mono text-[11px] text-muted">
                    {k.hint}
                    {k.tier === "primary" && k.priority ? ` · p${k.priority}` : ""}
                    {k.weight && k.weight !== 1 ? ` · w${k.weight}` : ""}
                    {` · ${num(k.usage_today)} today`}
                  </div>
                </td>
                <td className="px-3 py-3">
                  <ValidityBadge validity={k.validity} reason={validityText(k, now)} />
                  <div className="mt-1 line-clamp-2 text-[11px] text-muted" title={validityText(k, now)}>
                    {validityText(k, now)}
                  </div>
                </td>
                <td className="px-3 py-3">
                  <div className="flex flex-col items-start gap-1">
                    <StatusPill k={k} now={now} serviceMax={serviceMax} />
                    <InUse k={k} serviceMax={serviceMax} />
                  </div>
                </td>
                <td className="px-3 py-3">
                  <QuotaMeter k={k} unit={quotaUnit ?? k.quota_unit} now={now} compact resetExact={resetExact} />
                </td>
                <td className="px-3 py-3 font-mono text-xs text-muted">
                  <TimeAgo iso={k.last_used_at} now={now} />
                </td>
                <td className="px-2 py-2">
                  <RowActions k={k} perms={perms} onEdit={onEdit} onReveal={onReveal} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* Phones and tablets: cards. */}
      <ul className="grid grid-cols-1 gap-2.5 md:grid-cols-2 lg:hidden">
        {rows.map((k) => (
          <li key={k.id} className="flex flex-col rounded-card bg-surface shadow-card">
            <button type="button" onClick={open(k)} className="flex w-full flex-1 flex-col gap-3 p-4 text-left transition-transform duration-150 ease-out active:scale-[0.99]">
              <div className="flex w-full items-start justify-between gap-3">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="min-w-0 break-words font-medium text-ink">{k.label || "Untitled"}</span>
                    <KeyBadges k={k} apps={apps} />
                  </div>
                  <div className="mt-0.5 font-mono text-[11px] text-muted">{k.hint}</div>
                </div>
                <ChevronRight className="mt-0.5 h-4 w-4 shrink-0 text-faint" aria-hidden="true" />
              </div>
              <div className="flex flex-wrap items-center gap-1.5">
                <ValidityBadge validity={k.validity} reason={validityText(k, now)} />
                <StatusPill k={k} now={now} serviceMax={serviceMax} />
                <InUse k={k} serviceMax={serviceMax} />
              </div>
              <div className="wrap-anywhere -mt-1 text-[12px] text-muted">{validityText(k, now)}</div>
              <QuotaMeter k={k} unit={quotaUnit ?? k.quota_unit} now={now} compact resetExact={resetExact} />
              <div className="font-mono text-[11px] text-muted">
                used <TimeAgo iso={k.last_used_at} now={now} /> · {num(k.usage_today)} today
              </div>
            </button>
            {(perms.manage || perms.reveal) && (
              <div className="border-t border-line px-2 py-1">
                <RowActions k={k} perms={perms} onEdit={onEdit} onReveal={onReveal} />
              </div>
            )}
          </li>
        ))}
      </ul>
    </>
  );
}
