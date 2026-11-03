import type { ReactNode } from "react";
import { RiExternalLinkLine } from "@remixicon/react";
import { Chip } from "@/components/base/badges/chip";
import { cx } from "@/utils/cx";
import type { ProviderSpec } from "../lib/types";
import { hasFreeOffer, limitText, matchText, offersOf, offersSummary, priceText } from "../lib/offers";
import { safeHref } from "../lib/url";

/**
 * "Free" chip for cards whose provider has at least one free offer. The
 * cards are links, so the offer summary rides on the native title and in
 * screen reader text rather than a focusable tooltip.
 */
export function FreeOfferChip({ spec, className }: { spec: ProviderSpec | null | undefined; className?: string }) {
  if (!hasFreeOffer(spec)) return null;
  const summary = offersSummary(spec);
  return (
    <Chip variant="caption" color="lime" title={summary} className={cx("-my-0.5 shrink-0 font-mono", className)}>
      Free
      <span className="sr-only">. Offers: {summary.replace(/\n/g, "; ")}</span>
    </Chip>
  );
}

/** "Offers and limits" on the service page: what is free, prices and the limits Acnologia paces keys under. */
export function OffersBlock({
  spec,
  className,
  checked: checkedLine,
  action,
  showEmpty = false,
}: {
  spec: ProviderSpec | null | undefined;
  className?: string;
  /** How and when the offers were last read; replaces the plain date. */
  checked?: ReactNode;
  /** A control on the card, such as Check now, with its result. */
  action?: ReactNode;
  /** Draw the card with no offers yet, so the action has a home. */
  showEmpty?: boolean;
}) {
  const offers = offersOf(spec);
  if (offers.length === 0 && !showEmpty) return null;
  const sources = [...new Set(offers.map((o) => o.source_url).filter((u): u is string => !!u && !!safeHref(u)))];
  const checked = offers.map((o) => o.checked_at ?? "").sort().pop() || undefined;
  return (
    <section aria-labelledby="offers-heading" className={cx("rounded-3xl border border-border-button-default bg-background-primary-default p-4 sm:p-5", className)}>
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <h2 id="offers-heading" className="text-body-semibold text-text-primary">
          Offers and limits
        </h2>
        <p className="text-caption-1-regular text-text-tertiary">
          {offers.length > 0 ? "Acnologia keeps each key under these limits." : "No offers recorded yet."}
          {checkedLine ? <> {checkedLine}</> : checked && <> Checked {checked}.</>}
        </p>
      </div>
      {offers.length > 0 && (
        <ul className="mt-3 divide-y divide-separator-border">
          {offers.map((o) => {
            const price = priceText(o);
            return (
              <li key={o.name} className="flex flex-col gap-1.5 py-2.5 first:pt-1 last:pb-0 sm:flex-row sm:items-center sm:gap-4">
                <div className="flex min-w-0 items-center gap-2 sm:w-64 sm:shrink-0">
                  <span className="truncate text-body-2-medium text-text-primary">{o.name}</span>
                  {o.free && (
                    <Chip variant="caption" color="lime" className="font-mono">
                      Free
                    </Chip>
                  )}
                </div>
                <span className="min-w-0 truncate font-mono text-caption-1-regular text-text-secondary sm:w-48 sm:shrink-0" title={matchText(o)}>
                  {matchText(o)}
                </span>
                <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
                  {price && !o.free && (
                    <Chip variant="caption" color="gray" className="font-mono">
                      {price}
                    </Chip>
                  )}
                  {(o.limits ?? []).map((l) => (
                    <Chip key={`${l.count}/${l.per}/${l.unit}`} variant="caption" color="blue" className="font-mono">
                      {limitText(l)}
                    </Chip>
                  ))}
                  {o.concurrency ? (
                    <Chip variant="caption" color="purple" className="font-mono">
                      {o.concurrency} at a time
                    </Chip>
                  ) : null}
                  {o.notes && (
                    <span className="min-w-0 basis-full text-caption-1-regular text-text-tertiary sm:line-clamp-2" title={o.notes}>
                      {o.notes}
                    </span>
                  )}
                </div>
              </li>
            );
          })}
        </ul>
      )}
      {action && <div className="mt-3 border-t border-separator-border pt-3">{action}</div>}
      {sources.length > 0 && (
        <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1">
          {sources.map((u) => (
            <a
              key={u}
              href={safeHref(u)}
              target="_blank"
              rel="noreferrer noopener"
              className="inline-flex min-w-0 items-center gap-1 rounded-sm text-caption-1-medium text-text-secondary underline-offset-2 outline-none hover:text-text-primary hover:underline focus-visible:ring-2 focus-visible:ring-border-focus-ring"
            >
              <span className="truncate">{hostOf(u)}</span>
              <RiExternalLinkLine className="size-3 shrink-0" aria-hidden />
              <span className="sr-only">(pricing source, opens in a new tab)</span>
            </a>
          ))}
        </div>
      )}
    </section>
  );
}

function hostOf(u: string): string {
  try {
    const x = new URL(u);
    return x.host.replace(/^www\./, "") + (x.pathname !== "/" ? x.pathname : "");
  } catch {
    return u;
  }
}
