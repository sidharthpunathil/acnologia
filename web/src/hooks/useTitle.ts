import { useEffect } from "react";

export const TAGLINE = "I think, therefore I am.";
export const BRAND_TITLE = `Cogito: ${TAGLINE}`;

/** Sets document.title to "<page> · Cogito", or the brand title when no page is given. */
export function useTitle(page?: string | null) {
  useEffect(() => {
    document.title = page ? `${page} · Cogito` : BRAND_TITLE;
  }, [page]);
}
