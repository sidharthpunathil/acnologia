const nf = new Intl.NumberFormat("en-US");

export function num(n: number | null | undefined): string {
  if (n === null || n === undefined) return "-";
  return nf.format(n);
}

export function compact(n: number): string {
  if (Math.abs(n) < 10000) return nf.format(n);
  return new Intl.NumberFormat("en-US", { notation: "compact", maximumFractionDigits: 1 }).format(n);
}

export function plural(n: number, one: string, many = `${one}s`): string {
  return `${nf.format(n)} ${n === 1 ? one : many}`;
}

/** "3m ago", "in 2h", "just now". */
export function relTime(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return "never";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "-";
  const diff = Math.round((t - now) / 1000);
  const abs = Math.abs(diff);
  if (abs < 10) return "just now";
  let v: string;
  if (abs < 60) v = `${abs}s`;
  else if (abs < 3600) v = `${Math.floor(abs / 60)}m`;
  else if (abs < 86400) v = `${Math.floor(abs / 3600)}h`;
  else if (abs < 86400 * 30) v = `${Math.floor(abs / 86400)}d`;
  else return new Date(t).toLocaleDateString();
  return diff < 0 ? `${v} ago` : `in ${v}`;
}

/** Countdown like "4:05" or "1h 02m" until an ISO time. Empty when in the past. */
export function countdown(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  let s = Math.max(0, Math.ceil((t - now) / 1000));
  if (s <= 0) return "";
  const h = Math.floor(s / 3600);
  s -= h * 3600;
  const m = Math.floor(s / 60);
  s -= m * 60;
  if (h > 0) return `${h}h ${String(m).padStart(2, "0")}m`;
  return `${m}:${String(s).padStart(2, "0")}`;
}

export function dateTime(iso: string | null | undefined): string {
  if (!iso) return "-";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "-";
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

export const STRATEGY_LABEL: Record<string, string> = {
  round_robin: "round robin",
  fill_first: "fill first",
  least_used: "least used",
  most_quota: "most quota",
  random: "weighted random",
};

export const STRATEGY_HELP: Record<string, string> = {
  round_robin: "Least recently used key first.",
  fill_first: "Lowest priority number first, then least recently used.",
  least_used: "Key with the lowest usage today.",
  most_quota: "Key with the most quota remaining.",
  random: "Weighted random by key weight.",
};

export function slugify(s: string): string {
  return s
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}
