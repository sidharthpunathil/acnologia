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
