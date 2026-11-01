/**
 * Returns the URL only when it is an absolute http(s) URL, so server supplied
 * strings can never become `javascript:` or `data:` links.
 */
export function safeHref(u: string | null | undefined): string | undefined {
  if (!u) return undefined;
  try {
    const parsed = new URL(u);
    return parsed.protocol === "http:" || parsed.protocol === "https:" ? parsed.href : undefined;
  } catch {
    return undefined;
  }
}

/**
 * Returns an in app path ("/services/3") from server data, or undefined when it
 * is anything else. Resolves against this origin and runs through safeHref, so
 * protocol relative or scripted links never reach a router Link.
 */
export function safeAppPath(p: string | null | undefined): string | undefined {
  if (!p || !p.startsWith("/") || p.startsWith("//")) return undefined;
  const origin = window.location.origin;
  let href: string | undefined;
  try {
    href = safeHref(new URL(p, origin).href);
  } catch {
    return undefined;
  }
  if (!href) return undefined;
  const u = new URL(href);
  return u.origin === origin ? u.pathname + u.search + u.hash : undefined;
}
