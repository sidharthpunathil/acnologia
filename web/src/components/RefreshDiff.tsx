import { RiAddLine, RiArrowRightLine, RiExternalLinkLine, RiSubtractLine } from "@remixicon/react";
import type { OfferLimit, SpecOffer } from "@/lib/types";
import { limitText, matchText, offerLine, priceText } from "@/lib/offers";
import type { FieldChange, OffersDiff } from "@/lib/refresh";
import { safeHref } from "@/lib/url";
import { cx } from "@/utils/cx";

const FIELD_LABEL: Record<string, string> = {
  match: "Requests",
  price: "Price",
  free: "Free",
  limits: "Limits",
  concurrency: "At a time",
  notes: "Notes",
  source_url: "Source",
  input: "Input",
  output: "Output",
  cache_write: "Cache write",
  cache_read: "Cache read",
};

/** A field value in words: the server sends JSON for match, price and limits. */
function readable(field: string, v: string): string {
  if (v === "none" || v === "") return "none";
  try {
    if (field === "price") return priceText({ name: "", price: JSON.parse(v) } as SpecOffer) ?? v;
    if (field === "limits") return (JSON.parse(v) as OfferLimit[]).map(limitText).join(", ") || "none";
    if (field === "match") return matchText({ name: "", match: JSON.parse(v) } as SpecOffer);
  } catch {
    return v;
  }
  if (field === "free") return v === "true" ? "yes" : "no";
  return v;
}

function Change({ c }: { c: FieldChange }) {
  return (
    <li className="grid grid-cols-1 gap-x-3 gap-y-0.5 sm:grid-cols-[6.5rem_minmax(0,1fr)]">
      <span className="text-caption-1-medium text-text-secondary">{FIELD_LABEL[c.field] ?? c.field}</span>
      <span className="flex min-w-0 flex-wrap items-baseline gap-x-1.5 text-body-2-regular">
        <span className="wrap-anywhere text-text-tertiary line-through decoration-1">{readable(c.field, c.from)}</span>
        <RiArrowRightLine className="size-3.5 shrink-0 self-center text-foreground-icon-tertiary rtl:-scale-x-100" aria-label="becomes" />
        <span className="wrap-anywhere text-text-primary">{readable(c.field, c.to)}</span>
      </span>
    </li>
  );
}

function Source({ url }: { url?: string }) {
  const href = url ? safeHref(url) : undefined;
  if (!href) return <span className="text-caption-1-regular text-text-tertiary">No source cited</span>;
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer noopener"
      className="inline-flex min-w-0 items-center gap-1 rounded-sm text-caption-1-medium text-text-secondary underline-offset-2 outline-none hover:text-text-primary hover:underline focus-visible:ring-2 focus-visible:ring-border-focus-ring"
    >
      <span className="truncate">{hostPath(href)}</span>
      <RiExternalLinkLine className="size-3 shrink-0" aria-hidden />
      <span className="sr-only">(source, opens in a new tab)</span>
    </a>
  );
}

function hostPath(u: string): string {
  try {
    const x = new URL(u);
    return x.host.replace(/^www\./, "") + (x.pathname !== "/" ? x.pathname : "");
  } catch {
    return u;
  }
}

/** Added, removed and changed offers (or model prices), as words. */
export function RefreshDiff({ diff, className }: { diff: OffersDiff | undefined; className?: string }) {
  if (!diff) return null;
  const added = diff.added ?? [];
  const removed = diff.removed ?? [];
  const changed = diff.changed ?? [];
  if (added.length + removed.length + changed.length === 0) {
    return <p className={cx("text-body-2-regular text-text-secondary", className)}>No change.</p>;
  }
  return (
    <ul className={cx("flex flex-col gap-3", className)}>
      {changed.map((c) => (
        <li key={`c-${c.name}`} className="rounded-xl bg-background-secondary-default px-3 py-2.5">
          <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
            <span className="text-body-2-medium text-text-primary wrap-anywhere">{c.name}</span>
            <Source url={c.source_url} />
          </div>
          <ul className="mt-2 flex flex-col gap-1.5">
            {c.fields.map((f) => (
              <Change key={f.field} c={f} />
            ))}
          </ul>
        </li>
      ))}
      {added.map((o) => (
        <li key={`a-${o.name}`} className="flex flex-col gap-1 rounded-xl bg-background-secondary-default px-3 py-2.5">
          <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
            <span className="inline-flex items-center gap-1.5 text-body-2-medium text-text-primary">
              <RiAddLine className="size-3.5 text-status-lime-text" aria-hidden />
              <span className="sr-only">Added: </span>
              {o.name}
            </span>
            <Source url={o.source_url} />
          </div>
          <span className="text-caption-1-regular text-text-secondary wrap-anywhere">{offerLine(o)}</span>
        </li>
      ))}
      {removed.map((o) => (
        <li key={`r-${o.name}`} className="flex flex-col gap-1 rounded-xl bg-background-secondary-default px-3 py-2.5">
          <span className="inline-flex items-center gap-1.5 text-body-2-medium text-text-primary">
            <RiSubtractLine className="size-3.5 text-text-error-primary" aria-hidden />
            <span className="sr-only">Removed: </span>
            <span className="line-through decoration-1">{o.name}</span>
          </span>
          <span className="text-caption-1-regular text-text-secondary wrap-anywhere">{offerLine(o)}</span>
        </li>
      ))}
    </ul>
  );
}
