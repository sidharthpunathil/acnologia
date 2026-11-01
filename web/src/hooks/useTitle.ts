import { useEffect } from "react";

export const TAGLINE = "I think, therefore I am.";
export const BRAND_TITLE = `Acnologia: ${TAGLINE}`;

/** Sets document.title to "<page> · Acnologia", or the brand title when no page is given. */
export function useTitle(page?: string | null) {
  useEffect(() => {
    document.title = page ? `${page} · Acnologia` : BRAND_TITLE;
  }, [page]);
}
