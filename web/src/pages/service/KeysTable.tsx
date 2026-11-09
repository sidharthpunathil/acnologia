import { useRef, useState, type ReactNode } from "react";
import {
  RiArrowRightSLine,
  RiDeleteBinLine,
  RiEditLine,
  RiEyeLine,
  RiEyeOffLine,
  RiLockLine,
  RiMore2Line,
  RiPulseLine,
  RiRestartLine,
} from "@remixicon/react";
import type { App, Key, Strategy } from "../../lib/types";
import { num } from "../../lib/format";
import { useKeys } from "../../stores/keys";
import { toast } from "../../stores/toast";
import { ApiError, errorMessage } from "../../lib/api";
import { StatusPill, isBusy } from "../../components/StatusPill";
import { AccountLine, OnChange, QuotaMeter, TimeAgo, ValidityLine, quotaInfo } from "../../components/KeyBits";
import { Chip } from "@/components/base/badges/chip";
import { Dropdown, DropdownDivider, DropdownItem, DropdownPopover, DropdownTrigger } from "@/components/base/dropdown/dropdown";
import { AlertDialog } from "@/components/base/modal/modal";
import { Pagination } from "@/components/base/pagination/pagination";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { Table, TableBody, TableCell, TableColumn, TableHeader, TableRow } from "@/components/base/table/table";
import { cx } from "@/utils/cx";
import { ExposureChips, useLoadHandovers } from "../../components/HandoverChip";
import { withLiveStatus } from "./keyUtil";
import { ActionIcon, CARD } from "./parts";

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
  const title = limit > 0 ? `${n} of ${limit} concurrent requests in use${k.max_concurrency !== null ? " (key override)" : ""}` : `${n} requests in flight`;
  return (
    <Chip
      variant="caption"
      color={n > 0 ? "purple" : "soft"}
      title={title}
      className={cx("gap-1 rounded-full px-2 py-0.5 font-mono tabular-nums", n === 0 && "text-text-tertiary")}
    >
      {busy && <span className="size-1.5 rounded-full bg-current" aria-hidden="true" />}
      {limit > 0 ? `${n}/${limit}` : n} in use
    </Chip>
  );
}

function KeyBadges({ k, apps }: { k: Key; apps: App[] }) {
  const allowed = k.app_ids.map((id) => apps.find((a) => a.id === id)?.name ?? `#${id}`);
  return (
    <span className="inline-flex flex-wrap items-center gap-1">
      {k.tier === "backup" && (
        <Chip variant="caption" color="blue" className="rounded-full px-2 py-0 font-mono">
          backup
        </Chip>
      )}
      {k.hidden && (
        <Chip variant="caption" color="gray" title="Hidden: only superadmins can see or count this key" className="gap-1 rounded-full px-2 py-0 font-mono">
          <RiEyeOffLine className="size-3" aria-hidden />
          hidden
        </Chip>
      )}
      {k.restricted && (
        <Chip
          variant="caption"
          color="purple"
          title={allowed.length ? `Only these apps can use it: ${allowed.join(", ")}` : "Restricted, and no apps are allowed yet"}
          className="gap-1 rounded-full px-2 py-0 font-mono"
        >
          <RiLockLine className="size-3" aria-hidden />
          {allowed.length}
          <span className="sr-only">{allowed.length === 1 ? "app allowed" : "apps allowed"}</span>
        </Chip>
      )}
    </span>
  );
}

/** Check, reset and delete for one key, with the toasts they report through. */
function useKeyActions(k: Key) {
  const { check, reset, remove } = useKeys();
  const [busy, setBusy] = useState<"check" | "reset" | null>(null);
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
        toast.info("This provider has no health check. Acnologia learns whether the key works from real requests.");
      else toast.error(errorMessage(e));
    } finally {
      setBusy(null);
    }
  };
  const del = async () => {
    try {
      await remove(k);
      toast.success(`Deleted ${k.label || k.hint}`);
    } catch (e) {
      toast.error(errorMessage(e));
    }
  };
  return { busy, run, del, needsReset: k.effective_status !== "active" || k.consecutive_fails > 0 };
}

/** Clicks and key presses inside the actions must not also open the row behind them. */
function StopRow({ className, children }: { className?: string; children: ReactNode }) {
  return (
    <div
      className={className}
      onClick={(e) => e.stopPropagation()}
      onPointerDown={(e) => e.stopPropagation()}
      onKeyDown={(e) => {
        // Enter and Space belong to the focused control; arrows still move through the grid.
        if (e.key === "Enter" || e.key === " ") e.stopPropagation();
      }}
    >
      {children}
    </div>
  );
}

type RowProps = { k: Key; perms: KeyPerms; onEdit: (k: Key) => void; onReveal: (k: Key) => void };

/** Large screens: Check and Edit stay one click away; the rarer and riskier actions sit in the menu. */
function RowActions({ k, perms, onEdit, onReveal }: RowProps) {
  const actions = useKeyActions(k);
  const canTouch = perms.manage && (!k.hidden || perms.hidden);
  if (!canTouch && !perms.reveal) return null;
  return (
    <StopRow className="flex items-center justify-end gap-1">
      {canTouch && (
        <>
          <ActionIcon
            icon={RiPulseLine}
            label="Check this key now"
            busy={actions.busy === "check"}
            disabled={actions.busy !== null}
            onClick={() => void actions.run("check")}
          />
          <ActionIcon icon={RiEditLine} label="Edit key" onClick={() => onEdit(k)} />
        </>
      )}
      <RowMenu k={k} perms={perms} onEdit={onEdit} onReveal={onReveal} actions={actions} extrasOnly />
    </StopRow>
  );
}

/**
 * The key's actions behind one menu button. Phones get all of them so the card
 * keeps its room; large screens pass `extrasOnly` for Reset, Reveal and Delete.
 */
function RowMenu({
  k,
  perms,
  onEdit,
  onReveal,
  actions,
  extrasOnly = false,
}: RowProps & { actions?: ReturnType<typeof useKeyActions>; extrasOnly?: boolean }) {
  const own = useKeyActions(k);
  const { busy, run, del, needsReset } = actions ?? own;
  const [open, setOpen] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const canTouch = perms.manage && (!k.hidden || perms.hidden);
  if (!canTouch && !perms.reveal) return null;
  const name = k.label || k.hint;
  const pick = (fn: () => void) => () => {
    setOpen(false);
    fn();
  };
  const row = extrasOnly ? "gap-2.5" : "gap-2.5 min-h-11";
  const menu = (
    <>
      <Dropdown isOpen={open} onOpenChange={setOpen}>
        <DropdownTrigger
          aria-label={`Actions for ${name}`}
          className={cx(
            "inline-flex items-center justify-center rounded-2lg outline-none transition-colors duration-150 focus-visible:ring-2 focus-visible:ring-border-focus-ring",
            extrasOnly
              ? "size-8 border border-border-button-default bg-background-primary-default text-foreground-icon-primary shadow-xs hover:bg-background-primary-hover"
              : "size-11 text-foreground-icon-secondary hover:bg-background-secondary-default",
          )}
        >
          {busy && (!extrasOnly || busy === "reset") ? (
            <ButtonSpinner className="size-4" />
          ) : (
            <RiMore2Line className={extrasOnly ? "size-4" : "size-5"} aria-hidden />
          )}
        </DropdownTrigger>
        <DropdownPopover aria-label={`Actions for ${name}`} placement="bottom end" className="w-56">
          {canTouch && !extrasOnly && (
            <DropdownItem className={row} onSelect={pick(() => void run("check"))}>
              <RiPulseLine className="size-4 text-foreground-icon-secondary" aria-hidden /> Check now
            </DropdownItem>
          )}
          {canTouch && needsReset && (
            <DropdownItem className={row} onSelect={pick(() => void run("reset"))}>
              <RiRestartLine className="size-4 text-foreground-icon-secondary" aria-hidden /> Reset cooldown and errors
            </DropdownItem>
          )}
          {canTouch && !extrasOnly && (
            <DropdownItem className={row} onSelect={pick(() => onEdit(k))}>
              <RiEditLine className="size-4 text-foreground-icon-secondary" aria-hidden /> Edit
            </DropdownItem>
          )}
          {perms.reveal && (
            <DropdownItem className={row} onSelect={pick(() => onReveal(k))}>
              <RiEyeLine className="size-4 text-foreground-icon-secondary" aria-hidden /> Reveal secret
            </DropdownItem>
          )}
          {canTouch && (
            <>
              {(perms.reveal || needsReset || !extrasOnly) && <DropdownDivider />}
              <DropdownItem className={cx(row, "text-text-destructive")} onSelect={pick(() => setConfirming(true))}>
                <RiDeleteBinLine className="size-4" aria-hidden /> Delete key
              </DropdownItem>
            </>
          )}
        </DropdownPopover>
      </Dropdown>
      <AlertDialog
        isOpen={confirming}
        onOpenChange={setConfirming}
        title={`Delete ${name}?`}
        description="It leaves rotation right away. This cannot be undone."
        confirmLabel="Delete"
        pendingLabel="Deleting"
        onConfirm={del}
      />
    </>
  );
  // Inside RowActions the StopRow wrapper is already there.
  return extrasOnly ? menu : <StopRow>{menu}</StopRow>;
}

/** The state the pill shows, so a change of state (not a ticking countdown) can replay its entrance. */
export function pillState(k: Key, serviceMax: number): string {
  return k.effective_status === "active" && isBusy(k, serviceMax) ? "busy" : k.effective_status;
}

/**
 * One column for the key's health: the live state as the only pill, then
 * whether the key works as a quiet line underneath. Invalid keys used to show
 * "invalid" twice in a row; the line now carries the reason instead.
 */
export function KeyHealth({ k, now, serviceMax, clamp = true }: { k: Key; now: number; serviceMax: number; clamp?: boolean }) {
  return (
    <div className="min-w-0">
      <div className="flex flex-wrap items-center gap-1.5">
        <OnChange id={pillState(k, serviceMax)}>
          <StatusPill k={k} now={now} serviceMax={serviceMax} />
        </OnChange>
        <InUse k={k} serviceMax={serviceMax} />
      </div>
      <ValidityLine k={k} now={now} clamp={clamp} className="mt-1.5" />
    </div>
  );
}

/** Rows per page. Every row re-renders each second for its live times, so long pools page. */
const PAGE_SIZE = 50;

/** Says which other services hold the same secret, so one key added twice is easy to spot. */
export function TwinsLine({ k, className }: { k: Key; className?: string }) {
  if (!k.twins?.length) return null;
  return (
    <div
      className={cx("truncate text-caption-1-regular text-status-purple-text", className)}
      title={k.twins.map((t) => `${t.label || "Untitled"} in ${t.service_name}`).join(", ")}
    >
      Same key as in {[...new Set(k.twins.map((t) => t.service_name))].join(", ")}
    </div>
  );
}

export function KeysTable({
  keys,
  apps,
  now,
  perms,
  serviceMax = 0,
  quotaUnit,
  resetExact,
  strategy,
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
  /** The service's rotation strategy; key priority only shows under fill first. */
  strategy?: Strategy;
  onOpen: (k: Key) => void;
  onEdit: (k: Key) => void;
  onReveal: (k: Key) => void;
}) {
  useLoadHandovers();
  const [page, setPage] = useState(1);
  const top = useRef<HTMLDivElement>(null);
  const pageCount = Math.max(1, Math.ceil(keys.length / PAGE_SIZE));
  const curPage = Math.min(page, pageCount);
  const pageKeys = pageCount > 1 ? keys.slice((curPage - 1) * PAGE_SIZE, curPage * PAGE_SIZE) : keys;
  const rows = pageKeys.map((k) => withLiveStatus(k, now));
  const goTo = (p: number) => {
    setPage(p);
    // Bring the top of the list back into view when the pager sits below the fold.
    const el = top.current;
    if (el && el.getBoundingClientRect().top < 0) el.scrollIntoView({ block: "start" });
  };
  const hasActions = perms.manage || perms.reveal;
  const fillFirst = strategy === "fill_first";
  const open = (k: Key) => {
    // Let people select text in a row without opening the drawer.
    if (window.getSelection()?.toString()) return;
    onOpen(k);
  };
  return (
    <div ref={top} className="scroll-mt-4">
      {/* Large screens: a table. Enter or a click on a row opens the key. */}
      <div className={cx(CARD, "hidden overflow-hidden lg:block")}>
        <Table
          aria-label="Keys in this service"
          size="sm"
          containerClassName="-mt-px"
          className="table-fixed"
          onRowAction={(id) => {
            const k = rows.find((x) => x.id === Number(id));
            if (k) open(k);
          }}
        >
          <TableHeader>
            <TableColumn isRowHeader className="w-[27%] ps-5">
              Key
            </TableColumn>
            <TableColumn className="w-[28%]">Health</TableColumn>
            <TableColumn className="w-[18%]">Quota</TableColumn>
            <TableColumn>Last used</TableColumn>
            {hasActions ? (
              <TableColumn className="w-[124px]">
                <span className="sr-only">Actions</span>
              </TableColumn>
            ) : (
              <TableColumn className="w-10">
                <span className="sr-only">Open</span>
              </TableColumn>
            )}
          </TableHeader>
          <TableBody items={rows}>
            {(k) => (
              <TableRow id={k.id} textValue={k.label || k.hint} className="group cursor-pointer hover:bg-background-secondary-default">
                <TableCell className="py-3 ps-5 align-top">
                  <div className="flex min-w-0 items-center gap-x-2">
                    <span className="min-w-0 truncate text-body-2-medium text-text-primary" title={k.label || undefined}>
                      {k.label || "Untitled"}
                    </span>
                    <span className="shrink-0">
                      <KeyBadges k={k} apps={apps} />
                    </span>
                  </div>
                  <ExposureChips k={k} className="mt-1" />
                  <AccountLine k={k} className="mt-0.5" />
                  <div className="mt-0.5 truncate font-mono text-caption-1-regular text-text-secondary">
                    {k.hint}
                    {/* Priority only steers fill first, so it stays out of sight under the other strategies. */}
                    {fillFirst && k.tier === "primary" ? ` · priority ${k.priority}` : ""}
                    {k.weight && k.weight !== 1 ? <span title={`Weight ${k.weight}`}>{` · w${k.weight}`}</span> : ""}
                  </div>
                  <TwinsLine k={k} className="mt-0.5" />
                </TableCell>
                <TableCell className="py-3 align-top">
                  <KeyHealth k={k} now={now} serviceMax={serviceMax} />
                </TableCell>
                <TableCell className="py-3 align-top">
                  <QuotaMeter k={k} unit={quotaUnit ?? k.quota_unit} now={now} compact resetExact={resetExact} />
                </TableCell>
                <TableCell className="py-3 align-top font-mono">
                  <TimeAgo iso={k.last_used_at} now={now} className="block truncate text-caption-1-regular text-text-primary" />
                  <div className="mt-0.5 truncate text-caption-1-regular tabular-nums text-text-secondary">{num(k.usage_today)} today</div>
                </TableCell>
                <TableCell className="py-2.5 pe-3 align-top">
                  {hasActions ? (
                    <RowActions k={k} perms={perms} onEdit={onEdit} onReveal={onReveal} />
                  ) : (
                    <RiArrowRightSLine className="mt-1 size-4 text-foreground-icon-tertiary rtl:rotate-180" aria-hidden />
                  )}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>

      {/* Phones and tablets: cards, with the actions in a menu. */}
      <ul className="grid grid-cols-1 gap-2.5 md:grid-cols-2 lg:hidden">
        {rows.map((k) => (
          <li key={k.id} className={cx(CARD, "relative flex min-w-0 flex-col")}>
            <button
              type="button"
              onClick={() => open(k)}
              aria-label={`Open details for ${k.label || k.hint}`}
              className="flex w-full min-w-0 flex-1 cursor-pointer flex-col gap-3 rounded-3xl p-4 pe-14 text-start outline-none transition-colors duration-150 ease hover:bg-background-primary-hover active:bg-background-primary-active focus-visible:ring-2 focus-visible:ring-border-focus-ring"
            >
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="wrap-anywhere min-w-0 text-body-medium text-text-primary">{k.label || "Untitled"}</span>
                  <KeyBadges k={k} apps={apps} />
                </div>
                <ExposureChips k={k} className="mt-1" />
                <AccountLine k={k} wrap className="mt-0.5" />
                <TwinsLine k={k} className="mt-0.5" />
                <div className="mt-0.5 font-mono text-caption-1-regular text-text-secondary">
                  {k.hint}
                  {fillFirst && k.tier === "primary" ? ` · priority ${k.priority}` : ""}
                </div>
              </div>
              <KeyHealth k={k} now={now} serviceMax={serviceMax} />
              {quotaInfo(k, quotaUnit ?? k.quota_unit) && <QuotaMeter k={k} unit={quotaUnit ?? k.quota_unit} now={now} compact resetExact={resetExact} />}
              <div className="font-mono text-caption-1-regular text-text-secondary">
                used <TimeAgo iso={k.last_used_at} now={now} /> · {num(k.usage_today)} today
              </div>
            </button>
            <div className="absolute end-1.5 top-1.5">
              {hasActions ? (
                <RowMenu k={k} perms={perms} onEdit={onEdit} onReveal={onReveal} />
              ) : (
                <RiArrowRightSLine className="m-3.5 size-4 text-foreground-icon-tertiary rtl:rotate-180" aria-hidden />
              )}
            </div>
          </li>
        ))}
      </ul>
      <Pagination page={curPage} totalPages={pageCount} onChange={goTo} aria-label="Key pages" className="mt-4" />
    </div>
  );
}
