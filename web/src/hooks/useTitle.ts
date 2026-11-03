import { useEffect } from "react";

export const TAGLINE = "Share API keys without giving them away.";
export const BRAND_TITLE = "Acnologia: share API keys without giving them away";
export const REPO_URL = "https://github.com/sidharthpunathil/acnologia";

/** Sets document.title to "<page> · Acnologia", or the brand title when no page is given. */
export function useTitle(page?: string | null) {
  useEffect(() => {
    document.title = page ? `${page} · Acnologia` : BRAND_TITLE;
  }, [page]);
}
