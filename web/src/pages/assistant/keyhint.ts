/** A key as the transcript shows it: its label (maybe empty) and last characters. */
export type KeyRef = { label: string; hint: string };

/**
 * One way to show a key's last characters everywhere in the Assistant:
 * "...c21e", "…c21e" and "···c21e" all become "…c21e".
 */
export function formatHint(hint: string): string {
  const tail = hint.trim().replace(/^[.…·\s]+/, "");
  return tail ? `…${tail}` : "";
}

/**
 * A line of an older message that only held a key: "acct-7, [key k1 ...c21e]",
 * "acct-1 [key k1 ...a1b2]", "acct-9: …91fe" or just "...91fe".
 */
const LEGACY_KEY_LINE =
  /^\s*(?:([^,:=\s][^,:=]*?)(?:\s*[,:=]\s*|\s+))?(?:\[key\s+k\d+\s+)?((?:\.\.\.|…)[A-Za-z0-9_-]{2,8})\]?\s*$/;

/**
 * Older messages carry their keys inline as placeholder lines. Splits those
 * lines out into label and hint, and returns the rest as prose.
 */
export function legacyKeys(text: string): { prose: string; keys: KeyRef[] } {
  const keys: KeyRef[] = [];
  const rest: string[] = [];
  for (const line of text.split("\n")) {
    const m = LEGACY_KEY_LINE.exec(line);
    if (m) keys.push({ label: (m[1] ?? "").trim(), hint: m[2] });
    else rest.push(line);
  }
  return { prose: rest.join("\n"), keys };
}
