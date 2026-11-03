import { useEffect, useRef, useState } from "react";
import { Link as RouterLink } from "react-router-dom";
import { RiCheckboxCircleLine, RiErrorWarningLine, RiSearchEyeLine, RiTimeLine } from "@remixicon/react";
import { errorMessage } from "@/lib/api";
import { dateTime } from "@/lib/format";
import { offersOf } from "@/lib/offers";
import { refreshApi, TRIGGER_LABEL, usd, type RefreshItem } from "@/lib/refresh";
import type { Provider } from "@/lib/types";
import { usePoll } from "@/hooks/usePoll";
import { toast } from "@/stores/toast";
import { Button } from "@/components/base/buttons/button";
import { Chip } from "@/components/base/badges/chip";
import { ButtonSpinner } from "@/components/base/spinner/spinner";
import { RefreshDiff } from "./RefreshDiff";

const LINK =
  "rounded-sm underline-offset-2 outline-none hover:text-text-primary hover:underline focus-visible:ring-2 focus-visible:ring-border-focus-ring";

function day(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
}

/**
 * How and when a provider's offers were last read: "Checked Nov 3, 2026 by
 * scheduled refresh", "Changed on Nov 3 (v4)", and a pending review badge.
 */
export function OfferCheckedLine({ provider }: { provider: Provider }) {
  const r = provider.refresh;
  const built = offersOf(provider.spec)
    .map((o) => o.checked_at ?? "")
    .sort()
    .pop();
  const parts = [];
  if (r?.checked_at && r.outcome !== "failed") {
    parts.push(
      <span key="c" title={dateTime(r.checked_at)}>
        Checked {day(r.checked_at)} by {TRIGGER_LABEL[r.trigger] ?? "refresh"}.
      </span>,
    );
  } else if (built) {
    parts.push(<span key="c">Checked {day(built)} when the provider was written.</span>);
  } else {
    parts.push(<span key="c">Never checked.</span>);
  }
  if (r?.changed_at && r.changed_version) {
    parts.push(
      <span key="v">
        Changed on {day(r.changed_at)} (
        <RouterLink to={`/providers/${encodeURIComponent(provider.slug)}?tab=versions`} className={LINK}>
          v{r.changed_version}
        </RouterLink>
        ).
      </span>,
    );
  }
  return (
    <span className="inline-flex flex-wrap items-center gap-x-1.5 gap-y-1">
      {parts}
      {r?.pending_review && (
        <RouterLink to="/settings#provider-refresh" className="rounded-md outline-none focus-visible:ring-2 focus-visible:ring-border-focus-ring">
          <Chip variant="caption" color="yellow">
            Change waiting for review
          </Chip>
        </RouterLink>
      )}
    </span>
  );
}

/**
 * "Check now" for a superadmin: re-reads this provider's pricing pages with
 * the refresh model, shows what it is doing, then the outcome and the diff.
 */
export function OfferCheckButton({ provider, onDone, className }: { provider: Provider; onDone: () => void; className?: string }) {
  const [runId, setRunId] = useState<number | null>(null);
  const [item, setItem] = useState<RefreshItem | null>(null);
  const [starting, setStarting] = useState(false);
  const doneRef = useRef(onDone);
  doneRef.current = onDone;

  const running = runId !== null && (!item || item.status === "pending" || item.status === "running");

  const poll = async () => {
    if (runId === null) return;
    try {
      const res = await refreshApi.run(runId);
      const it = res.items[0] ?? null;
      setItem(it);
      if (res.run.status !== "running") doneRef.current();
    } catch {
      /* keep polling */
    }
  };
  usePoll(poll, 1500, running);
  useEffect(() => {
    if (runId !== null) void poll();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runId]);

  const start = async () => {
    setStarting(true);
    setItem(null);
    try {
      const r = await refreshApi.checkProvider(provider.slug);
      setRunId(r.run.id);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setStarting(false);
    }
  };

  return (
    <div className={className}>
      <Button
        variant="secondary"
        size="small"
        leadingIcon={running || starting ? ButtonSpinner : RiSearchEyeLine}
        disabled={running || starting}
        onClick={() => void start()}
      >
        {running ? "Checking" : "Check now"}
      </Button>
      {runId !== null && <CheckResult item={item} running={running} />}
    </div>
  );
}

function CheckResult({ item, running }: { item: RefreshItem | null; running: boolean }) {
  if (running) {
    return (
      <p role="status" className="mt-2 flex items-center gap-1.5 text-caption-1-regular text-text-secondary">
        <RiTimeLine className="size-3.5 shrink-0" aria-hidden />
        <span className="wrap-anywhere">{item?.progress || "Waiting for the refresh model"}</span>
      </p>
    );
  }
  if (!item) return null;
  const cost = <span className="text-text-tertiary"> Cost {usd(item.cost_usd)}.</span>;
  let head;
  switch (item.status) {
    case "unchanged":
      head = (
        <>
          <RiCheckboxCircleLine className="size-4 shrink-0 text-status-lime-text" aria-hidden />
          <span>No change. The offers are current.{cost}</span>
        </>
      );
      break;
    case "applied":
      head = (
        <>
          <RiCheckboxCircleLine className="size-4 shrink-0 text-status-lime-text" aria-hidden />
          <span>
            Updated and published as v{item.version}.{cost}
          </span>
        </>
      );
      break;
    case "review":
      head = (
        <>
          <RiTimeLine className="size-4 shrink-0 text-status-yellow-text" aria-hidden />
          <span>
            Found changes. They wait for review in{" "}
            <RouterLink to="/settings#provider-refresh" className={LINK}>
              Settings
            </RouterLink>
            .{cost}
          </span>
        </>
      );
      break;
    default:
      head = (
        <>
          <RiErrorWarningLine className="size-4 shrink-0 text-text-error-primary" aria-hidden />
          <span className="wrap-anywhere">
            {item.detail || "The check failed."}
            {cost}
          </span>
        </>
      );
  }
  return (
    <div role="status" className="mt-3 flex flex-col gap-3">
      <p className="flex items-start gap-1.5 text-body-2-regular text-text-primary">{head}</p>
      {(item.status === "applied" || item.status === "review") && <RefreshDiff diff={item.diff} />}
      {item.detail && item.status !== "failed" && <p className="text-caption-1-regular text-text-secondary">{item.detail}</p>}
    </div>
  );
}
