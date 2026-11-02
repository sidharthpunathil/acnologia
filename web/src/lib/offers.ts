import type { OfferLimit, ProviderSpec, SpecOffer } from "./types";

/** Units an offer price may use. Keep in step with providers.PriceUnits on the server. */
export const PRICE_UNITS = ["request", "search", "query", "url", "page", "step", "minute", "check", "token", "character", "credit", "image", "second", "session", "run", "gb"];

export const LIMIT_PERS = ["second", "minute", "hour", "day", "month"] as const;

const nf = new Intl.NumberFormat("en-US");

/** "30 requests/minute". */
export function limitText(l: OfferLimit): string {
  return `${nf.format(l.count)} ${l.unit || "requests"}/${l.per}`;
}

/** "$0.016 per step", or "Free". */
export function priceText(o: SpecOffer): string | null {
  if (o.free) return "Free";
  const p = o.price;
  if (!p) return null;
  const amount = Number(p.amount.toFixed(6)).toString();
  const cur = !p.currency || p.currency === "USD" ? `$${amount}` : `${amount} ${p.currency}`;
  return `${cur} per ${p.unit}`;
}

/** "GET /search", "examples Search", or "All requests". */
export function matchText(o: SpecOffer): string {
  const m = o.match;
  if (!m) return "All requests";
  const parts: string[] = [];
  const sel = `${(m.method ?? "").toUpperCase()} ${m.path ?? ""}`.trim();
  if (sel) parts.push(sel);
  if (m.examples?.length) parts.push(`examples ${m.examples.join(", ")}`);
  return parts.join("; ") || "All requests";
}

/** One line per offer: "TinySearch: Free, 30 requests/minute, 500 requests/hour". */
export function offerLine(o: SpecOffer): string {
  const bits = [priceText(o), ...(o.limits ?? []).map(limitText), o.concurrency ? `${o.concurrency} at a time` : null].filter(Boolean);
  return bits.length ? `${o.name}: ${bits.join(", ")}` : o.name;
}

export function offersOf(spec: ProviderSpec | null | undefined): SpecOffer[] {
  return Array.isArray(spec?.offers) ? spec!.offers : [];
}

export function hasFreeOffer(spec: ProviderSpec | null | undefined): boolean {
  return offersOf(spec).some((o) => o.free);
}

/** Tooltip text summarising every offer, one per line. */
export function offersSummary(spec: ProviderSpec | null | undefined): string {
  return offersOf(spec).map(offerLine).join("\n");
}
