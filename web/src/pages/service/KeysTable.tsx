import { useState } from "react";
import { Activity, Eye, EyeOff, Lock, Pencil, RotateCcw } from "lucide-react";
import type { App, Key } from "../../lib/types";
import { num, relTime, dateTime } from "../../lib/format";
import { useKeys } from "../../stores/keys";
import { toast } from "../../stores/toast";
import { errorMessage } from "../../lib/api";
import { StatusPill } from "../../components/StatusPill";
import { ConfirmButton } from "../../components/ConfirmButton";
import { Badge, IconButton, Spinner, cx } from "../../components/ui";
import { withLiveStatus } from "./keyUtil";

export interface KeyPerms {
  manage: boolean;
  hidden: boolean;
  reveal: boolean;
}

function QuotaBar({ k }: { k: Key }) {
  if (k.quota_limit === null && k.quota_remaining === null) return <span className="font-mono text-xs text-faint">-</span>;
  const limit = k.quota_limit ?? 0;
  const rem = k.quota_remaining ?? limit;
  const pct = limit > 0 ? Math.max(0, Math.min(100, (rem / limit) * 100)) : 0;
  const color = pct > 40 ? "bg-emerald-500" : pct > 10 ? "bg-amber-500" : "bg-red-500";
  return (
    <div className="w-[120px]" title={k.quota_reset_at ? `Resets ${dateTime(k.quota_reset_at)}` : undefined}>
      <div className="font-mono text-[11px] text-ink">
        {num(rem)}
        <span className="text-faint"> / {k.quota_limit === null ? "?" : num(k.quota_limit)}</span>
      </div>
      {limit > 0 && (
        <div className="mt-1 h-1 overflow-hidden rounded-full bg-[#ecebe5]">
          <div className={cx("h-full rounded-full", color)} style={{ width: `${pct}%` }} />
        </div>
      )}
    </div>
  );
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
      {busy && <span className="h-1.5 w-1.5 rounded-full bg-violet-600" aria-hidden="true" />}
      {limit > 0 ? `${n}/${limit}` : n} {busy ? "busy" : "in use"}
    </span>
  );
}

function KeyBadges({ k, apps }: { k: Key; apps: App[] }) {
  const allowed = k.app_ids.map((id) => apps.find((a) => a.id === id)?.name ?? `#${id}`);
  return (
    <span className="inline-flex flex-wrap items-center gap-1">
      {k.tier === "backup" && <Badge tone="blue">backup</Badge>}
      {k.hidden && (
        <Badge tone="dark" title="Hidden: only superadmins see or count this key">
          <EyeOff className="h-3 w-3" />
          hidden
        </Badge>
      )}
      {k.restricted && (
        <Badge tone="violet" title={allowed.length ? `Allowed apps: ${allowed.join(", ")}` : "Restricted: no apps allowed yet"}>
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
        toast.success(`${k.label || k.hint}: ${nk.effective_status === "active" ? "healthy" : nk.effective_status}`);
      } else {
        await reset(k);
        toast.success(`${k.label || k.hint} reset and active`);
      }
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(null);
    }
  };

  const needsReset = k.effective_status !== "active" || k.consecutive_fails > 0;

  return (
    <div className="flex items-center justify-end gap-0.5">
      {canTouch && (
        <>
          <IconButton label="Check health now" onClick={() => run("check")} disabled={busy !== null}>
            {busy === "check" ? <Spinner /> : <Activity className="h-4 w-4" />}
          </IconButton>
          <IconButton label="Reset cooldown and set active" onClick={() => run("reset")} disabled={busy !== null || !needsReset}>
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

export function KeysTable({
  keys,
  apps,
  now,
  perms,
  serviceMax = 0,
  onEdit,
  onReveal,
}: {
  keys: Key[];
  apps: App[];
  now: number;
  perms: KeyPerms;
  serviceMax?: number;
  onEdit: (k: Key) => void;
  onReveal: (k: Key) => void;
}) {
  const rows = keys.map((k) => withLiveStatus(k, now));
  return (
    <>
      {/* Desktop table */}
      <div className="hidden overflow-x-auto rounded-card bg-white shadow-card md:block">
        <table className="w-full min-w-[900px] text-left text-[13px]">
          <thead>
            <tr className="border-b border-line font-mono text-[11px] uppercase tracking-wide text-faint">
              <th className="px-4 py-3 font-normal">Key</th>
              <th className="px-3 py-3 font-normal">Status</th>
              <th className="px-3 py-3 font-normal">Quota</th>
              <th className="px-3 py-3 font-normal">Usage</th>
              <th className="whitespace-nowrap px-3 py-3 font-normal">Last used</th>
              <th className="px-3 py-3 font-normal">Last error</th>
              <th className="px-3 py-3" />
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {rows.map((k) => (
              <tr key={k.id} className="align-middle hover:bg-[#fbfbf8]">
                <td className="px-4 py-3">
                  <div className="flex items-center gap-2">
                    <span className="whitespace-nowrap font-medium text-ink">{k.label || "Untitled"}</span>
                    <KeyBadges k={k} apps={apps} />
                  </div>
                  <div className="mt-0.5 font-mono text-[11px] text-muted">
                    {k.hint}
                    {k.tier === "primary" && k.priority ? ` · p${k.priority}` : ""}
                    {k.weight && k.weight !== 1 ? ` · w${k.weight}` : ""}
                  </div>
                </td>
                <td className="px-3 py-3">
                  <div className="flex flex-col items-start gap-1">
                    <StatusPill k={k} now={now} />
                    <InUse k={k} serviceMax={serviceMax} />
                  </div>
                </td>
                <td className="px-3 py-3">
                  <QuotaBar k={k} />
                </td>
                <td className="whitespace-nowrap px-3 py-3 font-mono text-xs">
                  <span className="text-ink">{num(k.usage_today)}</span>
                  <span className="text-faint"> today · {num(k.usage_total)}</span>
                </td>
                <td className="whitespace-nowrap px-3 py-3 font-mono text-xs text-muted" title={dateTime(k.last_used_at)}>
                  {relTime(k.last_used_at, now)}
                </td>
                <td className="max-w-[200px] px-3 py-3">
                  <div className="truncate text-xs text-muted" title={k.last_error}>
                    {k.last_error || <span className="text-faint">-</span>}
                  </div>
                </td>
                <td className="px-3 py-2">
                  <RowActions k={k} perms={perms} onEdit={onEdit} onReveal={onReveal} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* Mobile cards */}
      <ul className="space-y-2.5 md:hidden">
        {rows.map((k) => (
          <li key={k.id} className="rounded-card bg-white p-4 shadow-card">
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium text-ink">{k.label || "Untitled"}</span>
                  <KeyBadges k={k} apps={apps} />
                </div>
                <div className="mt-0.5 font-mono text-[11px] text-muted">{k.hint}</div>
              </div>
              <div className="flex shrink-0 flex-col items-end gap-1">
                <StatusPill k={k} now={now} />
                <InUse k={k} serviceMax={serviceMax} />
              </div>
            </div>
            <div className="mt-3 flex flex-wrap items-end justify-between gap-3">
              <QuotaBar k={k} />
              <div className="font-mono text-[11px] text-muted">
                {num(k.usage_today)} today · {num(k.usage_total)} total · {relTime(k.last_used_at, now)}
              </div>
            </div>
            {k.last_error && <div className="mt-2 truncate text-xs text-red-700/80">{k.last_error}</div>}
            <div className="mt-2 border-t border-line pt-2">
              <RowActions k={k} perms={perms} onEdit={onEdit} onReveal={onReveal} />
            </div>
          </li>
        ))}
      </ul>
    </>
  );
}
