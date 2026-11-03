// The project's GitHub star count, from GET /api/public/github. The server
// asks GitHub and caches the answer (the page's CSP keeps the browser on this
// origin). stars is null while the repo is private or GitHub gives no count,
// and then the page shows no number at all, never a 0 or a placeholder.
import { useEffect, useState } from "react";

interface GithubInfo {
  repo?: string;
  url?: string;
  stars?: number | null;
}

export function useGithubStars(): number | null {
  const [stars, setStars] = useState<number | null>(null);
  useEffect(() => {
    const ctrl = new AbortController();
    fetch("/api/public/github", { signal: ctrl.signal, headers: { Accept: "application/json" } })
      .then((r) => (r.ok ? (r.json() as Promise<GithubInfo>) : null))
      .then((info) => {
        if (info && typeof info.stars === "number" && Number.isFinite(info.stars)) setStars(info.stars);
      })
      .catch(() => {
        // No count is a fine answer: the link still works without it.
      });
    return () => ctrl.abort();
  }, []);
  return stars;
}

const compact = new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 });
const full = new Intl.NumberFormat("en");

/** "1.2K" on the link, "1,234" for screen readers. */
export function formatStars(n: number): { short: string; long: string } {
  return { short: compact.format(n), long: full.format(n) };
}
